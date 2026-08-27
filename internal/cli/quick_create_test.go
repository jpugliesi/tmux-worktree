package cli_test

import (
	"bytes"
	"fmt"
	"io"
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

func TestNextPromptsSwitchesThenArchivesTheCurrentWorkspace(t *testing.T) {
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
	templatePath := filepath.Join(configDir, "templates", "example.yaml")
	template := fmt.Sprintf("version: 1\nname: example\nrepositories:\n  - name: app\n    clone:\n      url: %s\n", source)
	if err := os.WriteFile(templatePath, []byte(template), 0o644); err != nil {
		t.Fatal(err)
	}
	socket := fmt.Sprintf("twt-test-%d", time.Now().UnixNano())
	t.Cleanup(func() { exec.Command("tmux", "-L", socket, "kill-server").Run() })
	options := cli.Options{ConfigDir: configDir, StateDir: filepath.Join(root, "state"), DataDir: filepath.Join(root, "data"), TicketsHome: filepath.Join(root, "tickets"), TmuxSocket: socket}
	executeWithOptions(t, options, nil, "tickets", "init")
	executeWithOptions(t, options, nil, "create", "old-workspace", "--template", "example", "--no-open")
	oldWorkspace, err := store.NewWorkspaceStore(options.StateDir).Find("old-workspace")
	if err != nil {
		t.Fatal(err)
	}
	oldPane := runCommand(t, "", "tmux", "-L", socket, "list-panes", "-t", "=example-old-workspace", "-F", "#{pane_id}")
	t.Setenv("TMUX_PANE", oldPane)
	t.Setenv("TWT_WORKSPACE_ID", "stale-workspace-id")
	runCommand(t, "", "tmux", "-L", socket, "set-option", "-t", oldPane, "@twt_workspace_id", oldWorkspace.Name)
	var invalidIDOutput, invalidIDError bytes.Buffer
	invalidIDOptions := options
	invalidIDOptions.Stdout, invalidIDOptions.Stderr = &invalidIDOutput, &invalidIDError
	invalidIDCommand := cli.New(invalidIDOptions)
	invalidIDCommand.SetArgs(forceTextOutput([]string{"next", "invalid-id-workspace"}))
	err = invalidIDCommand.Execute()
	if err == nil || !strings.Contains(err.Error(), "does not contain an immutable Workspace ID") {
		t.Fatalf("quick create with a Workspace name in tmux metadata = %v", err)
	}
	if _, err := store.NewWorkspaceStore(options.StateDir).Find("invalid-id-workspace"); err == nil {
		t.Fatal("quick create accepted a Workspace name as tmux identity")
	}
	runCommand(t, "", "tmux", "-L", socket, "set-option", "-t", oldPane, "@twt_workspace_id", oldWorkspace.ID)

	latestTemplate := strings.Replace(template, "  - name: app\n", "  - name: app\n    window_name: latest-app\n", 1)
	if err := os.WriteFile(templatePath, []byte(latestTemplate), 0o644); err != nil {
		t.Fatal(err)
	}
	dryRun := executeWithOptions(t, options, nil, "next", "dry-workspace", "--dry-run")
	if !strings.Contains(dryRun, "workspaces.next: valid") {
		t.Fatalf("quick create dry-run output = %s", dryRun)
	}
	if _, err := store.NewWorkspaceStore(options.StateDir).Find("dry-workspace"); err == nil {
		t.Fatal("quick create dry-run created a Workspace")
	}
	for _, jsonArgs := range [][]string{
		{"next", "json-workspace", "--output", "json"},
		{"next", "json-workspace", "--dry-run", "--output", "json"},
	} {
		var jsonOutput, jsonError bytes.Buffer
		jsonOptions := options
		jsonOptions.Stdout, jsonOptions.Stderr = &jsonOutput, &jsonError
		jsonCommand := cli.New(jsonOptions)
		jsonCommand.SetArgs(forceTextOutput(jsonArgs))
		err = jsonCommand.Execute()
		if err == nil || !strings.Contains(err.Error(), "use 'twt create' for JSON automation") {
			t.Fatalf("quick create JSON error for %v = %v", jsonArgs, err)
		}
		if _, err := store.NewWorkspaceStore(options.StateDir).Find("json-workspace"); err == nil {
			t.Fatalf("quick create with JSON output created a Workspace for %v", jsonArgs)
		}
	}
	var missingNameOutput, missingNameError bytes.Buffer
	missingNameOptions := options
	missingNameOptions.Stdout, missingNameOptions.Stderr = &missingNameOutput, &missingNameError
	missingName := cli.New(missingNameOptions)
	missingName.SetIn(strings.NewReader(""))
	missingName.SetArgs(forceTextOutput([]string{"next", "--dry-run"}))
	err = missingName.Execute()
	if err == nil || !strings.Contains(err.Error(), "no Workspace name was given") {
		t.Fatalf("quick create without a Workspace name = %v", err)
	}
	attachControlClient(t, socket, "example-old-workspace")

	var promptOutput, promptError bytes.Buffer
	promptOptions := options
	promptOptions.Stdout, promptOptions.Stderr = &promptOutput, &promptError
	promptCommand := cli.New(promptOptions)
	promptCommand.SetIn(strings.NewReader("new-workspace\n"))
	promptCommand.SetArgs(forceTextOutput([]string{"next"}))
	if err := promptCommand.Execute(); err != nil {
		t.Fatalf("quick create with prompt: %v", err)
	}
	if !strings.HasPrefix(promptError.String(), "Workspace name: ") {
		t.Fatalf("quick create prompt = %q", promptError.String())
	}
	if !strings.Contains(promptError.String(), "Step ") || !strings.Contains(promptError.String(), "Base: origin/main @ ") {
		t.Fatalf("quick create progress = %q", promptError.String())
	}
	output := promptOutput.String()
	if !strings.Contains(output, "Created Workspace \"new-workspace\"") {
		t.Fatalf("quick create output = %q", output)
	}
	newWorkspace, err := store.NewWorkspaceStore(options.StateDir).Find("new-workspace")
	if err != nil {
		t.Fatal(err)
	}
	oldWorkspace, err = store.NewWorkspaceStore(options.StateDir).Find(oldWorkspace.ID)
	if err != nil || oldWorkspace.Status != domain.WorkspaceArchived {
		t.Fatalf("old Workspace after quick create: status=%q error=%v", oldWorkspace.Status, err)
	}
	if newWorkspace.Status != domain.WorkspaceActive || newWorkspace.TemplateName != "example" || newWorkspace.Repositories[0].WindowName != "latest-app" {
		t.Fatalf("new Workspace does not use the latest Workspace Template: %+v", newWorkspace)
	}
	clientSession := runCommand(t, "", "tmux", "-L", socket, "list-clients", "-F", "#{session_name}")
	if clientSession != newWorkspace.TmuxSession {
		t.Fatalf("client session = %q, want %q", clientSession, newWorkspace.TmuxSession)
	}

	newPane := runCommand(t, "", "tmux", "-L", socket, "list-panes", "-t", "=example-new-workspace", "-F", "#{pane_id}")
	t.Setenv("TMUX_PANE", newPane)
	var stdout, stderr bytes.Buffer
	options.Stdout, options.Stderr = &stdout, &stderr
	failingTemplate := latestTemplate + "    initialize:\n      command: [\"false\"]\n"
	if err := os.WriteFile(templatePath, []byte(failingTemplate), 0o644); err != nil {
		t.Fatal(err)
	}
	stdout.Reset()
	stderr.Reset()
	command := cli.New(options)
	command.SetArgs(forceTextOutput([]string{"next", "setup-fails"}))
	err = command.Execute()
	if err == nil || !strings.Contains(err.Error(), "initialization") {
		t.Fatalf("quick create setup failure = %v", err)
	}
	if _, findErr := store.NewWorkspaceStore(options.StateDir).Find("setup-fails"); findErr == nil {
		t.Fatal("repository initialization failure created a Workspace")
	}
	environments, findErr := store.NewEnvironmentStore(options.StateDir).List()
	if findErr != nil {
		t.Fatal(findErr)
	}
	failedEnvironments := 0
	for _, environment := range environments {
		if environment.Status == domain.EnvironmentFailed {
			failedEnvironments++
		}
	}
	if failedEnvironments == 0 {
		t.Fatal("repository initialization failure did not keep a failed Prepared Environment record")
	}
	currentAfterFailure, findErr := store.NewWorkspaceStore(options.StateDir).Find(newWorkspace.ID)
	if findErr != nil || currentAfterFailure.Status != domain.WorkspaceActive {
		t.Fatalf("current Workspace after setup failure: status=%q error=%v", currentAfterFailure.Status, findErr)
	}
}

func TestNextRequiresACurrentWorkspace(t *testing.T) {
	t.Setenv("TMUX_PANE", "")
	root := t.TempDir()
	var stdout, stderr bytes.Buffer
	options := cli.Options{
		ConfigDir: filepath.Join(root, "config"),
		StateDir:  filepath.Join(root, "state"),
		DataDir:   filepath.Join(root, "data"),
		Stdout:    &stdout,
		Stderr:    &stderr,
	}
	command := cli.New(options)
	command.SetArgs(forceTextOutput([]string{"next", "new-workspace"}))
	err := command.Execute()
	if err == nil || !strings.Contains(err.Error(), "no current Workspace") || !strings.Contains(err.Error(), "twt create") {
		t.Fatalf("next without a current Workspace error = %v", err)
	}
	workspaces, listErr := store.NewWorkspaceStore(options.StateDir).List()
	if listErr != nil {
		t.Fatal(listErr)
	}
	if len(workspaces) != 0 {
		t.Fatalf("quick create outside tmux made Workspaces: %+v", workspaces)
	}

	// A template does not make next valid without a current Workspace.
	if err := os.MkdirAll(filepath.Join(root, "config", "templates"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"alpha", "beta"} {
		template := fmt.Sprintf("version: 1\nname: %s\nrepositories:\n  - name: app\n    clone:\n      url: https://example.com/app.git\n", name)
		if err := os.WriteFile(filepath.Join(root, "config", "templates", name+".yaml"), []byte(template), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	command = cli.New(options)
	command.SetArgs(forceTextOutput([]string{"next", "new-workspace", "--template", "alpha"}))
	err = command.Execute()
	if err == nil || !strings.Contains(err.Error(), "no current Workspace") {
		t.Fatalf("next without a current Workspace and with a template = %v", err)
	}
}

func TestNextChecksDirtyStateBeforeWorkspaceSetup(t *testing.T) {
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
	templatePath := filepath.Join(configDir, "templates", "example.yaml")
	template := fmt.Sprintf("version: 1\nname: example\nrepositories:\n  - name: app\n    clone:\n      url: %s\n", source)
	if err := os.WriteFile(templatePath, []byte(template), 0o644); err != nil {
		t.Fatal(err)
	}
	socket := fmt.Sprintf("twt-test-%d", time.Now().UnixNano())
	t.Cleanup(func() { _ = exec.Command("tmux", "-L", socket, "kill-server").Run() })
	options := cli.Options{ConfigDir: configDir, StateDir: filepath.Join(root, "state"), DataDir: filepath.Join(root, "data"), TmuxSocket: socket}
	executeWithOptions(t, options, nil, "create", "old-workspace", "--template", "example", "--no-open")
	oldPane := runCommand(t, "", "tmux", "-L", socket, "list-panes", "-t", "=example-old-workspace", "-F", "#{pane_id}")
	t.Setenv("TMUX_PANE", oldPane)
	initLog := filepath.Join(root, "init.log")
	t.Setenv("TWT_TEST_INIT_LOG", initLog)
	slowTemplate := template + "    initialize:\n      command: [\"sh\", \"-c\", \"sleep 1.2; printf initialized > \\\"$TWT_TEST_INIT_LOG\\\"\"]\n"
	if err := os.WriteFile(templatePath, []byte(slowTemplate), 0o644); err != nil {
		t.Fatal(err)
	}

	attachControlClient(t, socket, "example-old-workspace")
	current, err := store.NewWorkspaceStore(options.StateDir).Find("old-workspace")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(current.Repositories[0].Path, "unsaved.txt"), []byte("keep\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	command := cli.New(options)
	command.SetArgs(forceTextOutput([]string{"next", "dirty-must-not-exist"}))
	err = command.Execute()
	if err == nil || !strings.Contains(err.Error(), "uncommitted changes") {
		t.Fatalf("quick create dirty preflight error = %v", err)
	}
	if _, err := os.Stat(initLog); !os.IsNotExist(err) {
		t.Fatalf("quick create ran initialization before dirty preflight: %v", err)
	}
	if _, err := store.NewWorkspaceStore(options.StateDir).Find("dirty-must-not-exist"); err == nil {
		t.Fatal("quick create made a Workspace before dirty preflight")
	}
}

func TestNextCleansBeforeItStopsTheSourceSession(t *testing.T) {
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
	socket := fmt.Sprintf("twt-test-%d", time.Now().UnixNano())
	t.Cleanup(func() { exec.Command("tmux", "-L", socket, "kill-server").Run() })
	options := cli.Options{
		ConfigDir:  configDir,
		StateDir:   filepath.Join(root, "state"),
		DataDir:    filepath.Join(root, "data"),
		TmuxSocket: socket,
	}
	executeWithOptions(t, options, nil, "workspaces", "create", "old-workspace", "--template", "example", "--no-open")
	oldWorkspace, err := store.NewWorkspaceStore(options.StateDir).Find("old-workspace")
	if err != nil {
		t.Fatal(err)
	}
	oldPane := runCommand(t, "", "tmux", "-L", socket, "list-panes", "-t", "=example-old-workspace", "-F", "#{pane_id}")
	otherPane := runCommand(t, "", "tmux", "-L", socket, "split-window", "-d", "-P", "-F", "#{pane_id}", "-t", oldPane)
	runCommand(t, "", "tmux", "-L", socket, "select-pane", "-t", otherPane)

	client := exec.Command("tmux", "-L", socket, "-C", "attach-session", "-t", "example-old-workspace")
	clientInput, err := client.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	client.Stdout = io.Discard
	client.Stderr = io.Discard
	if err := client.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		clientInput.Close()
		if client.Process != nil {
			client.Process.Kill()
		}
		client.Wait()
	})
	waitFor(t, 2*time.Second, func() bool {
		data, err := exec.Command("tmux", "-L", socket, "list-clients", "-F", "#{pane_id}").CombinedOutput()
		return err == nil && strings.TrimSpace(string(data)) == otherPane
	}, "control client did not attach to the other pane in the old Workspace")

	t.Setenv("TMUX_PANE", oldPane)
	t.Setenv("TWT_WORKSPACE_ID", "stale-workspace-id")
	failingOutputOptions := options
	failingOutputOptions.Stdout = errorWriter{}
	failingOutputCommand := cli.New(failingOutputOptions)
	failingOutputCommand.SetArgs(forceTextOutput([]string{"next", "output-fails"}))
	err = failingOutputCommand.Execute()
	if err == nil || !strings.Contains(err.Error(), "test output failure") ||
		!strings.Contains(err.Error(), "could not report its cleanup step") ||
		!strings.Contains(err.Error(), "current Workspace stays active") {
		t.Fatalf("quick create output failure = %v", err)
	}
	failedOutputWorkspace, findErr := store.NewWorkspaceStore(options.StateDir).Find("output-fails")
	if findErr != nil || failedOutputWorkspace.Status != domain.WorkspaceActive {
		t.Fatalf("new Workspace after output failure: status=%q error=%v", failedOutputWorkspace.Status, findErr)
	}
	currentWorkspace, findErr := store.NewWorkspaceStore(options.StateDir).Find(oldWorkspace.ID)
	if findErr != nil || currentWorkspace.Status != domain.WorkspaceActive {
		t.Fatalf("current Workspace after output failure: status=%q error=%v", currentWorkspace.Status, findErr)
	}
	clientSessionBeforeSuccess := runCommand(t, "", "tmux", "-L", socket, "list-clients", "-F", "#{session_name}")
	if clientSessionBeforeSuccess != oldWorkspace.TmuxSession {
		t.Fatalf("calling client after output failure = %q, want %q", clientSessionBeforeSuccess, oldWorkspace.TmuxSession)
	}
	output := executeWithOptions(t, options, nil, "next", "new-workspace")
	if !strings.Contains(output, "Archived Workspace \"old-workspace\"") {
		t.Fatalf("real quick create output = %q", output)
	}
	workspace, err := store.NewWorkspaceStore(options.StateDir).Find(oldWorkspace.ID)
	if err != nil || workspace.Status != domain.WorkspaceArchived || workspace.Materialized || workspace.EnvironmentID != "" {
		t.Fatalf("old Workspace after next = %+v, error = %v", workspace, err)
	}
	newWorkspace, err := store.NewWorkspaceStore(options.StateDir).Find("new-workspace")
	if err != nil || newWorkspace.Status != domain.WorkspaceActive {
		t.Fatalf("new Workspace after real quick create: status=%q error=%v", newWorkspace.Status, err)
	}
	clientSession := runCommand(t, "", "tmux", "-L", socket, "list-clients", "-F", "#{session_name}")
	if clientSession != newWorkspace.TmuxSession {
		t.Fatalf("calling client session = %q, want %q", clientSession, newWorkspace.TmuxSession)
	}
	windows := runCommand(t, "", "tmux", "-L", socket, "list-windows", "-t", newWorkspace.TmuxSession, "-F", "#{window_name}")
	if windows != "app" {
		t.Fatalf("destination windows = %q, want only app", windows)
	}
}

func TestNextRequiresTheCurrentWorkspaceTmuxPane(t *testing.T) {
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
	socket := fmt.Sprintf("twt-test-%d", time.Now().UnixNano())
	t.Cleanup(func() { _ = exec.Command("tmux", "-L", socket, "kill-server").Run() })
	options := cli.Options{ConfigDir: configDir, StateDir: filepath.Join(root, "state"), DataDir: filepath.Join(root, "data"), TmuxSocket: socket}
	executeWithOptions(t, options, nil, "create", "old-workspace", "--template", "example", "--no-open")
	oldWorkspace, err := store.NewWorkspaceStore(options.StateDir).Find("old-workspace")
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("TMUX_PANE", "")
	t.Setenv("TWT_WORKSPACE_ID", oldWorkspace.ID)
	var stdout, stderr bytes.Buffer
	outsideOptions := options
	outsideOptions.Stdout, outsideOptions.Stderr = &stdout, &stderr
	outsideCommand := cli.New(outsideOptions)
	outsideCommand.SetArgs(forceTextOutput([]string{"next", "outsider"}))
	err = outsideCommand.Execute()
	if err == nil || !strings.Contains(err.Error(), "current Workspace tmux session") {
		t.Fatalf("next outside the current Workspace tmux session = %v", err)
	}
	if _, err := store.NewWorkspaceStore(options.StateDir).Find("outsider"); err == nil {
		t.Fatal("next outside the current Workspace tmux session created a Workspace")
	}
	workspace, err := store.NewWorkspaceStore(options.StateDir).Find(oldWorkspace.ID)
	if err != nil || workspace.Status != domain.WorkspaceActive {
		t.Fatalf("current Workspace after refused next: status=%q error=%v", workspace.Status, err)
	}
}

type errorWriter struct{}

func (errorWriter) Write([]byte) (int, error) {
	return 0, fmt.Errorf("test output failure")
}

// attachControlClient attaches a tmux control-mode client to a session so
// that the quick create client preflight can find a calling client.
func attachControlClient(t *testing.T, socket, session string) {
	t.Helper()
	client := exec.Command("tmux", "-L", socket, "-C", "attach-session", "-t", "="+session)
	clientInput, err := client.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	client.Stdout = io.Discard
	client.Stderr = io.Discard
	if err := client.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		clientInput.Close()
		if client.Process != nil {
			client.Process.Kill()
		}
		client.Wait()
	})
	waitFor(t, 2*time.Second, func() bool {
		data, err := exec.Command("tmux", "-L", socket, "list-clients", "-F", "#{session_name}").CombinedOutput()
		return err == nil && strings.Contains(string(data), session)
	}, "control client did not attach to session "+session)
}

func waitFor(t *testing.T, timeout time.Duration, condition func() bool, message string) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for !condition() {
		if time.Now().After(deadline) {
			t.Fatal(message)
		}
		time.Sleep(20 * time.Millisecond)
	}
}
