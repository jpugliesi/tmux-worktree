package localdispatch

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jpugliesi/tmux-worktree/internal/clierr"
	"github.com/jpugliesi/tmux-worktree/internal/domain"
	"github.com/jpugliesi/tmux-worktree/internal/store"
	ticketservice "github.com/jpugliesi/tmux-worktree/internal/ticket"
)

type fakeLauncher struct {
	validateErr   error
	launchResult  LaunchResult
	launchErr     error
	validateFunc  func(LaunchRequest) error
	launchFunc    func(LaunchRequest) (LaunchResult, error)
	validateCalls []LaunchRequest
	launchCalls   []LaunchRequest
}

func (f *fakeLauncher) Validate(request LaunchRequest) error {
	f.validateCalls = append(f.validateCalls, request)
	if f.validateFunc != nil {
		return f.validateFunc(request)
	}
	return f.validateErr
}

func (f *fakeLauncher) Launch(request LaunchRequest) (LaunchResult, error) {
	f.launchCalls = append(f.launchCalls, request)
	if f.launchFunc != nil {
		return f.launchFunc(request)
	}
	return f.launchResult, f.launchErr
}

type localFixture struct {
	service  *Service
	tickets  *ticketservice.Service
	launcher *fakeLauncher
	state    string
}

func startedResult() LaunchResult {
	return LaunchResult{
		WorkspaceID:    "ws0123456789abcdef",
		WorkspaceName:  "fix-auth",
		TmuxSession:    "fix-auth",
		AgentSessionID: "agent0123",
		AgentStarted:   true,
	}
}

func newLocalFixture(t *testing.T) localFixture {
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
		},
		LocalDispatch: &domain.LocalDispatchSpec{
			Provider:       "grok",
			Effort:         domain.DispatchEffortLarge,
			Instructions:   "Use the repo skills.",
			MaxConcurrency: 2,
		},
	}
	templates := store.NewTemplateStore(config)
	if err := templates.Create(template); err != nil {
		t.Fatalf("Create Template: %v", err)
	}
	if _, err := tickets.CreateProjectWithTemplate("core", "product", false); err != nil {
		t.Fatalf("Create Project: %v", err)
	}
	launcher := &fakeLauncher{launchResult: startedResult()}
	now := time.Date(2026, 8, 25, 12, 0, 0, 0, time.UTC)
	service := NewService(ServiceOptions{
		StateDir:  state,
		Templates: templates,
		Tickets:   tickets,
		Launcher:  launcher,
		Config:    store.TicketAgentConfig{Provider: "codex", Effort: "medium"},
		LookPath:  func(name string) (string, error) { return "/bin/" + name, nil },
		Now:       func() time.Time { return now },
		NewID:     func() (string, error) { return "0123456789abcdef0123456789abcdef", nil },
	})
	return localFixture{service: service, tickets: tickets, launcher: launcher, state: state}
}

func (f localFixture) createTicket(t *testing.T, title string, blockedBy ...string) domain.Ticket {
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

func TestDispatchStartsAnImplementationAgentAndClaimsTheTicket(t *testing.T) {
	fixture := newLocalFixture(t)
	ticket := fixture.createTicket(t, "Fix auth")

	session, err := fixture.service.Dispatch(DispatchOptions{TicketRef: ticket.Slug, Mode: domain.DispatchModeAgent})
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if session.Status != domain.LocalDispatchRunning {
		t.Fatalf("Status = %q, want running", session.Status)
	}
	if session.Claimant != "twt-local-01234567" {
		t.Fatalf("Claimant = %q", session.Claimant)
	}
	if session.Provider != "grok" || session.AgentLabel != "ticket-impl" {
		t.Fatalf("Provider/Label = %q/%q", session.Provider, session.AgentLabel)
	}
	if session.WorkspaceID != "ws0123456789abcdef" || session.AgentSessionID != "agent0123" {
		t.Fatalf("workspace/agent = %q/%q", session.WorkspaceID, session.AgentSessionID)
	}
	claimed, err := fixture.tickets.Resolve(ticket.Slug)
	if err != nil {
		t.Fatal(err)
	}
	if claimed.ClaimedBy != session.Claimant {
		t.Fatalf("ClaimedBy = %q, want %q", claimed.ClaimedBy, session.Claimant)
	}
	saved, err := store.NewLocalDispatchSessionStore(fixture.state).Find(session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if saved.Status != domain.LocalDispatchRunning {
		t.Fatalf("saved Status = %q", saved.Status)
	}
	if len(fixture.launcher.launchCalls) != 1 {
		t.Fatalf("launch calls = %d", len(fixture.launcher.launchCalls))
	}
	request := fixture.launcher.launchCalls[0]
	if request.Name != ticket.Slug || request.AgentLabel != "ticket-impl" || request.Project != "core" {
		t.Fatalf("request = %+v", request)
	}
	injected := request.Template.Agents[len(request.Template.Agents)-1]
	if injected.Label != "ticket-impl" || injected.Provider != "grok" || !injected.PreferProviderResume {
		t.Fatalf("injected agent = %+v", injected)
	}
	if injected.PreferredPane == nil || injected.PreferredPane.Repository != "api" || injected.PreferredPane.Index != 3 {
		t.Fatalf("injected agent preferred pane = %+v", injected.PreferredPane)
	}
	prompt := injected.Start[len(injected.Start)-1]
	for _, want := range []string{
		"Use the repo skills.",
		"Implement twt Ticket `" + ticket.Slug + "`.",
		"twt tickets complete " + ticket.Slug + " --as twt-local-01234567",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt lacks %q:\n%s", want, prompt)
		}
	}
	if session.PromptSnapshot != prompt {
		t.Fatal("prompt snapshot does not match the injected agent prompt")
	}
}

func TestDispatchPlanModeRoutesToThePlanningLaunch(t *testing.T) {
	fixture := newLocalFixture(t)
	ticket := fixture.createTicket(t, "Fix auth")

	session, err := fixture.service.Dispatch(DispatchOptions{TicketRef: ticket.Slug, Mode: domain.DispatchModePlan})
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if session.AgentLabel != "ticket-plan" {
		t.Fatalf("AgentLabel = %q, want ticket-plan", session.AgentLabel)
	}
	request := fixture.launcher.launchCalls[0]
	injected := request.Template.Agents[len(request.Template.Agents)-1]
	// Planning agents run in normal autonomous mode with a plan-only prompt
	// contract; the planning prompt carries the tickets plan write.
	prompt := strings.Join(injected.Start, " ")
	if !strings.Contains(prompt, "HARD RULE: plan only") || !strings.Contains(prompt, "twt tickets plan fix-auth --stdin") {
		t.Fatalf("plan launch = %v", injected.Start)
	}
}

func TestDispatchValidatesBeforeTheClaim(t *testing.T) {
	fixture := newLocalFixture(t)
	ticket := fixture.createTicket(t, "Fix auth")
	fixture.launcher.validateErr = clierr.New(clierr.Locked, "ticket is linked to active Workspace")

	_, err := fixture.service.Dispatch(DispatchOptions{TicketRef: ticket.Slug, Mode: domain.DispatchModeAgent})
	if clierr.CodeOf(err) != clierr.Locked {
		t.Fatalf("Dispatch error = %v, want locked", err)
	}
	unclaimed, err := fixture.tickets.Resolve(ticket.Slug)
	if err != nil {
		t.Fatal(err)
	}
	if unclaimed.ClaimedBy != "" {
		t.Fatalf("ClaimedBy = %q after a failed validate, want empty", unclaimed.ClaimedBy)
	}
	if sessions, _ := store.NewLocalDispatchSessionStore(fixture.state).List(); len(sessions) != 0 {
		t.Fatalf("sessions saved after a failed validate: %d", len(sessions))
	}
	if len(fixture.launcher.launchCalls) != 0 {
		t.Fatal("launch ran after a failed validate")
	}
}

func TestDispatchLaunchFailureReturnsTheTicketAndNamesTheWorkspace(t *testing.T) {
	fixture := newLocalFixture(t)
	ticket := fixture.createTicket(t, "Fix auth")
	fixture.launcher.launchResult = LaunchResult{WorkspaceID: "ws-partial"}
	fixture.launcher.launchErr = errors.New("workspace setup failed")

	session, err := fixture.service.Dispatch(DispatchOptions{TicketRef: ticket.Slug, Mode: domain.DispatchModeAgent})
	if err == nil {
		t.Fatal("Dispatch succeeded despite a launch failure")
	}
	if !strings.Contains(clierr.HintOf(err), "ws-partial") {
		t.Fatalf("failure hint does not name the Workspace: %v", err)
	}
	if session.Status != domain.LocalDispatchFailed || !session.TicketTransitioned {
		t.Fatalf("session = %+v", session)
	}
	returned, err := fixture.tickets.Resolve(ticket.Slug)
	if err != nil {
		t.Fatal(err)
	}
	if returned.ClaimedBy != "" || returned.Status != domain.TicketReadyForAgent {
		t.Fatalf("ticket after failure = %q %q", returned.ClaimedBy, returned.Status)
	}
	saved, err := store.NewLocalDispatchSessionStore(fixture.state).Find(session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if saved.Status != domain.LocalDispatchFailed || saved.WorkspaceID != "ws-partial" {
		t.Fatalf("saved = %+v", saved)
	}
}

func TestDispatchTreatsAPanelessAgentAsAFailure(t *testing.T) {
	fixture := newLocalFixture(t)
	ticket := fixture.createTicket(t, "Fix auth")
	result := startedResult()
	result.AgentStarted = false
	fixture.launcher.launchResult = result

	session, err := fixture.service.Dispatch(DispatchOptions{TicketRef: ticket.Slug, Mode: domain.DispatchModeAgent})
	if clierr.CodeOf(err) != clierr.UnsafeState {
		t.Fatalf("Dispatch error = %v, want unsafe_state", err)
	}
	if session.Status != domain.LocalDispatchFailed {
		t.Fatalf("Status = %q, want failed", session.Status)
	}
	returned, err := fixture.tickets.Resolve(ticket.Slug)
	if err != nil {
		t.Fatal(err)
	}
	if returned.ClaimedBy != "" {
		t.Fatalf("ClaimedBy = %q after a paneless agent, want empty", returned.ClaimedBy)
	}
}

func TestDispatchEnforcesTheLocalCapacity(t *testing.T) {
	fixture := newLocalFixture(t)
	first := fixture.createTicket(t, "One")
	second := fixture.createTicket(t, "Two")
	third := fixture.createTicket(t, "Three")

	ids := []string{
		"aaaa0000000000000000000000000001",
		"aaaa0000000000000000000000000002",
		"aaaa0000000000000000000000000003",
	}
	fixture.service.options.NewID = func() (string, error) {
		id := ids[0]
		ids = ids[1:]
		return id, nil
	}
	for _, slug := range []string{first.Slug, second.Slug} {
		if _, err := fixture.service.Dispatch(DispatchOptions{TicketRef: slug, Mode: domain.DispatchModeAgent}); err != nil {
			t.Fatalf("dispatch %s: %v", slug, err)
		}
	}
	_, err := fixture.service.Dispatch(DispatchOptions{TicketRef: third.Slug, Mode: domain.DispatchModeAgent})
	if clierr.CodeOf(err) != clierr.PreconditionFailed {
		t.Fatalf("third dispatch error = %v, want precondition_failed (capacity 2)", err)
	}
	if len(fixture.launcher.launchCalls) != 2 {
		t.Fatalf("launch calls = %d, want 2", len(fixture.launcher.launchCalls))
	}
}

func TestDispatchRejectsASecondActiveSessionForTheTicket(t *testing.T) {
	fixture := newLocalFixture(t)
	ticket := fixture.createTicket(t, "Fix auth")
	if _, err := fixture.service.Dispatch(DispatchOptions{TicketRef: ticket.Slug, Mode: domain.DispatchModeAgent}); err != nil {
		t.Fatalf("first dispatch: %v", err)
	}
	fixture.service.options.NewID = func() (string, error) { return "bbbb0000000000000000000000000001", nil }
	_, err := fixture.service.Dispatch(DispatchOptions{TicketRef: ticket.Slug, Mode: domain.DispatchModeAgent})
	if clierr.CodeOf(err) != clierr.Locked {
		t.Fatalf("second dispatch error = %v, want locked", err)
	}
}

func TestDispatchDryRunWritesNothing(t *testing.T) {
	fixture := newLocalFixture(t)
	ticket := fixture.createTicket(t, "Fix auth")

	session, err := fixture.service.Dispatch(DispatchOptions{TicketRef: ticket.Slug, Mode: domain.DispatchModeAgent, DryRun: true})
	if err != nil {
		t.Fatalf("dry-run dispatch: %v", err)
	}
	if session.Status != domain.LocalDispatchCreating {
		t.Fatalf("dry-run Status = %q", session.Status)
	}
	if sessions, _ := store.NewLocalDispatchSessionStore(fixture.state).List(); len(sessions) != 0 {
		t.Fatalf("dry run saved %d sessions", len(sessions))
	}
	unclaimed, err := fixture.tickets.Resolve(ticket.Slug)
	if err != nil {
		t.Fatal(err)
	}
	if unclaimed.ClaimedBy != "" {
		t.Fatalf("dry run claimed the ticket: %q", unclaimed.ClaimedBy)
	}
	if len(fixture.launcher.launchCalls) != 0 {
		t.Fatal("dry run launched a workspace")
	}
	if len(fixture.launcher.validateCalls) != 1 {
		t.Fatal("dry run skipped launcher validation")
	}
}

func TestDispatchRefusesATicketThatIsNotReady(t *testing.T) {
	fixture := newLocalFixture(t)
	blocker := fixture.createTicket(t, "Blocker")
	blocked := fixture.createTicket(t, "Blocked", blocker.Slug)

	_, err := fixture.service.Dispatch(DispatchOptions{TicketRef: blocked.Slug, Mode: domain.DispatchModeAgent})
	if clierr.CodeOf(err) != clierr.PreconditionFailed {
		t.Fatalf("blocked dispatch error = %v, want precondition_failed", err)
	}
	if sessions, _ := store.NewLocalDispatchSessionStore(fixture.state).List(); len(sessions) != 0 {
		t.Fatalf("blocked dispatch saved %d sessions", len(sessions))
	}
}

func TestDispatchGatesAnUnapprovedPlan(t *testing.T) {
	fixture := newLocalFixture(t)
	ticket := fixture.createTicket(t, "Fix auth")
	if _, err := fixture.tickets.SetPlanSection(ticket.Slug, "", "1. Do the thing.", false); err != nil {
		t.Fatal(err)
	}
	_, err := fixture.service.Dispatch(DispatchOptions{TicketRef: ticket.Slug, Mode: domain.DispatchModeAgent})
	if clierr.CodeOf(err) != clierr.PreconditionFailed {
		t.Fatalf("unapproved-plan dispatch error = %v, want precondition_failed", err)
	}
	// Plan-mode dispatch stays free: that is how plans get written.
	if _, err := fixture.service.Dispatch(DispatchOptions{TicketRef: ticket.Slug, Mode: domain.DispatchModePlan, DryRun: true}); err != nil {
		t.Fatalf("plan-mode dispatch gated: %v", err)
	}
	if _, err := fixture.tickets.Approve(ticket.Slug, "john", "", false); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.service.Dispatch(DispatchOptions{TicketRef: ticket.Slug, Mode: domain.DispatchModeAgent, DryRun: true}); err != nil {
		t.Fatalf("approved-plan dispatch: %v", err)
	}
}

func TestDispatchFailsFastWhenTheProviderIsNotInstalled(t *testing.T) {
	fixture := newLocalFixture(t)
	ticket := fixture.createTicket(t, "Fix auth")
	fixture.service.options.LookPath = func(name string) (string, error) {
		return "", errors.New("not found")
	}

	_, err := fixture.service.Dispatch(DispatchOptions{TicketRef: ticket.Slug, Mode: domain.DispatchModeAgent})
	if clierr.CodeOf(err) != clierr.PreconditionFailed {
		t.Fatalf("missing provider error = %v, want precondition_failed", err)
	}
	unclaimed, err := fixture.tickets.Resolve(ticket.Slug)
	if err != nil {
		t.Fatal(err)
	}
	if unclaimed.ClaimedBy != "" {
		t.Fatal("missing provider mutated the ticket")
	}
}
