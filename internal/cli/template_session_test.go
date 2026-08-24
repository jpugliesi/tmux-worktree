package cli_test

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jpugliesi/tmux-worktree/internal/cli"
	"github.com/jpugliesi/tmux-worktree/internal/store"
)

// The session command of a Workspace Template lays out the tmux session. twt
// runs it each time it creates the session, and never against a live session.
func TestWorkspacesCreateRunsTheTemplateSessionCommand(t *testing.T) {
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
	// The script makes a three-pane layout: one split beside the first pane,
	// then one split under it.
	layout := filepath.Join(root, "layout.sh")
	script := `#!/bin/sh
set -e
call_tmux() {
  if [ -n "$TWT_TMUX_SOCKET" ]; then
    tmux -L "$TWT_TMUX_SOCKET" -f /dev/null "$@"
  else
    tmux "$@"
  fi
}
test -n "$TWT_WORKSPACE_ID"
test -d "$TWT_REPOSITORY_APP"
call_tmux split-window -d -h -l 34% -t "$TWT_TMUX_WINDOW_APP" -c "$TWT_REPOSITORY_APP"
call_tmux split-window -d -v -l 25% -t "$TWT_TMUX_WINDOW_APP" -c "$TWT_REPOSITORY_APP"
`
	if err := os.WriteFile(layout, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	template := fmt.Sprintf(`version: 1
name: example
repositories:
  - name: app
    window_name: app
    clone:
      url: %s
session:
  command:
    - %s
`, source, layout)
	if err := os.WriteFile(filepath.Join(configDir, "templates", "example.yaml"), []byte(template), 0o644); err != nil {
		t.Fatal(err)
	}
	socket := fmt.Sprintf("twt-test-%d", time.Now().UnixNano())
	t.Cleanup(func() { _ = exec.Command("tmux", "-L", socket, "kill-server").Run() })
	options := cli.Options{
		ConfigDir: configDir, StateDir: filepath.Join(root, "state"),
		DataDir: filepath.Join(root, "data"), TmuxSocket: socket,
	}

	executeWithOptions(t, options, nil, "templates", "validate", "example")
	executeWithOptions(t, options, nil, "workspaces", "create", "layout-me", "--template", "example", "--no-open")
	workspace, err := store.NewWorkspaceStore(options.StateDir).Find("layout-me")
	if err != nil {
		t.Fatal(err)
	}
	if workspace.TmuxSession != "example-layout-me" {
		t.Fatalf("Workspace tmux session = %q, want %q", workspace.TmuxSession, "example-layout-me")
	}
	if panes := paneCount(t, socket, workspace.TmuxSession, "app"); panes != 3 {
		t.Fatalf("panes in the repository window after create = %d, want 3", panes)
	}

	// A setup retry must not run the session command against the live
	// session, so the pane count stays the same.
	executeWithOptions(t, options, nil, "workspaces", "setup", "retry", workspace.ID)
	if panes := paneCount(t, socket, workspace.TmuxSession, "app"); panes != 3 {
		t.Fatalf("panes in the repository window after a setup retry = %d, want 3", panes)
	}

	// Open makes the tmux session again, so the session command runs again.
	t.Setenv("TMUX_PANE", "")
	executeWithOptions(t, options, nil, "workspaces", "archive", workspace.ID)
	if err := exec.Command("tmux", "-L", socket, "has-session", "-t", "=example-layout-me").Run(); err == nil {
		t.Fatal("archive kept the Workspace tmux session")
	}
	executeWithOptions(t, options, nil, "workspaces", "open", workspace.ID, "--no-attach")
	if panes := paneCount(t, socket, workspace.TmuxSession, "app"); panes != 3 {
		t.Fatalf("panes in the repository window after open = %d, want 3", panes)
	}
}

// paneCount returns the number of panes in one window of a tmux session.
func paneCount(t *testing.T, socket, session, window string) int {
	t.Helper()
	rows := runCommand(t, "", "tmux", "-L", socket, "-f", "/dev/null",
		"list-panes", "-t", "="+session+":"+window, "-F", "#{pane_id}")
	return len(strings.Fields(rows))
}

// A Workspace Template with a broken session command must fail the tmux step and
// must keep the Workspace for a setup retry.
func TestWorkspacesCreateReportsABrokenSessionCommand(t *testing.T) {
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
session:
  command: [sh, -c, "echo layout failed >&2; exit 3"]
`, source)
	if err := os.WriteFile(filepath.Join(configDir, "templates", "example.yaml"), []byte(template), 0o644); err != nil {
		t.Fatal(err)
	}
	socket := fmt.Sprintf("twt-test-%d", time.Now().UnixNano())
	t.Cleanup(func() { _ = exec.Command("tmux", "-L", socket, "kill-server").Run() })
	options := cli.Options{
		ConfigDir: configDir, StateDir: filepath.Join(root, "state"),
		DataDir: filepath.Join(root, "data"), TmuxSocket: socket,
	}

	command := cli.New(options)
	command.SetArgs(forceTextOutput([]string{"workspaces", "create", "broken-layout", "--template", "example", "--no-open"}))
	err := command.Execute()
	if err == nil || !strings.Contains(err.Error(), "session command") || !strings.Contains(err.Error(), "layout failed") {
		t.Fatalf("workspaces create error = %v", err)
	}
}
