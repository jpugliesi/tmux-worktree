package cursorcloud

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jpugliesi/tmux-worktree/internal/clierr"
	"github.com/jpugliesi/tmux-worktree/internal/domain"
	"github.com/jpugliesi/tmux-worktree/internal/store"
	ticketservice "github.com/jpugliesi/tmux-worktree/internal/ticket"
)

type fakeHarness struct {
	dispatchResult DispatchResult
	dispatchErr    error
	dispatchCalls  []DispatchRequest
	dispatchFunc   func(context.Context, DispatchRequest) (DispatchResult, error)
	syncResult     SyncResult
	syncErr        error
	syncCalls      []SyncRequest
	syncFunc       func(context.Context, SyncRequest) (SyncResult, error)
}

func (f *fakeHarness) Dispatch(ctx context.Context, request DispatchRequest) (DispatchResult, error) {
	f.dispatchCalls = append(f.dispatchCalls, request)
	if f.dispatchFunc != nil {
		return f.dispatchFunc(ctx, request)
	}
	return f.dispatchResult, f.dispatchErr
}

func (f *fakeHarness) Sync(ctx context.Context, request SyncRequest) (SyncResult, error) {
	f.syncCalls = append(f.syncCalls, request)
	if f.syncFunc != nil {
		return f.syncFunc(ctx, request)
	}
	return f.syncResult, f.syncErr
}

type cloudFixture struct {
	service *Service
	tickets *ticketservice.Service
	harness *fakeHarness
	state   string
}

func newCloudFixture(t *testing.T) cloudFixture {
	t.Helper()
	config := t.TempDir()
	state := t.TempDir()
	home := t.TempDir()
	tickets := ticketservice.NewService(ticketservice.Options{Home: home, StateDir: state})
	if _, err := tickets.Init(false); err != nil {
		t.Fatalf("Init: %v", err)
	}
	template := domain.Template{
		Version: domain.TemplateVersion,
		Name:    "product",
		Repositories: []domain.RepositorySpec{
			{Name: "api", Clone: domain.CloneSpec{URL: "https://github.com/acme/api.git"}, DefaultBranch: "main"},
			{Name: "web", Clone: domain.CloneSpec{URL: "git@github.com:acme/web.git"}, DefaultBranch: "trunk"},
		},
		CursorCloud: &domain.CursorCloudSpec{
			Effort:         domain.CursorCloudEffortLarge,
			Instructions:   "Follow the repository rules.",
			MaxConcurrency: 2,
			Repositories: []domain.CursorCloudRepositorySpec{
				{Name: "api"},
				{Name: "web", URL: "https://github.com/acme/web.git", StartingRef: "release"},
			},
		},
	}
	templates := store.NewTemplateStore(config)
	if err := templates.Create(template); err != nil {
		t.Fatalf("Create Template: %v", err)
	}
	if _, err := tickets.CreateProjectWithTemplate("core", "product", false); err != nil {
		t.Fatalf("Create Project: %v", err)
	}
	harness := &fakeHarness{dispatchResult: DispatchResult{
		AgentID: "bc-agent", RunID: "run-one", RequestID: "request-one",
		Effort: EffectiveEffort{Kind: "parameter", Value: "high"},
	}}
	now := time.Date(2026, 8, 25, 12, 0, 0, 0, time.UTC)
	service := NewService(ServiceOptions{
		StateDir: state, Templates: templates, Tickets: tickets, Harness: harness,
		Now: func() time.Time { return now }, NewID: func() (string, error) { return "0123456789abcdef0123456789abcdef", nil },
	})
	return cloudFixture{service: service, tickets: tickets, harness: harness, state: state}
}

func (f cloudFixture) createTicket(t *testing.T, title string, blockedBy ...string) domain.Ticket {
	t.Helper()
	result, err := f.tickets.Create(ticketservice.CreateRequest{
		Title: title, Body: "Implement the acceptance criteria.\n", Project: "core",
		Status: domain.TicketReadyForAgent, Priority: 1, BlockedBy: blockedBy,
	}, false)
	if err != nil {
		t.Fatalf("Create Ticket: %v", err)
	}
	return result.Ticket
}

func TestDispatchFreezesPromptAndClaimsTheTicketBeforeTheHarnessCall(t *testing.T) {
	fixture := newCloudFixture(t)
	ticket := fixture.createTicket(t, "Fix auth")

	session, err := fixture.service.Dispatch(context.Background(), DispatchOptions{TicketRef: ticket.Slug, Mode: domain.CursorCloudModeAgent})
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if session.Status != domain.CursorCloudRunning || session.CursorAgentID != "bc-agent" || session.RunID != "run-one" {
		t.Fatalf("Session = %+v", session)
	}
	if session.EffectiveEffort.Kind != "parameter" || session.EffectiveEffort.Value != "high" {
		t.Fatalf("EffectiveEffort = %+v", session.EffectiveEffort)
	}
	if len(fixture.harness.dispatchCalls) != 1 {
		t.Fatalf("dispatch calls = %d", len(fixture.harness.dispatchCalls))
	}
	request := fixture.harness.dispatchCalls[0]
	if !strings.HasPrefix(request.Prompt, "Follow the repository rules.\n\n") ||
		!strings.Contains(request.Prompt, "Implement the acceptance criteria.") {
		t.Fatalf("prompt = %q", request.Prompt)
	}
	if len(request.Repositories) != 2 || request.Repositories[1].StartingRef != "release" {
		t.Fatalf("repositories = %+v", request.Repositories)
	}
	claimed, err := fixture.tickets.Resolve(ticket.Slug)
	if err != nil || claimed.ClaimedBy != "cursor-cloud-01234567" {
		t.Fatalf("claimed Ticket = %+v, %v", claimed, err)
	}
	stored, err := store.NewCursorCloudSessionStore(fixture.state).Find(session.ID)
	if err != nil || stored.PromptSnapshot != request.Prompt {
		t.Fatalf("stored Session = %+v, %v", stored, err)
	}
}

func TestDispatchDryRunDoesNotClaimSaveOrCallCursor(t *testing.T) {
	fixture := newCloudFixture(t)
	ticket := fixture.createTicket(t, "Dry run")
	fixture.service.options.Harness = nil

	session, err := fixture.service.Dispatch(context.Background(), DispatchOptions{TicketRef: ticket.Slug, Mode: domain.CursorCloudModePlan, DryRun: true})
	if err != nil {
		t.Fatalf("Dispatch dry run: %v", err)
	}
	if session.Mode != domain.CursorCloudModePlan || len(fixture.harness.dispatchCalls) != 0 {
		t.Fatalf("dry-run Session = %+v; calls = %d", session, len(fixture.harness.dispatchCalls))
	}
	resolved, _ := fixture.tickets.Resolve(ticket.Slug)
	if resolved.ClaimedBy != "" {
		t.Fatalf("dry run claimed Ticket: %+v", resolved)
	}
	sessions, err := store.NewCursorCloudSessionStore(fixture.state).List()
	if err != nil || len(sessions) != 0 {
		t.Fatalf("dry-run stored Sessions = %+v, %v", sessions, err)
	}
}

func TestDispatchKeepsTheClaimAfterAnUncertainFailure(t *testing.T) {
	fixture := newCloudFixture(t)
	ticket := fixture.createTicket(t, "Network failure")
	fixture.harness.dispatchErr = &Error{Kind: "network", Message: "request timed out", Retryable: true}

	session, err := fixture.service.Dispatch(context.Background(), DispatchOptions{TicketRef: ticket.Slug, Mode: domain.CursorCloudModeAgent})
	if err == nil || session.Status != domain.CursorCloudCreatingUnknown {
		t.Fatalf("Dispatch = %+v, %v", session, err)
	}
	resolved, _ := fixture.tickets.Resolve(ticket.Slug)
	if resolved.ClaimedBy == "" {
		t.Fatal("uncertain dispatch failure cleared the Ticket claim")
	}
}

func TestAbandonCannotChangeASessionWhileDispatchCreatesTheRemoteAgent(t *testing.T) {
	fixture := newCloudFixture(t)
	ticket := fixture.createTicket(t, "Concurrent abandon")
	dispatchStarted := make(chan struct{})
	finishDispatch := make(chan struct{})
	fixture.harness.dispatchFunc = func(_ context.Context, _ DispatchRequest) (DispatchResult, error) {
		close(dispatchStarted)
		<-finishDispatch
		return fixture.harness.dispatchResult, nil
	}
	dispatchDone := make(chan error, 1)
	go func() {
		_, err := fixture.service.Dispatch(context.Background(), DispatchOptions{
			TicketRef: ticket.Slug, Mode: domain.CursorCloudModeAgent,
		})
		dispatchDone <- err
	}()
	<-dispatchStarted

	sessions, err := store.NewCursorCloudSessionStore(fixture.state).List()
	if err != nil || len(sessions) != 1 {
		t.Fatalf("creating Sessions = %+v, %v", sessions, err)
	}
	_, abandonErr := fixture.service.Abandon(sessions[0].ID, false)
	close(finishDispatch)
	if clierr.CodeOf(abandonErr) != clierr.Locked {
		t.Fatalf("Abandon during dispatch = %v, want locked", abandonErr)
	}
	if err := <-dispatchDone; err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	stored, err := store.NewCursorCloudSessionStore(fixture.state).Find(sessions[0].ID)
	if err != nil || stored.Status != domain.CursorCloudRunning {
		t.Fatalf("stored Session = %+v, %v", stored, err)
	}
}

func TestSyncRecoversRemoteIDsFromCursorMetadata(t *testing.T) {
	fixture := newCloudFixture(t)
	ticket := fixture.createTicket(t, "Recover IDs")
	fixture.harness.dispatchErr = &Error{Kind: "network", Message: "request timed out", Retryable: true}
	session, err := fixture.service.Dispatch(context.Background(), DispatchOptions{TicketRef: ticket.Slug, Mode: domain.CursorCloudModeAgent})
	if err == nil {
		t.Fatal("Dispatch succeeded")
	}
	fixture.harness.syncResult = SyncResult{Sessions: []SyncObservation{{
		SessionID: session.ID, AgentID: "bc-recovered", RunID: "run-recovered", Status: "running",
	}}}

	result, err := fixture.service.Sync(context.Background(), "core", false)
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if len(result.Sessions) != 1 || result.Sessions[0].CursorAgentID != "bc-recovered" || result.Sessions[0].RunID != "run-recovered" {
		t.Fatalf("Sync result = %+v", result)
	}
	if !result.Capacity.Known || result.Capacity.Maximum != 2 || result.Capacity.Active != 1 || result.Capacity.Available != 1 {
		t.Fatalf("Sync capacity = %+v", result.Capacity)
	}
	if len(fixture.harness.syncCalls) != 1 || fixture.harness.syncCalls[0].Sessions[0].AgentID != "" {
		t.Fatalf("sync request = %+v", fixture.harness.syncCalls)
	}
}

func TestDispatchReleasesTheClaimAfterADefiniteFailure(t *testing.T) {
	fixture := newCloudFixture(t)
	ticket := fixture.createTicket(t, "Configuration failure")
	fixture.harness.dispatchErr = &Error{Kind: "configuration", Message: "GitHub is not connected"}

	session, err := fixture.service.Dispatch(context.Background(), DispatchOptions{TicketRef: ticket.Slug, Mode: domain.CursorCloudModeAgent})
	if err == nil || session.Status != domain.CursorCloudFailed || !session.TicketTransitioned {
		t.Fatalf("Dispatch = %+v, %v", session, err)
	}
	resolved, _ := fixture.tickets.Resolve(ticket.Slug)
	if resolved.Status != domain.TicketReadyForAgent || resolved.ClaimedBy != "" {
		t.Fatalf("Ticket after definite failure = %+v", resolved)
	}
}

func TestDispatchEnforcesProjectCapacityAndAcceptsAnExplicitOverride(t *testing.T) {
	fixture := newCloudFixture(t)
	ids := []string{
		"11111111111111111111111111111111",
		"22222222222222222222222222222222",
		"33333333333333333333333333333333",
		"44444444444444444444444444444444",
	}
	fixture.service.options.NewID = func() (string, error) {
		id := ids[0]
		ids = ids[1:]
		return id, nil
	}
	first := fixture.createTicket(t, "Capacity one")
	second := fixture.createTicket(t, "Capacity two")
	third := fixture.createTicket(t, "Capacity three")
	for _, ticket := range []domain.Ticket{first, second} {
		if _, err := fixture.service.Dispatch(context.Background(), DispatchOptions{TicketRef: ticket.Slug, Mode: domain.CursorCloudModeAgent}); err != nil {
			t.Fatalf("Dispatch %s: %v", ticket.Slug, err)
		}
	}
	if _, err := fixture.service.Dispatch(context.Background(), DispatchOptions{TicketRef: third.Slug, Mode: domain.CursorCloudModeAgent}); clierr.CodeOf(err) != clierr.PreconditionFailed {
		t.Fatalf("Dispatch above capacity = %v, want precondition_failed", err)
	}
	if _, err := fixture.service.Dispatch(context.Background(), DispatchOptions{
		TicketRef: third.Slug, Mode: domain.CursorCloudModeAgent, MaxConcurrency: 3,
	}); err != nil {
		t.Fatalf("Dispatch with override: %v", err)
	}
}

func TestConcurrentDispatchesCannotTakeTheSameCapacitySlot(t *testing.T) {
	fixture := newCloudFixture(t)
	first := fixture.createTicket(t, "Concurrent one")
	second := fixture.createTicket(t, "Concurrent two")

	services := []*Service{
		NewService(ServiceOptions{
			StateDir: fixture.state, Templates: fixture.service.options.Templates, Tickets: fixture.tickets,
			Harness: &fakeHarness{dispatchResult: fixture.harness.dispatchResult},
			Now:     fixture.service.options.Now, NewID: func() (string, error) { return "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", nil },
		}),
		NewService(ServiceOptions{
			StateDir: fixture.state, Templates: fixture.service.options.Templates, Tickets: fixture.tickets,
			Harness: &fakeHarness{dispatchResult: fixture.harness.dispatchResult},
			Now:     fixture.service.options.Now, NewID: func() (string, error) { return "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", nil },
		}),
	}
	tickets := []domain.Ticket{first, second}
	start := make(chan struct{})
	errorsByDispatch := make([]error, len(services))
	var workers sync.WaitGroup
	for index := range services {
		workers.Add(1)
		go func() {
			defer workers.Done()
			<-start
			_, errorsByDispatch[index] = services[index].Dispatch(context.Background(), DispatchOptions{
				TicketRef: tickets[index].Slug, Mode: domain.CursorCloudModeAgent, MaxConcurrency: 1,
			})
		}()
	}
	close(start)
	workers.Wait()

	successes := 0
	for _, dispatchErr := range errorsByDispatch {
		if dispatchErr == nil {
			successes++
			continue
		}
		if code := clierr.CodeOf(dispatchErr); code != clierr.Locked && code != clierr.PreconditionFailed {
			t.Fatalf("dispatch error = %v, code = %s", dispatchErr, code)
		}
	}
	if successes != 1 {
		t.Fatalf("successful dispatches = %d, errors = %v", successes, errorsByDispatch)
	}
	sessions, err := store.NewCursorCloudSessionStore(fixture.state).List()
	active := 0
	for _, session := range sessions {
		if session.Active() {
			active++
		}
	}
	if err != nil || active != 1 {
		t.Fatalf("active Sessions after concurrent dispatch = %d, %v", active, err)
	}
}

func TestSyncCapturesPullRequestsAndMovesTheTicketToHumanReview(t *testing.T) {
	fixture := newCloudFixture(t)
	ticket := fixture.createTicket(t, "Fix auth")
	session, err := fixture.service.Dispatch(context.Background(), DispatchOptions{TicketRef: ticket.Slug, Mode: domain.CursorCloudModeAgent})
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	fixture.harness.syncResult = SyncResult{Sessions: []SyncObservation{{
		SessionID: session.ID, Status: "finished", Result: "Implemented.", Repositories: []RepositoryResult{{
			URL: "https://github.com/acme/api.git", Branch: "cursor/fix-auth", PRURL: "https://github.com/acme/api/pull/42",
		}},
	}}}

	result, err := fixture.service.Sync(context.Background(), "core", false)
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if len(result.Sessions) != 1 || !result.Sessions[0].TicketTransitioned || result.Sessions[0].Repositories[0].PRURL == "" {
		t.Fatalf("Sync result = %+v", result)
	}
	if !result.Capacity.Known || result.Capacity.Active != 0 || result.Capacity.Available != 2 {
		t.Fatalf("Sync capacity = %+v", result.Capacity)
	}
	resolved, _ := fixture.tickets.Resolve(ticket.Slug)
	if resolved.Status != domain.TicketReadyForHuman || resolved.ClaimedBy != "" {
		t.Fatalf("Ticket after sync = %+v", resolved)
	}
	if len(resolved.PullRequests) != 1 || resolved.PullRequests[0] != "https://github.com/acme/api/pull/42" {
		t.Fatalf("Ticket pull requests after sync = %v", resolved.PullRequests)
	}
}

func TestSyncDryRunLeavesTheSessionAndTicketUntouched(t *testing.T) {
	fixture := newCloudFixture(t)
	ticket := fixture.createTicket(t, "Fix auth")
	session, err := fixture.service.Dispatch(context.Background(), DispatchOptions{TicketRef: ticket.Slug, Mode: domain.CursorCloudModeAgent})
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	fixture.harness.syncResult = SyncResult{Sessions: []SyncObservation{{
		SessionID: session.ID, Status: "finished", Result: "Implemented.", Repositories: []RepositoryResult{{
			URL: "https://github.com/acme/api.git", Branch: "cursor/fix-auth", PRURL: "https://github.com/acme/api/pull/42",
		}},
	}}}

	result, err := fixture.service.Sync(context.Background(), "core", true)
	if err != nil {
		t.Fatalf("dry-run Sync: %v", err)
	}
	if len(result.Sessions) != 1 {
		t.Fatalf("dry-run Sync result = %+v", result)
	}
	if result.Sessions[0].TicketTransitioned {
		t.Fatal("dry-run sync reported an in-memory ticket transition")
	}
	saved, err := store.NewCursorCloudSessionStore(fixture.state).Find(session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if saved.Status != domain.CursorCloudRunning || saved.TicketTransitioned {
		t.Fatalf("dry-run sync changed the saved session: %+v", saved)
	}
	resolved, _ := fixture.tickets.Resolve(ticket.Slug)
	if resolved.ClaimedBy != session.Claimant || resolved.Status != domain.TicketReadyForAgent {
		t.Fatalf("dry-run sync changed the ticket: %+v", resolved)
	}
}

func TestSyncMarksAnAgentRunWithoutAPullRequestForHumanReview(t *testing.T) {
	fixture := newCloudFixture(t)
	ticket := fixture.createTicket(t, "No changes")
	session, err := fixture.service.Dispatch(context.Background(), DispatchOptions{TicketRef: ticket.Slug, Mode: domain.CursorCloudModeAgent})
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	fixture.harness.syncResult = SyncResult{Sessions: []SyncObservation{{
		SessionID: session.ID, Status: "finished", Result: "Changes are ready without a pull request.",
		Repositories: []RepositoryResult{{URL: "https://github.com/acme/api.git", Branch: "cursor/fix-auth"}},
	}}}

	result, err := fixture.service.Sync(context.Background(), "core", false)
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if !result.Sessions[0].HandoffIncomplete {
		t.Fatalf("Sync result = %+v", result)
	}
}

func TestSyncLeavesUnknownRunsClaimed(t *testing.T) {
	fixture := newCloudFixture(t)
	ticket := fixture.createTicket(t, "Unknown run")
	session, err := fixture.service.Dispatch(context.Background(), DispatchOptions{TicketRef: ticket.Slug, Mode: domain.CursorCloudModeAgent})
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	fixture.harness.syncResult = SyncResult{Sessions: []SyncObservation{{
		SessionID: session.ID, Status: "unknown", Error: &Error{Kind: "network", Message: "timeout", Retryable: true},
	}}}

	result, err := fixture.service.Sync(context.Background(), "core", false)
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if result.Sessions[0].Status != domain.CursorCloudRunUnknown || result.Sessions[0].RequestID != "request-one" {
		t.Fatalf("Sync result = %+v", result)
	}
	resolved, _ := fixture.tickets.Resolve(ticket.Slug)
	if resolved.ClaimedBy == "" {
		t.Fatal("unknown run cleared the claim")
	}
}

func TestSyncDoesNotRegressAnAbandonedSession(t *testing.T) {
	fixture := newCloudFixture(t)
	ticket := fixture.createTicket(t, "Concurrent sync")
	session, err := fixture.service.Dispatch(context.Background(), DispatchOptions{TicketRef: ticket.Slug, Mode: domain.CursorCloudModeAgent})
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	syncStarted := make(chan struct{})
	finishSync := make(chan struct{})
	fixture.harness.syncFunc = func(_ context.Context, _ SyncRequest) (SyncResult, error) {
		close(syncStarted)
		<-finishSync
		return SyncResult{Sessions: []SyncObservation{{SessionID: session.ID, Status: "running"}}}, nil
	}
	syncDone := make(chan error, 1)
	go func() {
		_, err := fixture.service.Sync(context.Background(), "core", false)
		syncDone <- err
	}()
	<-syncStarted

	abandoned, err := fixture.service.Abandon(session.ID, false)
	close(finishSync)
	if err != nil {
		t.Fatalf("Abandon: %v", err)
	}
	if abandoned.Status != domain.CursorCloudCancelled {
		t.Fatalf("abandoned Session = %+v", abandoned)
	}
	if err := <-syncDone; err != nil {
		t.Fatalf("Sync: %v", err)
	}
	stored, err := store.NewCursorCloudSessionStore(fixture.state).Find(session.ID)
	if err != nil || stored.Status != domain.CursorCloudCancelled || !stored.TicketTransitioned {
		t.Fatalf("stored Session = %+v, %v", stored, err)
	}
}

func TestSyncContinuesAfterOneSessionCannotBeApplied(t *testing.T) {
	fixture := newCloudFixture(t)
	ids := []string{
		"11111111111111111111111111111111",
		"22222222222222222222222222222222",
	}
	fixture.service.options.NewID = func() (string, error) {
		id := ids[0]
		ids = ids[1:]
		return id, nil
	}
	first := fixture.createTicket(t, "Bad observation")
	second := fixture.createTicket(t, "Good observation")
	firstSession, err := fixture.service.Dispatch(context.Background(), DispatchOptions{TicketRef: first.Slug, Mode: domain.CursorCloudModeAgent})
	if err != nil {
		t.Fatalf("Dispatch first: %v", err)
	}
	secondSession, err := fixture.service.Dispatch(context.Background(), DispatchOptions{TicketRef: second.Slug, Mode: domain.CursorCloudModeAgent})
	if err != nil {
		t.Fatalf("Dispatch second: %v", err)
	}
	fixture.harness.syncResult = SyncResult{Sessions: []SyncObservation{
		{SessionID: firstSession.ID, Status: "not-a-status"},
		{SessionID: secondSession.ID, AgentID: "bc-second", RunID: "run-second", Status: "running"},
	}}

	result, err := fixture.service.Sync(context.Background(), "core", false)
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if len(result.Diagnostics) != 1 || result.Diagnostics[0].SessionID != firstSession.ID {
		t.Fatalf("diagnostics = %+v", result.Diagnostics)
	}
	if len(result.Sessions) != 2 || result.Sessions[1].CursorAgentID != "bc-second" {
		t.Fatalf("sessions = %+v", result.Sessions)
	}
	if result.Capacity.Known || result.Capacity.Available != 0 {
		t.Fatalf("capacity with a diagnostic = %+v", result.Capacity)
	}
}

func TestAbandonRecoversASessionSavedBeforeTheTicketClaim(t *testing.T) {
	fixture := newCloudFixture(t)
	ticket := fixture.createTicket(t, "Interrupted reservation")
	preview, err := fixture.service.Dispatch(context.Background(), DispatchOptions{
		TicketRef: ticket.Slug, Mode: domain.CursorCloudModeAgent, DryRun: true,
	})
	if err != nil {
		t.Fatalf("Dispatch dry run: %v", err)
	}
	if err := store.NewCursorCloudSessionStore(fixture.state).Save(preview); err != nil {
		t.Fatalf("Save interrupted Session: %v", err)
	}

	abandoned, err := fixture.service.Abandon(preview.ID[:8], false)
	if err != nil {
		t.Fatalf("Abandon: %v", err)
	}
	if abandoned.Status != domain.CursorCloudCancelled || !abandoned.TicketTransitioned {
		t.Fatalf("abandoned Session = %+v", abandoned)
	}
	resolved, err := fixture.tickets.Resolve(ticket.Slug)
	if err != nil || resolved.ClaimedBy != "" || resolved.Status != domain.TicketReadyForAgent {
		t.Fatalf("Ticket after abandon = %+v, %v", resolved, err)
	}
	queue, err := fixture.tickets.Queue("core", 0)
	if err != nil || len(queue.Ready) != 1 || queue.Ready[0].Slug != ticket.Slug {
		t.Fatalf("queue after abandon = %+v, %v", queue, err)
	}
}

func TestAbandonPreservesANewerTicketClaim(t *testing.T) {
	fixture := newCloudFixture(t)
	ticket := fixture.createTicket(t, "Preserve new owner")
	session, err := fixture.service.Dispatch(context.Background(), DispatchOptions{TicketRef: ticket.Slug, Mode: domain.CursorCloudModeAgent})
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if _, err := fixture.tickets.Unclaim(ticket.Slug, session.Claimant, false); err != nil {
		t.Fatalf("Unclaim Cloud Session: %v", err)
	}
	if _, err := fixture.tickets.Claim(ticket.Slug, "human-owner", false); err != nil {
		t.Fatalf("Claim Ticket: %v", err)
	}

	abandoned, err := fixture.service.Abandon(session.ID, false)
	if err != nil {
		t.Fatalf("Abandon: %v", err)
	}
	if !abandoned.TicketTransitioned {
		t.Fatalf("abandoned Session = %+v", abandoned)
	}
	resolved, err := fixture.tickets.Resolve(ticket.Slug)
	if err != nil || resolved.ClaimedBy != "human-owner" || resolved.Status != domain.TicketReadyForAgent {
		t.Fatalf("Ticket after abandon = %+v, %v", resolved, err)
	}
}
