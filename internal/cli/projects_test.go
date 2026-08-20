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
		CreatedAt:    time.Now().UTC().Add(-3 * 24 * time.Hour),
	}
	if err := store.NewProjectStore(filepath.Join(root, "state")).Save(project); err != nil {
		t.Fatal(err)
	}

	output := executeWithOptions(t, cli.Options{
		ConfigDir: filepath.Join(root, "config"),
		StateDir:  filepath.Join(root, "state"),
		DataDir:   filepath.Join(root, "data"),
	}, nil, "projects", "list")

	want := "NAME           TEMPLATE     STATUS  AGE\n" +
		"everysphere-0  everysphere  active  3d\n"
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
	if strings.Contains(jsonOutput, `"bytes"`) {
		t.Fatalf("projects list JSON still has a bytes field: %s", jsonOutput)
	}
}

func TestProjectsListShowsRecentActiveProjectsBeforeArchives(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	now := time.Now().UTC()
	archivedAt := now.Add(-5 * time.Hour)
	projects := []domain.Project{
		{Version: domain.ProjectVersion, ID: "old-active", Name: "old-active", TemplateName: "example", Status: domain.ProjectActive, CreatedAt: now.Add(-2 * 24 * time.Hour)},
		{Version: domain.ProjectVersion, ID: "new-archive", Name: "new-archive", TemplateName: "example", Status: domain.ProjectArchived, CreatedAt: now.Add(-30 * time.Minute), ArchivedAt: &archivedAt},
		{Version: domain.ProjectVersion, ID: "new-active", Name: "new-active", TemplateName: "example", Status: domain.ProjectActive, CreatedAt: now.Add(-time.Hour)},
	}
	projectStore := store.NewProjectStore(filepath.Join(root, "state"))
	for _, project := range projects {
		if err := projectStore.Save(project); err != nil {
			t.Fatal(err)
		}
	}

	options := cli.Options{
		ConfigDir: filepath.Join(root, "config"),
		StateDir:  filepath.Join(root, "state"),
		DataDir:   filepath.Join(root, "data"),
	}
	output := executeWithOptions(t, options, nil, "projects", "list", "--limit", "2")
	want := "NAME        TEMPLATE  STATUS  AGE\n" +
		"new-active  example   active  1h\n" +
		"old-active  example   active  2d\n"
	if output != want {
		t.Fatalf("limited projects list output = %q, want %q", output, want)
	}

	// An archived Project shows its age since the archive time.
	fullOutput := executeWithOptions(t, options, nil, "projects", "list")
	if !strings.Contains(fullOutput, "new-archive  example   archived  5h\n") {
		t.Fatalf("projects list archived age = %q", fullOutput)
	}
}

func TestProjectsListReturnsTableFlushErrors(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	project := domain.Project{
		Version:      domain.ProjectVersion,
		ID:           "output-error-project",
		Name:         "output-error",
		TemplateName: "example",
		Status:       domain.ProjectActive,
		CreatedAt:    time.Now().UTC(),
	}
	if err := store.NewProjectStore(filepath.Join(root, "state")).Save(project); err != nil {
		t.Fatal(err)
	}

	command := cli.New(cli.Options{
		ConfigDir: filepath.Join(root, "config"),
		StateDir:  filepath.Join(root, "state"),
		DataDir:   filepath.Join(root, "data"),
		Stdout:    errorWriter{},
		Stderr:    &bytes.Buffer{},
	})
	command.SetArgs([]string{"projects", "list"})
	if err := command.Execute(); err == nil || !strings.Contains(err.Error(), "test output failure") {
		t.Fatalf("projects list output error = %v", err)
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
	if err := exec.Command("tmux", "-L", socket, "has-session", "-t", "=twt2-archive-me").Run(); err == nil {
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
	if err := exec.Command("tmux", "-L", socket, "has-session", "-t", "=twt2-archive-me").Run(); err != nil {
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
	pane := runCommand(t, "", "tmux", "-L", socket, "list-panes", "-t", "=twt2-archive-me", "-F", "#{pane_id}")
	t.Setenv("TMUX_PANE", pane)
	command = cli.New(options)
	command.SetArgs([]string{"projects", "archive", project.ID})
	err = command.Execute()
	// Inside the owned session, archive relocates the calling tmux client
	// first. Without a client, the relocation fails and nothing changes.
	if err == nil || !strings.Contains(err.Error(), "not active in a client") {
		t.Fatalf("archive from the target session error = %v", err)
	}
	stillActive, err = store.NewProjectStore(options.StateDir).Find(project.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stillActive.Status != domain.ProjectActive {
		t.Fatalf("self-archive changed Project status to %q", stillActive.Status)
	}

	blockedPlan := executeWithOptions(t, options, nil, "projects", "remove", project.ID)
	if !strings.Contains(blockedPlan, "Blocked:") || !strings.Contains(blockedPlan, "not archived") || !strings.Contains(blockedPlan, "The removal is blocked") {
		t.Fatalf("active Project removal plan = %q", blockedPlan)
	}
	now := time.Now().UTC()
	stillActive.Status = domain.ProjectArchived
	stillActive.ArchivedAt = &now
	if err := store.NewProjectStore(options.StateDir).Save(stillActive); err != nil {
		t.Fatal(err)
	}
	command = cli.New(options)
	command.SetArgs([]string{"projects", "remove", project.ID, "--apply"})
	err = command.Execute()
	if err == nil || !strings.Contains(err.Error(), "cannot remove") || !strings.Contains(err.Error(), "inside its tmux session") {
		t.Fatalf("self-removal error = %v", err)
	}
	if err := exec.Command("tmux", "-L", socket, "has-session", "-t", "=twt2-archive-me").Run(); err != nil {
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

	windows := runCommand(t, "", "tmux", "-L", socket, "list-windows", "-t", "=twt2-auth-refresh", "-F", "#{window_name}")
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
	runCommand(t, "", "tmux", "-L", socket, "new-session", "-d", "-s", "twt2-collision")
	options := cli.Options{ConfigDir: configDir, StateDir: filepath.Join(root, "state"), DataDir: filepath.Join(root, "data"), TmuxSocket: socket}
	executeWithOptions(t, options, nil, "projects", "create", "collision", "--template", "example", "--no-open")
	sessions := strings.Split(runCommand(t, "", "tmux", "-L", socket, "list-sessions", "-F", "#{session_name}|#{@twt2_project_id}"), "\n")
	if len(sessions) != 2 || sessions[0] != "twt2-collision|" || !strings.HasPrefix(sessions[1], "twt2-collision-") || strings.HasSuffix(sessions[1], "|") {
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

	windows := runCommand(t, "", "tmux", "-L", socket, "list-windows", "-t", "=twt2-docs-refresh", "-F", "#{window_name}")
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
	shellPane := runCommand(t, "", "tmux", "-L", socket, "list-panes", "-t", "=twt2-agent-test", "-F", "#{pane_id}")
	var rejectedOut, rejectedErr bytes.Buffer
	rejectedOptions := baseOptions
	rejectedOptions.Stdout, rejectedOptions.Stderr = &rejectedOut, &rejectedErr
	rejected := cli.New(rejectedOptions)
	rejected.SetArgs([]string{"agents", "register", "--project", "agent-test", "--provider", "codex", "--pane", shellPane})
	if err := rejected.Execute(); err == nil || !strings.Contains(err.Error(), "live direct process") {
		t.Fatalf("normal shell registration error = %v", err)
	}
	pane := runCommand(t, "", "tmux", "-L", socket, "new-window", "-d", "-P", "-F", "#{pane_id}", "-t", "=twt2-agent-test", "-n", "agent", "--", "cat")

	registration := executeWithOptions(t, baseOptions, nil, "agents", "register", "--project", "agent-test", "--provider", "command", "--label", "review", "--pane", pane, "--", "cat")
	fields := strings.Fields(registration)
	if len(fields) < 4 {
		t.Fatalf("registration output = %q", registration)
	}
	agentID := fields[3]
	projectStore := store.NewProjectStore(baseOptions.StateDir)
	otherProject, err := projectStore.Find("agent-test")
	if err != nil {
		t.Fatal(err)
	}
	otherProject.ID = "other-project"
	otherProject.Name = "other-project"
	otherProject.Root = filepath.Join(root, "data", "projects", "other-project")
	otherProject.TmuxSession = "other-project"
	for index := range otherProject.Repositories {
		otherProject.Repositories[index].Path = filepath.Join(otherProject.Root, otherProject.Repositories[index].Name)
	}
	if err := projectStore.Save(otherProject); err != nil {
		t.Fatal(err)
	}
	wrongProject := cli.New(baseOptions)
	wrongProject.SetIn(strings.NewReader("must not reach the Agent Session\n"))
	wrongProject.SetArgs([]string{"agents", "send", agentID, "--project", otherProject.ID, "--stdin"})
	if err := wrongProject.Execute(); err == nil || !strings.Contains(err.Error(), "does not belong") {
		t.Fatalf("cross-Project send error = %v", err)
	}
	wrongCapture := runCommand(t, "", "tmux", "-L", socket, "capture-pane", "-p", "-t", pane)
	if strings.Contains(wrongCapture, "must not reach") {
		t.Fatalf("cross-Project feedback reached the Agent Session: %s", wrongCapture)
	}
	var duplicateOut, duplicateErr bytes.Buffer
	duplicateOptions := baseOptions
	duplicateOptions.Stdout, duplicateOptions.Stderr = &duplicateOut, &duplicateErr
	duplicate := cli.New(duplicateOptions)
	duplicate.SetArgs([]string{"agents", "register", "--project", "agent-test", "--provider", "command", "--pane", pane, "--", "cat"})
	if err := duplicate.Execute(); err == nil || !strings.Contains(err.Error(), "already owned by Agent Session") {
		t.Fatalf("duplicate pane registration error = %v", err)
	}
	list := executeWithOptions(t, baseOptions, nil, "agents", "list", "--project", "agent-test", "--output", "json")
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
	executeWithOptions(t, baseOptions, strings.NewReader(feedback), "agents", "send", agentID, "--stdin", "--output", "json")
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
	archivedList := executeWithOptions(t, baseOptions, nil, "agents", "list", "--project", "agent-test", "--output", "json")
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
	project, err := store.NewProjectStore(options.StateDir).Find("remove-me")
	if err != nil {
		t.Fatal(err)
	}
	snapshotRoot := filepath.Join(options.StateDir, "snapshots", "projects", project.ID)
	if err := os.MkdirAll(snapshotRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	marker := fmt.Sprintf("{\"version\":1,\"owner\":\"twt2\",\"projectId\":%q}\n", project.ID)
	if err := os.WriteFile(filepath.Join(snapshotRoot, ".twt2-snapshot.json"), []byte(marker), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(snapshotRoot, "latest.md"), []byte("Project transcript\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	executeWithOptions(t, options, nil, "projects", "archive", "remove-me")
	if _, err := os.Stat(filepath.Join(snapshotRoot, "latest.md")); err != nil {
		t.Fatalf("archive removed the Transcript Snapshot: %v", err)
	}
	wrongMarker := "{\"version\":1,\"owner\":\"twt2\",\"projectId\":\"another-project\"}\n"
	if err := os.WriteFile(filepath.Join(snapshotRoot, ".twt2-snapshot.json"), []byte(wrongMarker), 0o600); err != nil {
		t.Fatal(err)
	}
	var rejectedOut, rejectedErr bytes.Buffer
	rejectedOptions := options
	rejectedOptions.Stdout, rejectedOptions.Stderr = &rejectedOut, &rejectedErr
	rejected := cli.New(rejectedOptions)
	rejected.SetArgs([]string{"projects", "remove", "remove-me", "--apply"})
	if err := rejected.Execute(); err == nil || !strings.Contains(err.Error(), "conflicting ownership marker") {
		t.Fatalf("conflicting Transcript Snapshot removal error = %v", err)
	}
	if _, err := os.Stat(projectRoot); err != nil {
		t.Fatalf("rejected removal changed Project data: %v", err)
	}
	if err := os.WriteFile(filepath.Join(snapshotRoot, ".twt2-snapshot.json"), []byte(marker), 0o600); err != nil {
		t.Fatal(err)
	}

	plan := executeWithOptions(t, options, nil, "projects", "remove", "remove-me")
	for _, want := range []string{"stop_tmux_session", "remove_worktree", "delete_branch", "keep_repository_cache", "delete_ownership_marker", "remove_project_root", "delete_transcript_snapshot", "delete_environment_record", "delete_project_state", "Run again with --apply"} {
		if !strings.Contains(plan, want) {
			t.Fatalf("removal plan does not contain %q: %s", want, plan)
		}
	}
	planJSON := executeWithOptions(t, options, nil, "projects", "remove", "remove-me", "--output", "json")
	var removal struct {
		SchemaVersion int  `json:"schemaVersion"`
		Applied       bool `json:"applied"`
		Blockers      []struct {
			Code string `json:"code"`
		} `json:"blockers"`
		Plan struct {
			Bytes   int64 `json:"bytes"`
			Actions []struct {
				Kind   string `json:"kind"`
				Target string `json:"target"`
			} `json:"actions"`
			Blockers []struct {
				Code string `json:"code"`
			} `json:"blockers"`
		} `json:"plan"`
	}
	if err := json.Unmarshal([]byte(planJSON), &removal); err != nil {
		t.Fatalf("decode removal plan JSON: %v\n%s", err, planJSON)
	}
	// A plan does not measure the Project size; only an applied removal and
	// the bulk plan report bytes.
	if removal.SchemaVersion != 1 || removal.Applied || removal.Plan.Bytes != 0 {
		t.Fatalf("removal plan JSON metadata = %+v", removal)
	}
	if len(removal.Blockers) != 0 || len(removal.Plan.Blockers) != 0 {
		t.Fatalf("clean removal plan has blockers: %s", planJSON)
	}
	kinds := map[string]bool{}
	for _, action := range removal.Plan.Actions {
		kinds[action.Kind] = true
	}
	for _, want := range []string{"delete_environment_record", "keep_repository_cache"} {
		if !kinds[want] {
			t.Fatalf("removal plan JSON has no %q action: %s", want, planJSON)
		}
	}
	if _, err := os.Stat(projectRoot); err != nil {
		t.Fatalf("plan changed Project data: %v", err)
	}
	if _, err := os.Stat(filepath.Join(snapshotRoot, "latest.md")); err != nil {
		t.Fatalf("plan changed the Transcript Snapshot: %v", err)
	}
	executeWithOptions(t, options, nil, "projects", "remove", "remove-me", "--apply")
	if _, err := os.Stat(projectRoot); !os.IsNotExist(err) {
		t.Fatalf("Project root still exists after removal: %v", err)
	}
	if output := executeWithOptions(t, options, nil, "projects", "list"); output != "" {
		t.Fatalf("projects list after removal = %q", output)
	}
	if _, err := os.Stat(snapshotRoot); !os.IsNotExist(err) {
		t.Fatalf("Transcript Snapshot still exists after removal: %v", err)
	}
	if err := exec.Command("tmux", "-L", socket, "has-session", "-t", "=twt2-remove-me").Run(); err == nil {
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

	// The plan renders with blockers and exit code 0 without --apply.
	planOutput := executeWithOptions(t, options, nil, "projects", "remove", "keep-me")
	if !strings.Contains(planOutput, "Blocked:") || !strings.Contains(planOutput, "uncommitted changes") || !strings.Contains(planOutput, "unsaved.txt") || !strings.Contains(planOutput, "The removal is blocked") {
		t.Fatalf("dirty removal plan = %q", planOutput)
	}
	if strings.Contains(planOutput, "Run again with --apply") {
		t.Fatalf("blocked removal plan invites --apply: %q", planOutput)
	}

	var stdout, stderr bytes.Buffer
	options.Stdout, options.Stderr = &stdout, &stderr
	command := cli.New(options)
	command.SetArgs([]string{"projects", "remove", "keep-me", "--apply", "--dry-run"})
	err := command.Execute()
	if err == nil || !strings.Contains(err.Error(), "--apply") {
		t.Fatalf("--dry-run with --apply error = %v", err)
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

	list := executeWithOptions(t, options, nil, "projects", "list", "--output", "json")
	var projects struct {
		SchemaVersion int `json:"schemaVersion"`
		Projects      []struct {
			ID string `json:"id"`
		} `json:"projects"`
	}
	if err := json.Unmarshal([]byte(list), &projects); err != nil || projects.SchemaVersion != 1 || len(projects.Projects) != 1 {
		t.Fatalf("projects JSON = %s; decode error = %v", list, err)
	}
	pane := runCommand(t, "", "tmux", "-L", socket, "list-panes", "-t", "=twt2-json-test", "-F", "#{pane_id}")
	t.Setenv("TWT2_PROJECT_ID", "")
	t.Setenv("TMUX_PANE", pane)
	contextOutput := executeWithOptions(t, options, nil, "context", "--output", "json")
	if !strings.Contains(contextOutput, `"name":"json-test"`) || strings.Contains(contextOutput, "tmuxSession") {
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
	if _, err := store.NewSnapshotStore(options.StateDir).Save(projects.Projects[0].ID, "aa11", "snapshot bytes\n"); err != nil {
		t.Fatal(err)
	}

	storageOutput := executeWithOptions(t, options, nil, "storage", "show", "--output", "json")
	var storageResult struct {
		SchemaVersion int `json:"schemaVersion"`
		Storage       struct {
			TotalBytes    int64 `json:"totalBytes"`
			SnapshotBytes int64 `json:"snapshotBytes"`
			ProjectCount  int   `json:"projectCount"`
			WorktreeCount int   `json:"worktreeCount"`
		} `json:"storage"`
	}
	if err := json.Unmarshal([]byte(storageOutput), &storageResult); err != nil || storageResult.SchemaVersion != 1 || storageResult.Storage.TotalBytes <= 0 || storageResult.Storage.SnapshotBytes < int64(len("snapshot bytes\n")) || storageResult.Storage.ProjectCount != 1 || storageResult.Storage.WorktreeCount != 1 {
		t.Fatalf("storage JSON = %s; decode error = %v", storageOutput, err)
	}
	doctorOutput := executeWithOptions(t, options, nil, "doctor", "--output", "json")
	if !strings.Contains(doctorOutput, `"healthy":true`) {
		t.Fatalf("doctor JSON = %s", doctorOutput)
	}
}

func TestStorageCleanPlansAndRemovesOnlyOrphanTranscriptSnapshots(t *testing.T) {
	root := t.TempDir()
	stateDir := filepath.Join(root, "state")
	now := time.Now().UTC()
	project := domain.Project{
		Version: domain.ProjectVersion, ID: "active-project", Name: "active-project",
		Status: domain.ProjectActive, CreatedAt: now, UpdatedAt: now,
	}
	if err := store.NewProjectStore(stateDir).Save(project); err != nil {
		t.Fatal(err)
	}
	snapshots := store.NewSnapshotStore(stateDir)
	if _, err := snapshots.Save(project.ID, "aa11", "active\n"); err != nil {
		t.Fatal(err)
	}
	if _, err := snapshots.Save("orphan-project", "bb22", "orphan\n"); err != nil {
		t.Fatal(err)
	}
	temporarySnapshot := filepath.Join(stateDir, "snapshots", "projects", ".twt2-snapshot-interrupted")
	if err := os.WriteFile(temporarySnapshot, []byte("incomplete\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	options := cli.Options{ConfigDir: filepath.Join(root, "config"), StateDir: stateDir, DataDir: filepath.Join(root, "data")}
	plan := executeWithOptions(t, options, nil, "storage", "clean")
	if !strings.Contains(plan, "orphan Transcript Snapshot \"orphan-project\"") || !strings.Contains(plan, "incomplete Transcript Snapshot write") || strings.Contains(plan, "active-project") {
		t.Fatalf("snapshot cleanup plan = %q", plan)
	}
	orphanDirectory, err := snapshots.ProjectDir("orphan-project")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(orphanDirectory); err != nil {
		t.Fatalf("cleanup preview changed orphan snapshot: %v", err)
	}
	if _, err := os.Stat(temporarySnapshot); err != nil {
		t.Fatalf("cleanup preview changed incomplete snapshot write: %v", err)
	}
	executeWithOptions(t, options, nil, "storage", "clean", "--apply")
	if _, err := os.Stat(orphanDirectory); !os.IsNotExist(err) {
		t.Fatalf("orphan Transcript Snapshot still exists: %v", err)
	}
	if _, err := os.Stat(temporarySnapshot); !os.IsNotExist(err) {
		t.Fatalf("incomplete Transcript Snapshot write still exists: %v", err)
	}
	activeDirectory, err := snapshots.ProjectDir(project.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(activeDirectory); err != nil {
		t.Fatalf("cleanup changed active Project snapshot: %v", err)
	}
}

func TestProjectsRemoveCancelReturnsRemovingProjectToArchived(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	options := cli.Options{ConfigDir: filepath.Join(root, "config"), StateDir: filepath.Join(root, "state"), DataDir: filepath.Join(root, "data")}
	project := domain.Project{Version: domain.ProjectVersion, ID: "cancel-me-id-0001", Name: "cancel-me", Status: domain.ProjectRemoving, CreatedAt: time.Now().UTC()}
	if err := store.NewProjectStore(options.StateDir).Save(project); err != nil {
		t.Fatal(err)
	}

	output := executeWithOptions(t, options, nil, "projects", "remove", project.ID, "--cancel")
	if !strings.Contains(output, "canceled") || !strings.Contains(output, "archived") {
		t.Fatalf("cancel output = %q", output)
	}
	canceled, err := store.NewProjectStore(options.StateDir).Find(project.ID)
	if err != nil {
		t.Fatal(err)
	}
	if canceled.Status != domain.ProjectArchived || canceled.ArchivedAt == nil {
		t.Fatalf("Project after cancel has status %q and archive time %v", canceled.Status, canceled.ArchivedAt)
	}

	var stdout, stderr bytes.Buffer
	repeatOptions := options
	repeatOptions.Stdout, repeatOptions.Stderr = &stdout, &stderr
	command := cli.New(repeatOptions)
	command.SetArgs([]string{"projects", "remove", project.ID, "--cancel"})
	err = command.Execute()
	if err == nil || !strings.Contains(err.Error(), "cancel requires status") {
		t.Fatalf("cancel of an archived Project error = %v", err)
	}

	command = cli.New(repeatOptions)
	command.SetArgs([]string{"projects", "remove", project.ID, "--cancel", "--apply"})
	err = command.Execute()
	if err == nil || !strings.Contains(err.Error(), "--cancel") {
		t.Fatalf("cancel with apply error = %v", err)
	}
}

func TestProjectsRemoveAllArchivedSelectsByAgeAndSkipsBlocked(t *testing.T) {
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

	projectStore := store.NewProjectStore(options.StateDir)
	ages := map[string]time.Duration{"old-clean": 20 * 24 * time.Hour, "old-dirty": 16 * 24 * time.Hour, "fresh": 2 * 24 * time.Hour}
	roots := map[string]string{}
	for _, name := range []string{"old-clean", "old-dirty", "fresh"} {
		executeWithOptions(t, options, nil, "projects", "create", name, "--template", "example", "--no-open")
		executeWithOptions(t, options, nil, "projects", "archive", name)
		project, err := projectStore.Find(name)
		if err != nil {
			t.Fatal(err)
		}
		roots[name] = project.Root
		archivedAt := time.Now().UTC().Add(-ages[name])
		project.ArchivedAt = &archivedAt
		if err := projectStore.Save(project); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(roots["old-dirty"], "app", "unsaved.txt"), []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}

	// The flags are mutually exclusive with a positional and --cancel.
	for _, args := range [][]string{
		{"projects", "remove", "old-clean", "--all-archived"},
		{"projects", "remove", "--all-archived", "--cancel"},
		{"projects", "remove", "old-clean", "--older-than", "14d"},
		{"projects", "remove", "--all-archived", "--older-than", "2x"},
	} {
		var stdout, stderr bytes.Buffer
		usageOptions := options
		usageOptions.Stdout, usageOptions.Stderr = &stdout, &stderr
		command := cli.New(usageOptions)
		command.SetArgs(args)
		if err := command.Execute(); err == nil {
			t.Fatalf("%v did not fail", args)
		}
	}

	plan := executeWithOptions(t, options, nil, "projects", "remove", "--all-archived", "--older-than", "14d")
	for _, want := range []string{"Project \"old-clean\": age 20d, size ", "Project \"old-dirty\": age 16d, size ", "Blocked:", "uncommitted changes", "Run again with --apply"} {
		if !strings.Contains(plan, want) {
			t.Fatalf("bulk removal plan does not contain %q: %s", want, plan)
		}
	}
	if strings.Contains(plan, "fresh") {
		t.Fatalf("bulk removal plan selected a fresh archive: %s", plan)
	}
	if strings.Contains(plan, "size 0 B") {
		t.Fatalf("bulk removal plan has no Project size: %s", plan)
	}
	for _, name := range []string{"old-clean", "old-dirty", "fresh"} {
		if _, err := os.Stat(roots[name]); err != nil {
			t.Fatalf("bulk removal plan changed Project %q: %v", name, err)
		}
	}

	planJSON := executeWithOptions(t, options, nil, "projects", "remove", "--all-archived", "--output", "json")
	var bulk struct {
		SchemaVersion int  `json:"schemaVersion"`
		Applied       bool `json:"applied"`
		RemovedCount  int  `json:"removedCount"`
		SkippedCount  int  `json:"skippedCount"`
		Plans         []struct {
			ProjectName string `json:"projectName"`
			ArchivedAt  string `json:"archivedAt"`
			Bytes       int64  `json:"bytes"`
		} `json:"plans"`
	}
	if err := json.Unmarshal([]byte(planJSON), &bulk); err != nil {
		t.Fatalf("decode bulk removal JSON: %v\n%s", err, planJSON)
	}
	if bulk.SchemaVersion != 1 || bulk.Applied || bulk.RemovedCount != 0 || bulk.SkippedCount != 0 || len(bulk.Plans) != 3 {
		t.Fatalf("bulk removal JSON metadata = %+v", bulk)
	}
	if bulk.Plans[0].ProjectName != "old-clean" || bulk.Plans[0].ArchivedAt == "" || bulk.Plans[0].Bytes <= 0 {
		t.Fatalf("bulk removal JSON first plan = %+v", bulk.Plans[0])
	}

	applied := executeWithOptions(t, options, nil, "projects", "remove", "--all-archived", "--older-than", "14d", "--apply")
	if !strings.Contains(applied, "Removed 1 Projects (") || !strings.Contains(applied, "Skipped 1 blocked Projects.") {
		t.Fatalf("bulk removal apply output = %q", applied)
	}
	if _, err := os.Stat(roots["old-clean"]); !os.IsNotExist(err) {
		t.Fatalf("bulk removal apply kept the clean Project root: %v", err)
	}
	if _, err := os.Stat(filepath.Join(roots["old-dirty"], "app", "unsaved.txt")); err != nil {
		t.Fatalf("bulk removal apply changed the blocked Project: %v", err)
	}
	if _, err := os.Stat(roots["fresh"]); err != nil {
		t.Fatalf("bulk removal apply changed the fresh archive: %v", err)
	}
	dirty, err := projectStore.Find("old-dirty")
	if err != nil || dirty.Status != domain.ProjectArchived {
		t.Fatalf("blocked Project after bulk apply: status=%q error=%v", dirty.Status, err)
	}
	if _, err := projectStore.Find("old-clean"); err == nil {
		t.Fatal("bulk removal apply kept the clean Project record")
	}
}

func TestParseAgeDuration(t *testing.T) {
	t.Parallel()

	valid := map[string]time.Duration{
		"14d": 14 * 24 * time.Hour,
		"0d":  0,
		"36h": 36 * time.Hour,
		"30m": 30 * time.Minute,
	}
	for value, want := range valid {
		got, err := cli.ParseAgeDuration(value)
		if err != nil || got != want {
			t.Fatalf("ParseAgeDuration(%q) = %v, %v; want %v", value, got, err, want)
		}
	}
	for _, value := range []string{"", "d", "14", "14x", "-3d", "1.5h", "d14", "3 d"} {
		if _, err := cli.ParseAgeDuration(value); err == nil {
			t.Fatalf("ParseAgeDuration(%q) did not fail", value)
		}
	}
}

func TestProjectsArchiveStopsAndReportsLiveAgentSessions(t *testing.T) {
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
	executeWithOptions(t, options, nil, "projects", "create", "agent-archive", "--template", "example", "--no-open")
	pane := runCommand(t, "", "tmux", "-L", socket, "new-window", "-d", "-P", "-F", "#{pane_id}", "-t", "=twt2-agent-archive", "-n", "agent", "--", "cat")
	registration := executeWithOptions(t, options, nil, "agents", "register", "--project", "agent-archive", "--provider", "command", "--label", "review", "--pane", pane, "--", "cat")
	fields := strings.Fields(registration)
	if len(fields) < 4 {
		t.Fatalf("registration output = %q", registration)
	}
	agentID := fields[3]
	project, err := store.NewProjectStore(options.StateDir).Find("agent-archive")
	if err != nil {
		t.Fatal(err)
	}

	output := executeWithOptions(t, options, nil, "projects", "archive", "agent-archive")
	if !strings.Contains(output, "Stopping 1 live Agent Sessions:") || !strings.Contains(output, agentID) || !strings.Contains(output, "command") || !strings.Contains(output, "review") {
		t.Fatalf("archive output with a live Agent Session = %q", output)
	}
	if !strings.Contains(output, "Archived Project \"agent-archive\"") {
		t.Fatalf("archive output = %q", output)
	}
	if err := exec.Command("tmux", "-L", socket, "has-session", "-t", "=twt2-agent-archive").Run(); err == nil {
		t.Fatal("archive kept the Project tmux session")
	}
	agents, err := store.NewAgentStore(options.StateDir).List(project.ID)
	if err != nil || len(agents) != 1 {
		t.Fatalf("read Agent Sessions after archive: agents=%v error=%v", agents, err)
	}
	stopped := agents[0]
	if stopped.TmuxPane != "" || stopped.PaneCommand != "" || stopped.PaneStart != "" {
		t.Fatalf("archive kept pane identity on the Agent Session: %+v", stopped)
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
