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

// The session command of a Project Template lays out the tmux session. twt
// runs it each time it creates the session, and never against a live session.
func TestProjectsCreateRunsTheTemplateSessionCommand(t *testing.T) {
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
test -n "$TWT_PROJECT_ID"
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
	executeWithOptions(t, options, nil, "projects", "create", "layout-me", "--template", "example", "--no-open")
	project, err := store.NewProjectStore(options.StateDir).Find("layout-me")
	if err != nil {
		t.Fatal(err)
	}
	if project.TmuxSession != "twt-layout-me" {
		t.Fatalf("Project tmux session = %q, want %q", project.TmuxSession, "twt-layout-me")
	}
	if panes := paneCount(t, socket, project.TmuxSession, "app"); panes != 3 {
		t.Fatalf("panes in the repository window after create = %d, want 3", panes)
	}

	// A setup retry must not run the session command against the live
	// session, so the pane count stays the same.
	executeWithOptions(t, options, nil, "projects", "setup", "retry", project.ID)
	if panes := paneCount(t, socket, project.TmuxSession, "app"); panes != 3 {
		t.Fatalf("panes in the repository window after a setup retry = %d, want 3", panes)
	}

	// Open makes the tmux session again, so the session command runs again.
	t.Setenv("TMUX_PANE", "")
	executeWithOptions(t, options, nil, "projects", "archive", project.ID)
	if err := exec.Command("tmux", "-L", socket, "has-session", "-t", "=twt-layout-me").Run(); err == nil {
		t.Fatal("archive kept the Project tmux session")
	}
	executeWithOptions(t, options, nil, "projects", "open", project.ID, "--no-attach")
	if panes := paneCount(t, socket, project.TmuxSession, "app"); panes != 3 {
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

// A Project Template with a broken session command must fail the tmux step and
// must keep the Project for a setup retry.
func TestProjectsCreateReportsABrokenSessionCommand(t *testing.T) {
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
	command.SetArgs(forceTextOutput([]string{"projects", "create", "broken-layout", "--template", "example", "--no-open"}))
	err := command.Execute()
	if err == nil || !strings.Contains(err.Error(), "session command") || !strings.Contains(err.Error(), "layout failed") {
		t.Fatalf("projects create error = %v", err)
	}
}
