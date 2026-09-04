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
	"github.com/jpugliesi/tmux-worktree/internal/clierr"
	"github.com/jpugliesi/tmux-worktree/internal/store"
)

func workspacesOpenFixture(t *testing.T) cli.Options {
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
	return cli.Options{
		ConfigDir:  configDir,
		StateDir:   filepath.Join(root, "state"),
		DataDir:    filepath.Join(root, "data"),
		TmuxSocket: socket,
	}
}

func TestOpenClaimsAnUnownedSessionWithTheExpectedName(t *testing.T) {
	options := workspacesOpenFixture(t)
	executeWithOptions(t, options, nil, "workspaces", "create", "fix-auth", "--template", "example", "--no-open")
	workspace, err := store.NewWorkspaceStore(options.StateDir).Find("fix-auth")
	if err != nil {
		t.Fatal(err)
	}
	runCommand(t, "", "tmux", "-L", options.TmuxSocket, "kill-session", "-t", "=example-fix-auth")
	runCommand(t, "", "tmux", "-L", options.TmuxSocket, "new-session", "-d", "-s", "example-fix-auth", "-n", "app", "-c", workspace.Repositories[0].Path)

	output := executeWithOptions(t, options, nil, "workspaces", "open", "fix-auth", "--no-attach")
	if !strings.Contains(output, `Opened Workspace "fix-auth"`) {
		t.Fatalf("open output = %q", output)
	}
	sessions := runCommand(t, "", "tmux", "-L", options.TmuxSocket, "list-sessions", "-F", "#{session_name}|#{@twt_workspace_id}")
	if sessions != "example-fix-auth|"+workspace.ID {
		t.Fatalf("open sessions after claim = %q, want one owned session", sessions)
	}
	windows := runCommand(t, "", "tmux", "-L", options.TmuxSocket, "list-windows", "-t", "=example-fix-auth", "-F", "#{window_name}|#{@twt_repository_name}")
	if windows != "app|app" {
		t.Fatalf("open windows after claim = %q", windows)
	}
}

func TestOpenAllActiveRepairsMissingSessions(t *testing.T) {
	options := workspacesOpenFixture(t)
	executeWithOptions(t, options, nil, "workspaces", "create", "alpha", "--template", "example", "--no-open")
	executeWithOptions(t, options, nil, "workspaces", "create", "beta", "--template", "example", "--no-open")
	runCommand(t, "", "tmux", "-L", options.TmuxSocket, "kill-session", "-t", "=example-alpha")
	runCommand(t, "", "tmux", "-L", options.TmuxSocket, "kill-session", "-t", "=example-beta")

	dryRun := executeWithOptions(t, options, nil, "workspaces", "open", "--all-active", "--dry-run", "--output", "json")
	if !strings.Contains(dryRun, `"status":"valid"`) || !strings.Contains(dryRun, `"name":"alpha"`) || !strings.Contains(dryRun, `"name":"beta"`) {
		t.Fatalf("open --all-active dry-run = %s", dryRun)
	}
	if err := exec.Command("tmux", "-L", options.TmuxSocket, "has-session", "-t", "=example-alpha").Run(); err == nil {
		t.Fatal("open --all-active dry-run created a session")
	}

	output := executeWithOptions(t, options, nil, "workspaces", "open", "--all-active")
	if !strings.Contains(output, "Opened 2 active Workspaces:") || !strings.Contains(output, "alpha") || !strings.Contains(output, "beta") {
		t.Fatalf("open --all-active output = %q", output)
	}
	for _, name := range []string{"example-alpha", "example-beta"} {
		if err := exec.Command("tmux", "-L", options.TmuxSocket, "has-session", "-t", "="+name).Run(); err != nil {
			t.Fatalf("open --all-active did not restore %s: %v", name, err)
		}
	}

	_, _, err := executeCollectingInput(t, options, nil, "workspaces", "open", "alpha", "--all-active")
	if err == nil || clierr.CodeOf(err) != clierr.InvalidUsage {
		t.Fatalf("open WORKSPACE --all-active = %v", err)
	}
	_, _, err = executeCollectingInput(t, options, nil, "workspaces", "open")
	if err == nil || !strings.Contains(err.Error(), "missing required argument WORKSPACE") {
		t.Fatalf("open without WORKSPACE = %v", err)
	}

	runCommand(t, "", "tmux", "-L", options.TmuxSocket, "kill-session", "-t", "=example-alpha")
	runCommand(t, "", "tmux", "-L", options.TmuxSocket, "kill-session", "-t", "=example-beta")
	syncOut := executeWithOptions(t, options, nil, "workspaces", "sync")
	if !strings.Contains(syncOut, "Opened 2 active Workspaces:") {
		t.Fatalf("workspaces sync output = %q", syncOut)
	}
	for _, name := range []string{"example-alpha", "example-beta"} {
		if err := exec.Command("tmux", "-L", options.TmuxSocket, "has-session", "-t", "="+name).Run(); err != nil {
			t.Fatalf("workspaces sync did not restore %s: %v", name, err)
		}
	}
}

func TestDoctorWarnsAboutAMissingWorkspaceSession(t *testing.T) {
	options := workspacesOpenFixture(t)
	executeWithOptions(t, options, nil, "workspaces", "create", "fix-auth", "--template", "example", "--no-open")
	runCommand(t, "", "tmux", "-L", options.TmuxSocket, "kill-session", "-t", "=example-fix-auth")

	output := executeWithOptions(t, options, nil, "doctor", "--output", "json")
	if !strings.Contains(output, `"healthy":true`) {
		t.Fatalf("doctor JSON is not healthy: %s", output)
	}
	if !strings.Contains(output, "no owned tmux session") || !strings.Contains(output, "workspaces sync") {
		t.Fatalf("doctor JSON has no session warning: %s", output)
	}

	executeWithOptions(t, options, nil, "workspaces", "open", "--all-active")
	repaired := executeWithOptions(t, options, nil, "doctor", "--output", "json")
	if strings.Contains(repaired, "no owned tmux session") {
		t.Fatalf("doctor still reports a session gap after open: %s", repaired)
	}
}

func TestApplyOpenAllActiveRepairsMissingSessions(t *testing.T) {
	options := workspacesOpenFixture(t)
	executeWithOptions(t, options, nil, "workspaces", "create", "fix-auth", "--template", "example", "--no-open")
	runCommand(t, "", "tmux", "-L", options.TmuxSocket, "kill-session", "-t", "=example-fix-auth")

	request := `{"operation":"workspaces.open","workspace":{"allActive":true}}`
	if _, _, err := executeCollectingInput(t, options, strings.NewReader(request), "apply", "-", "--dry-run", "--output", "json"); err != nil {
		t.Fatal(err)
	}
	if err := exec.Command("tmux", "-L", options.TmuxSocket, "has-session", "-t", "=example-fix-auth").Run(); err == nil {
		t.Fatal("apply open dry-run created a session")
	}
	if _, _, err := executeCollectingInput(t, options, strings.NewReader(request), "apply", "-", "--output", "json"); err != nil {
		t.Fatal(err)
	}
	if err := exec.Command("tmux", "-L", options.TmuxSocket, "has-session", "-t", "=example-fix-auth").Run(); err != nil {
		t.Fatalf("apply open --all-active did not restore the session: %v", err)
	}
}
