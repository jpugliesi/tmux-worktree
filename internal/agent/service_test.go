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

func TestRegisterReloadsProjectInsideMutationLock(t *testing.T) {
	service := NewService(t.TempDir(), "")
	project := domain.Project{
		Version: domain.ProjectVersion,
		ID:      "project-that-was-removed",
		Name:    "removed",
		Status:  domain.ProjectActive,
	}

	_, err := service.Register(project, "command", "test", "", "", []string{"true"})
	if err == nil || !strings.Contains(err.Error(), "does not exist") {
		t.Fatalf("Register after Project removal error = %v", err)
	}
	agents, listErr := service.List(project.ID)
	if listErr != nil {
		t.Fatal(listErr)
	}
	if len(agents) != 0 {
		t.Fatalf("Register saved an Agent Session for a removed Project: %+v", agents)
	}
}

func TestValidateRegistrationRejectsUnsupportedTranscriptLink(t *testing.T) {
	service := NewService(t.TempDir(), "")
	project := domain.Project{ID: "project-one", Name: "project-one", Status: domain.ProjectActive}
	err := service.ValidateRegistration(project, "cursor", "", "cursor-session", []string{"cursor-agent", "resume"})
	if err == nil || !strings.Contains(err.Error(), "does not support verifiable linked transcripts") {
		t.Fatalf("Cursor transcript registration error = %v", err)
	}
	if err := service.ValidateRegistration(project, "cursor", "", "", []string{"cursor-agent", "resume"}); err != nil {
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
			service, project := activeProject(t)
			agent, err := service.Register(project, "", "", "", "", test.resumeCommand)
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
	service, project := activeProject(t)
	agent, err := service.Register(project, "command", "", "", "", []string{"codex", "resume", "session-one"})
	if err != nil {
		t.Fatal(err)
	}
	if agent.Provider != "command" || agent.ProviderSessionID != "" {
		t.Fatalf("explicit provider registration = %+v", agent)
	}
	linked, err := service.Register(project, "codex", "", "", "session-explicit", []string{"codex", "resume", "session-one"})
	if err != nil {
		t.Fatal(err)
	}
	if linked.ProviderSessionID != "session-explicit" {
		t.Fatalf("explicit provider session = %q", linked.ProviderSessionID)
	}
}

func TestRegisterMakesUniqueDefaultLabelsAndRejectsDuplicateLabels(t *testing.T) {
	service, project := activeProject(t)
	for index, want := range []string{"codex", "codex-2", "codex-3"} {
		agent, err := service.Register(project, "", "", "", "", []string{"codex", "resume", fmt.Sprintf("session-%d", index)})
		if err != nil {
			t.Fatal(err)
		}
		if agent.Label != want {
			t.Fatalf("default label %d = %q, want %q", index, agent.Label, want)
		}
	}
	if _, err := service.Register(project, "", "review", "", "", []string{"codex", "resume", "session-review"}); err != nil {
		t.Fatal(err)
	}
	_, err := service.Register(project, "", "review", "", "", []string{"codex", "resume", "session-other"})
	if err == nil || !strings.Contains(err.Error(), "already in use") {
		t.Fatalf("duplicate label error = %v", err)
	}
	if clierr.CodeOf(err) != clierr.AlreadyExists {
		t.Fatalf("duplicate label code = %q", clierr.CodeOf(err))
	}
	if err := service.ValidateLabel(project.ID, "review"); err == nil {
		t.Fatal("ValidateLabel accepted a used label")
	}
	if err := service.ValidateLabel(project.ID, "free"); err != nil {
		t.Fatalf("ValidateLabel(free) = %v", err)
	}
}

func TestRegisterAndResumeExplainAnArchivedProject(t *testing.T) {
	service, project := activeProject(t)
	project.Status = domain.ProjectArchived
	if err := store.NewProjectStore(service.stateDir).Save(project); err != nil {
		t.Fatal(err)
	}
	err := service.ValidateRegistration(project, "codex", "", "", []string{"codex", "resume", "session-one"})
	if err == nil || !strings.Contains(err.Error(), "is archived") {
		t.Fatalf("archived Project registration error = %v", err)
	}
	if hint := clierr.HintOf(err); !strings.Contains(hint, "projects open") {
		t.Fatalf("archived Project hint = %q", hint)
	}
	if clierr.CodeOf(err) != clierr.PreconditionFailed {
		t.Fatalf("archived Project code = %q", clierr.CodeOf(err))
	}
	resumeErr := service.ValidateResume(domain.AgentSession{ID: "agent-one", ProjectID: project.ID}, project)
	if resumeErr == nil || !strings.Contains(resumeErr.Error(), "is archived") {
		t.Fatalf("archived Project resume error = %v", resumeErr)
	}
}

func TestRemoveDeletesOnlyTheSelectedAgentSessionRecord(t *testing.T) {
	service, project := activeProject(t)
	first, err := service.Register(project, "", "", "", "", []string{"codex", "resume", "session-one"})
	if err != nil {
		t.Fatal(err)
	}
	second, err := service.Register(project, "", "", "", "", []string{"codex", "resume", "session-two"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Remove(first.ID, "other-project"); err == nil || !strings.Contains(err.Error(), "does not belong") {
		t.Fatalf("Remove() for another Project error = %v", err)
	}
	if _, err := service.Remove(first.ID, project.ID); err != nil {
		t.Fatal(err)
	}
	remaining, err := service.List(project.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(remaining) != 1 || remaining[0].ID != second.ID {
		t.Fatalf("Agent Sessions after Remove() = %+v", remaining)
	}
	if _, err := service.Remove(first.ID, project.ID); clierr.CodeOf(err) != clierr.NotFound {
		t.Fatalf("Remove() of a missing Agent Session error = %v", err)
	}
}

func TestBuildSessionMakesARecordWithoutALockOrAStoreWrite(t *testing.T) {
	service := NewService(t.TempDir(), "")
	project := domain.Project{Version: domain.ProjectVersion, ID: "project-one", Name: "project-one", Status: domain.ProjectInitializing}
	now := time.Now().UTC()

	session, err := BuildSession(project, "codex", "review", "", "", []string{"codex"}, nil, now)
	if err != nil {
		t.Fatal(err)
	}
	if session.ID == "" || session.ProjectID != project.ID || session.Provider != "codex" || session.Label != "review" {
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
	agents, err := service.List(project.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(agents) != 0 {
		t.Fatalf("BuildSession() saved a record: %+v", agents)
	}

	if _, err := BuildSession(project, "codex", "review", "", "", []string{"codex"}, []domain.AgentSession{session}, now); err == nil || !strings.Contains(err.Error(), "already in use") {
		t.Fatalf("BuildSession() with a used label error = %v", err)
	}
	if _, err := BuildSession(project, "robot", "review", "", "", []string{"robot"}, nil, now); err == nil || !strings.Contains(err.Error(), "unsupported agent provider") {
		t.Fatalf("BuildSession() with an unsupported provider error = %v", err)
	}
	defaulted, err := BuildSession(project, "", "", "", "", []string{"codex", "resume", "session-one"}, []domain.AgentSession{session}, now)
	if err != nil {
		t.Fatal(err)
	}
	if defaulted.Provider != "codex" || defaulted.Label != "codex" || defaulted.ProviderSessionID != "session-one" {
		t.Fatalf("BuildSession() with inference = %+v", defaulted)
	}
}

func TestUserFacingErrorsCarryConsistentCodes(t *testing.T) {
	service, project := activeProject(t)
	agent, err := service.Register(project, "", "", "", "", []string{"codex", "resume", "session-one"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.ValidateRemove(agent.ID, "other-project"); clierr.CodeOf(err) != clierr.PreconditionFailed {
		t.Fatalf("remove outside the Project code = %q", clierr.CodeOf(err))
	}
	if err := service.Send(agent, "other-project", "text"); clierr.CodeOf(err) != clierr.PreconditionFailed {
		t.Fatalf("send outside the Project code = %q", clierr.CodeOf(err))
	}
	if err := service.ValidateResume(agent, domain.Project{ID: "other-project", Name: "other"}); clierr.CodeOf(err) != clierr.PreconditionFailed {
		t.Fatalf("resume outside the Project code = %q", clierr.CodeOf(err))
	}
	other := domain.Project{Version: domain.ProjectVersion, ID: "other-project", Name: "other", Status: domain.ProjectActive}
	if err := store.NewProjectStore(service.stateDir).Save(other); err != nil {
		t.Fatal(err)
	}
	if err := service.ValidateTranscriptLink(agent.ID, other.ID, "session-two"); clierr.CodeOf(err) != clierr.PreconditionFailed {
		t.Fatalf("link outside the Project code = %q", clierr.CodeOf(err))
	}
	if err := service.Send(agent, project.ID, ""); clierr.CodeOf(err) != clierr.InvalidUsage {
		t.Fatalf("empty feedback code = %q", clierr.CodeOf(err))
	}
	err = service.ValidateRegistration(project, "cursor", "", "cursor-session", []string{"cursor-agent", "resume"})
	if clierr.CodeOf(err) != clierr.InvalidUsage {
		t.Fatalf("unsupported transcript link code = %q", clierr.CodeOf(err))
	}
	live, err := service.Live(project.ID)
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

func activeProject(t *testing.T) (*Service, domain.Project) {
	t.Helper()
	stateDir := t.TempDir()
	service := NewService(stateDir, "")
	now := time.Now().UTC()
	project := domain.Project{
		Version: domain.ProjectVersion, ID: "project-one", Name: "project-one", Status: domain.ProjectActive,
		Root: t.TempDir(), CreatedAt: now, UpdatedAt: now,
	}
	if err := store.NewProjectStore(stateDir).Save(project); err != nil {
		t.Fatal(err)
	}
	return service, project
}
