package cli_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jpugliesi/tmux-worktree/internal/cli"
	"github.com/jpugliesi/tmux-worktree/internal/domain"
	"github.com/jpugliesi/tmux-worktree/internal/store"
)

func TestProjectsListShowsHumanFieldsFirst(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	project := domain.Project{
		Version:      domain.ProjectVersion,
		ID:           "514a26ed287e429b888000aaa288333a",
		Name:         "everysphere-0",
		TemplateName: "everysphere",
		Status:       domain.ProjectActive,
		CreatedAt:    time.Date(2026, time.August, 19, 12, 0, 0, 0, time.UTC),
	}
	if err := store.NewProjectStore(filepath.Join(root, "state")).Save(project); err != nil {
		t.Fatal(err)
	}

	output := executeWithOptions(t, cli.Options{
		ConfigDir: filepath.Join(root, "config"),
		StateDir:  filepath.Join(root, "state"),
		DataDir:   filepath.Join(root, "data"),
	}, nil, "projects", "list")

	want := "everysphere-0\teverysphere\tactive\n"
	if output != want {
		t.Fatalf("projects list output = %q, want %q", output, want)
	}
	if strings.Contains(output, project.ID) {
		t.Fatalf("projects list output contains opaque Project ID: %q", output)
	}

	jsonOutput := executeWithOptions(t, cli.Options{
		ConfigDir: filepath.Join(root, "config"),
		StateDir:  filepath.Join(root, "state"),
		DataDir:   filepath.Join(root, "data"),
	}, nil, "projects", "list", "--output", "json")
	var result struct {
		SchemaVersion int `json:"schemaVersion"`
		Projects      []struct {
			ID       string `json:"id"`
			Name     string `json:"name"`
			Template string `json:"template"`
			Status   string `json:"status"`
		} `json:"projects"`
	}
	if err := json.Unmarshal([]byte(jsonOutput), &result); err != nil {
		t.Fatalf("decode projects list JSON: %v", err)
	}
	if result.SchemaVersion != 1 || len(result.Projects) != 1 {
		t.Fatalf("projects list JSON metadata = %#v", result)
	}
	got := result.Projects[0]
	if got.ID != project.ID || got.Name != project.Name || got.Template != project.TemplateName || got.Status != string(project.Status) {
		t.Fatalf("projects list JSON Project = %#v", got)
	}
}

func TestProjectsListShowsRecentActiveProjectsBeforeArchives(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	projects := []domain.Project{
		{Version: domain.ProjectVersion, ID: "old-active", Name: "old-active", TemplateName: "example", Status: domain.ProjectActive, CreatedAt: time.Date(2026, time.August, 18, 12, 0, 0, 0, time.UTC)},
		{Version: domain.ProjectVersion, ID: "new-archive", Name: "new-archive", TemplateName: "example", Status: domain.ProjectArchived, CreatedAt: time.Date(2026, time.August, 20, 12, 0, 0, 0, time.UTC)},
		{Version: domain.ProjectVersion, ID: "new-active", Name: "new-active", TemplateName: "example", Status: domain.ProjectActive, CreatedAt: time.Date(2026, time.August, 19, 12, 0, 0, 0, time.UTC)},
	}
	projectStore := store.NewProjectStore(filepath.Join(root, "state"))
	for _, project := range projects {
		if err := projectStore.Save(project); err != nil {
			t.Fatal(err)
		}
	}

	output := executeWithOptions(t, cli.Options{
		ConfigDir: filepath.Join(root, "config"),
		StateDir:  filepath.Join(root, "state"),
		DataDir:   filepath.Join(root, "data"),
	}, nil, "projects", "list", "--limit", "2")
	want := "new-active\texample\tactive\nold-active\texample\tactive\n"
	if output != want {
		t.Fatalf("limited projects list output = %q, want %q", output, want)
	}
}

func TestProjectsArchivePreservesDataAndOpenRestoresSession(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is not installed")
	}
	t.Setenv("TMUX_PANE", "")

	root := t.TempDir()
	source := filepath.Join(root, "source")
	initGitRepository(t, source)
	configDir := filepath.Join(root, "config")
	if err := os.MkdirAll(filepath.Join(configDir, "templates"), 0o755); err != nil {
		t.Fatal(err)
	}
	template := fmt.Sprintf("version: 1\nname: example\nrepositories:\n  - name: app\n    clone:\n      url: %s\n", source)
	if err := os.WriteFile(filepath.Join(configDir, "templates", "example.yaml"), []byte(template), 0o644); err != nil {
		t.Fatal(err)
	}
	socket := fmt.Sprintf("twt2-test-%d", time.Now().UnixNano())
	t.Cleanup(func() { exec.Command("tmux", "-L", socket, "kill-server").Run() })
	options := cli.Options{
		ConfigDir:  configDir,
		StateDir:   filepath.Join(root, "state"),
		DataDir:    filepath.Join(root, "data"),
		TmuxSocket: socket,
	}
	executeWithOptions(t, options, nil, "projects", "create", "archive-me", "--template", "example", "--no-open")
	project, err := store.NewProjectStore(options.StateDir).Find("archive-me")
	if err != nil {
		t.Fatal(err)
	}
	agent := domain.AgentSession{
		Version:       domain.AgentVersion,
		ID:            "agent-session-1",
		ProjectID:     project.ID,
		Provider:      "codex",
		Label:         "review",
		ResumeCommand: []string{"codex", "resume", "session-1"},
		CreatedAt:     time.Now().UTC(),
		UpdatedAt:     time.Now().UTC(),
	}
	if err := store.NewAgentStore(options.StateDir).Save(agent); err != nil {
		t.Fatal(err)
	}
	dryRun := executeWithOptions(t, options, nil, "projects", "archive", "archive-me", "--dry-run", "--output", "json")
	if !strings.Contains(dryRun, `"operation":"projects.archive"`) || !strings.Contains(dryRun, `"status":"valid"`) {
		t.Fatalf("projects archive dry-run output = %s", dryRun)
	}
	stillActive, err := store.NewProjectStore(options.StateDir).Find(project.ID)
	if err != nil || stillActive.Status != domain.ProjectActive {
		t.Fatalf("archive dry-run changed Project: status=%q error=%v", stillActive.Status, err)
	}

	output := executeWithOptions(t, options, nil, "projects", "archive", "archive-me")
	if output != "Archived Project \"archive-me\"\n" {
		t.Fatalf("projects archive output = %q", output)
	}
	archived, err := store.NewProjectStore(options.StateDir).Find(project.ID)
	if err != nil {
		t.Fatal(err)
	}
	if archived.Status != "archived" {
		t.Fatalf("Project status after archive = %q", archived.Status)
	}
	if archived.ArchivedAt == nil {
		t.Fatal("archived Project has no archive time")
	}
	firstArchivedAt := *archived.ArchivedAt
	if _, err := os.Stat(archived.Root); err != nil {
		t.Fatalf("archive removed the Project root: %v", err)
	}
	if _, err := os.Stat(archived.Repositories[0].Path); err != nil {
		t.Fatalf("archive removed the worktree: %v", err)
	}
	agents, err := store.NewAgentStore(options.StateDir).List(project.ID)
	if err != nil || len(agents) != 1 || agents[0].ID != agent.ID {
		t.Fatalf("archive changed Agent Session records: agents=%v error=%v", agents, err)
	}
	agentList := executeWithOptions(t, options, nil, "agents", "list", "--project", project.ID, "--output", "json")
	if !strings.Contains(agentList, `"status":"stopped"`) || !strings.Contains(agentList, `"canResume":false`) {
		t.Fatalf("archived Agent Session capabilities = %s", agentList)
	}
	if err := exec.Command("tmux", "-L", socket, "has-session", "-t", "=archive-me").Run(); err == nil {
		t.Fatal("archive kept the Project tmux session")
	}

	// A retry must complete cleanup and must not remove Project data.
	executeWithOptions(t, options, nil, "projects", "archive", project.ID)
	archivedAgain, err := store.NewProjectStore(options.StateDir).Find(project.ID)
	if err != nil {
		t.Fatal(err)
	}
	if archivedAgain.ArchivedAt == nil || !archivedAgain.ArchivedAt.Equal(firstArchivedAt) {
		t.Fatalf("archive retry changed archive time: first=%s retry=%v", firstArchivedAt, archivedAgain.ArchivedAt)
	}
	executeWithOptions(t, options, nil, "projects", "open", project.ID, "--no-attach")
	reopened, err := store.NewProjectStore(options.StateDir).Find(project.ID)
	if err != nil {
		t.Fatal(err)
	}
	if reopened.Status != domain.ProjectActive || reopened.ArchivedAt != nil {
		t.Fatalf("Project after open has status %q and archive time %v", reopened.Status, reopened.ArchivedAt)
	}
	if err := exec.Command("tmux", "-L", socket, "has-session", "-t", "=archive-me").Run(); err != nil {
		t.Fatalf("open did not restore the Project tmux session: %v", err)
	}
	agentList = executeWithOptions(t, options, nil, "agents", "list", "--project", project.ID, "--output", "json")
	if !strings.Contains(agentList, `"canResume":true`) {
		t.Fatalf("reopened Agent Session capabilities = %s", agentList)
	}

	// The short command resolves the current Project without an argument.
	t.Setenv("TWT2_PROJECT_ID", project.ID)
	if output := executeWithOptions(t, options, nil, "archive"); output != "Archived Project \"archive-me\"\n" {
		t.Fatalf("root archive output = %q", output)
	}
	command := cli.New(options)
	command.SetArgs([]string{"projects", "setup", "retry", project.ID})
	err = command.Execute()
	if err == nil || !strings.Contains(err.Error(), "archived") || !strings.Contains(err.Error(), "projects open") {
		t.Fatalf("archived Project retry error = %v", err)
	}
	archivedAfterRetry, err := store.NewProjectStore(options.StateDir).Find(project.ID)
	if err != nil || archivedAfterRetry.Status != domain.ProjectArchived {
		t.Fatalf("retry changed archived Project: status=%q error=%v", archivedAfterRetry.Status, err)
	}

	executeWithOptions(t, options, nil, "projects", "open", project.ID, "--no-attach")
	pane := runCommand(t, "", "tmux", "-L", socket, "list-panes", "-t", "=archive-me", "-F", "#{pane_id}")
	t.Setenv("TMUX_PANE", pane)
	command = cli.New(options)
	command.SetArgs([]string{"projects", "archive", project.ID})
	err = command.Execute()
	if err == nil || !strings.Contains(err.Error(), "cannot archive") || !strings.Contains(err.Error(), "switch to another session") {
		t.Fatalf("archive from the target session error = %v", err)
	}
	stillActive, err = store.NewProjectStore(options.StateDir).Find(project.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stillActive.Status != domain.ProjectActive {
		t.Fatalf("self-archive changed Project status to %q", stillActive.Status)
	}

	command = cli.New(options)
	command.SetArgs([]string{"projects", "remove", project.ID})
	err = command.Execute()
	if err == nil || !strings.Contains(err.Error(), "archive") {
		t.Fatalf("active Project removal error = %v", err)
	}
	now := time.Now().UTC()
	stillActive.Status = domain.ProjectArchived
	stillActive.ArchivedAt = &now
	if err := store.NewProjectStore(options.StateDir).Save(stillActive); err != nil {
		t.Fatal(err)
	}
	command = cli.New(options)
	command.SetArgs([]string{"projects", "remove", project.ID, "--apply", "--dry-run"})
	err = command.Execute()
	if err == nil || !strings.Contains(err.Error(), "cannot remove") || !strings.Contains(err.Error(), "inside its tmux session") {
		t.Fatalf("self-removal error = %v", err)
	}
	if err := exec.Command("tmux", "-L", socket, "has-session", "-t", "=archive-me").Run(); err != nil {
		t.Fatal("self-removal stopped the Project tmux session")
	}
}

func TestProjectsArchiveFailsWhenTmuxOwnershipIsNotSafe(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is not installed")
	}

	root := t.TempDir()
	socket := fmt.Sprintf("twt2-test-%d", time.Now().UnixNano())
	t.Cleanup(func() { exec.Command("tmux", "-L", socket, "kill-server").Run() })
	options := cli.Options{StateDir: filepath.Join(root, "state"), DataDir: filepath.Join(root, "data"), TmuxSocket: socket}
	project := domain.Project{Version: domain.ProjectVersion, ID: "safe-archive-id", Name: "safe-archive", Status: domain.ProjectActive}
	if err := store.NewProjectStore(options.StateDir).Save(project); err != nil {
		t.Fatal(err)
	}
	runCommand(t, "", "tmux", "-L", socket, "-f", "/dev/null", "new-session", "-d", "-s", "safe-archive", "sleep", "60")
	runCommand(t, "", "tmux", "-L", socket, "set-option", "-t", "safe-archive", "@twt2_project_id", project.ID)

	t.Setenv("TMUX_PANE", "%not-a-real-pane")
	command := cli.New(options)
	command.SetArgs([]string{"projects", "archive", project.ID})
	err := command.Execute()
	if err == nil || !strings.Contains(err.Error(), "inspect the current tmux pane") {
		t.Fatalf("archive with an unknown current pane error = %v", err)
	}
	unchanged, err := store.NewProjectStore(options.StateDir).Find(project.ID)
	if err != nil || unchanged.Status != domain.ProjectActive {
		t.Fatalf("unsafe archive changed Project: status=%q error=%v", unchanged.Status, err)
	}

	t.Setenv("TMUX_PANE", "")
	runCommand(t, "", "tmux", "-L", socket, "new-session", "-d", "-s", "duplicate-owner", "sleep", "60")
	runCommand(t, "", "tmux", "-L", socket, "set-option", "-t", "duplicate-owner", "@twt2_project_id", project.ID)
	command = cli.New(options)
	command.SetArgs([]string{"projects", "archive", project.ID})
	err = command.Execute()
	if err == nil || !strings.Contains(err.Error(), "more than one tmux session") {
		t.Fatalf("archive with duplicate owned sessions error = %v", err)
	}
	for _, session := range []string{"safe-archive", "duplicate-owner"} {
		if err := exec.Command("tmux", "-L", socket, "has-session", "-t", "="+session).Run(); err != nil {
			t.Fatalf("unsafe archive stopped session %q", session)
		}
	}
}

func TestProjectsCreateProvisionsCheckoutAndTmuxSession(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is not installed")
	}

	root := t.TempDir()
	source := filepath.Join(root, "source")
	initGitRepository(t, source)

	configDir := filepath.Join(root, "config")
	if err := os.MkdirAll(filepath.Join(configDir, "templates"), 0o755); err != nil {
		t.Fatal(err)
	}
	template := fmt.Sprintf(`version: 1
name: example
repositories:
  - name: app
    clone:
      url: %s
    initialize:
      command: ["./init.sh"]
`, source)
	if err := os.WriteFile(filepath.Join(configDir, "templates", "example.yaml"), []byte(template), 0o644); err != nil {
		t.Fatal(err)
	}

	socket := fmt.Sprintf("twt2-test-%d", time.Now().UnixNano())
	t.Cleanup(func() {
		exec.Command("tmux", "-L", socket, "kill-server").Run()
	})

	var stdout, stderr bytes.Buffer
	command := cli.New(cli.Options{
		ConfigDir:  configDir,
		StateDir:   filepath.Join(root, "state"),
		DataDir:    filepath.Join(root, "data"),
		TmuxSocket: socket,
		Stdout:     &stdout,
		Stderr:     &stderr,
	})
	command.SetArgs([]string{"projects", "create", "auth-refresh", "--template", "example", "--no-open"})
	if err := command.Execute(); err != nil {
		t.Fatalf("projects create returned an error: %v\nstderr: %s", err, stderr.String())
	}

	projectEntries, err := os.ReadDir(filepath.Join(root, "data", "projects"))
	if err != nil {
		t.Fatalf("read Project directory: %v", err)
	}
	if len(projectEntries) != 1 {
		t.Fatalf("Project directory count = %d, want 1", len(projectEntries))
	}
	checkout := filepath.Join(root, "data", "projects", projectEntries[0].Name(), "app")
	initialized, err := os.ReadFile(filepath.Join(checkout, ".initialized"))
	if err != nil {
		t.Fatalf("repository initialization did not create its marker: %v", err)
	}
	if string(initialized) != "initialized\n" {
		t.Fatalf("initialization marker = %q", initialized)
	}

	branch := runCommand(t, checkout, "git", "branch", "--show-current")
	if !strings.Contains(branch, "auth-refresh") {
		t.Fatalf("branch %q does not identify the Project", branch)
	}

	windows := runCommand(t, "", "tmux", "-L", socket, "list-windows", "-t", "=auth-refresh", "-F", "#{window_name}")
	if windows != "app" {
		t.Fatalf("tmux windows = %q, want app", windows)
	}
}

func TestProjectsCreateUsesSafeTmuxNameWhenAnUnownedNameExists(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is not installed")
	}
	root := t.TempDir()
	source := filepath.Join(root, "source")
	initGitRepository(t, source)
	configDir := filepath.Join(root, "config")
	if err := os.MkdirAll(filepath.Join(configDir, "templates"), 0o755); err != nil {
		t.Fatal(err)
	}
	template := fmt.Sprintf("version: 1\nname: example\nrepositories:\n  - name: app\n    clone:\n      url: %s\n", source)
	if err := os.WriteFile(filepath.Join(configDir, "templates", "example.yaml"), []byte(template), 0o644); err != nil {
		t.Fatal(err)
	}
	socket := fmt.Sprintf("twt2-test-%d", time.Now().UnixNano())
	t.Cleanup(func() { exec.Command("tmux", "-L", socket, "kill-server").Run() })
	runCommand(t, "", "tmux", "-L", socket, "new-session", "-d", "-s", "collision")
	options := cli.Options{ConfigDir: configDir, StateDir: filepath.Join(root, "state"), DataDir: filepath.Join(root, "data"), TmuxSocket: socket}
	executeWithOptions(t, options, nil, "projects", "create", "collision", "--template", "example", "--no-open")
	sessions := strings.Split(runCommand(t, "", "tmux", "-L", socket, "list-sessions", "-F", "#{session_name}|#{@twt2_project_id}"), "\n")
	if len(sessions) != 2 || sessions[0] != "collision|" || !strings.HasPrefix(sessions[1], "collision-") || strings.HasSuffix(sessions[1], "|") {
		t.Fatalf("tmux collision sessions = %q", sessions)
	}
}

func TestProjectsCreateProvisionsOneWindowForEachRepository(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is not installed")
	}

	root := t.TempDir()
	appSource := filepath.Join(root, "app-source")
	docsSource := filepath.Join(root, "docs-source")
	initGitRepository(t, appSource)
	initGitRepository(t, docsSource)
	configDir := filepath.Join(root, "config")
	if err := os.MkdirAll(filepath.Join(configDir, "templates"), 0o755); err != nil {
		t.Fatal(err)
	}
	template := fmt.Sprintf(`version: 1
name: full-stack
repositories:
  - name: app
    clone:
      url: %s
  - name: docs
    clone:
      url: %s
    window_name: guides
`, appSource, docsSource)
	if err := os.WriteFile(filepath.Join(configDir, "templates", "full-stack.yaml"), []byte(template), 0o644); err != nil {
		t.Fatal(err)
	}

	socket := fmt.Sprintf("twt2-test-%d", time.Now().UnixNano())
	t.Cleanup(func() { exec.Command("tmux", "-L", socket, "kill-server").Run() })
	command := cli.New(cli.Options{
		ConfigDir: configDir, StateDir: filepath.Join(root, "state"), DataDir: filepath.Join(root, "data"),
		TmuxSocket: socket, Stdout: &bytes.Buffer{}, Stderr: &bytes.Buffer{},
	})
	command.SetArgs([]string{"projects", "create", "docs-refresh", "--template", "full-stack", "--no-open"})
	if err := command.Execute(); err != nil {
		t.Fatalf("projects create returned an error: %v", err)
	}

	windows := runCommand(t, "", "tmux", "-L", socket, "list-windows", "-t", "=docs-refresh", "-F", "#{window_name}")
	if windows != "app\nguides" {
		t.Fatalf("tmux windows = %q, want app and guides", windows)
	}
	projectEntries, err := os.ReadDir(filepath.Join(root, "data", "projects"))
	if err != nil || len(projectEntries) != 1 {
		t.Fatalf("read Project root: entries=%v error=%v", projectEntries, err)
	}
	for _, name := range []string{"app", "docs"} {
		path := filepath.Join(root, "data", "projects", projectEntries[0].Name(), name)
		if got := runCommand(t, path, "git", "branch", "--show-current"); !strings.Contains(got, "docs-refresh") {
			t.Fatalf("repository %q branch = %q", name, got)
		}
	}
}

func TestProjectsSetupRetryUsesSavedTemplateSnapshot(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is not installed")
	}

	root := t.TempDir()
	source := filepath.Join(root, "source")
	initGitRepository(t, source)
	retryScript := "#!/bin/sh\nset -eu\nif [ ! -f .attempted ]; then touch .attempted; exit 7; fi\nprintf 'retried\\n' > .initialized\n"
	if err := os.WriteFile(filepath.Join(source, "init.sh"), []byte(retryScript), 0o755); err != nil {
		t.Fatal(err)
	}
	runCommand(t, source, "git", "add", "init.sh")
	runCommand(t, source, "git", "commit", "-qm", "make initialization retryable")

	configDir := filepath.Join(root, "config")
	if err := os.MkdirAll(filepath.Join(configDir, "templates"), 0o755); err != nil {
		t.Fatal(err)
	}
	templatePath := filepath.Join(configDir, "templates", "example.yaml")
	template := fmt.Sprintf("version: 1\nname: example\nrepositories:\n  - name: app\n    clone:\n      url: %s\ninitialize:\n  working_directory: app\n  command: [\"./init.sh\"]\n", source)
	if err := os.WriteFile(templatePath, []byte(template), 0o644); err != nil {
		t.Fatal(err)
	}
	socket := fmt.Sprintf("twt2-test-%d", time.Now().UnixNano())
	t.Cleanup(func() { exec.Command("tmux", "-L", socket, "kill-server").Run() })
	options := cli.Options{
		ConfigDir: configDir, StateDir: filepath.Join(root, "state"), DataDir: filepath.Join(root, "data"),
		TmuxSocket: socket, Stdout: &bytes.Buffer{}, Stderr: &bytes.Buffer{},
	}
	create := cli.New(options)
	create.SetArgs([]string{"projects", "create", "retry-me", "--template", "example", "--no-open"})
	if err := create.Execute(); err == nil || !strings.Contains(err.Error(), "initialization") {
		t.Fatalf("first create error = %v, want initialization failure", err)
	}

	changedTemplate := strings.Replace(template, "[\"./init.sh\"]", "[\"false\"]", 1)
	if err := os.WriteFile(templatePath, []byte(changedTemplate), 0o644); err != nil {
		t.Fatal(err)
	}
	retryOutput := &bytes.Buffer{}
	options.Stdout = retryOutput
	retry := cli.New(options)
	retry.SetArgs([]string{"projects", "setup", "retry", "retry-me"})
	if err := retry.Execute(); err != nil {
		t.Fatalf("projects setup retry returned an error: %v", err)
	}

	projectEntries, err := os.ReadDir(filepath.Join(root, "data", "projects"))
	if err != nil || len(projectEntries) != 1 {
		t.Fatalf("read Project root: entries=%v error=%v", projectEntries, err)
	}
	marker, err := os.ReadFile(filepath.Join(root, "data", "projects", projectEntries[0].Name(), "app", ".initialized"))
	if err != nil {
		t.Fatalf("read retry marker: %v", err)
	}
	if string(marker) != "retried\n" {
		t.Fatalf("retry marker = %q", marker)
	}
}

func TestAgentsListAndSendUseOwnedProjectPane(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is not installed")
	}

	root := t.TempDir()
	source := filepath.Join(root, "source")
	initGitRepository(t, source)
	configDir := filepath.Join(root, "config")
	if err := os.MkdirAll(filepath.Join(configDir, "templates"), 0o755); err != nil {
		t.Fatal(err)
	}
	template := fmt.Sprintf("version: 1\nname: example\nrepositories:\n  - name: app\n    clone:\n      url: %s\n", source)
	if err := os.WriteFile(filepath.Join(configDir, "templates", "example.yaml"), []byte(template), 0o644); err != nil {
		t.Fatal(err)
	}
	socket := fmt.Sprintf("twt2-test-%d", time.Now().UnixNano())
	t.Cleanup(func() { exec.Command("tmux", "-L", socket, "kill-server").Run() })
	baseOptions := cli.Options{ConfigDir: configDir, StateDir: filepath.Join(root, "state"), DataDir: filepath.Join(root, "data"), TmuxSocket: socket}
	executeWithOptions(t, baseOptions, nil, "projects", "create", "agent-test", "--template", "example", "--no-open")
	shellPane := runCommand(t, "", "tmux", "-L", socket, "list-panes", "-t", "=agent-test", "-F", "#{pane_id}")
	var rejectedOut, rejectedErr bytes.Buffer
	rejectedOptions := baseOptions
	rejectedOptions.Stdout, rejectedOptions.Stderr = &rejectedOut, &rejectedErr
	rejected := cli.New(rejectedOptions)
	rejected.SetArgs([]string{"agents", "register", "--project", "agent-test", "--provider", "codex", "--pane", shellPane})
	if err := rejected.Execute(); err == nil || !strings.Contains(err.Error(), "live direct process") {
		t.Fatalf("normal shell registration error = %v", err)
	}
	pane := runCommand(t, "", "tmux", "-L", socket, "new-window", "-d", "-P", "-F", "#{pane_id}", "-t", "=agent-test", "-n", "agent", "--", "cat")

	registration := executeWithOptions(t, baseOptions, nil, "agents", "register", "--project", "agent-test", "--provider", "command", "--label", "review", "--pane", pane, "--", "cat")
	fields := strings.Fields(registration)
	if len(fields) < 4 {
		t.Fatalf("registration output = %q", registration)
	}
	agentID := fields[3]
	var duplicateOut, duplicateErr bytes.Buffer
	duplicateOptions := baseOptions
	duplicateOptions.Stdout, duplicateOptions.Stderr = &duplicateOut, &duplicateErr
	duplicate := cli.New(duplicateOptions)
	duplicate.SetArgs([]string{"agents", "register", "--project", "agent-test", "--provider", "command", "--pane", pane, "--", "cat"})
	if err := duplicate.Execute(); err == nil || !strings.Contains(err.Error(), "already owned by Agent Session") {
		t.Fatalf("duplicate pane registration error = %v", err)
	}
	list := executeWithOptions(t, baseOptions, nil, "agents", "list", "--project", "agent-test", "--format", "json")
	var result struct {
		SchemaVersion int `json:"schemaVersion"`
		Agents        []struct {
			ID           string `json:"id"`
			Status       string `json:"status"`
			Capabilities struct {
				CanResume bool `json:"canResume"`
				CanSend   bool `json:"canSend"`
				CanFocus  bool `json:"canFocus"`
			} `json:"capabilities"`
		} `json:"agents"`
	}
	if err := json.Unmarshal([]byte(list), &result); err != nil {
		t.Fatalf("decode agents list JSON: %v\n%s", err, list)
	}
	if result.SchemaVersion != 1 || len(result.Agents) != 1 || result.Agents[0].ID != agentID || result.Agents[0].Status != "live" || !result.Agents[0].Capabilities.CanSend {
		t.Fatalf("agents list result = %+v", result)
	}

	feedback := "review feedback must remain text\n"
	executeWithOptions(t, baseOptions, strings.NewReader(feedback), "agents", "send", agentID, "--stdin", "--format", "json")
	deadline := time.Now().Add(2 * time.Second)
	for {
		capture := runCommand(t, "", "tmux", "-L", socket, "capture-pane", "-p", "-t", pane)
		if strings.Contains(capture, "review feedback must remain text") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("Agent Session did not receive feedback: %s", capture)
		}
		time.Sleep(25 * time.Millisecond)
	}
	project, err := store.NewProjectStore(baseOptions.StateDir).Find("agent-test")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	project.Status = domain.ProjectArchived
	project.ArchivedAt = &now
	if err := store.NewProjectStore(baseOptions.StateDir).Save(project); err != nil {
		t.Fatal(err)
	}
	archivedList := executeWithOptions(t, baseOptions, nil, "agents", "list", "--project", "agent-test", "--format", "json")
	var archivedResult struct {
		Agents []struct {
			Status       string `json:"status"`
			Capabilities struct {
				CanResume bool `json:"canResume"`
				CanSend   bool `json:"canSend"`
				CanFocus  bool `json:"canFocus"`
			} `json:"capabilities"`
		} `json:"agents"`
	}
	if err := json.Unmarshal([]byte(archivedList), &archivedResult); err != nil || len(archivedResult.Agents) != 1 {
		t.Fatalf("decode archived agents list: result=%+v error=%v", archivedResult, err)
	}
	archivedAgent := archivedResult.Agents[0]
	if archivedAgent.Status != "live" || archivedAgent.Capabilities.CanResume || !archivedAgent.Capabilities.CanSend || !archivedAgent.Capabilities.CanFocus {
		t.Fatalf("live Agent Session in incomplete archive = %+v", archivedAgent)
	}
}

func TestProjectsRemovePlansThenAppliesCleanRemoval(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is not installed")
	}
	t.Setenv("TMUX_PANE", "")

	root := t.TempDir()
	source := filepath.Join(root, "source")
	initGitRepository(t, source)
	configDir := filepath.Join(root, "config")
	if err := os.MkdirAll(filepath.Join(configDir, "templates"), 0o755); err != nil {
		t.Fatal(err)
	}
	template := fmt.Sprintf("version: 1\nname: example\nrepositories:\n  - name: app\n    clone:\n      url: %s\n", source)
	if err := os.WriteFile(filepath.Join(configDir, "templates", "example.yaml"), []byte(template), 0o644); err != nil {
		t.Fatal(err)
	}
	socket := fmt.Sprintf("twt2-test-%d", time.Now().UnixNano())
	t.Cleanup(func() { exec.Command("tmux", "-L", socket, "kill-server").Run() })
	options := cli.Options{ConfigDir: configDir, StateDir: filepath.Join(root, "state"), DataDir: filepath.Join(root, "data"), TmuxSocket: socket}
	executeWithOptions(t, options, nil, "projects", "create", "remove-me", "--template", "example", "--no-open")
	entries, err := os.ReadDir(filepath.Join(root, "data", "projects"))
	if err != nil || len(entries) != 1 {
		t.Fatalf("read Project roots: entries=%v error=%v", entries, err)
	}
	projectRoot := filepath.Join(root, "data", "projects", entries[0].Name())
	executeWithOptions(t, options, nil, "projects", "archive", "remove-me")

	plan := executeWithOptions(t, options, nil, "projects", "remove", "remove-me")
	for _, want := range []string{"stop_tmux_session", "remove_worktree", "delete_branch", "delete_ownership_marker", "remove_project_root", "delete_project_state", "Run again with --apply"} {
		if !strings.Contains(plan, want) {
			t.Fatalf("removal plan does not contain %q: %s", want, plan)
		}
	}
	if _, err := os.Stat(projectRoot); err != nil {
		t.Fatalf("plan changed Project data: %v", err)
	}
	executeWithOptions(t, options, nil, "projects", "remove", "remove-me", "--apply")
	if _, err := os.Stat(projectRoot); !os.IsNotExist(err) {
		t.Fatalf("Project root still exists after removal: %v", err)
	}
	if output := executeWithOptions(t, options, nil, "projects", "list"); output != "" {
		t.Fatalf("projects list after removal = %q", output)
	}
	if err := exec.Command("tmux", "-L", socket, "has-session", "-t", "=remove-me").Run(); err == nil {
		t.Fatal("Project tmux session still exists after removal")
	}
}

func TestProjectsRemoveRefusesDirtyWorktree(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is not installed")
	}
	t.Setenv("TMUX_PANE", "")

	root := t.TempDir()
	source := filepath.Join(root, "source")
	initGitRepository(t, source)
	configDir := filepath.Join(root, "config")
	if err := os.MkdirAll(filepath.Join(configDir, "templates"), 0o755); err != nil {
		t.Fatal(err)
	}
	template := fmt.Sprintf("version: 1\nname: example\nrepositories:\n  - name: app\n    clone:\n      url: %s\n", source)
	if err := os.WriteFile(filepath.Join(configDir, "templates", "example.yaml"), []byte(template), 0o644); err != nil {
		t.Fatal(err)
	}
	socket := fmt.Sprintf("twt2-test-%d", time.Now().UnixNano())
	t.Cleanup(func() { exec.Command("tmux", "-L", socket, "kill-server").Run() })
	options := cli.Options{ConfigDir: configDir, StateDir: filepath.Join(root, "state"), DataDir: filepath.Join(root, "data"), TmuxSocket: socket}
	executeWithOptions(t, options, nil, "projects", "create", "keep-me", "--template", "example", "--no-open")
	entries, _ := os.ReadDir(filepath.Join(root, "data", "projects"))
	projectRoot := filepath.Join(root, "data", "projects", entries[0].Name())
	if err := os.WriteFile(filepath.Join(projectRoot, "app", "unsaved.txt"), []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	executeWithOptions(t, options, nil, "projects", "archive", "keep-me")
	var stdout, stderr bytes.Buffer
	options.Stdout, options.Stderr = &stdout, &stderr
	command := cli.New(options)
	command.SetArgs([]string{"projects", "remove", "keep-me", "--apply", "--dry-run"})
	err := command.Execute()
	if err == nil || !strings.Contains(err.Error(), "uncommitted changes") {
		t.Fatalf("dirty dry-run removal error = %v", err)
	}
	stdout.Reset()
	stderr.Reset()
	command = cli.New(options)
	command.SetArgs([]string{"projects", "remove", "keep-me", "--apply"})
	err = command.Execute()
	if err == nil || !strings.Contains(err.Error(), "uncommitted changes") {
		t.Fatalf("dirty removal error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(projectRoot, "app", "unsaved.txt")); err != nil {
		t.Fatalf("dirty removal changed Project data: %v", err)
	}
}

func TestContextStorageAndDoctorProvideStableJSON(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is not installed")
	}

	root := t.TempDir()
	source := filepath.Join(root, "source")
	initGitRepository(t, source)
	configDir := filepath.Join(root, "config")
	if err := os.MkdirAll(filepath.Join(configDir, "templates"), 0o755); err != nil {
		t.Fatal(err)
	}
	template := fmt.Sprintf("version: 1\nname: example\nrepositories:\n  - name: app\n    clone:\n      url: %s\n", source)
	if err := os.WriteFile(filepath.Join(configDir, "templates", "example.yaml"), []byte(template), 0o644); err != nil {
		t.Fatal(err)
	}
	socket := fmt.Sprintf("twt2-test-%d", time.Now().UnixNano())
	t.Cleanup(func() { exec.Command("tmux", "-L", socket, "kill-server").Run() })
	options := cli.Options{ConfigDir: configDir, StateDir: filepath.Join(root, "state"), DataDir: filepath.Join(root, "data"), TmuxSocket: socket}
	createOutput := executeWithOptions(t, options, nil, "projects", "create", "json-test", "--template", "example", "--output", "json")
	if !strings.Contains(createOutput, `"status":"applied"`) {
		t.Fatalf("JSON create output = %s", createOutput)
	}

	list := executeWithOptions(t, options, nil, "projects", "list", "--format", "json")
	var projects struct {
		SchemaVersion int `json:"schemaVersion"`
		Projects      []struct {
			ID string `json:"id"`
		} `json:"projects"`
	}
	if err := json.Unmarshal([]byte(list), &projects); err != nil || projects.SchemaVersion != 1 || len(projects.Projects) != 1 {
		t.Fatalf("projects JSON = %s; decode error = %v", list, err)
	}
	pane := runCommand(t, "", "tmux", "-L", socket, "list-panes", "-t", "=json-test", "-F", "#{pane_id}")
	t.Setenv("TWT2_PROJECT_ID", "")
	t.Setenv("TMUX_PANE", pane)
	contextOutput := executeWithOptions(t, options, nil, "context", "--format", "json")
	if !strings.Contains(contextOutput, `"name":"json-test"`) || strings.Contains(contextOutput, "tmuxSession") || strings.Contains(contextOutput, `"root"`) {
		t.Fatalf("context JSON has an invalid contract: %s", contextOutput)
	}
	projectRoots, err := os.ReadDir(filepath.Join(root, "data", "projects"))
	if err != nil || len(projectRoots) != 1 {
		t.Fatalf("read Project roots: %v, %v", projectRoots, err)
	}
	t.Setenv("TMUX_PANE", "%not-a-real-pane")
	explicitContext := executeWithOptions(t, options, nil, "context", "--directory", filepath.Join(root, "data", "projects", projectRoots[0].Name(), "app"), "--output", "json")
	if !strings.Contains(explicitContext, `"name":"json-test"`) || !strings.Contains(explicitContext, `"repositoryName":"app"`) {
		t.Fatalf("explicit directory context JSON = %s", explicitContext)
	}

	storageOutput := executeWithOptions(t, options, nil, "storage", "status", "--format", "json")
	var storageResult struct {
		SchemaVersion int `json:"schemaVersion"`
		Storage       struct {
			TotalBytes    int64 `json:"totalBytes"`
			ProjectCount  int   `json:"projectCount"`
			WorktreeCount int   `json:"worktreeCount"`
		} `json:"storage"`
	}
	if err := json.Unmarshal([]byte(storageOutput), &storageResult); err != nil || storageResult.SchemaVersion != 1 || storageResult.Storage.TotalBytes <= 0 || storageResult.Storage.ProjectCount != 1 || storageResult.Storage.WorktreeCount != 1 {
		t.Fatalf("storage JSON = %s; decode error = %v", storageOutput, err)
	}
	doctorOutput := executeWithOptions(t, options, nil, "doctor", "--format", "json")
	if !strings.Contains(doctorOutput, `"healthy":true`) {
		t.Fatalf("doctor JSON = %s", doctorOutput)
	}
}

func executeWithOptions(t *testing.T, options cli.Options, stdin *strings.Reader, args ...string) string {
	t.Helper()
	var stdout, stderr bytes.Buffer
	options.Stdout = &stdout
	options.Stderr = &stderr
	command := cli.New(options)
	if stdin != nil {
		command.SetIn(stdin)
	}
	command.SetArgs(args)
	if err := command.Execute(); err != nil {
		t.Fatalf("twt2 %s: %v\nstderr: %s", strings.Join(args, " "), err, stderr.String())
	}
	return stdout.String()
}

func initGitRepository(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
	runCommand(t, "", "git", "init", "-q", "-b", "main", path)
	runCommand(t, path, "git", "config", "user.name", "twt2 test")
	runCommand(t, path, "git", "config", "user.email", "test@example.com")
	if err := os.WriteFile(filepath.Join(path, "README.md"), []byte("test repository\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path, "init.sh"), []byte("#!/bin/sh\nset -eu\nprintf 'initialized\\n' > .initialized\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	runCommand(t, path, "git", "add", "README.md", "init.sh")
	runCommand(t, path, "git", "commit", "-qm", "initial commit")
}

func runCommand(t *testing.T, dir, name string, args ...string) string {
	t.Helper()
	command := exec.Command(name, args...)
	command.Dir = dir
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("%s %s: %v\n%s", name, strings.Join(args, " "), err, output)
	}
	return strings.TrimSpace(string(output))
}
