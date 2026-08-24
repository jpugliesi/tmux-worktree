package cli_test

import (
	"bytes"
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
	workspaceservice "github.com/jpugliesi/tmux-worktree/internal/workspace"
)

// doneFixture prepares a config directory with the "example" template, a
// private tmux server, and base options.
func doneFixture(t *testing.T) cli.Options {
	t.Helper()
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
	return cli.Options{ConfigDir: configDir, StateDir: filepath.Join(root, "state"), DataDir: filepath.Join(root, "data"), TmuxSocket: socket}
}

func addSubmoduleToDoneFixture(t *testing.T, options cli.Options) {
	t.Helper()
	fixtureRoot := filepath.Dir(options.ConfigDir)
	sourceRepository := filepath.Join(fixtureRoot, "source")
	submoduleRepository := filepath.Join(fixtureRoot, "submodule")
	initGitRepository(t, submoduleRepository)
	runCommand(t, sourceRepository, "git", "-c", "protocol.file.allow=always", "submodule", "add", submoduleRepository, "dependencies/example")
	runCommand(t, sourceRepository, "git", "commit", "-qm", "add submodule")
}

func TestDoneOutsideSessionSupportsDryRunKeepAndFullRemoval(t *testing.T) {
	options := doneFixture(t)
	t.Setenv("TMUX_PANE", "")
	t.Setenv("TWT_WORKSPACE_ID", "")
	executeWithOptions(t, options, nil, "workspaces", "create", "finish-me", "--template", "example", "--no-open")
	workspace, err := store.NewWorkspaceStore(options.StateDir).Find("finish-me")
	if err != nil {
		t.Fatal(err)
	}

	dryRun := executeWithOptions(t, options, nil, "done", "finish-me", "--dry-run")
	for _, want := range []string{"Archive of Workspace \"finish-me\" is valid.", "Removal plan for Workspace \"finish-me\":", "remove_worktree"} {
		if !strings.Contains(dryRun, want) {
			t.Fatalf("done dry-run output does not contain %q: %s", want, dryRun)
		}
	}
	if strings.Contains(dryRun, "Blocked:") || strings.Contains(dryRun, "not archived") {
		t.Fatalf("done dry-run of a clean Workspace shows blockers: %s", dryRun)
	}
	unchanged, err := store.NewWorkspaceStore(options.StateDir).Find(workspace.ID)
	if err != nil || unchanged.Status != domain.WorkspaceActive {
		t.Fatalf("done dry-run changed the Workspace: status=%q error=%v", unchanged.Status, err)
	}
	if _, err := os.Stat(workspace.Root); err != nil {
		t.Fatalf("done dry-run changed Workspace data: %v", err)
	}

	keepOutput := executeWithOptions(t, options, nil, "done", "finish-me", "--keep")
	if !strings.Contains(keepOutput, "Archived Workspace \"finish-me\"") || strings.Contains(keepOutput, "Removed") {
		t.Fatalf("done --keep output = %q", keepOutput)
	}
	archived, err := store.NewWorkspaceStore(options.StateDir).Find(workspace.ID)
	if err != nil || archived.Status != domain.WorkspaceArchived {
		t.Fatalf("Workspace after done --keep: status=%q error=%v", archived.Status, err)
	}
	if _, err := os.Stat(workspace.Root); err != nil {
		t.Fatalf("done --keep removed Workspace data: %v", err)
	}

	output := executeWithOptions(t, options, nil, "done", "finish-me")
	if !strings.Contains(output, "Archived Workspace \"finish-me\"") || !strings.Contains(output, "Removed Workspace \"finish-me\". Reclaimed ") {
		t.Fatalf("done output = %q", output)
	}
	if _, err := os.Stat(workspace.Root); !os.IsNotExist(err) {
		t.Fatalf("Workspace root still exists after done: %v", err)
	}
	if _, err := store.NewWorkspaceStore(options.StateDir).Find(workspace.ID); err == nil {
		t.Fatal("Workspace record still exists after done")
	}
	if err := exec.Command("tmux", "-L", options.TmuxSocket, "has-session", "-t", "=example-finish-me").Run(); err == nil {
		t.Fatal("Workspace tmux session still exists after done")
	}
}

func TestDoneLeavesABlockedWorkspaceArchived(t *testing.T) {
	options := doneFixture(t)
	t.Setenv("TMUX_PANE", "")
	t.Setenv("TWT_WORKSPACE_ID", "")
	executeWithOptions(t, options, nil, "workspaces", "create", "block-me", "--template", "example", "--no-open")
	workspace, err := store.NewWorkspaceStore(options.StateDir).Find("block-me")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace.Root, "app", "unsaved.txt"), []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	blockedOptions := options
	blockedOptions.Stdout, blockedOptions.Stderr = &stdout, &stderr
	command := cli.New(blockedOptions)
	command.SetArgs(forceTextOutput([]string{"done", "block-me"}))
	err = command.Execute()
	if err == nil || !strings.Contains(err.Error(), "uncommitted changes") {
		t.Fatalf("blocked done error = %v", err)
	}
	output := stdout.String()
	if !strings.Contains(output, "Archived Workspace \"block-me\"") || !strings.Contains(output, "Blocked:") || !strings.Contains(output, "stays archived") {
		t.Fatalf("blocked done output = %q", output)
	}
	if !strings.Contains(output, "twt done "+workspace.ID) {
		t.Fatalf("blocked done output has no retry command: %q", output)
	}
	archived, findErr := store.NewWorkspaceStore(options.StateDir).Find(workspace.ID)
	if findErr != nil || archived.Status != domain.WorkspaceArchived {
		t.Fatalf("blocked done Workspace: status=%q error=%v", archived.Status, findErr)
	}
	if _, err := os.Stat(filepath.Join(workspace.Root, "app", "unsaved.txt")); err != nil {
		t.Fatalf("blocked done changed Workspace data: %v", err)
	}
}

func TestDoneAndArchiveRelocateInsideTheWorkspaceSession(t *testing.T) {
	options := doneFixture(t)
	t.Setenv("TMUX_PANE", "")
	t.Setenv("TWT_WORKSPACE_ID", "")
	for _, name := range []string{"alpha", "beta", "gamma"} {
		executeWithOptions(t, options, nil, "workspaces", "create", name, "--template", "example", "--no-open")
	}
	workspaces := store.NewWorkspaceStore(options.StateDir)
	alpha, err := workspaces.Find("alpha")
	if err != nil {
		t.Fatal(err)
	}
	beta, err := workspaces.Find("beta")
	if err != nil {
		t.Fatal(err)
	}
	gamma, err := workspaces.Find("gamma")
	if err != nil {
		t.Fatal(err)
	}

	gammaPane := runCommand(t, "", "tmux", "-L", options.TmuxSocket, "list-panes", "-t", "=example-gamma", "-F", "#{pane_id}")
	t.Setenv("TMUX_PANE", gammaPane)

	// JSON output cannot relocate the tmux client.
	var stdout, stderr bytes.Buffer
	jsonOptions := options
	jsonOptions.Stdout, jsonOptions.Stderr = &stdout, &stderr
	jsonCommand := cli.New(jsonOptions)
	jsonCommand.SetArgs(forceTextOutput([]string{"done", "--output", "json"}))
	err = jsonCommand.Execute()
	if err == nil || !strings.Contains(err.Error(), "text output") {
		t.Fatalf("done JSON inside the Workspace session error = %v", err)
	}
	if unchanged, findErr := workspaces.Find(gamma.ID); findErr != nil || unchanged.Status != domain.WorkspaceActive {
		t.Fatalf("refused JSON done changed the Workspace: %+v error=%v", unchanged, findErr)
	}

	// Archive with JSON output refuses too; the unified policy covers both.
	var archiveStdout, archiveStderr bytes.Buffer
	archiveJSONOptions := options
	archiveJSONOptions.Stdout, archiveJSONOptions.Stderr = &archiveStdout, &archiveStderr
	archiveJSONCommand := cli.New(archiveJSONOptions)
	archiveJSONCommand.SetArgs(forceTextOutput([]string{"archive", "--output", "json"}))
	err = archiveJSONCommand.Execute()
	if err == nil || !strings.Contains(err.Error(), "text output") {
		t.Fatalf("archive JSON inside the Workspace session error = %v", err)
	}

	// The fake hook completes the work like the real worker does.
	var requests []cli.RelocationRequest
	options.DoneRelocate = func(request cli.RelocationRequest) error {
		requests = append(requests, request)
		service := workspaceservice.NewService(workspaceservice.Options{StateDir: options.StateDir, DataDir: options.DataDir, TmuxSocket: options.TmuxSocket})
		if _, err := service.Archive(request.WorkspaceID, ""); err != nil {
			return err
		}
		if request.Keep {
			return nil
		}
		_, err := service.Remove(request.WorkspaceID, "", workspaceservice.RemovalOptions{AllowUnpublished: request.AllowUnpublished})
		return err
	}

	// Done relocates to the most recently updated other active Workspace.
	output := executeWithOptions(t, options, nil, "done")
	if !strings.Contains(output, "Finishing Workspace \"gamma\"; switching the client to Workspace \"beta\"") {
		t.Fatalf("relocated done output = %q", output)
	}
	if _, err := workspaces.Find(gamma.ID); err == nil {
		t.Fatal("relocated done kept the Workspace record")
	}
	if _, err := os.Stat(gamma.Root); !os.IsNotExist(err) {
		t.Fatalf("relocated done kept the Workspace root: %v", err)
	}

	// Archive relocates too, keeps the Workspace data, and behaves like done
	// --keep.
	betaPane := runCommand(t, "", "tmux", "-L", options.TmuxSocket, "list-panes", "-t", "=example-beta", "-F", "#{pane_id}")
	t.Setenv("TMUX_PANE", betaPane)
	archiveOutput := executeWithOptions(t, options, nil, "archive")
	if !strings.Contains(archiveOutput, "Archiving Workspace \"beta\"; switching the client to Workspace \"alpha\"") {
		t.Fatalf("relocated archive output = %q", archiveOutput)
	}
	archivedBeta, err := workspaces.Find(beta.ID)
	if err != nil || archivedBeta.Status != domain.WorkspaceArchived {
		t.Fatalf("relocated archive Workspace: status=%q error=%v", archivedBeta.Status, err)
	}
	if _, err := os.Stat(beta.Root); err != nil {
		t.Fatalf("relocated archive removed Workspace data: %v", err)
	}

	// Without another active Workspace, done reports an empty destination.
	alphaPane := runCommand(t, "", "tmux", "-L", options.TmuxSocket, "list-panes", "-t", "=example-alpha", "-F", "#{pane_id}")
	t.Setenv("TMUX_PANE", alphaPane)
	lastOutput := executeWithOptions(t, options, nil, "done")
	if !strings.Contains(lastOutput, "No other active Workspace exists.") {
		t.Fatalf("last done output = %q", lastOutput)
	}
	if _, err := os.Stat(alpha.Root); !os.IsNotExist(err) {
		t.Fatalf("last done kept the Workspace root: %v", err)
	}

	wantDestinations := []string{beta.ID, alpha.ID, ""}
	gotDestinations := make([]string, 0, len(requests))
	wantKeep := []bool{false, true, false}
	for index, request := range requests {
		gotDestinations = append(gotDestinations, request.DestinationWorkspaceID)
		if request.Keep != wantKeep[index] {
			t.Fatalf("relocation request %d keep = %t, want %t", index, request.Keep, wantKeep[index])
		}
	}
	if strings.Join(gotDestinations, "\n") != strings.Join(wantDestinations, "\n") {
		t.Fatalf("relocation destinations = %v, want %v", gotDestinations, wantDestinations)
	}
}

func TestDoneWorkerArchivesAndRemovesFromAnotherSession(t *testing.T) {
	options := doneFixture(t)
	t.Setenv("TWT_WORKSPACE_ID", "")
	executeWithOptions(t, options, nil, "workspaces", "create", "worker-src", "--template", "example", "--no-open")
	executeWithOptions(t, options, nil, "workspaces", "create", "worker-dest", "--template", "example", "--no-open")
	source, err := store.NewWorkspaceStore(options.StateDir).Find("worker-src")
	if err != nil {
		t.Fatal(err)
	}
	helperPane := runCommand(t, "", "tmux", "-L", options.TmuxSocket, "new-window", "-d", "-P", "-F", "#{pane_id}", "-t", "=example-worker-dest", "-n", "done-helper", "--", "sleep", "60")
	t.Setenv("TMUX_PANE", helperPane)

	timeoutOptions := options
	timeoutOptions.QuickCreateWaitTimeout = 50 * time.Millisecond
	err = cli.RunDoneWorker(timeoutOptions, []string{source.ID, "keep=false", "force=false", "-", "-", "-", "twt-done-timeout", "no-client"})
	if err == nil || !strings.Contains(err.Error(), "signal timed out") || !strings.Contains(err.Error(), "twt done "+source.ID) {
		t.Fatalf("done worker timeout = %v", err)
	}
	unchanged, err := store.NewWorkspaceStore(options.StateDir).Find(source.ID)
	if err != nil || unchanged.Status != domain.WorkspaceActive {
		t.Fatalf("Workspace after worker timeout: status=%q error=%v", unchanged.Status, err)
	}
	windowName := runCommand(t, "", "tmux", "-L", options.TmuxSocket, "display-message", "-p", "-t", helperPane, "#{window_name}")
	if windowName != "done-failed" {
		t.Fatalf("worker timeout window = %q", windowName)
	}
	runCommand(t, "", "tmux", "-L", options.TmuxSocket, "rename-window", "-t", helperPane, "done-helper")

	channel := "twt-done-worker-test"
	signalResult := make(chan error, 1)
	go func() {
		time.Sleep(50 * time.Millisecond)
		signalResult <- exec.Command("tmux", "-L", options.TmuxSocket, "wait-for", "-S", channel).Run()
	}()
	if err := cli.RunDoneWorker(options, []string{source.ID, "keep=false", "force=false", "-", "-", "-", channel, "no-client"}); err != nil {
		t.Fatalf("run done worker: %v", err)
	}
	if err := <-signalResult; err != nil {
		t.Fatalf("signal done worker: %v", err)
	}
	if _, err := store.NewWorkspaceStore(options.StateDir).Find(source.ID); err == nil {
		t.Fatal("done worker kept the Workspace record")
	}
	if _, err := os.Stat(source.Root); !os.IsNotExist(err) {
		t.Fatalf("done worker kept the Workspace root: %v", err)
	}
	if err := exec.Command("tmux", "-L", options.TmuxSocket, "has-session", "-t", "=example-worker-src").Run(); err == nil {
		t.Fatal("done worker kept the source tmux session")
	}
}

func TestDoneWorkerRemovesACleanCheckoutWithAnInitializedSubmodule(t *testing.T) {
	options := doneFixture(t)
	t.Setenv("TWT_WORKSPACE_ID", "")
	addSubmoduleToDoneFixture(t, options)

	executeWithOptions(t, options, nil, "workspaces", "create", "submodule-src", "--template", "example", "--no-open")
	executeWithOptions(t, options, nil, "workspaces", "create", "submodule-dest", "--template", "example", "--no-open")
	source, err := store.NewWorkspaceStore(options.StateDir).Find("submodule-src")
	if err != nil {
		t.Fatal(err)
	}
	runCommand(t, filepath.Join(source.Root, "app"), "git", "-c", "protocol.file.allow=always", "submodule", "update", "--init")
	helpPane := runCommand(t, "", "tmux", "-L", options.TmuxSocket, "new-window", "-d", "-P", "-F", "#{pane_id}", "-t", "=example-submodule-dest", "-n", "done-helper", "--", "sleep", "60")
	t.Setenv("TMUX_PANE", helpPane)

	channel := "twt-done-submodule-worker-test"
	signalResult := make(chan error, 1)
	go func() {
		time.Sleep(50 * time.Millisecond)
		signalResult <- exec.Command("tmux", "-L", options.TmuxSocket, "wait-for", "-S", channel).Run()
	}()
	workerErr := cli.RunDoneWorker(options, []string{source.ID, "keep=false", "force=false", "-", "-", "-", channel, "no-client"})
	if err := <-signalResult; err != nil {
		t.Fatalf("signal done worker: %v", err)
	}
	windowName := runCommand(t, "", "tmux", "-L", options.TmuxSocket, "display-message", "-p", "-t", helpPane, "#{window_name}")
	if workerErr != nil {
		t.Fatalf("run done worker for a clean submodule checkout: %v; destination window = %q", workerErr, windowName)
	}
	if _, err := store.NewWorkspaceStore(options.StateDir).Find(source.ID); err == nil {
		t.Fatal("done worker kept the Workspace record")
	}
	if _, err := os.Stat(source.Root); !os.IsNotExist(err) {
		t.Fatalf("done worker kept the Workspace root: %v", err)
	}
	if windowName == "done-failed" {
		t.Fatal("done worker renamed the destination window to done-failed")
	}
}

func TestWorkspaceRemovalRefusesIgnoredSubmoduleChanges(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(t *testing.T, submodulePath string) string
	}{
		{
			name: "modified tracked file",
			mutate: func(t *testing.T, submodulePath string) string {
				t.Helper()
				path := filepath.Join(submodulePath, "README.md")
				if err := os.WriteFile(path, []byte("changed\n"), 0o644); err != nil {
					t.Fatal(err)
				}
				return path
			},
		},
		{
			name: "untracked file",
			mutate: func(t *testing.T, submodulePath string) string {
				t.Helper()
				path := filepath.Join(submodulePath, "unsaved.txt")
				if err := os.WriteFile(path, []byte("keep\n"), 0o644); err != nil {
					t.Fatal(err)
				}
				return path
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			options := doneFixture(t)
			t.Setenv("TMUX_PANE", "")
			t.Setenv("TWT_WORKSPACE_ID", "")
			addSubmoduleToDoneFixture(t, options)
			executeWithOptions(t, options, nil, "workspaces", "create", "submodule-dirty", "--template", "example", "--no-open")
			workspace, err := store.NewWorkspaceStore(options.StateDir).Find("submodule-dirty")
			if err != nil {
				t.Fatal(err)
			}
			checkoutPath := filepath.Join(workspace.Root, "app")
			submodulePath := filepath.Join(checkoutPath, "dependencies", "example")
			runCommand(t, checkoutPath, "git", "-c", "protocol.file.allow=always", "submodule", "update", "--init")
			runCommand(t, checkoutPath, "git", "config", "submodule.dependencies/example.ignore", "all")
			changedPath := test.mutate(t, submodulePath)
			executeWithOptions(t, options, nil, "workspaces", "archive", workspace.ID)
			planOutput := executeWithOptions(t, options, nil, "workspaces", "remove", workspace.ID)
			if !strings.Contains(planOutput, "uncommitted changes") || !strings.Contains(planOutput, "dependencies/example") {
				t.Fatalf("submodule removal plan does not identify the hidden changes: %q", planOutput)
			}

			var stdout, stderr bytes.Buffer
			removeOptions := options
			removeOptions.Stdout, removeOptions.Stderr = &stdout, &stderr
			command := cli.New(removeOptions)
			command.SetArgs(forceTextOutput([]string{"workspaces", "remove", workspace.ID, "--apply"}))
			err = command.Execute()
			if err == nil || !strings.Contains(err.Error(), "uncommitted changes") {
				t.Fatalf("submodule removal error = %v; stdout = %q", err, stdout.String())
			}
			if _, err := os.Stat(changedPath); err != nil {
				t.Fatalf("submodule removal changed user data: %v", err)
			}
			unchanged, err := store.NewWorkspaceStore(options.StateDir).Find(workspace.ID)
			if err != nil || unchanged.Status != domain.WorkspaceArchived {
				t.Fatalf("blocked Workspace status = %q, error = %v", unchanged.Status, err)
			}
		})
	}
}

func TestDoneRelocationClosesItsHelperAfterRemovingASubmoduleCheckout(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go is not installed")
	}
	options := doneFixture(t)
	binary := filepath.Join(filepath.Dir(options.ConfigDir), "twt")
	runCommand(t, filepath.Join("..", ".."), "go", "build", "-o", binary, "./cmd/twt")
	options.QuickCreateExecutable = binary
	addSubmoduleToDoneFixture(t, options)

	executeWithOptions(t, options, nil, "workspaces", "create", "relocation-src", "--template", "example", "--no-open")
	executeWithOptions(t, options, nil, "workspaces", "create", "relocation-dest", "--template", "example", "--no-open")
	workspaceStore := store.NewWorkspaceStore(options.StateDir)
	source, err := workspaceStore.Find("relocation-src")
	if err != nil {
		t.Fatal(err)
	}
	destination, err := workspaceStore.Find("relocation-dest")
	if err != nil {
		t.Fatal(err)
	}
	runCommand(t, filepath.Join(source.Root, "app"), "git", "-c", "protocol.file.allow=always", "submodule", "update", "--init")
	sourcePane := runCommand(t, "", "tmux", "-L", options.TmuxSocket, "list-panes", "-t", "="+source.TmuxSession, "-F", "#{pane_id}")
	attachControlClient(t, options.TmuxSocket, source.TmuxSession)
	t.Setenv("TMUX_PANE", sourcePane)
	t.Setenv("TWT_WORKSPACE_ID", source.ID)

	output := executeWithOptions(t, options, nil, "done", source.ID)
	if !strings.Contains(output, "switching the client to Workspace \"relocation-dest\"") {
		t.Fatalf("relocated done output = %q", output)
	}
	waitFor(t, 5*time.Second, func() bool {
		_, findErr := workspaceStore.Find(source.ID)
		return findErr != nil
	}, "the done worker did not remove the source Workspace")
	if _, err := workspaceStore.Find(source.ID); err == nil {
		t.Fatal("the done worker kept the source Workspace record")
	}
	if _, err := os.Stat(source.Root); !os.IsNotExist(err) {
		t.Fatalf("the done worker kept the source Workspace root: %v", err)
	}
	clientSession := runCommand(t, "", "tmux", "-L", options.TmuxSocket, "list-clients", "-F", "#{session_name}")
	if clientSession != destination.TmuxSession {
		t.Fatalf("calling client session = %q, want %q", clientSession, destination.TmuxSession)
	}
	waitFor(t, 2*time.Second, func() bool {
		windows, listErr := exec.Command("tmux", "-L", options.TmuxSocket, "list-windows", "-t", "="+destination.TmuxSession, "-F", "#{window_name}").CombinedOutput()
		return listErr == nil && strings.TrimSpace(string(windows)) == "app"
	}, "the successful done helper window did not close")
}
