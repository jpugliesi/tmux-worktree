package cli_test

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	workspaceservice "github.com/jpugliesi/tmux-worktree/internal/workspace"
)

func TestBeginSessionCloseSkipsAClosingSessionAndRenames(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is not installed")
	}
	socket := fmt.Sprintf("twt-closing-test-%d", time.Now().UnixNano())
	t.Cleanup(func() { exec.Command("tmux", "-L", socket, "kill-server").Run() })
	tmux := func(args ...string) string {
		t.Helper()
		return runCommand(t, "", "tmux", append([]string{"-L", socket, "-f", "/dev/null"}, args...)...)
	}
	for _, name := range []string{"landing", "other", "leaving"} {
		tmux("new-session", "-d", "-s", name)
	}
	// Another release closes "other". The client used it last.
	tmux("set-option", "-t", "=other:", "@twt_closing", "1")
	attachControlClient(t, socket, "landing")
	client := tmux("list-clients", "-F", "#{client_name}")
	tmux("switch-client", "-c", client, "-t", "=other")
	tmux("switch-client", "-c", client, "-t", "=leaving")
	leavingID := tmux("display-message", "-p", "-t", "=leaving:", "#{session_id}")
	root := t.TempDir()
	service := workspaceservice.NewService(workspaceservice.Options{StateDir: filepath.Join(root, "state"), DataDir: filepath.Join(root, "data"), TmuxSocket: socket})

	closing := service.BeginSessionClose(leavingID)
	if got := tmux("list-clients", "-F", "#{session_name}"); got != "landing" {
		t.Fatalf("client session = %q, want %q", got, "landing")
	}
	if got := tmux("display-message", "-p", "-t", leavingID, "#{session_name}|#{@twt_closing}"); got != "twt-closing-leaving|1" {
		t.Fatalf("closing session = %q", got)
	}

	service.CancelSessionClose(closing)
	if got := tmux("list-clients", "-F", "#{session_name}"); got != "leaving" {
		t.Fatalf("client session after cancel = %q, want %q", got, "leaving")
	}
	if got := tmux("display-message", "-p", "-t", leavingID, "#{session_name}|#{@twt_closing}"); got != "leaving|" {
		t.Fatalf("session after cancel = %q", got)
	}
}
