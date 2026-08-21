package project

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jpugliesi/tmux-worktree/internal/domain"
	"github.com/jpugliesi/tmux-worktree/internal/store"
)

func TestTemplateAgentStepWithoutALiveTmuxSession(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is not installed")
	}

	stateDir := t.TempDir()
	template := domain.Template{
		Version: domain.TemplateVersion, Name: "example",
		Repositories: []domain.RepositorySpec{{Name: "app", Clone: domain.CloneSpec{URL: "https://example.com/app.git"}}},
		Agents:       []domain.TemplateAgent{{Label: "review", Provider: "codex", Start: []string{"codex"}}},
	}
	now := time.Now().UTC()
	project := domain.Project{
		Version: domain.ProjectVersion, ID: "project-one", Name: "project-one", TemplateName: template.Name,
		TemplateSnapshot: template, Status: domain.ProjectInitializing, Root: t.TempDir(),
		TmuxSession: "project-one", CreatedAt: now, UpdatedAt: now,
	}
	service := NewService(Options{
		StateDir:   stateDir,
		DataDir:    t.TempDir(),
		TmuxSocket: fmt.Sprintf("twt-agent-step-test-%d", time.Now().UnixNano()),
	})

	if err := service.ensureTemplateAgent(project, "review"); err != nil {
		t.Fatalf("ensureTemplateAgent() without a live tmux session: %v", err)
	}
	agents := store.NewAgentStore(stateDir)
	sessions, err := agents.List(project.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 1 {
		t.Fatalf("Agent Sessions = %+v, want one", sessions)
	}
	session := sessions[0]
	if session.Label != "review" || session.Provider != "codex" {
		t.Fatalf("registered Agent Session = %+v", session)
	}
	if session.TmuxPane != "" {
		t.Fatalf("registered Agent Session has a pane without a live tmux session: %+v", session)
	}
	if len(session.ResumeCommand) != 1 || session.ResumeCommand[0] != "codex" {
		t.Fatalf("resume command = %v, want the declared start command", session.ResumeCommand)
	}

	// The step is idempotent, so a setup retry keeps one record.
	if err := service.ensureTemplateAgent(project, "review"); err != nil {
		t.Fatalf("second ensureTemplateAgent(): %v", err)
	}
	sessions, err = agents.List(project.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 1 || sessions[0].ID != session.ID {
		t.Fatalf("Agent Sessions after a retry = %+v", sessions)
	}

	if err := service.ensureTemplateAgent(project, "missing"); err == nil || !strings.Contains(err.Error(), "does not declare") {
		t.Fatalf("ensureTemplateAgent() with an unknown label error = %v", err)
	}
}

func TestCreateStartsTheDeclaredAgentSessions(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is not installed")
	}

	stateDir := t.TempDir()
	dataDir := t.TempDir()
	source := filepath.Join(t.TempDir(), "source")
	initCreateTestRepository(t, source)
	template := domain.Template{
		Version: domain.TemplateVersion, Name: "example",
		Repositories: []domain.RepositorySpec{{Name: "app", Clone: domain.CloneSpec{URL: source}}},
		Agents:       []domain.TemplateAgent{{Label: "review", Provider: "command", Start: []string{"sleep", "60"}}},
	}
	socket := fmt.Sprintf("twt-agent-create-test-%d", time.Now().UnixNano())
	t.Cleanup(func() { _ = exec.Command("tmux", "-L", socket, "kill-server").Run() })
	service := NewService(Options{StateDir: stateDir, DataDir: dataDir, TmuxSocket: socket})

	project, err := service.CreateWithOptions("fix-auth", template.Name, template, CreateOptions{})
	if err != nil {
		t.Fatalf("CreateWithOptions() with a declared Agent Session: %v", err)
	}
	if project.Status != domain.ProjectActive {
		t.Fatalf("Project status = %q, want %q", project.Status, domain.ProjectActive)
	}
	var agentStep *domain.SetupStep
	for index := range project.Steps {
		if project.Steps[index].Kind == domain.StepAgent {
			agentStep = &project.Steps[index]
		}
	}
	if agentStep == nil {
		t.Fatalf("Project has no agent setup step: %+v", project.Steps)
	}
	if agentStep.ID != "agent:review" || agentStep.Agent != "review" || agentStep.Status != domain.StepSucceeded {
		t.Fatalf("agent setup step = %+v", *agentStep)
	}

	agents := store.NewAgentStore(stateDir)
	sessions, err := agents.List(project.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 1 {
		t.Fatalf("Agent Sessions after create = %+v, want one", sessions)
	}
	session := sessions[0]
	if session.Label != "review" || session.Provider != "command" {
		t.Fatalf("registered Agent Session = %+v", session)
	}
	if session.TmuxPane == "" || session.PaneStart == "" {
		t.Fatalf("registered Agent Session has no pane identity: %+v", session)
	}
	if windows := agentWindowCount(t, socket, project.TmuxSession, "review"); windows != 1 {
		t.Fatalf("tmux windows named review = %d, want 1", windows)
	}

	// A setup retry after a failed agent step keeps one Agent Session record.
	stored, err := store.NewProjectStore(stateDir).Find(project.ID)
	if err != nil {
		t.Fatal(err)
	}
	for index := range stored.Steps {
		if stored.Steps[index].Kind == domain.StepAgent {
			stored.Steps[index].Status = domain.StepFailed
			stored.Steps[index].Error = "simulated failure"
		}
	}
	stored.Status = domain.ProjectSetupFailed
	if err := store.NewProjectStore(stateDir).Save(stored); err != nil {
		t.Fatal(err)
	}
	retried, err := service.Retry(project.ID)
	if err != nil {
		t.Fatalf("Retry() after a failed agent step: %v", err)
	}
	if retried.Status != domain.ProjectActive {
		t.Fatalf("Project status after retry = %q", retried.Status)
	}
	sessions, err = agents.List(project.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 1 || sessions[0].ID != session.ID {
		t.Fatalf("Agent Sessions after retry = %+v, want the same single record", sessions)
	}
	if windows := agentWindowCount(t, socket, project.TmuxSession, "review"); windows != 1 {
		t.Fatalf("tmux windows named review after retry = %d, want 1", windows)
	}
}

func agentWindowCount(t *testing.T, socket, sessionName, windowName string) int {
	t.Helper()
	command := exec.Command("tmux", "-L", socket, "-f", "/dev/null", "list-windows", "-t", sessionName, "-F", "#{window_name}")
	data, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("tmux list-windows: %v\n%s", err, data)
	}
	count := 0
	for _, name := range strings.Fields(string(data)) {
		if name == windowName {
			count++
		}
	}
	return count
}
