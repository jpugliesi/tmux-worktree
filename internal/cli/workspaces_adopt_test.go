package cli_test

import (
	"encoding/json"
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

func TestWorkspacesAdoptTurnsATmuxSessionIntoAWorkspace(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is not installed")
	}

	root := t.TempDir()
	home := filepath.Join(root, "home")
	t.Setenv("HOME", home)
	t.Setenv("TMUX_PANE", "")
	t.Setenv("TWT_WORKSPACE_ID", "")
	repository := filepath.Join(root, "code", "app")
	initGitRepository(t, repository)
	plain := filepath.Join(root, "notes")
	if err := os.MkdirAll(plain, 0o755); err != nil {
		t.Fatal(err)
	}
	// tmux and git report resolved paths; macOS puts the test directory
	// behind a /var symlink.
	repository = resolvePath(t, repository)
	plain = resolvePath(t, plain)

	socket := fmt.Sprintf("twt-test-%d", time.Now().UnixNano())
	t.Cleanup(func() { exec.Command("tmux", "-L", socket, "kill-server").Run() })
	runCommand(t, "", "tmux", "-L", socket, "new-session", "-d", "-s", "handmade", "-c", repository)
	runCommand(t, "", "tmux", "-L", socket, "new-window", "-t", "handmade", "-c", plain)
	options := cli.Options{ConfigDir: filepath.Join(root, "config"), StateDir: filepath.Join(root, "state"), DataDir: filepath.Join(root, "data"), TmuxSocket: socket}

	// A dry run validates the adopt and writes nothing.
	dry := executeWithOptions(t, options, nil, "workspaces", "adopt", "handmade", "--dry-run", "--output", "json")
	if !strings.Contains(dry, `"status":"valid"`) {
		t.Fatalf("dry-run adopt = %s", dry)
	}
	if workspaces, err := store.NewWorkspaceStore(options.StateDir).List(); err != nil || len(workspaces) != 0 {
		t.Fatalf("dry-run adopt saved a Workspace: %+v (%v)", workspaces, err)
	}

	adopted := executeWithOptions(t, options, nil, "workspaces", "adopt", "handmade", "--output", "json")
	if !strings.Contains(adopted, `"status":"applied"`) {
		t.Fatalf("adopt = %s", adopted)
	}
	workspace, err := store.NewWorkspaceStore(options.StateDir).Find("handmade")
	if err != nil {
		t.Fatal(err)
	}
	if !workspace.Adopted || workspace.Name != "handmade" || workspace.TmuxSession != "handmade" {
		t.Fatalf("adopted Workspace = %+v", workspace)
	}
	if len(workspace.Repositories) != 1 || workspace.Repositories[0].Path != repository || workspace.Repositories[0].Name != "app" {
		t.Fatalf("adopted repositories = %+v", workspace.Repositories)
	}
	if workspace.Root != repository {
		t.Fatalf("adopted root = %q", workspace.Root)
	}
	owner := runCommand(t, "", "tmux", "-L", socket, "show-options", "-t", "handmade", "-v", "@twt_workspace_id")
	if owner != workspace.ID {
		t.Fatalf("session marker = %q, want %q", owner, workspace.ID)
	}

	// The session now belongs to a Workspace: a second adopt refuses.
	_, _, err = executeRaw(t, options, "workspaces", "adopt", "handmade")
	if err == nil || clierr.CodeOf(err) != clierr.AlreadyExists {
		t.Fatalf("second adopt = %v", err)
	}

	// The adopted Workspace works with agent-session discovery: a provider
	// session inside the adopted repository appears in the list.
	writeTestLines(t, filepath.Join(home, ".codex", "sessions", "rollout-codex-adopted.jsonl"),
		`{"type":"session_meta","payload":{"id":"codex-adopted","cwd":`+quoteJSON(t, repository)+`}}
{"type":"response_item","payload":{"role":"user","content":[{"type":"input_text","text":"Adopted question"}]}}
`)
	list := executeWithOptions(t, options, nil, "agents", "list", "--workspace", "handmade", "--output", "json")
	if !strings.Contains(list, `"id":"codex-adopted"`) || !strings.Contains(list, `"status":"discovered"`) {
		t.Fatalf("agents list for the adopted Workspace = %s", list)
	}

	// Done archives and removes the adopted Workspace. The plan keeps the
	// directories, and removal deletes only the twt state.
	done := executeWithOptions(t, options, nil, "done", "handmade", "--output", "json")
	if !strings.Contains(done, "keep_directory") || strings.Contains(done, "remove_worktree") {
		t.Fatalf("done plan for the adopted Workspace = %s", done)
	}
	if _, err := store.NewWorkspaceStore(options.StateDir).Find("handmade"); err == nil {
		t.Fatal("done kept the adopted Workspace record")
	}
	if _, err := os.Stat(filepath.Join(repository, "README.md")); err != nil {
		t.Fatalf("done deleted the adopted repository: %v", err)
	}
	if _, err := os.Stat(plain); err != nil {
		t.Fatalf("done deleted the plain pane directory: %v", err)
	}
}

// resolvePath resolves symbolic links, because tmux and git report resolved
// pane and repository paths.
func resolvePath(t *testing.T, path string) string {
	t.Helper()
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatal(err)
	}
	return resolved
}

func TestWorkspacesAdoptWorksWithoutARepositoryAndDefaultsToThePaneSession(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is not installed")
	}

	root := t.TempDir()
	t.Setenv("HOME", filepath.Join(root, "home"))
	t.Setenv("TWT_WORKSPACE_ID", "")
	plain := filepath.Join(root, "scratch")
	if err := os.MkdirAll(plain, 0o755); err != nil {
		t.Fatal(err)
	}
	plain = resolvePath(t, plain)

	socket := fmt.Sprintf("twt-test-%d", time.Now().UnixNano())
	t.Cleanup(func() { exec.Command("tmux", "-L", socket, "kill-server").Run() })
	runCommand(t, "", "tmux", "-L", socket, "new-session", "-d", "-s", "scratchpad", "-c", plain)
	pane := runCommand(t, "", "tmux", "-L", socket, "list-panes", "-t", "scratchpad", "-F", "#{pane_id}")
	t.Setenv("TMUX_PANE", pane)
	options := cli.Options{ConfigDir: filepath.Join(root, "config"), StateDir: filepath.Join(root, "state"), DataDir: filepath.Join(root, "data"), TmuxSocket: socket}

	// Without a SESSION argument, adopt uses the session of the calling pane.
	adopted := executeWithOptions(t, options, nil, "workspaces", "adopt", "--output", "json")
	if !strings.Contains(adopted, `"status":"applied"`) {
		t.Fatalf("adopt = %s", adopted)
	}
	workspace, err := store.NewWorkspaceStore(options.StateDir).Find("scratchpad")
	if err != nil {
		t.Fatal(err)
	}
	if !workspace.Adopted || len(workspace.Repositories) != 0 || workspace.Root != plain {
		t.Fatalf("adopted Workspace = %+v", workspace)
	}
	var decoded struct {
		Workspace struct {
			Adopted bool `json:"adopted"`
		} `json:"workspace"`
	}
	shown := executeWithOptions(t, options, nil, "workspaces", "show", "scratchpad", "--output", "json")
	if err := json.Unmarshal([]byte(shown), &decoded); err != nil {
		t.Fatal(err)
	}
	if !decoded.Workspace.Adopted {
		t.Fatalf("workspaces show = %s", shown)
	}

	// Archive stops the session; removal then deletes only the twt state.
	// The pane directory survives.
	t.Setenv("TMUX_PANE", "")
	executeWithOptions(t, options, nil, "workspaces", "archive", "scratchpad", "--output", "json")
	removed := executeWithOptions(t, options, nil, "workspaces", "remove", "scratchpad", "--apply", "--output", "json")
	if !strings.Contains(removed, "release_tmux_session") {
		t.Fatalf("removal plan = %s", removed)
	}
	if _, err := os.Stat(plain); err != nil {
		t.Fatalf("removal deleted the pane directory: %v", err)
	}
}
