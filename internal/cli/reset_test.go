package cli_test

import (
	"encoding/json"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/jpugliesi/tmux-worktree/internal/cli"
	"github.com/jpugliesi/tmux-worktree/internal/clierr"
)

func TestResetRequiresATmuxPane(t *testing.T) {
	t.Setenv("TMUX_PANE", "")
	_, _, err := executeCollectingInput(t, cli.Options{
		ConfigDir: t.TempDir(), StateDir: t.TempDir(), DataDir: t.TempDir(),
	}, nil, "reset")
	if err == nil || clierr.CodeOf(err) != clierr.InvalidUsage {
		t.Fatalf("reset without a pane = %v (code %q)", err, clierr.CodeOf(err))
	}
	if hint := clierr.HintOf(err); hint != "Run 'twt reset' from a tmux pane." {
		t.Fatalf("reset hint = %q", hint)
	}
}

func TestResetRespawnsEveryPaneIncludingTheCaller(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is not installed")
	}
	socket := "twt-test-reset-" + time.Now().Format("150405.000000000")
	t.Cleanup(func() { exec.Command("tmux", "-L", socket, "kill-server").Run() })
	runCommand(t, "", "tmux", "-L", socket, "new-session", "-d", "-s", "reset-test", "-n", "main", "--", "/bin/sh")
	runCommand(t, "", "tmux", "-L", socket, "has-session", "-t", "=reset-test")
	runCommand(t, "", "tmux", "-L", socket, "split-window", "-t", "=reset-test:main", "--", "sleep", "300")

	panes := strings.Fields(runCommand(t, "", "tmux", "-L", socket, "list-panes", "-t", "=reset-test", "-F", "#{pane_id}"))
	if len(panes) != 2 {
		t.Fatalf("test window panes = %v", panes)
	}
	caller, hung := panes[0], panes[1]
	waitForPaneCommand(t, socket, hung, "sleep")
	callerPID := paneField(t, socket, caller, "#{pane_pid}")
	hungPID := paneField(t, socket, hung, "#{pane_pid}")

	t.Setenv("TMUX_PANE", caller)
	options := cli.Options{
		ConfigDir: t.TempDir(), StateDir: t.TempDir(), DataDir: t.TempDir(),
		TmuxSocket: socket,
	}

	stdout, _, err := executeCollectingInput(t, options, nil, "reset", "--dry-run", "--output", "json")
	if err != nil {
		t.Fatal(err)
	}
	preview := decodeResetOutput(t, stdout)
	if preview.Operation != "reset" || preview.Status != "valid" || strings.Join(preview.Panes, ",") != strings.Join(panes, ",") {
		t.Fatalf("dry-run reset = %+v\n%s", preview, stdout)
	}
	if paneField(t, socket, caller, "#{pane_pid}") != callerPID || paneField(t, socket, hung, "#{pane_pid}") != hungPID {
		t.Fatal("a dry-run reset respawned a pane")
	}

	stdout, _, err = executeCollectingInput(t, options, nil, "reset")
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(stdout) != "Reset 2 panes" {
		t.Fatalf("reset text = %q", stdout)
	}
	if paneField(t, socket, caller, "#{pane_pid}") == callerPID {
		t.Fatal("reset left the caller pane on the same process")
	}
	if paneField(t, socket, hung, "#{pane_pid}") == hungPID {
		t.Fatal("reset left the hung pane on the same process")
	}
	if cmd := paneField(t, socket, hung, "#{pane_current_command}"); cmd == "sleep" {
		t.Fatalf("reset restarted the hung command %q", cmd)
	}

	stdout, _, err = executeCollectingInput(t, options, nil, "reset", "--output", "json")
	if err != nil {
		t.Fatal(err)
	}
	applied := decodeResetOutput(t, stdout)
	if applied.SchemaVersion != 2 || applied.Operation != "reset" || applied.Status != "applied" {
		t.Fatalf("reset envelope = %+v\n%s", applied, stdout)
	}
	if strings.Join(applied.Panes, ",") != strings.Join(panes, ",") {
		t.Fatalf("reset panes = %v, want %v", applied.Panes, panes)
	}
}

func paneField(t *testing.T, socket, pane, format string) string {
	t.Helper()
	return runCommand(t, "", "tmux", "-L", socket, "display-message", "-p", "-t", pane, format)
}

func waitForPaneCommand(t *testing.T, socket, pane, want string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if paneField(t, socket, pane, "#{pane_current_command}") == want {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("pane %s command never became %q", pane, want)
}

func decodeResetOutput(t *testing.T, stdout string) struct {
	SchemaVersion int      `json:"schemaVersion"`
	Operation     string   `json:"operation"`
	Status        string   `json:"status"`
	Panes         []string `json:"panes"`
} {
	t.Helper()
	var result struct {
		SchemaVersion int      `json:"schemaVersion"`
		Operation     string   `json:"operation"`
		Status        string   `json:"status"`
		Panes         []string `json:"panes"`
	}
	if err := json.Unmarshal([]byte(stdout), &result); err != nil {
		t.Fatalf("decode reset output: %v\n%s", err, stdout)
	}
	return result
}
