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

func TestDoneOutsideSessionReleasesTheWorkspace(t *testing.T) {
	options := doneFixture(t)
	t.Setenv("TMUX_PANE", "")
	t.Setenv("TWT_WORKSPACE_ID", "")
	executeWithOptions(t, options, nil, "workspaces", "create", "finish-me", "--template", "example", "--no-open")
	workspace, err := store.NewWorkspaceStore(options.StateDir).Find("finish-me")
	if err != nil {
		t.Fatal(err)
	}

	dryRun := executeWithOptions(t, options, nil, "done", "finish-me", "--dry-run")
	if dryRun != "workspaces.done: valid\n" {
		t.Fatalf("done dry-run output = %q", dryRun)
	}
	unchanged, err := store.NewWorkspaceStore(options.StateDir).Find(workspace.ID)
	if err != nil || unchanged.Status != domain.WorkspaceActive {
		t.Fatalf("done dry-run changed the Workspace: status=%q error=%v", unchanged.Status, err)
	}
	if _, err := os.Stat(workspace.Root); err != nil {
		t.Fatalf("done dry-run changed Workspace data: %v", err)
	}

	output := executeWithOptions(t, options, nil, "done", "finish-me")
	if !strings.Contains(output, "Archived Workspace \"finish-me\"") || !strings.Contains(output, "Finished Workspace \"finish-me\"") {
		t.Fatalf("done output = %q", output)
	}
	if _, err := os.Stat(workspace.Root); err != nil {
		t.Fatalf("prepared root does not exist after done: %v", err)
	}
	archived, err := store.NewWorkspaceStore(options.StateDir).Find(workspace.ID)
	if err != nil || archived.Status != domain.WorkspaceArchived || archived.Materialized || archived.Root != "" {
		t.Fatalf("Workspace after done: %+v, error = %v", archived, err)
	}
	if err := exec.Command("tmux", "-L", options.TmuxSocket, "has-session", "-t", "=example-finish-me").Run(); err == nil {
		t.Fatal("Workspace tmux session still exists after done")
	}
	doctor := executeWithOptions(t, options, nil, "doctor")
	if strings.Contains(doctor, "ownership marker is missing") {
		t.Fatalf("doctor rejected the released Workspace:\n%s", doctor)
	}
}

func TestDoneDoesNotChangeADirtyWorkspaceWithoutForce(t *testing.T) {
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
	if stdout.Len() != 0 {
		t.Fatalf("blocked done output = %q", stdout.String())
	}
	unchanged, findErr := store.NewWorkspaceStore(options.StateDir).Find(workspace.ID)
	if findErr != nil || unchanged.Status != domain.WorkspaceActive || !unchanged.Materialized {
		t.Fatalf("blocked done Workspace: %+v, error = %v", unchanged, findErr)
	}
	if _, err := os.Stat(filepath.Join(workspace.Root, "app", "unsaved.txt")); err != nil {
		t.Fatalf("blocked done changed Workspace data: %v", err)
	}
}

func TestDoneInsideSessionStopsTheSourceWithoutAHelper(t *testing.T) {
	options := doneFixture(t)
	t.Setenv("TWT_WORKSPACE_ID", "")
	executeWithOptions(t, options, nil, "workspaces", "create", "source", "--template", "example", "--no-open")
	executeWithOptions(t, options, nil, "workspaces", "create", "destination", "--template", "example", "--no-open")

	workspaceStore := store.NewWorkspaceStore(options.StateDir)
	source, err := workspaceStore.Find("source")
	if err != nil {
		t.Fatal(err)
	}
	destination, err := workspaceStore.Find("destination")
	if err != nil {
		t.Fatal(err)
	}
	sourcePane := runCommand(t, "", "tmux", "-L", options.TmuxSocket, "list-panes", "-t", "="+source.TmuxSession, "-F", "#{pane_id}")
	runCommand(t, "", "tmux", "-L", options.TmuxSocket, "split-window", "-d", "-t", sourcePane, "--", "sleep", "60")
	attachControlClient(t, options.TmuxSocket, source.TmuxSession)
	t.Setenv("TMUX_PANE", sourcePane)

	output := executeWithOptions(t, options, nil, "done", source.ID)
	if !strings.Contains(output, "Finished Workspace \"source\"") {
		t.Fatalf("done output = %q", output)
	}
	if err := exec.Command("tmux", "-L", options.TmuxSocket, "has-session", "-t", "="+source.TmuxSession).Run(); err == nil {
		t.Fatal("done kept the source tmux session")
	}
	waitFor(t, 2*time.Second, func() bool {
		data, err := exec.Command("tmux", "-L", options.TmuxSocket, "list-clients", "-F", "#{session_name}").CombinedOutput()
		return err == nil && strings.TrimSpace(string(data)) == destination.TmuxSession
	}, "tmux did not move the client to the remaining Workspace session")
	sessions := runCommand(t, "", "tmux", "-L", options.TmuxSocket, "list-sessions", "-F", "#{session_name}")
	if strings.Contains(sessions, "twt-done") || strings.Contains(sessions, "done-failed") {
		t.Fatalf("done created a helper session: %q", sessions)
	}
	released, err := workspaceStore.Find(source.ID)
	if err != nil || released.Status != domain.WorkspaceArchived || released.Materialized {
		t.Fatalf("done Workspace = %+v, error = %v", released, err)
	}
}

func TestDoneStopsTheSourceWhenFinalOutputFails(t *testing.T) {
	options := doneFixture(t)
	t.Setenv("TWT_WORKSPACE_ID", "")
	executeWithOptions(t, options, nil, "workspaces", "create", "source", "--template", "example", "--no-open")

	workspaceStore := store.NewWorkspaceStore(options.StateDir)
	source, err := workspaceStore.Find("source")
	if err != nil {
		t.Fatal(err)
	}
	sourcePane := runCommand(t, "", "tmux", "-L", options.TmuxSocket, "list-panes", "-t", "="+source.TmuxSession, "-F", "#{pane_id}")
	t.Setenv("TMUX_PANE", sourcePane)

	output := &failSecondWrite{}
	options.Stdout = output
	command := cli.New(options)
	command.SetArgs(forceTextOutput([]string{"done", source.ID}))
	err = command.Execute()
	if err == nil || !strings.Contains(err.Error(), "test final output failure") {
		t.Fatalf("done output error = %v", err)
	}
	if err := exec.Command("tmux", "-L", options.TmuxSocket, "has-session", "-t", "="+source.TmuxSession).Run(); err == nil {
		t.Fatal("done kept the source session after an output failure")
	}
	released, err := workspaceStore.Find(source.ID)
	if err != nil || released.Status != domain.WorkspaceArchived || released.Materialized {
		t.Fatalf("done Workspace = %+v, error = %v", released, err)
	}
}

type failSecondWrite struct {
	writes int
}

func (w *failSecondWrite) Write(data []byte) (int, error) {
	w.writes++
	if w.writes == 2 {
		return 0, fmt.Errorf("test final output failure")
	}
	return len(data), nil
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
			now := time.Now().UTC()
			workspace.Status = domain.WorkspaceArchived
			workspace.ArchivedAt = &now
			if err := store.NewWorkspaceStore(options.StateDir).Save(workspace); err != nil {
				t.Fatal(err)
			}
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

// A tmux restore (for example tmux-resurrect) recreates a Workspace session
// without the owner option. Done adopts the session back through the saved
// session name, then stops it as usual.
func TestDoneInsideARestoredUnownedSessionStopsIt(t *testing.T) {
	options := doneFixture(t)
	t.Setenv("TWT_WORKSPACE_ID", "")
	executeWithOptions(t, options, nil, "workspaces", "create", "restored", "--template", "example", "--no-open")
	executeWithOptions(t, options, nil, "workspaces", "create", "landing", "--template", "example", "--no-open")

	workspaceStore := store.NewWorkspaceStore(options.StateDir)
	restored, err := workspaceStore.Find("restored")
	if err != nil {
		t.Fatal(err)
	}
	// Simulate the restore: the session keeps its name and loses the owner.
	runCommand(t, "", "tmux", "-L", options.TmuxSocket, "set-option", "-t", restored.TmuxSession, "@twt_workspace_id", "")
	sourcePane := runCommand(t, "", "tmux", "-L", options.TmuxSocket, "list-panes", "-t", "="+restored.TmuxSession, "-F", "#{pane_id}")
	attachControlClient(t, options.TmuxSocket, restored.TmuxSession)
	t.Setenv("TMUX_PANE", sourcePane)

	output := executeWithOptions(t, options, nil, "done", restored.ID)
	if !strings.Contains(output, "Finished Workspace \"restored\"") {
		t.Fatalf("done output = %q", output)
	}
	if err := exec.Command("tmux", "-L", options.TmuxSocket, "has-session", "-t", "="+restored.TmuxSession).Run(); err == nil {
		t.Fatal("done kept the restored unowned tmux session")
	}
	released, err := workspaceStore.Find(restored.ID)
	if err != nil || released.Status != domain.WorkspaceArchived {
		t.Fatalf("done Workspace = %+v, error = %v", released, err)
	}
}

// A restore after a rename can also lose the saved session name. Done adopts
// an unowned session when every pane in it works inside the Workspace.
func TestDoneInsideARestoredRenamedSessionStopsIt(t *testing.T) {
	options := doneFixture(t)
	t.Setenv("TWT_WORKSPACE_ID", "")
	executeWithOptions(t, options, nil, "workspaces", "create", "restored", "--template", "example", "--no-open")
	executeWithOptions(t, options, nil, "workspaces", "create", "landing", "--template", "example", "--no-open")

	workspaceStore := store.NewWorkspaceStore(options.StateDir)
	restored, err := workspaceStore.Find("restored")
	if err != nil {
		t.Fatal(err)
	}
	runCommand(t, "", "tmux", "-L", options.TmuxSocket, "set-option", "-t", restored.TmuxSession, "@twt_workspace_id", "")
	runCommand(t, "", "tmux", "-L", options.TmuxSocket, "rename-session", "-t", "="+restored.TmuxSession, "example-old-name")
	sourcePane := runCommand(t, "", "tmux", "-L", options.TmuxSocket, "list-panes", "-t", "=example-old-name", "-F", "#{pane_id}")
	attachControlClient(t, options.TmuxSocket, "example-old-name")
	t.Setenv("TMUX_PANE", sourcePane)

	output := executeWithOptions(t, options, nil, "done", restored.ID)
	if !strings.Contains(output, "Cleanup runs before tmux stops this session") {
		t.Fatalf("done did not run from the session: %q", output)
	}
	if err := exec.Command("tmux", "-L", options.TmuxSocket, "has-session", "-t", "=example-old-name").Run(); err == nil {
		t.Fatal("done kept the restored renamed tmux session")
	}
}

// Done never adopts an unowned session that has a pane outside the
// Workspace: a session stop would end unrelated work.
func TestDoneDoesNotAdoptAnUnownedSessionWithAForeignPane(t *testing.T) {
	options := doneFixture(t)
	t.Setenv("TWT_WORKSPACE_ID", "")
	executeWithOptions(t, options, nil, "workspaces", "create", "restored", "--template", "example", "--no-open")
	executeWithOptions(t, options, nil, "workspaces", "create", "landing", "--template", "example", "--no-open")

	workspaceStore := store.NewWorkspaceStore(options.StateDir)
	restored, err := workspaceStore.Find("restored")
	if err != nil {
		t.Fatal(err)
	}
	runCommand(t, "", "tmux", "-L", options.TmuxSocket, "set-option", "-t", restored.TmuxSession, "@twt_workspace_id", "")
	runCommand(t, "", "tmux", "-L", options.TmuxSocket, "rename-session", "-t", "="+restored.TmuxSession, "example-old-name")
	runCommand(t, "", "tmux", "-L", options.TmuxSocket, "split-window", "-d", "-t", "=example-old-name:", "-c", t.TempDir())
	sourcePane := runCommand(t, "", "tmux", "-L", options.TmuxSocket, "display-message", "-p", "-t", "=example-old-name:", "#{pane_id}")
	t.Setenv("TMUX_PANE", sourcePane)

	// The caller pane runs done, so it is never a foreign pane. Done releases
	// the worktree and keeps the session; a claim skips the busy worktree.
	if _, _, err := executeCollectingInput(t, options, strings.NewReader("n\n"), "done", restored.ID); err != nil {
		t.Fatal(err)
	}
	if err := exec.Command("tmux", "-L", options.TmuxSocket, "has-session", "-t", "=example-old-name").Run(); err != nil {
		t.Fatal("done stopped an unowned session with a foreign pane")
	}
}

// A pane outside the Workspace session that still works in its worktree
// stops done. Done asks before it stops the pane, and No keeps everything.
func TestDoneAsksBeforeItStopsAForeignPane(t *testing.T) {
	options := doneFixture(t)
	t.Setenv("TWT_WORKSPACE_ID", "")
	t.Setenv("TMUX_PANE", "")
	executeWithOptions(t, options, nil, "workspaces", "create", "busy", "--template", "example", "--no-open")
	busy, err := store.NewWorkspaceStore(options.StateDir).Find("busy")
	if err != nil {
		t.Fatal(err)
	}
	runCommand(t, "", "tmux", "-L", options.TmuxSocket, "new-session", "-d", "-s", "elsewhere", "-c", busy.Repositories[0].Path)
	waitForPaneInside(t, options.TmuxSocket, "=elsewhere:", busy.Root)

	_, stderr, err := executeCollectingInput(t, options, strings.NewReader("n\n"), "done", busy.ID)
	if err == nil || !strings.Contains(stderr, "Stop these panes?") {
		t.Fatalf("done with No: error = %v, stderr = %q", err, stderr)
	}
	if err := exec.Command("tmux", "-L", options.TmuxSocket, "has-session", "-t", "=elsewhere").Run(); err != nil {
		t.Fatal("done stopped the foreign pane after No")
	}

	if _, _, err := executeCollectingInput(t, options, strings.NewReader("y\n"), "done", busy.ID); err != nil {
		t.Fatal(err)
	}
	if err := exec.Command("tmux", "-L", options.TmuxSocket, "has-session", "-t", "=elsewhere").Run(); err == nil {
		t.Fatal("done kept the foreign pane after Yes")
	}
}

// --force stops a foreign pane without a prompt, and JSON output without
// --force refuses with unsafe_state.
func TestDoneForceStopsAForeignPane(t *testing.T) {
	options := doneFixture(t)
	t.Setenv("TWT_WORKSPACE_ID", "")
	t.Setenv("TMUX_PANE", "")
	executeWithOptions(t, options, nil, "workspaces", "create", "busy", "--template", "example", "--no-open")
	busy, err := store.NewWorkspaceStore(options.StateDir).Find("busy")
	if err != nil {
		t.Fatal(err)
	}
	runCommand(t, "", "tmux", "-L", options.TmuxSocket, "new-session", "-d", "-s", "elsewhere", "-c", busy.Repositories[0].Path)
	waitForPaneInside(t, options.TmuxSocket, "=elsewhere:", busy.Root)

	if _, _, err := executeRaw(t, options, "done", busy.ID, "--output", "json"); err == nil || !strings.Contains(err.Error(), "still work in Workspace") {
		t.Fatalf("done --output json without --force: error = %v", err)
	}
	if _, _, err := executeRaw(t, options, "done", busy.ID, "--force", "--output", "json"); err != nil {
		t.Fatal(err)
	}
	if err := exec.Command("tmux", "-L", options.TmuxSocket, "has-session", "-t", "=elsewhere").Run(); err == nil {
		t.Fatal("done --force kept the foreign pane")
	}
}

// waitForPaneInside waits until tmux reports the pane directory inside root.
// A new pane can report another directory until its shell starts.
func waitForPaneInside(t *testing.T, socket, target, root string) {
	t.Helper()
	resolved, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		path := runCommand(t, "", "tmux", "-L", socket, "display-message", "-p", "-t", target, "#{pane_current_path}")
		if strings.HasPrefix(path, resolved) || strings.HasPrefix(path, root) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("pane %s works in %q, want a directory inside %q", target, path, root)
		}
		time.Sleep(20 * time.Millisecond)
	}
}
