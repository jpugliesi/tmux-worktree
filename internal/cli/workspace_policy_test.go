package cli_test

import (
	"bytes"
	"encoding/json"
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

func TestWorkspacesCreateUsesDeclaredDefaultBranch(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is not installed")
	}

	root := t.TempDir()
	source := filepath.Join(root, "source")
	initGitRepository(t, source)
	runCommand(t, source, "git", "switch", "-qc", "other")
	if err := os.WriteFile(filepath.Join(source, "other-only.txt"), []byte("other\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runCommand(t, source, "git", "add", "other-only.txt")
	runCommand(t, source, "git", "commit", "-qm", "other branch")

	configDir := filepath.Join(root, "config")
	if err := os.MkdirAll(filepath.Join(configDir, "templates"), 0o755); err != nil {
		t.Fatal(err)
	}
	template := fmt.Sprintf("version: 1\nname: policy\nrepositories:\n  - name: app\n    clone:\n      url: %s\n    default_branch: main\n", source)
	if err := os.WriteFile(filepath.Join(configDir, "templates", "policy.yaml"), []byte(template), 0o644); err != nil {
		t.Fatal(err)
	}
	socket := fmt.Sprintf("twt-test-%d", time.Now().UnixNano())
	t.Cleanup(func() { exec.Command("tmux", "-L", socket, "kill-server").Run() })
	options := cli.Options{ConfigDir: configDir, StateDir: filepath.Join(root, "state"), DataDir: filepath.Join(root, "data"), TmuxSocket: socket, Stdout: &bytes.Buffer{}, Stderr: &bytes.Buffer{}}
	command := cli.New(options)
	command.SetArgs(forceTextOutput([]string{"workspaces", "create", "from-main", "--template", "policy", "--no-open"}))
	if err := command.Execute(); err != nil {
		t.Fatalf("workspaces create returned an error: %v", err)
	}
	entries, _ := os.ReadDir(filepath.Join(root, "data", "projects"))
	checkout := filepath.Join(root, "data", "projects", entries[0].Name(), "app")
	if _, err := os.Stat(filepath.Join(checkout, "other-only.txt")); !os.IsNotExist(err) {
		t.Fatalf("checkout used the clone HEAD instead of default_branch main: %v", err)
	}
	caches, err := os.ReadDir(filepath.Join(root, "data", "caches"))
	if err != nil || len(caches) != 1 {
		t.Fatalf("read repository caches: %v, %v", caches, err)
	}
	cache := filepath.Join(root, "data", "caches", caches[0].Name())
	if err := exec.Command("git", "-C", cache, "show-ref", "--verify", "--quiet", "refs/remotes/origin/other").Run(); err == nil {
		t.Fatal("cache fetched a non-default branch from origin")
	}
}

func TestWorkspacesCreateRefusesConflictingSharedCacheRemote(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is not installed")
	}

	root := t.TempDir()
	source := filepath.Join(root, "source")
	firstMirror := filepath.Join(root, "first-mirror")
	secondMirror := filepath.Join(root, "second-mirror")
	initGitRepository(t, source)
	initGitRepository(t, firstMirror)
	initGitRepository(t, secondMirror)
	configDir := filepath.Join(root, "config")
	if err := os.MkdirAll(filepath.Join(configDir, "templates"), 0o755); err != nil {
		t.Fatal(err)
	}
	templatePath := filepath.Join(configDir, "templates", "policy.yaml")
	template := func(mirror string) string {
		return fmt.Sprintf("version: 1\nname: policy\nrepositories:\n  - name: app\n    clone:\n      url: %s\n    remotes:\n      mirror: %s\n", source, mirror)
	}
	if err := os.WriteFile(templatePath, []byte(template(firstMirror)), 0o644); err != nil {
		t.Fatal(err)
	}
	socket := fmt.Sprintf("twt-test-%d", time.Now().UnixNano())
	t.Cleanup(func() { exec.Command("tmux", "-L", socket, "kill-server").Run() })
	options := cli.Options{ConfigDir: configDir, StateDir: filepath.Join(root, "state"), DataDir: filepath.Join(root, "data"), TmuxSocket: socket}
	executeWithOptions(t, options, nil, "workspaces", "create", "first", "--template", "policy", "--no-open")
	if err := os.WriteFile(templatePath, []byte(template(secondMirror)), 0o644); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	options.Stdout, options.Stderr = &stdout, &stderr
	command := cli.New(options)
	command.SetArgs(forceTextOutput([]string{"workspaces", "create", "second", "--template", "policy", "--no-open"}))
	err := command.Execute()
	if err == nil || !strings.Contains(err.Error(), "cache remote") {
		t.Fatalf("conflicting cache remote error = %v", err)
	}
	entries, readErr := os.ReadDir(filepath.Join(root, "data", "projects"))
	if readErr != nil {
		t.Fatal(readErr)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), "second-") {
			if _, statErr := os.Stat(filepath.Join(root, "data", "projects", entry.Name(), "app")); !os.IsNotExist(statErr) {
				t.Fatalf("conflicting cache remote created a checkout: %v", statErr)
			}
		}
	}
}

func TestWorkspacesRemoveDeletesUnpublishedWorkWithoutForce(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is not installed")
	}
	t.Setenv("TMUX_PANE", "")

	root := t.TempDir()
	source := filepath.Join(root, "source")
	initGitRepository(t, source)
	configDir := filepath.Join(root, "config")
	if err := os.MkdirAll(filepath.Join(configDir, "templates"), 0o755); err != nil {
		t.Fatal(err)
	}
	template := fmt.Sprintf("version: 1\nname: policy\nrepositories:\n  - name: app\n    clone:\n      url: %s\n", source)
	if err := os.WriteFile(filepath.Join(configDir, "templates", "policy.yaml"), []byte(template), 0o644); err != nil {
		t.Fatal(err)
	}
	socket := fmt.Sprintf("twt-test-%d", time.Now().UnixNano())
	t.Cleanup(func() { exec.Command("tmux", "-L", socket, "kill-server").Run() })
	options := cli.Options{ConfigDir: configDir, StateDir: filepath.Join(root, "state"), DataDir: filepath.Join(root, "data"), TmuxSocket: socket}
	executeWithOptions(t, options, nil, "workspaces", "create", "unpublished", "--template", "policy", "--no-open")
	workspace, err := store.NewWorkspaceStore(options.StateDir).Find("unpublished")
	if err != nil {
		t.Fatal(err)
	}
	checkout := workspace.Repositories[0].Path
	runCommand(t, checkout, "git", "config", "user.name", "twt test")
	runCommand(t, checkout, "git", "config", "user.email", "test@example.com")
	if err := os.WriteFile(filepath.Join(checkout, "new-work.txt"), []byte("important\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runCommand(t, checkout, "git", "add", "new-work.txt")
	runCommand(t, checkout, "git", "commit", "-qm", "unpublished work")
	executeWithOptions(t, options, nil, "workspaces", "archive", "unpublished")

	// Removal never requires branch publication and never reads the remote:
	// the origin URL points to a missing repository.
	runCommand(t, "", "git", "-C", workspace.Repositories[0].CachePath, "remote", "set-url", "origin", filepath.Join(root, "missing.git"))
	plan := executeWithOptions(t, options, nil, "workspaces", "remove", "unpublished")
	if strings.Contains(plan, "Blocked:") || !strings.Contains(plan, "Run again with --apply") {
		t.Fatalf("unpublished removal plan = %q", plan)
	}

	// The plan migrates the origin fetch refspec into the bare cache, so a
	// push updates the origin tracking refs.
	refspecs := runCommand(t, "", "git", "-C", workspace.Repositories[0].CachePath, "config", "--get-all", "remote.origin.fetch")
	if !strings.Contains(refspecs, "+refs/heads/*:refs/remotes/origin/*") {
		t.Fatalf("cache origin fetch refspecs = %q", refspecs)
	}

	executeWithOptions(t, options, nil, "workspaces", "remove", "unpublished", "--apply")
	if _, err := store.NewWorkspaceStore(options.StateDir).Find("unpublished"); err == nil {
		t.Fatal("removal kept the Workspace record")
	}
	branches := runCommand(t, "", "git", "-C", workspace.Repositories[0].CachePath, "for-each-ref", "--format=%(refname)", "refs/heads/"+workspace.Repositories[0].Branch)
	if strings.TrimSpace(branches) != "" {
		t.Fatalf("removal kept the unpublished branch: %q", branches)
	}
}

func TestWorkspacesRemoveForceRemovesUnpublishedWork(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is not installed")
	}
	t.Setenv("TMUX_PANE", "")

	root := t.TempDir()
	source := filepath.Join(root, "source")
	initGitRepository(t, source)
	configDir := filepath.Join(root, "config")
	if err := os.MkdirAll(filepath.Join(configDir, "templates"), 0o755); err != nil {
		t.Fatal(err)
	}
	template := fmt.Sprintf("version: 1\nname: policy\nrepositories:\n  - name: app\n    clone:\n      url: %s\n", source)
	if err := os.WriteFile(filepath.Join(configDir, "templates", "policy.yaml"), []byte(template), 0o644); err != nil {
		t.Fatal(err)
	}
	socket := fmt.Sprintf("twt-test-%d", time.Now().UnixNano())
	t.Cleanup(func() { exec.Command("tmux", "-L", socket, "kill-server").Run() })
	options := cli.Options{ConfigDir: configDir, StateDir: filepath.Join(root, "state"), DataDir: filepath.Join(root, "data"), TmuxSocket: socket}
	executeWithOptions(t, options, nil, "workspaces", "create", "escape", "--template", "policy", "--no-open")
	workspace, err := store.NewWorkspaceStore(options.StateDir).Find("escape")
	if err != nil {
		t.Fatal(err)
	}
	checkout := workspace.Repositories[0].Path
	runCommand(t, checkout, "git", "config", "user.name", "twt test")
	runCommand(t, checkout, "git", "config", "user.email", "test@example.com")
	if err := os.WriteFile(filepath.Join(checkout, "throwaway.txt"), []byte("temporary\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runCommand(t, checkout, "git", "add", "throwaway.txt")
	runCommand(t, checkout, "git", "commit", "-qm", "throwaway work")
	executeWithOptions(t, options, nil, "workspaces", "archive", "escape")

	plan := executeWithOptions(t, options, nil, "workspaces", "remove", "escape", "--force")
	if strings.Contains(plan, "Blocked:") || !strings.Contains(plan, "Run again with --apply") {
		t.Fatalf("force plan = %q", plan)
	}
	executeWithOptions(t, options, nil, "workspaces", "remove", "escape", "--force", "--apply")
	if _, err := os.Stat(workspace.Root); err != nil {
		t.Fatalf("force removal deleted the prepared root: %v", err)
	}
	if output := executeWithOptions(t, options, nil, "workspaces", "list"); output != "" {
		t.Fatalf("workspaces list after force removal = %q", output)
	}
}

func TestWorkspacesArchiveAndRemoveWorkFromSetupFailed(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is not installed")
	}
	t.Setenv("TMUX_PANE", "")

	root := t.TempDir()
	source := filepath.Join(root, "source")
	initGitRepository(t, source)
	configDir := filepath.Join(root, "config")
	if err := os.MkdirAll(filepath.Join(configDir, "templates"), 0o755); err != nil {
		t.Fatal(err)
	}
	template := fmt.Sprintf("version: 1\nname: policy\nrepositories:\n  - name: app\n    clone:\n      url: %s\ninitialize:\n  working_directory: app\n  command: [\"false\"]\n", source)
	if err := os.WriteFile(filepath.Join(configDir, "templates", "policy.yaml"), []byte(template), 0o644); err != nil {
		t.Fatal(err)
	}
	socket := fmt.Sprintf("twt-test-%d", time.Now().UnixNano())
	t.Cleanup(func() { exec.Command("tmux", "-L", socket, "kill-server").Run() })
	options := cli.Options{ConfigDir: configDir, StateDir: filepath.Join(root, "state"), DataDir: filepath.Join(root, "data"), TmuxSocket: socket, Stdout: &bytes.Buffer{}, Stderr: &bytes.Buffer{}}

	for _, name := range []string{"fail-archive", "fail-remove"} {
		command := cli.New(options)
		command.SetArgs(forceTextOutput([]string{"workspaces", "create", name, "--template", "policy", "--no-open"}))
		if err := command.Execute(); err == nil {
			t.Fatalf("create %q did not fail", name)
		}
		workspace, err := store.NewWorkspaceStore(options.StateDir).Find(name)
		if err != nil {
			t.Fatal(err)
		}
		if workspace.Status != domain.WorkspaceSetupFailed {
			t.Fatalf("Workspace %q status = %q, want %q", name, workspace.Status, domain.WorkspaceSetupFailed)
		}
	}

	// Archive works from setup_failed.
	output := executeWithOptions(t, options, nil, "workspaces", "archive", "fail-archive")
	if !strings.Contains(output, "Archived Workspace \"fail-archive\"") {
		t.Fatalf("archive from setup_failed output = %q", output)
	}
	archived, err := store.NewWorkspaceStore(options.StateDir).Find("fail-archive")
	if err != nil || archived.Status != domain.WorkspaceArchived {
		t.Fatalf("archive from setup_failed: status=%q error=%v", archived.Status, err)
	}

	// Removal works directly from setup_failed, also for a partial root
	// with a missing checkout and branch.
	failRemove, err := store.NewWorkspaceStore(options.StateDir).Find("fail-remove")
	if err != nil {
		t.Fatal(err)
	}
	repository := failRemove.Repositories[0]
	runCommand(t, "", "git", "-C", repository.CachePath, "worktree", "remove", "--force", repository.Path)
	runCommand(t, "", "git", "-C", repository.CachePath, "branch", "-D", repository.Branch)
	plan := executeWithOptions(t, options, nil, "workspaces", "remove", "fail-remove")
	if strings.Contains(plan, "not archived") || !strings.Contains(plan, "Run again with --apply") {
		t.Fatalf("setup_failed removal plan = %q", plan)
	}
	executeWithOptions(t, options, nil, "workspaces", "remove", "fail-remove", "--apply")
	if _, err := os.Stat(failRemove.Root); !os.IsNotExist(err) {
		t.Fatalf("setup_failed removal kept the Workspace root: %v", err)
	}
}

func TestWorkspacesRemoveProtectsTheDefaultBranchPin(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is not installed")
	}
	t.Setenv("TMUX_PANE", "")

	root := t.TempDir()
	source := filepath.Join(root, "source")
	initGitRepository(t, source)
	configDir := filepath.Join(root, "config")
	if err := os.MkdirAll(filepath.Join(configDir, "templates"), 0o755); err != nil {
		t.Fatal(err)
	}
	template := fmt.Sprintf("version: 1\nname: policy\nrepositories:\n  - name: app\n    clone:\n      url: %s\n    default_branch: main\n", source)
	if err := os.WriteFile(filepath.Join(configDir, "templates", "policy.yaml"), []byte(template), 0o644); err != nil {
		t.Fatal(err)
	}
	socket := fmt.Sprintf("twt-test-%d", time.Now().UnixNano())
	t.Cleanup(func() { exec.Command("tmux", "-L", socket, "kill-server").Run() })
	stateDir := filepath.Join(root, "state")
	options := cli.Options{ConfigDir: configDir, StateDir: stateDir, DataDir: filepath.Join(root, "data"), TmuxSocket: socket}
	executeWithOptions(t, options, nil, "workspaces", "create", "pinned", "--template", "policy", "--no-open")
	executeWithOptions(t, options, nil, "workspaces", "archive", "pinned")
	stateEntries, _ := os.ReadDir(filepath.Join(stateDir, "projects"))
	statePath := filepath.Join(stateDir, "projects", stateEntries[0].Name())
	data, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatal(err)
	}
	var workspace domain.Workspace
	if err := json.Unmarshal(data, &workspace); err != nil {
		t.Fatal(err)
	}
	cachePath := workspace.Repositories[0].CachePath
	workspace.Repositories[0].Branch = "main"
	changed, _ := json.Marshal(workspace)
	if err := os.WriteFile(statePath, changed, 0o600); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	options.Stdout, options.Stderr = &stdout, &stderr
	command := cli.New(options)
	command.SetArgs(forceTextOutput([]string{"workspaces", "remove", "pinned", "--apply"}))
	err = command.Execute()
	if err == nil || !strings.Contains(err.Error(), "default branch") {
		t.Fatalf("default-branch removal error = %v", err)
	}
	if err := exec.Command("git", "-C", cachePath, "show-ref", "--verify", "--quiet", "refs/heads/main").Run(); err != nil {
		t.Fatal("default-branch removal deleted the cache default branch")
	}
}

func TestWorkspacesCreateRefreshesReusedRepositoryCache(t *testing.T) {
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
	template := fmt.Sprintf("version: 1\nname: policy\nrepositories:\n  - name: app\n    clone:\n      url: %s\n", source)
	if err := os.WriteFile(filepath.Join(configDir, "templates", "policy.yaml"), []byte(template), 0o644); err != nil {
		t.Fatal(err)
	}
	socket := fmt.Sprintf("twt-test-%d", time.Now().UnixNano())
	t.Cleanup(func() { exec.Command("tmux", "-L", socket, "kill-server").Run() })
	options := cli.Options{ConfigDir: configDir, StateDir: filepath.Join(root, "state"), DataDir: filepath.Join(root, "data"), TmuxSocket: socket}
	executeWithOptions(t, options, nil, "workspaces", "create", "first", "--template", "policy", "--no-open")
	if err := os.WriteFile(filepath.Join(source, "new-source-commit.txt"), []byte("new\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runCommand(t, source, "git", "add", "new-source-commit.txt")
	runCommand(t, source, "git", "commit", "-qm", "new source commit")
	executeWithOptions(t, options, nil, "workspaces", "create", "second", "--template", "policy", "--no-open")
	second, err := store.NewWorkspaceStore(options.StateDir).Find("second")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(second.Repositories[0].Path, "new-source-commit.txt")); err != nil {
		t.Fatalf("second Workspace did not use the refreshed cache: %v", err)
	}
}

func TestRenamedWorkspaceSessionRemainsTheImmutableTmuxTarget(t *testing.T) {
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
	template := fmt.Sprintf("version: 1\nname: policy\nrepositories:\n  - name: app\n    clone:\n      url: %s\n", source)
	if err := os.WriteFile(filepath.Join(configDir, "templates", "policy.yaml"), []byte(template), 0o644); err != nil {
		t.Fatal(err)
	}
	socket := fmt.Sprintf("twt-test-%d", time.Now().UnixNano())
	t.Cleanup(func() { exec.Command("tmux", "-L", socket, "kill-server").Run() })
	options := cli.Options{ConfigDir: configDir, StateDir: filepath.Join(root, "state"), DataDir: filepath.Join(root, "data"), TmuxSocket: socket}
	executeWithOptions(t, options, nil, "workspaces", "create", "stable", "--template", "policy", "--no-open")
	runCommand(t, "", "tmux", "-L", socket, "rename-session", "-t", "policy-stable", "display-name")
	openOutput := executeWithOptions(t, options, nil, "workspaces", "open", "stable", "--output", "json")
	if !strings.Contains(openOutput, `"status":"applied"`) {
		t.Fatalf("JSON open output = %q", openOutput)
	}
	if sessions := runCommand(t, "", "tmux", "-L", socket, "list-sessions", "-F", "#{session_name}"); sessions != "display-name" {
		t.Fatalf("workspaces open created or renamed a session: %q", sessions)
	}

	registration := executeWithOptions(t, options, nil, "agents", "register", "--workspace", "stable", "--provider", "command", "--label", "sleeper", "--", "sleep", "60")
	fields := strings.Fields(registration)
	if len(fields) < 4 {
		t.Fatalf("registration output = %q", registration)
	}
	executeWithOptions(t, options, nil, "agents", "resume", fields[3], "--output", "json")
	windows := runCommand(t, "", "tmux", "-L", socket, "list-windows", "-t", "display-name", "-F", "#{window_name}")
	if windows != "app\nsleeper" {
		t.Fatalf("Agent Session did not resume in the renamed Workspace session: %q", windows)
	}
}

func TestWorkspacesRenameRenamesTheTmuxSession(t *testing.T) {
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
	template := fmt.Sprintf("version: 1\nname: policy\nrepositories:\n  - name: app\n    clone:\n      url: %s\n", source)
	if err := os.WriteFile(filepath.Join(configDir, "templates", "policy.yaml"), []byte(template), 0o644); err != nil {
		t.Fatal(err)
	}
	socket := fmt.Sprintf("twt-test-%d", time.Now().UnixNano())
	t.Cleanup(func() { exec.Command("tmux", "-L", socket, "kill-server").Run() })
	options := cli.Options{ConfigDir: configDir, StateDir: filepath.Join(root, "state"), DataDir: filepath.Join(root, "data"), TmuxSocket: socket}
	executeWithOptions(t, options, nil, "workspaces", "create", "stable", "--template", "policy", "--no-open")

	output := executeWithOptions(t, options, nil, "workspaces", "rename", "stable", "renamed")
	if output != "Renamed Workspace \"stable\" to \"renamed\"\n" {
		t.Fatalf("workspaces rename output = %q", output)
	}
	if sessions := runCommand(t, "", "tmux", "-L", socket, "list-sessions", "-F", "#{session_name}"); sessions != "policy-renamed" {
		t.Fatalf("tmux session after rename = %q", sessions)
	}
	got, err := store.NewWorkspaceStore(options.StateDir).Find("renamed")
	if err != nil || got.Name != "renamed" || got.TmuxSession != "policy-renamed" {
		t.Fatalf("renamed Workspace = %+v, %v", got, err)
	}

	runCommand(t, "", "tmux", "-L", socket, "new-session", "-d", "-s", "policy-taken")
	command := cli.New(options)
	command.SetArgs(forceTextOutput([]string{"workspaces", "rename", "renamed", "taken"}))
	if err := command.Execute(); err == nil || !strings.Contains(err.Error(), "policy-taken") {
		t.Fatalf("rename onto a taken tmux session = %v", err)
	}
	still, err := store.NewWorkspaceStore(options.StateDir).Find("renamed")
	if err != nil || still.Name != "renamed" || still.TmuxSession != "policy-renamed" {
		t.Fatalf("rejected rename changed Workspace = %+v, %v", still, err)
	}
}

func TestWorkspacesRemoveRejectsChangedStatePaths(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is not installed")
	}
	t.Setenv("TMUX_PANE", "")

	root := t.TempDir()
	source := filepath.Join(root, "source")
	initGitRepository(t, source)
	configDir := filepath.Join(root, "config")
	if err := os.MkdirAll(filepath.Join(configDir, "templates"), 0o755); err != nil {
		t.Fatal(err)
	}
	template := fmt.Sprintf("version: 1\nname: policy\nrepositories:\n  - name: app\n    clone:\n      url: %s\n", source)
	if err := os.WriteFile(filepath.Join(configDir, "templates", "policy.yaml"), []byte(template), 0o644); err != nil {
		t.Fatal(err)
	}
	socket := fmt.Sprintf("twt-test-%d", time.Now().UnixNano())
	t.Cleanup(func() { exec.Command("tmux", "-L", socket, "kill-server").Run() })
	stateDir := filepath.Join(root, "state")
	options := cli.Options{ConfigDir: configDir, StateDir: stateDir, DataDir: filepath.Join(root, "data"), TmuxSocket: socket}
	executeWithOptions(t, options, nil, "workspaces", "create", "tampered", "--template", "policy", "--no-open")
	executeWithOptions(t, options, nil, "workspaces", "archive", "tampered")
	stateEntries, _ := os.ReadDir(filepath.Join(stateDir, "projects"))
	statePath := filepath.Join(stateDir, "projects", stateEntries[0].Name())
	data, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatal(err)
	}
	var workspace domain.Workspace
	if err := json.Unmarshal(data, &workspace); err != nil {
		t.Fatal(err)
	}
	protected := filepath.Join(root, "must-not-change")
	if err := os.WriteFile(protected, []byte("protected"), 0o644); err != nil {
		t.Fatal(err)
	}
	workspace.Repositories[0].Path = protected
	changed, _ := json.Marshal(workspace)
	if err := os.WriteFile(statePath, changed, 0o600); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	options.Stdout, options.Stderr = &stdout, &stderr
	command := cli.New(options)
	command.SetArgs(forceTextOutput([]string{"workspaces", "remove", "tampered", "--apply"}))
	err = command.Execute()
	if err == nil || !strings.Contains(err.Error(), "invalid logical state") {
		t.Fatalf("changed state removal error = %v", err)
	}
	if data, err := os.ReadFile(protected); err != nil || string(data) != "protected" {
		t.Fatalf("changed state path was modified: data=%q error=%v", data, err)
	}
}

func TestWorkspacesRemoveRetriesAfterPartialDataRemoval(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is not installed")
	}
	t.Setenv("TMUX_PANE", "")

	root := t.TempDir()
	source := filepath.Join(root, "source")
	initGitRepository(t, source)
	configDir := filepath.Join(root, "config")
	if err := os.MkdirAll(filepath.Join(configDir, "templates"), 0o755); err != nil {
		t.Fatal(err)
	}
	template := fmt.Sprintf("version: 1\nname: policy\nrepositories:\n  - name: app\n    clone:\n      url: %s\n", source)
	if err := os.WriteFile(filepath.Join(configDir, "templates", "policy.yaml"), []byte(template), 0o644); err != nil {
		t.Fatal(err)
	}
	socket := fmt.Sprintf("twt-test-%d", time.Now().UnixNano())
	t.Cleanup(func() { exec.Command("tmux", "-L", socket, "kill-server").Run() })
	stateDir := filepath.Join(root, "state")
	options := cli.Options{ConfigDir: configDir, StateDir: stateDir, DataDir: filepath.Join(root, "data"), TmuxSocket: socket}
	executeWithOptions(t, options, nil, "workspaces", "create", "partial", "--template", "policy", "--no-open")
	stateEntries, _ := os.ReadDir(filepath.Join(stateDir, "projects"))
	statePath := filepath.Join(stateDir, "projects", stateEntries[0].Name())
	data, _ := os.ReadFile(statePath)
	var workspace domain.Workspace
	if err := json.Unmarshal(data, &workspace); err != nil {
		t.Fatal(err)
	}
	repository := workspace.Repositories[0]
	runCommand(t, "", "git", "-C", repository.CachePath, "worktree", "remove", repository.Path)
	runCommand(t, "", "git", "-C", repository.CachePath, "branch", "-D", repository.Branch)
	if err := os.Remove(filepath.Join(workspace.Root, ".twt-owned.json")); err != nil {
		t.Fatal(err)
	}
	workspace.Status = domain.WorkspaceRemoving
	changed, _ := json.Marshal(workspace)
	if err := os.WriteFile(statePath, changed, 0o600); err != nil {
		t.Fatal(err)
	}
	if workspace.EnvironmentID == "" {
		t.Fatal("created Workspace has no Prepared Environment ID")
	}
	if err := store.NewEnvironmentStore(stateDir).Delete(workspace.EnvironmentID); err != nil {
		t.Fatal(err)
	}

	executeWithOptions(t, options, nil, "workspaces", "remove", "partial", "--apply")
	if _, err := os.Stat(workspace.Root); !os.IsNotExist(err) {
		t.Fatalf("partial Workspace root still exists: %v", err)
	}
	if output := executeWithOptions(t, options, nil, "workspaces", "list"); output != "" {
		t.Fatalf("partial Workspace state still exists: %s", output)
	}
}

func TestWorkspacesRemoveNeedsNoRemoteForABranchWithoutWork(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is not installed")
	}
	t.Setenv("TMUX_PANE", "")

	root := t.TempDir()
	source := filepath.Join(root, "source")
	initGitRepository(t, source)
	configDir := filepath.Join(root, "config")
	if err := os.MkdirAll(filepath.Join(configDir, "templates"), 0o755); err != nil {
		t.Fatal(err)
	}
	template := fmt.Sprintf("version: 1\nname: policy\nrepositories:\n  - name: app\n    clone:\n      url: %s\n", source)
	if err := os.WriteFile(filepath.Join(configDir, "templates", "policy.yaml"), []byte(template), 0o644); err != nil {
		t.Fatal(err)
	}
	socket := fmt.Sprintf("twt-test-%d", time.Now().UnixNano())
	t.Cleanup(func() { exec.Command("tmux", "-L", socket, "kill-server").Run() })
	options := cli.Options{ConfigDir: configDir, StateDir: filepath.Join(root, "state"), DataDir: filepath.Join(root, "data"), TmuxSocket: socket}
	executeWithOptions(t, options, nil, "workspaces", "create", "idle", "--template", "policy", "--no-open")
	workspace, err := store.NewWorkspaceStore(options.StateDir).Find("idle")
	if err != nil {
		t.Fatal(err)
	}
	executeWithOptions(t, options, nil, "workspaces", "archive", "idle")

	// The branch has no commits after its recorded base. Removal must not
	// read the remote, so a missing origin cannot block it.
	runCommand(t, "", "git", "-C", workspace.Repositories[0].CachePath, "remote", "set-url", "origin", filepath.Join(root, "missing.git"))
	planJSON := executeWithOptions(t, options, nil, "workspaces", "remove", "idle", "--output", "json")
	var removal struct {
		Blockers []struct {
			Code string `json:"code"`
		} `json:"blockers"`
	}
	if err := json.Unmarshal([]byte(planJSON), &removal); err != nil {
		t.Fatalf("decode removal plan: %v", err)
	}
	if len(removal.Blockers) != 0 {
		t.Fatalf("expected no blockers for a branch without work, got %v", removal.Blockers)
	}
	executeWithOptions(t, options, nil, "workspaces", "remove", "idle", "--apply")
	if _, err := store.NewWorkspaceStore(options.StateDir).Find("idle"); err == nil {
		t.Fatal("expected the Workspace record to be removed")
	}
}
