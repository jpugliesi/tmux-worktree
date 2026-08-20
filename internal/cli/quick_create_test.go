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
	projectservice "github.com/jpugliesi/tmux-worktree/internal/project"
	"github.com/jpugliesi/tmux-worktree/internal/store"
)

func TestQuickCreatePromptsSwitchesThenArchivesTheCurrentProject(t *testing.T) {
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
	socket := fmt.Sprintf("twt2-test-%d", time.Now().UnixNano())
	t.Cleanup(func() { exec.Command("tmux", "-L", socket, "kill-server").Run() })
	options := cli.Options{ConfigDir: configDir, StateDir: filepath.Join(root, "state"), DataDir: filepath.Join(root, "data"), TmuxSocket: socket}
	executeWithOptions(t, options, nil, "projects", "create", "old-project", "--template", "example", "--no-open")
	oldProject, err := store.NewProjectStore(options.StateDir).Find("old-project")
	if err != nil {
		t.Fatal(err)
	}
	oldPane := runCommand(t, "", "tmux", "-L", socket, "list-panes", "-t", "=old-project", "-F", "#{pane_id}")
	t.Setenv("TMUX_PANE", oldPane)
	t.Setenv("TWT2_PROJECT_ID", "stale-project-id")
	runCommand(t, "", "tmux", "-L", socket, "set-option", "-t", oldPane, "@twt2_project_id", oldProject.Name)
	var invalidIDOutput, invalidIDError bytes.Buffer
	invalidIDOptions := options
	invalidIDOptions.Stdout, invalidIDOptions.Stderr = &invalidIDOutput, &invalidIDError
	invalidIDCommand := cli.New(invalidIDOptions)
	invalidIDCommand.SetArgs([]string{"new", "invalid-id-project"})
	err = invalidIDCommand.Execute()
	if err == nil || !strings.Contains(err.Error(), "does not contain an immutable Project ID") {
		t.Fatalf("quick create with a Project name in tmux metadata = %v", err)
	}
	if _, err := store.NewProjectStore(options.StateDir).Find("invalid-id-project"); err == nil {
		t.Fatal("quick create accepted a Project name as tmux identity")
	}
	runCommand(t, "", "tmux", "-L", socket, "set-option", "-t", oldPane, "@twt2_project_id", oldProject.ID)

	latestTemplate := strings.Replace(template, "  - name: app\n", "  - name: app\n    window_name: latest-app\n", 1)
	if err := os.WriteFile(templatePath, []byte(latestTemplate), 0o644); err != nil {
		t.Fatal(err)
	}
	dryRun := executeWithOptions(t, options, nil, "new", "dry-project", "--dry-run")
	if !strings.Contains(dryRun, "projects.quick_create: valid") {
		t.Fatalf("quick create dry-run output = %s", dryRun)
	}
	if _, err := store.NewProjectStore(options.StateDir).Find("dry-project"); err == nil {
		t.Fatal("quick create dry-run created a Project")
	}
	for _, jsonArgs := range [][]string{
		{"new", "json-project", "--output", "json"},
		{"new", "json-project", "--dry-run", "--output", "json"},
	} {
		var jsonOutput, jsonError bytes.Buffer
		jsonOptions := options
		jsonOptions.Stdout, jsonOptions.Stderr = &jsonOutput, &jsonError
		jsonCommand := cli.New(jsonOptions)
		jsonCommand.SetArgs(jsonArgs)
		err = jsonCommand.Execute()
		if err == nil || !strings.Contains(err.Error(), "use 'twt2 projects create' for JSON automation") {
			t.Fatalf("quick create JSON error for %v = %v", jsonArgs, err)
		}
		if _, err := store.NewProjectStore(options.StateDir).Find("json-project"); err == nil {
			t.Fatalf("quick create with JSON output created a Project for %v", jsonArgs)
		}
	}
	var missingNameOutput, missingNameError bytes.Buffer
	missingNameOptions := options
	missingNameOptions.Stdout, missingNameOptions.Stderr = &missingNameOutput, &missingNameError
	missingName := cli.New(missingNameOptions)
	missingName.SetIn(strings.NewReader(""))
	missingName.SetArgs([]string{"new", "--dry-run"})
	err = missingName.Execute()
	if err == nil || !strings.Contains(err.Error(), "no Project name was given") {
		t.Fatalf("quick create without a Project name = %v", err)
	}
	var events []string
	options.QuickCreateSwitch = func(_ string, session string) error {
		events = append(events, "switch:"+session)
		return nil
	}
	options.QuickCreateArchive = func(_ string, projectID string, _ string) error {
		events = append(events, "archive:"+projectID)
		service := projectservice.NewService(projectservice.Options{StateDir: options.StateDir, DataDir: options.DataDir, TmuxSocket: socket})
		_, err := service.Archive(projectID, "")
		return err
	}
	attachControlClient(t, socket, "old-project")

	var promptOutput, promptError bytes.Buffer
	promptOptions := options
	promptOptions.Stdout, promptOptions.Stderr = &promptOutput, &promptError
	promptCommand := cli.New(promptOptions)
	promptCommand.SetIn(strings.NewReader("new-project\n"))
	promptCommand.SetArgs([]string{"new"})
	if err := promptCommand.Execute(); err != nil {
		t.Fatalf("quick create with prompt: %v", err)
	}
	if !strings.HasPrefix(promptError.String(), "Project name: ") {
		t.Fatalf("quick create prompt = %q", promptError.String())
	}
	if !strings.Contains(promptError.String(), "Step ") || !strings.Contains(promptError.String(), "Base: origin/main @ ") {
		t.Fatalf("quick create progress = %q", promptError.String())
	}
	output := promptOutput.String()
	if !strings.Contains(output, "Created Project \"new-project\"") {
		t.Fatalf("quick create output = %q", output)
	}
	newProject, err := store.NewProjectStore(options.StateDir).Find("new-project")
	if err != nil {
		t.Fatal(err)
	}
	wantEvents := []string{"switch:" + newProject.TmuxSession, "archive:" + oldProject.ID}
	if strings.Join(events, "\n") != strings.Join(wantEvents, "\n") {
		t.Fatalf("quick create events = %v, want %v", events, wantEvents)
	}
	oldProject, err = store.NewProjectStore(options.StateDir).Find(oldProject.ID)
	if err != nil || oldProject.Status != domain.ProjectArchived {
		t.Fatalf("old Project after quick create: status=%q error=%v", oldProject.Status, err)
	}
	if newProject.Status != domain.ProjectActive || newProject.TemplateName != "example" || newProject.Repositories[0].WindowName != "latest-app" {
		t.Fatalf("new Project does not use the latest Project Template: %+v", newProject)
	}

	newPane := runCommand(t, "", "tmux", "-L", socket, "list-panes", "-t", "=new-project", "-F", "#{pane_id}")
	t.Setenv("TMUX_PANE", newPane)
	attachControlClient(t, socket, "new-project")
	archiveCalled := false
	options.QuickCreateSwitch = func(string, string) error { return fmt.Errorf("test switch failure") }
	options.QuickCreateArchive = func(string, string, string) error {
		archiveCalled = true
		return nil
	}
	var stdout, stderr bytes.Buffer
	options.Stdout, options.Stderr = &stdout, &stderr
	command := cli.New(options)
	command.SetArgs([]string{"new", "failed-switch"})
	err = command.Execute()
	if err == nil || !strings.Contains(err.Error(), "test switch failure") ||
		!strings.Contains(err.Error(), "could not switch to the new Project") ||
		!strings.Contains(err.Error(), "twt2 projects open failed-switch") {
		t.Fatalf("quick create switch failure = %v", err)
	}
	if archiveCalled {
		t.Fatal("quick create archived the current Project after a failed switch")
	}
	failedSwitch, findErr := store.NewProjectStore(options.StateDir).Find("failed-switch")
	if findErr != nil || failedSwitch.Status != domain.ProjectActive {
		t.Fatalf("new Project after switch failure: status=%q error=%v", failedSwitch.Status, findErr)
	}
	currentAfterFailure, findErr := store.NewProjectStore(options.StateDir).Find(newProject.ID)
	if findErr != nil || currentAfterFailure.Status != domain.ProjectActive {
		t.Fatalf("current Project after switch failure: status=%q error=%v", currentAfterFailure.Status, findErr)
	}

	failingTemplate := latestTemplate + "    initialize:\n      command: [\"false\"]\n"
	if err := os.WriteFile(templatePath, []byte(failingTemplate), 0o644); err != nil {
		t.Fatal(err)
	}
	stdout.Reset()
	stderr.Reset()
	command = cli.New(options)
	command.SetArgs([]string{"new", "setup-fails"})
	err = command.Execute()
	if err == nil || !strings.Contains(err.Error(), "initialization") {
		t.Fatalf("quick create setup failure = %v", err)
	}
	if _, findErr := store.NewProjectStore(options.StateDir).Find("setup-fails"); findErr == nil {
		t.Fatal("repository initialization failure created a Project")
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
	currentAfterFailure, findErr = store.NewProjectStore(options.StateDir).Find(newProject.ID)
	if findErr != nil || currentAfterFailure.Status != domain.ProjectActive {
		t.Fatalf("current Project after setup failure: status=%q error=%v", currentAfterFailure.Status, findErr)
	}
}

func TestQuickCreateOutsideASessionNeedsATemplate(t *testing.T) {
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
	command.SetArgs([]string{"new", "new-project"})
	err := command.Execute()
	if err == nil || !strings.Contains(err.Error(), "no Project Templates exist") {
		t.Fatalf("quick create outside tmux error = %v", err)
	}
	projects, listErr := store.NewProjectStore(options.StateDir).List()
	if listErr != nil {
		t.Fatal(listErr)
	}
	if len(projects) != 0 {
		t.Fatalf("quick create outside tmux made Projects: %+v", projects)
	}

	// Two templates without a last-used record need an explicit selection.
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
	command.SetArgs([]string{"new", "new-project"})
	err = command.Execute()
	if err == nil || !strings.Contains(err.Error(), "--template TEMPLATE") ||
		!strings.Contains(err.Error(), "alpha") || !strings.Contains(err.Error(), "beta") {
		t.Fatalf("quick create outside tmux with two templates = %v", err)
	}
}

func TestQuickCreateChecksTheTmuxClientBeforeProjectSetup(t *testing.T) {
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
	socket := fmt.Sprintf("twt2-test-%d", time.Now().UnixNano())
	t.Cleanup(func() { _ = exec.Command("tmux", "-L", socket, "kill-server").Run() })
	options := cli.Options{ConfigDir: configDir, StateDir: filepath.Join(root, "state"), DataDir: filepath.Join(root, "data"), TmuxSocket: socket}
	executeWithOptions(t, options, nil, "projects", "create", "old-project", "--template", "example", "--no-open")
	oldPane := runCommand(t, "", "tmux", "-L", socket, "list-panes", "-t", "=old-project", "-F", "#{pane_id}")
	t.Setenv("TMUX_PANE", oldPane)
	initLog := filepath.Join(root, "init.log")
	t.Setenv("TWT2_TEST_INIT_LOG", initLog)
	slowTemplate := template + "    initialize:\n      command: [\"sh\", \"-c\", \"sleep 1.2; printf initialized > \\\"$TWT2_TEST_INIT_LOG\\\"\"]\n"
	if err := os.WriteFile(templatePath, []byte(slowTemplate), 0o644); err != nil {
		t.Fatal(err)
	}

	command := cli.New(options)
	command.SetArgs([]string{"new", "must-not-exist"})
	started := time.Now()
	err := command.Execute()
	if err == nil || !strings.Contains(err.Error(), "clients are attached to its Project session") {
		t.Fatalf("quick create client preflight error = %v", err)
	}
	if elapsed := time.Since(started); elapsed >= time.Second {
		t.Fatalf("quick create client preflight took %s", elapsed)
	}
	if _, err := os.Stat(initLog); !os.IsNotExist(err) {
		t.Fatalf("quick create ran initialization before client preflight: %v", err)
	}
	if _, err := store.NewProjectStore(options.StateDir).Find("must-not-exist"); err == nil {
		t.Fatal("quick create made a Project before client preflight")
	}
}

func TestQuickCreateWorkerArchivesOldProjectFromTheNewSession(t *testing.T) {
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
	executeWithOptions(t, options, nil, "projects", "create", "old-project", "--template", "example", "--no-open")
	executeWithOptions(t, options, nil, "projects", "create", "new-project", "--template", "example", "--no-open")
	oldProject, err := store.NewProjectStore(options.StateDir).Find("old-project")
	if err != nil {
		t.Fatal(err)
	}
	newProject, err := store.NewProjectStore(options.StateDir).Find("new-project")
	if err != nil {
		t.Fatal(err)
	}
	helperPane := runCommand(t, "", "tmux", "-L", socket, "new-window", "-d", "-P", "-F", "#{pane_id}", "-t", "=new-project", "-n", "archive-helper", "--", "sleep", "60")
	t.Setenv("TMUX_PANE", helperPane)
	timeoutOptions := options
	timeoutOptions.QuickCreateWaitTimeout = 50 * time.Millisecond
	err = cli.RunQuickCreateWorker(timeoutOptions, []string{oldProject.ID, newProject.ID, "twt2-create-worker-timeout", "no-client"})
	if err == nil || !strings.Contains(err.Error(), "signal timed out") || !strings.Contains(err.Error(), "twt2 archive "+oldProject.ID) {
		t.Fatalf("quick create worker timeout = %v", err)
	}
	oldProject, err = store.NewProjectStore(options.StateDir).Find(oldProject.ID)
	if err != nil || oldProject.Status != domain.ProjectActive {
		t.Fatalf("old Project after worker timeout: status=%q error=%v", oldProject.Status, err)
	}
	windowName := runCommand(t, "", "tmux", "-L", socket, "display-message", "-p", "-t", helperPane, "#{window_name}")
	if windowName != "archive-failed" {
		t.Fatalf("worker timeout window = %q", windowName)
	}
	runCommand(t, "", "tmux", "-L", socket, "rename-window", "-t", helperPane, "archive-helper")
	channel := "twt2-create-worker-test"
	signalResult := make(chan error, 1)
	go func() {
		time.Sleep(50 * time.Millisecond)
		signalResult <- exec.Command("tmux", "-L", socket, "wait-for", "-S", channel).Run()
	}()
	if err := cli.RunQuickCreateWorker(options, []string{oldProject.ID, newProject.ID, channel, "no-client"}); err != nil {
		t.Fatalf("run quick create worker: %v", err)
	}
	if err := <-signalResult; err != nil {
		t.Fatalf("signal quick create worker: %v", err)
	}
	oldProject, err = store.NewProjectStore(options.StateDir).Find(oldProject.ID)
	if err != nil || oldProject.Status != domain.ProjectArchived {
		t.Fatalf("old Project after worker: status=%q error=%v", oldProject.Status, err)
	}
	newProject, err = store.NewProjectStore(options.StateDir).Find(newProject.ID)
	if err != nil || newProject.Status != domain.ProjectActive {
		t.Fatalf("new Project after worker: status=%q error=%v", newProject.Status, err)
	}
	if err := exec.Command("tmux", "-L", socket, "has-session", "-t", "=old-project").Run(); err == nil {
		t.Fatal("quick create worker kept the old tmux session")
	}
}

func TestQuickCreateUsesTheCallingClientAndRealArchiveHelper(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is not installed")
	}
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go is not installed")
	}

	root := t.TempDir()
	binary := filepath.Join(root, "twt2")
	runCommand(t, filepath.Join("..", ".."), "go", "build", "-o", binary, "./cmd/twt2")
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
		ConfigDir:             configDir,
		StateDir:              filepath.Join(root, "state"),
		DataDir:               filepath.Join(root, "data"),
		TmuxSocket:            socket,
		QuickCreateExecutable: binary,
	}
	executeWithOptions(t, options, nil, "projects", "create", "old-project", "--template", "example", "--no-open")
	oldProject, err := store.NewProjectStore(options.StateDir).Find("old-project")
	if err != nil {
		t.Fatal(err)
	}
	oldPane := runCommand(t, "", "tmux", "-L", socket, "list-panes", "-t", "=old-project", "-F", "#{pane_id}")
	otherPane := runCommand(t, "", "tmux", "-L", socket, "split-window", "-d", "-P", "-F", "#{pane_id}", "-t", oldPane)
	runCommand(t, "", "tmux", "-L", socket, "select-pane", "-t", otherPane)

	client := exec.Command("tmux", "-L", socket, "-C", "attach-session", "-t", "old-project")
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
	}, "control client did not attach to the other pane in the old Project")

	t.Setenv("TMUX_PANE", oldPane)
	t.Setenv("TWT2_PROJECT_ID", "stale-project-id")
	failingOutputOptions := options
	failingOutputOptions.Stdout = errorWriter{}
	failingOutputCommand := cli.New(failingOutputOptions)
	failingOutputCommand.SetArgs([]string{"new", "output-fails"})
	err = failingOutputCommand.Execute()
	if err == nil || !strings.Contains(err.Error(), "test output failure") ||
		!strings.Contains(err.Error(), "could not switch to the new Project") ||
		!strings.Contains(err.Error(), "twt2 projects open output-fails") {
		t.Fatalf("quick create output failure = %v", err)
	}
	failedOutputProject, findErr := store.NewProjectStore(options.StateDir).Find("output-fails")
	if findErr != nil || failedOutputProject.Status != domain.ProjectActive {
		t.Fatalf("new Project after output failure: status=%q error=%v", failedOutputProject.Status, findErr)
	}
	currentProject, findErr := store.NewProjectStore(options.StateDir).Find(oldProject.ID)
	if findErr != nil || currentProject.Status != domain.ProjectActive {
		t.Fatalf("current Project after output failure: status=%q error=%v", currentProject.Status, findErr)
	}
	clientSessionBeforeSuccess := runCommand(t, "", "tmux", "-L", socket, "list-clients", "-F", "#{session_name}")
	if clientSessionBeforeSuccess != oldProject.TmuxSession {
		t.Fatalf("calling client after output failure = %q, want %q", clientSessionBeforeSuccess, oldProject.TmuxSession)
	}
	output := executeWithOptions(t, options, nil, "new", "new-project")
	if !strings.Contains(output, "archiving Project \"old-project\"") {
		t.Fatalf("real quick create output = %q", output)
	}
	waitFor(t, 3*time.Second, func() bool {
		project, err := store.NewProjectStore(options.StateDir).Find(oldProject.ID)
		return err == nil && project.Status == domain.ProjectArchived
	}, "old Project did not become archived")
	newProject, err := store.NewProjectStore(options.StateDir).Find("new-project")
	if err != nil || newProject.Status != domain.ProjectActive {
		t.Fatalf("new Project after real quick create: status=%q error=%v", newProject.Status, err)
	}
	clientSession := runCommand(t, "", "tmux", "-L", socket, "list-clients", "-F", "#{session_name}")
	if clientSession != newProject.TmuxSession {
		t.Fatalf("calling client session = %q, want %q", clientSession, newProject.TmuxSession)
	}
	waitFor(t, 2*time.Second, func() bool {
		windows, err := exec.Command("tmux", "-L", socket, "list-windows", "-t", newProject.TmuxSession, "-F", "#{window_name}").CombinedOutput()
		return err == nil && strings.TrimSpace(string(windows)) == "app"
	}, "successful archive helper window did not close")
}

func TestQuickCreateKeepCurrentAndOutsideSessionFallback(t *testing.T) {
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
	t.Cleanup(func() { _ = exec.Command("tmux", "-L", socket, "kill-server").Run() })
	options := cli.Options{ConfigDir: configDir, StateDir: filepath.Join(root, "state"), DataDir: filepath.Join(root, "data"), TmuxSocket: socket}
	executeWithOptions(t, options, nil, "projects", "create", "old-project", "--template", "example", "--no-open")
	oldProject, err := store.NewProjectStore(options.StateDir).Find("old-project")
	if err != nil {
		t.Fatal(err)
	}
	oldPane := runCommand(t, "", "tmux", "-L", socket, "list-panes", "-t", "=old-project", "-F", "#{pane_id}")
	t.Setenv("TMUX_PANE", oldPane)
	attachControlClient(t, socket, "old-project")

	var events []string
	options.QuickCreateSwitch = func(_ string, session string) error {
		events = append(events, "switch:"+session)
		return nil
	}
	options.QuickCreateArchive = func(_ string, projectID string, _ string) error {
		events = append(events, "archive:"+projectID)
		return nil
	}

	// --keep-current switches without an archive.
	keepOutput := executeWithOptions(t, options, nil, "new", "second", "--keep-current")
	if !strings.Contains(keepOutput, "stays active") {
		t.Fatalf("quick create --keep-current output = %q", keepOutput)
	}
	second, err := store.NewProjectStore(options.StateDir).Find("second")
	if err != nil || second.Status != domain.ProjectActive {
		t.Fatalf("new Project after --keep-current: status=%q error=%v", second.Status, err)
	}
	oldProject, err = store.NewProjectStore(options.StateDir).Find(oldProject.ID)
	if err != nil || oldProject.Status != domain.ProjectActive {
		t.Fatalf("old Project after --keep-current: status=%q error=%v", oldProject.Status, err)
	}
	if strings.Join(events, "\n") != "switch:"+second.TmuxSession {
		t.Fatalf("quick create --keep-current events = %v", events)
	}

	// Outside a Project session quick create uses the last-used template.
	otherTemplate := "version: 1\nname: zeta\nrepositories:\n  - name: app\n    clone:\n      url: " + source + "\n"
	if err := os.WriteFile(filepath.Join(configDir, "templates", "zeta.yaml"), []byte(otherTemplate), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TMUX_PANE", "")
	events = nil
	var stdout, stderr bytes.Buffer
	outsideOptions := options
	outsideOptions.Stdout, outsideOptions.Stderr = &stdout, &stderr
	outsideCommand := cli.New(outsideOptions)
	outsideCommand.SetArgs([]string{"new", "outsider"})
	if err := outsideCommand.Execute(); err != nil {
		t.Fatalf("quick create outside a Project session: %v\n%s", err, stderr.String())
	}
	if !strings.Contains(stderr.String(), "Template: example (last used)") {
		t.Fatalf("outside quick create inference = %q", stderr.String())
	}
	outsider, err := store.NewProjectStore(options.StateDir).Find("outsider")
	if err != nil || outsider.Status != domain.ProjectActive || outsider.TemplateName != "example" {
		t.Fatalf("outside quick create Project = %+v, error=%v", outsider, err)
	}
	if strings.Join(events, "\n") != "switch:"+outsider.TmuxSession {
		t.Fatalf("outside quick create events = %v; the archive hook must not run", events)
	}
	for _, reference := range []string{oldProject.ID, second.ID} {
		project, err := store.NewProjectStore(options.StateDir).Find(reference)
		if err != nil || project.Status != domain.ProjectActive {
			t.Fatalf("Project %s after outside quick create: status=%q error=%v", reference, project.Status, err)
		}
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
