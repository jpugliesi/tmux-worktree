package localdispatch

import (
	"strings"
	"testing"

	"github.com/jpugliesi/tmux-worktree/internal/clierr"
	"github.com/jpugliesi/tmux-worktree/internal/domain"
	"github.com/jpugliesi/tmux-worktree/internal/store"
)

type fakeObserver struct {
	observation   AgentObservation
	workspaceGone bool
	err           error
	observeFunc   func(workspaceID, agentSessionID, agentLabel string) (AgentObservation, bool, error)
	calls         int
}

func (f *fakeObserver) Observe(workspaceID, agentSessionID, agentLabel string) (AgentObservation, bool, error) {
	f.calls++
	if f.observeFunc != nil {
		return f.observeFunc(workspaceID, agentSessionID, agentLabel)
	}
	return f.observation, f.workspaceGone, f.err
}

// dispatchRunning starts one running session through the real dispatch path.
func dispatchRunning(t *testing.T, fixture localFixture, slug string) domain.LocalDispatchSession {
	t.Helper()
	session, err := fixture.service.Dispatch(DispatchOptions{TicketRef: slug, Mode: domain.DispatchModeAgent})
	if err != nil {
		t.Fatalf("dispatch %s: %v", slug, err)
	}
	return session
}

func withObserver(fixture localFixture, observer *fakeObserver) localFixture {
	fixture.service.options.Observer = observer
	return fixture
}

func diagnosticCodes(result SyncResult) map[string]bool {
	codes := map[string]bool{}
	for _, diagnostic := range result.Diagnostics {
		codes[diagnostic.Code] = true
	}
	return codes
}

func TestSyncKeepsAHealthyRunningSession(t *testing.T) {
	fixture := withObserver(newLocalFixture(t), &fakeObserver{observation: AgentObservation{Complete: true, Found: true, Live: true}})
	ticket := fixture.createTicket(t, "Fix auth")
	session := dispatchRunning(t, fixture, ticket.Slug)

	result, err := fixture.service.Sync("core", false)
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if len(result.Diagnostics) != 0 {
		t.Fatalf("diagnostics = %+v", result.Diagnostics)
	}
	if len(result.Sessions) != 1 || result.Sessions[0].ID != session.ID || result.Sessions[0].Status != domain.LocalDispatchRunning {
		t.Fatalf("sessions = %+v", result.Sessions)
	}
	if !result.Capacity.Known || result.Capacity.Active != 1 || result.Capacity.Available != 1 {
		t.Fatalf("capacity = %+v", result.Capacity)
	}
}

func TestSyncReportsAStuckSessionWithoutReleasingTheClaim(t *testing.T) {
	fixture := withObserver(newLocalFixture(t), &fakeObserver{observation: AgentObservation{Complete: true, Found: true, Live: false}})
	ticket := fixture.createTicket(t, "Fix auth")
	session := dispatchRunning(t, fixture, ticket.Slug)

	result, err := fixture.service.Sync("core", false)
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if !diagnosticCodes(result)[SyncFindingStuck] {
		t.Fatalf("diagnostics = %+v", result.Diagnostics)
	}
	if result.Capacity.Known || result.Capacity.Available != 0 {
		t.Fatalf("capacity = %+v", result.Capacity)
	}
	claimed, err := fixture.tickets.Resolve(ticket.Slug)
	if err != nil {
		t.Fatal(err)
	}
	if claimed.ClaimedBy != session.Claimant {
		t.Fatalf("stuck sync released the claim: %q", claimed.ClaimedBy)
	}
	saved, _ := store.NewLocalDispatchSessionStore(fixture.state).Find(session.ID)
	if saved.Status != domain.LocalDispatchRunning {
		t.Fatalf("stuck sync changed the session status: %q", saved.Status)
	}
}

func TestSyncNeverConcludesStoppedFromAnIncompleteObservation(t *testing.T) {
	fixture := withObserver(newLocalFixture(t), &fakeObserver{observation: AgentObservation{Complete: false, Diagnostics: []string{"tmux unreachable"}}})
	ticket := fixture.createTicket(t, "Fix auth")
	dispatchRunning(t, fixture, ticket.Slug)

	result, err := fixture.service.Sync("core", false)
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}
	codes := diagnosticCodes(result)
	if !codes[SyncFindingObserveIncomplete] || codes[SyncFindingStuck] {
		t.Fatalf("diagnostics = %+v", result.Diagnostics)
	}
	if result.Capacity.Known {
		t.Fatal("capacity is known despite an incomplete observation")
	}
}

func TestSyncReportsAMissingWorkspaceWithoutUnclaiming(t *testing.T) {
	fixture := withObserver(newLocalFixture(t), &fakeObserver{workspaceGone: true, observation: AgentObservation{Complete: true}})
	ticket := fixture.createTicket(t, "Fix auth")
	session := dispatchRunning(t, fixture, ticket.Slug)

	result, err := fixture.service.Sync("core", false)
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if !diagnosticCodes(result)[SyncFindingWorkspaceMissing] {
		t.Fatalf("diagnostics = %+v", result.Diagnostics)
	}
	claimed, _ := fixture.tickets.Resolve(ticket.Slug)
	if claimed.ClaimedBy != session.Claimant {
		t.Fatal("missing workspace released the claim")
	}
}

func TestSyncFinishesASessionWhoseWorkerCompletedTheTicket(t *testing.T) {
	fixture := withObserver(newLocalFixture(t), &fakeObserver{observation: AgentObservation{Complete: true, Found: true, Live: false}})
	ticket := fixture.createTicket(t, "Fix auth")
	session := dispatchRunning(t, fixture, ticket.Slug)

	// The worker records its pull request and releases the claim itself.
	if _, err := fixture.tickets.CompleteWork(ticket.Slug, session.Claimant, domain.TicketReadyForHuman,
		[]string{"https://origin.cursor.com/acme/api/pull/7"}, false); err != nil {
		t.Fatal(err)
	}

	result, err := fixture.service.Sync("core", false)
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if len(result.Diagnostics) != 0 {
		t.Fatalf("diagnostics = %+v", result.Diagnostics)
	}
	saved, _ := store.NewLocalDispatchSessionStore(fixture.state).Find(session.ID)
	if saved.Status != domain.LocalDispatchFinished || !saved.TicketTransitioned {
		t.Fatalf("session after worker completion = %+v", saved)
	}
	// The claim is already gone; sync must not have called CompleteClaim
	// (that would have failed with unsafe_state and become a diagnostic).
	if !result.Capacity.Known || result.Capacity.Active != 0 || result.Capacity.Available != 2 {
		t.Fatalf("capacity = %+v", result.Capacity)
	}
}

func TestSyncFailsASessionWhoseWorkerUnclaimedTheTicket(t *testing.T) {
	fixture := withObserver(newLocalFixture(t), &fakeObserver{observation: AgentObservation{Complete: true, Found: true, Live: false}})
	ticket := fixture.createTicket(t, "Fix auth")
	session := dispatchRunning(t, fixture, ticket.Slug)
	if _, err := fixture.tickets.Unclaim(ticket.Slug, session.Claimant, false); err != nil {
		t.Fatal(err)
	}

	if _, err := fixture.service.Sync("core", false); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	saved, _ := store.NewLocalDispatchSessionStore(fixture.state).Find(session.ID)
	if saved.Status != domain.LocalDispatchFailed || !saved.TicketTransitioned {
		t.Fatalf("session after worker unclaim = %+v", saved)
	}
}

func TestSyncCancelsASessionWhoseTicketWasReclaimed(t *testing.T) {
	fixture := withObserver(newLocalFixture(t), &fakeObserver{observation: AgentObservation{Complete: true, Found: true, Live: true}})
	ticket := fixture.createTicket(t, "Fix auth")
	session := dispatchRunning(t, fixture, ticket.Slug)
	if _, err := fixture.tickets.Unclaim(ticket.Slug, session.Claimant, false); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.tickets.Claim(ticket.Slug, "human-user", false); err != nil {
		t.Fatal(err)
	}

	if _, err := fixture.service.Sync("core", false); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	saved, _ := store.NewLocalDispatchSessionStore(fixture.state).Find(session.ID)
	if saved.Status != domain.LocalDispatchCancelled || saved.Error == nil || saved.Error.Kind != "superseded" {
		t.Fatalf("session after reclaim = %+v", saved)
	}
	claimed, _ := fixture.tickets.Resolve(ticket.Slug)
	if claimed.ClaimedBy != "human-user" {
		t.Fatalf("sync touched a re-claimed ticket: %q", claimed.ClaimedBy)
	}
}

func TestSyncDryRunObservesWithoutWriting(t *testing.T) {
	fixture := withObserver(newLocalFixture(t), &fakeObserver{observation: AgentObservation{Complete: true, Found: true, Live: false}})
	ticket := fixture.createTicket(t, "Fix auth")
	session := dispatchRunning(t, fixture, ticket.Slug)
	if _, err := fixture.tickets.CompleteWork(ticket.Slug, session.Claimant, domain.TicketReadyForHuman, nil, false); err != nil {
		t.Fatal(err)
	}

	result, err := fixture.service.Sync("core", true)
	if err != nil {
		t.Fatalf("dry-run Sync: %v", err)
	}
	if len(result.Sessions) != 1 || result.Sessions[0].TicketTransitioned {
		t.Fatalf("dry-run sessions = %+v", result.Sessions)
	}
	saved, _ := store.NewLocalDispatchSessionStore(fixture.state).Find(session.ID)
	if saved.Status != domain.LocalDispatchRunning || saved.TicketTransitioned {
		t.Fatalf("dry-run sync wrote the session: %+v", saved)
	}
}

func TestAbandonReleasesTheClaimAndKeepsTheWorkspace(t *testing.T) {
	fixture := withObserver(newLocalFixture(t), &fakeObserver{observation: AgentObservation{Complete: true, Found: true, Live: false}})
	ticket := fixture.createTicket(t, "Fix auth")
	session := dispatchRunning(t, fixture, ticket.Slug)
	// The CLI launcher stamps the Workspace onto the ticket; do the same.
	if _, err := fixture.tickets.SetWorkspace(ticket.Slug, session.WorkspaceID, false); err != nil {
		t.Fatal(err)
	}

	abandoned, err := fixture.service.Abandon(session.ID[:8], false)
	if err != nil {
		t.Fatalf("Abandon: %v", err)
	}
	if abandoned.Status != domain.LocalDispatchCancelled || !abandoned.TicketTransitioned {
		t.Fatalf("abandoned = %+v", abandoned)
	}
	if abandoned.WorkspaceID != "ws0123456789abcdef" {
		t.Fatal("abandon dropped the workspace join key")
	}
	returned, _ := fixture.tickets.Resolve(ticket.Slug)
	if returned.ClaimedBy != "" || returned.Status != domain.TicketReadyForAgent {
		t.Fatalf("ticket after abandon = %+v", returned)
	}
	if returned.WorkspaceID == "" {
		t.Fatal("abandon cleared twt_workspace_id; the ticket must keep its Workspace join key")
	}
	_, err = fixture.service.Abandon(session.ID[:8], false)
	if clierr.CodeOf(err) != clierr.PreconditionFailed {
		t.Fatalf("second abandon error = %v, want precondition_failed", err)
	}
}

func TestAbandonPreservesANewerTicketClaim(t *testing.T) {
	fixture := withObserver(newLocalFixture(t), &fakeObserver{observation: AgentObservation{Complete: true, Found: true, Live: false}})
	ticket := fixture.createTicket(t, "Fix auth")
	session := dispatchRunning(t, fixture, ticket.Slug)
	if _, err := fixture.tickets.Unclaim(ticket.Slug, session.Claimant, false); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.tickets.Claim(ticket.Slug, "human-user", false); err != nil {
		t.Fatal(err)
	}

	if _, err := fixture.service.Abandon(session.ID[:8], false); err != nil {
		t.Fatalf("Abandon: %v", err)
	}
	claimed, _ := fixture.tickets.Resolve(ticket.Slug)
	if claimed.ClaimedBy != "human-user" {
		t.Fatalf("abandon touched a newer claim: %q", claimed.ClaimedBy)
	}
}

func TestSyncReportsWaitingOnInputWithoutBlockingCapacity(t *testing.T) {
	fixture := withObserver(newLocalFixture(t), &fakeObserver{observation: AgentObservation{Complete: true, Found: true, Live: true}})
	ticket := fixture.createTicket(t, "Fix auth")
	session := dispatchRunning(t, fixture, ticket.Slug)
	if _, err := fixture.tickets.Ask(ticket.Slug, session.Claimant, "Which provider?", false); err != nil {
		t.Fatal(err)
	}

	result, err := fixture.service.Sync("core", false)
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}
	codes := diagnosticCodes(result)
	if !codes[SyncFindingWaitingOnInput] || codes[SyncFindingStuck] {
		t.Fatalf("diagnostics = %+v", result.Diagnostics)
	}
	if !result.Diagnostics[0].Informational {
		t.Fatal("waiting_on_input is not marked informational")
	}
	// Waiting is healthy: capacity stays known and the session holds a slot.
	if !result.Capacity.Known || result.Capacity.Active != 1 || result.Capacity.Available != 1 {
		t.Fatalf("capacity = %+v", result.Capacity)
	}
	claimed, err := fixture.tickets.Resolve(ticket.Slug)
	if err != nil {
		t.Fatal(err)
	}
	if claimed.ClaimedBy != session.Claimant {
		t.Fatal("sync released the claim of a waiting ticket")
	}
}

func TestSyncWaitingWithDeadAgentIsNotStuck(t *testing.T) {
	fixture := withObserver(newLocalFixture(t), &fakeObserver{observation: AgentObservation{Complete: true, Found: true, Live: false}})
	ticket := fixture.createTicket(t, "Fix auth")
	session := dispatchRunning(t, fixture, ticket.Slug)
	if _, err := fixture.tickets.Ask(ticket.Slug, session.Claimant, "Which provider?", false); err != nil {
		t.Fatal(err)
	}

	result, err := fixture.service.Sync("core", false)
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}
	codes := diagnosticCodes(result)
	if !codes[SyncFindingWaitingOnInput] || codes[SyncFindingStuck] {
		t.Fatalf("diagnostics = %+v", result.Diagnostics)
	}
	if !strings.Contains(result.Diagnostics[0].Hint, "twt agents resume") {
		t.Fatalf("dead-agent waiting hint = %q", result.Diagnostics[0].Hint)
	}
}
