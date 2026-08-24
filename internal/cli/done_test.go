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
