package agent

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/jpugliesi/tmux-worktree/internal/clierr"
	"github.com/jpugliesi/tmux-worktree/internal/domain"
	"github.com/jpugliesi/tmux-worktree/internal/store"
)

func TestRegisterReloadsWorkspaceInsideMutationLock(t *testing.T) {
	service := NewService(t.TempDir(), "")
	workspace := domain.Workspace{
		Version: domain.WorkspaceVersion,
		ID:      "workspace-that-was-removed",
		Name:    "removed",
		Status:  domain.WorkspaceActive,
	}

	_, err := service.Register(workspace, "command", "test", "", "", []string{"true"})
	if err == nil || !strings.Contains(err.Error(), "does not exist") {
		t.Fatalf("Register after Workspace removal error = %v", err)
	}
	agents, listErr := service.List(workspace.ID)
	if listErr != nil {
		t.Fatal(listErr)
	}
	if len(agents) != 0 {
		t.Fatalf("Register saved an Agent Session for a removed Workspace: %+v", agents)
	}
}

func TestValidateRegistrationRejectsUnsupportedTranscriptLink(t *testing.T) {
	service := NewService(t.TempDir(), "")
	workspace := domain.Workspace{ID: "workspace-one", Name: "workspace-one", Status: domain.WorkspaceActive}
	err := service.ValidateRegistration(workspace, "cursor", "", "cursor-session", []string{"cursor-agent", "resume"})
	if err == nil || !strings.Contains(err.Error(), "does not support verifiable linked transcripts") {
		t.Fatalf("Cursor transcript registration error = %v", err)
	}
	if err := service.ValidateRegistration(workspace, "cursor", "", "", []string{"cursor-agent", "resume"}); err != nil {
		t.Fatalf("Cursor registration without a transcript link: %v", err)
	}
}

func TestRegisterInfersProviderAndProviderSessionFromTheResumeCommand(t *testing.T) {
	tests := []struct {
		name            string
		resumeCommand   []string
		wantProvider    string
		wantSessionID   string
		wantErrorSubstr string
	}{
		{name: "codex resume", resumeCommand: []string{"codex", "resume", "session-one"}, wantProvider: "codex", wantSessionID: "session-one"},
		{name: "codex flag", resumeCommand: []string{"/opt/bin/codex", "--resume", "session-two"}, wantProvider: "codex", wantSessionID: "session-two"},
		{name: "claude flag value", resumeCommand: []string{"claude", "--resume=session-three"}, wantProvider: "claude", wantSessionID: "session-three"},
		{name: "grok resume", resumeCommand: []string{"grok", "--resume", "01a02626-4685-7c72-9679-5dbf6dec43ce"}, wantProvider: "grok", wantSessionID: "01a02626-4685-7c72-9679-5dbf6dec43ce"},
		{name: "grok short flag", resumeCommand: []string{"/opt/bin/grok", "-r", "session-grok"}, wantProvider: "grok", wantSessionID: "session-grok"},
		{name: "grok session flag", resumeCommand: []string{"grok", "--session", "session-from-flag"}, wantProvider: "grok", wantSessionID: "session-from-flag"},
		{name: "claude without a session", resumeCommand: []string{"claude", "--continue"}, wantProvider: "claude"},
		{name: "cursor keeps no link", resumeCommand: []string{"cursor-agent", "resume", "session-four"}, wantProvider: "cursor"},
		{name: "plain command", resumeCommand: []string{"./run-agent", "--resume", "session-five"}, wantProvider: "command"},
		{name: "shell has no provider", resumeCommand: []string{"bash", "-lc", "codex resume x"}, wantErrorSubstr: "cannot infer the provider"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			service, workspace := activeWorkspace(t)
			agent, err := service.Register(workspace, "", "", "", "", test.resumeCommand)
			if test.wantErrorSubstr != "" {
				if err == nil || !strings.Contains(err.Error(), test.wantErrorSubstr) {
					t.Fatalf("Register() error = %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if agent.Provider != test.wantProvider || agent.ProviderSessionID != test.wantSessionID {
				t.Fatalf("Register() provider = %q, provider session = %q", agent.Provider, agent.ProviderSessionID)
			}
			if agent.Label != test.wantProvider {
				t.Fatalf("Register() label = %q", agent.Label)
			}
		})
	}
}

func TestRegisterKeepsExplicitProviderAndSession(t *testing.T) {
	service, workspace := activeWorkspace(t)
	agent, err := service.Register(workspace, "command", "", "", "", []string{"codex", "resume", "session-one"})
	if err != nil {
		t.Fatal(err)
	}
	if agent.Provider != "command" || agent.ProviderSessionID != "" {
		t.Fatalf("explicit provider registration = %+v", agent)
	}
	linked, err := service.Register(workspace, "codex", "", "", "session-explicit", []string{"codex", "resume", "session-one"})
	if err != nil {
		t.Fatal(err)
	}
	if linked.ProviderSessionID != "session-explicit" {
		t.Fatalf("explicit provider session = %q", linked.ProviderSessionID)
	}
}

func TestRegisterMakesUniqueDefaultLabelsAndRejectsDuplicateLabels(t *testing.T) {
	service, workspace := activeWorkspace(t)
	for index, want := range []string{"codex", "codex-2", "codex-3"} {
		agent, err := service.Register(workspace, "", "", "", "", []string{"codex", "resume", fmt.Sprintf("session-%d", index)})
		if err != nil {
			t.Fatal(err)
		}
		if agent.Label != want {
			t.Fatalf("default label %d = %q, want %q", index, agent.Label, want)
		}
	}
	if _, err := service.Register(workspace, "", "review", "", "", []string{"codex", "resume", "session-review"}); err != nil {
		t.Fatal(err)
	}
	_, err := service.Register(workspace, "", "review", "", "", []string{"codex", "resume", "session-other"})
	if err == nil || !strings.Contains(err.Error(), "already in use") {
		t.Fatalf("duplicate label error = %v", err)
	}
	if clierr.CodeOf(err) != clierr.AlreadyExists {
		t.Fatalf("duplicate label code = %q", clierr.CodeOf(err))
	}
	if err := service.ValidateLabel(workspace.ID, "review"); err == nil {
		t.Fatal("ValidateLabel accepted a used label")
	}
	if err := service.ValidateLabel(workspace.ID, "free"); err != nil {
		t.Fatalf("ValidateLabel(free) = %v", err)
	}
}

func TestRegisterAndResumeExplainAnArchivedWorkspace(t *testing.T) {
	service, workspace := activeWorkspace(t)
	workspace.Status = domain.WorkspaceArchived
	if err := store.NewWorkspaceStore(service.stateDir).Save(workspace); err != nil {
		t.Fatal(err)
	}
	err := service.ValidateRegistration(workspace, "codex", "", "", []string{"codex", "resume", "session-one"})
	if err == nil || !strings.Contains(err.Error(), "is archived") {
		t.Fatalf("archived Workspace registration error = %v", err)
	}
	if hint := clierr.HintOf(err); !strings.Contains(hint, "workspaces open") {
		t.Fatalf("archived Workspace hint = %q", hint)
	}
	if clierr.CodeOf(err) != clierr.PreconditionFailed {
		t.Fatalf("archived Workspace code = %q", clierr.CodeOf(err))
	}
	resumeErr := service.ValidateResume(domain.AgentSession{ID: "agent-one", WorkspaceID: workspace.ID}, workspace)
	if resumeErr == nil || !strings.Contains(resumeErr.Error(), "is archived") {
		t.Fatalf("archived Workspace resume error = %v", resumeErr)
	}
}

func TestRemoveDeletesOnlyTheSelectedAgentSessionRecord(t *testing.T) {
	service, workspace := activeWorkspace(t)
	first, err := service.Register(workspace, "", "", "", "", []string{"codex", "resume", "session-one"})
	if err != nil {
		t.Fatal(err)
	}
	second, err := service.Register(workspace, "", "", "", "", []string{"codex", "resume", "session-two"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Remove(first.ID, "other-workspace"); err == nil || !strings.Contains(err.Error(), "does not belong") {
		t.Fatalf("Remove() for another Workspace error = %v", err)
	}
	if _, err := service.Remove(first.ID, workspace.ID); err != nil {
		t.Fatal(err)
	}
	remaining, err := service.List(workspace.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(remaining) != 1 || remaining[0].ID != second.ID {
		t.Fatalf("Agent Sessions after Remove() = %+v", remaining)
	}
	if _, err := service.Remove(first.ID, workspace.ID); clierr.CodeOf(err) != clierr.NotFound {
		t.Fatalf("Remove() of a missing Agent Session error = %v", err)
	}
}

func TestBuildSessionMakesARecordWithoutALockOrAStoreWrite(t *testing.T) {
	service := NewService(t.TempDir(), "")
	workspace := domain.Workspace{Version: domain.WorkspaceVersion, ID: "workspace-one", Name: "workspace-one", Status: domain.WorkspaceInitializing}
	now := time.Now().UTC()

	session, err := BuildSession(workspace, "codex", "review", "", "", []string{"codex"}, nil, now)
	if err != nil {
		t.Fatal(err)
	}
	if session.ID == "" || session.WorkspaceID != workspace.ID || session.Provider != "codex" || session.Label != "review" {
		t.Fatalf("BuildSession() = %+v", session)
	}
	if session.TmuxPane != "" || session.PaneCommand != "" || session.PaneStart != "" {
		t.Fatalf("BuildSession() recorded a pane: %+v", session)
	}
	if len(session.ResumeCommand) != 1 || session.ResumeCommand[0] != "codex" {
		t.Fatalf("BuildSession() resume command = %v", session.ResumeCommand)
	}
	if !session.CreatedAt.Equal(now) || !session.UpdatedAt.Equal(now) {
		t.Fatalf("BuildSession() times = %s and %s", session.CreatedAt, session.UpdatedAt)
	}
	agents, err := service.List(workspace.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(agents) != 0 {
		t.Fatalf("BuildSession() saved a record: %+v", agents)
	}

	if _, err := BuildSession(workspace, "codex", "review", "", "", []string{"codex"}, []domain.AgentSession{session}, now); err == nil || !strings.Contains(err.Error(), "already in use") {
		t.Fatalf("BuildSession() with a used label error = %v", err)
	}
	if _, err := BuildSession(workspace, "robot", "review", "", "", []string{"robot"}, nil, now); err == nil || !strings.Contains(err.Error(), "unsupported agent provider") {
		t.Fatalf("BuildSession() with an unsupported provider error = %v", err)
	}
	defaulted, err := BuildSession(workspace, "", "", "", "", []string{"codex", "resume", "session-one"}, []domain.AgentSession{session}, now)
	if err != nil {
		t.Fatal(err)
	}
	if defaulted.Provider != "codex" || defaulted.Label != "codex" || defaulted.ProviderSessionID != "session-one" {
		t.Fatalf("BuildSession() with inference = %+v", defaulted)
	}
}

func TestEffectiveResumeCommandCanPreferALinkedProviderSession(t *testing.T) {
	agent := domain.AgentSession{
		Provider: "codex", ProviderSessionID: "linked-one",
		ResumeCommand: []string{"codex", "fallback"},
	}
	if got := strings.Join(EffectiveResumeCommand(agent), " "); got != "codex fallback" {
		t.Fatalf("legacy resume command = %q", got)
	}
	agent.PreferProviderResume = true
	if got := strings.Join(EffectiveResumeCommand(agent), " "); got != "codex resume linked-one" {
		t.Fatalf("preferred linked resume command = %q", got)
	}
	agent.ProviderSessionID = ""
	if got := strings.Join(EffectiveResumeCommand(agent), " "); got != "codex fallback" {
		t.Fatalf("unlinked fallback resume command = %q", got)
	}
}

func TestUserFacingErrorsCarryConsistentCodes(t *testing.T) {
	service, workspace := activeWorkspace(t)
	agent, err := service.Register(workspace, "", "", "", "", []string{"codex", "resume", "session-one"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.ValidateRemove(agent.ID, "other-workspace"); clierr.CodeOf(err) != clierr.PreconditionFailed {
		t.Fatalf("remove outside the Workspace code = %q", clierr.CodeOf(err))
	}
	if err := service.Send(agent, "other-workspace", "text"); clierr.CodeOf(err) != clierr.PreconditionFailed {
		t.Fatalf("send outside the Workspace code = %q", clierr.CodeOf(err))
	}
	if err := service.ValidateResume(agent, domain.Workspace{ID: "other-workspace", Name: "other"}); clierr.CodeOf(err) != clierr.PreconditionFailed {
		t.Fatalf("resume outside the Workspace code = %q", clierr.CodeOf(err))
	}
	other := domain.Workspace{Version: domain.WorkspaceVersion, ID: "other-workspace", Name: "other", Status: domain.WorkspaceActive}
	if err := store.NewWorkspaceStore(service.stateDir).Save(other); err != nil {
		t.Fatal(err)
	}
	if err := service.ValidateTranscriptLink(agent.ID, other.ID, "session-two"); clierr.CodeOf(err) != clierr.PreconditionFailed {
		t.Fatalf("link outside the Workspace code = %q", clierr.CodeOf(err))
	}
	if err := service.Send(agent, workspace.ID, ""); clierr.CodeOf(err) != clierr.InvalidUsage {
		t.Fatalf("empty feedback code = %q", clierr.CodeOf(err))
	}
	err = service.ValidateRegistration(workspace, "cursor", "", "cursor-session", []string{"cursor-agent", "resume"})
	if clierr.CodeOf(err) != clierr.InvalidUsage {
		t.Fatalf("unsupported transcript link code = %q", clierr.CodeOf(err))
	}
	live, err := service.Live(workspace.ID)
	if err != nil || len(live) != 0 {
		t.Fatalf("Live() = %+v, %v", live, err)
	}
}

func TestMatchesProvider(t *testing.T) {
	if !MatchesProvider("codex", "codex resume x", "codex", []string{"codex", "resume", "x"}) {
		t.Fatal("MatchesProvider() for a direct codex pane = false")
	}
	if MatchesProvider("bash", "bash -lc 'codex'", "codex", []string{"codex"}) {
		t.Fatal("MatchesProvider() for a shell pane = true")
	}
}

func activeWorkspace(t *testing.T) (*Service, domain.Workspace) {
	t.Helper()
	stateDir := t.TempDir()
	service := NewService(stateDir, "")
	now := time.Now().UTC()
	workspace := domain.Workspace{
		Version: domain.WorkspaceVersion, ID: "workspace-one", Name: "workspace-one", Status: domain.WorkspaceActive,
		Root: t.TempDir(), CreatedAt: now, UpdatedAt: now,
	}
	if err := store.NewWorkspaceStore(stateDir).Save(workspace); err != nil {
		t.Fatal(err)
	}
	return service, workspace
}
