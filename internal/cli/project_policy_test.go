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

func TestProjectsCreateUsesDeclaredDefaultBranch(t *testing.T) {
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
	socket := fmt.Sprintf("twt2-test-%d", time.Now().UnixNano())
	t.Cleanup(func() { exec.Command("tmux", "-L", socket, "kill-server").Run() })
	options := cli.Options{ConfigDir: configDir, StateDir: filepath.Join(root, "state"), DataDir: filepath.Join(root, "data"), TmuxSocket: socket, Stdout: &bytes.Buffer{}, Stderr: &bytes.Buffer{}}
	command := cli.New(options)
	command.SetArgs([]string{"projects", "create", "from-main", "--template", "policy", "--no-open"})
	if err := command.Execute(); err != nil {
		t.Fatalf("projects create returned an error: %v", err)
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

func TestProjectsCreateRefusesConflictingSharedCacheRemote(t *testing.T) {
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
	socket := fmt.Sprintf("twt2-test-%d", time.Now().UnixNano())
	t.Cleanup(func() { exec.Command("tmux", "-L", socket, "kill-server").Run() })
	options := cli.Options{ConfigDir: configDir, StateDir: filepath.Join(root, "state"), DataDir: filepath.Join(root, "data"), TmuxSocket: socket}
	executeWithOptions(t, options, nil, "projects", "create", "first", "--template", "policy", "--no-open")
	if err := os.WriteFile(templatePath, []byte(template(secondMirror)), 0o644); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	options.Stdout, options.Stderr = &stdout, &stderr
	command := cli.New(options)
	command.SetArgs([]string{"projects", "create", "second", "--template", "policy", "--no-open"})
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

func TestProjectsRemoveRefusesUnpublishedCommits(t *testing.T) {
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
	socket := fmt.Sprintf("twt2-test-%d", time.Now().UnixNano())
	t.Cleanup(func() { exec.Command("tmux", "-L", socket, "kill-server").Run() })
	options := cli.Options{ConfigDir: configDir, StateDir: filepath.Join(root, "state"), DataDir: filepath.Join(root, "data"), TmuxSocket: socket}
	executeWithOptions(t, options, nil, "projects", "create", "unpublished", "--template", "policy", "--no-open")
	entries, _ := os.ReadDir(filepath.Join(root, "data", "projects"))
	checkout := filepath.Join(root, "data", "projects", entries[0].Name(), "app")
	runCommand(t, checkout, "git", "config", "user.name", "twt2 test")
	runCommand(t, checkout, "git", "config", "user.email", "test@example.com")
	if err := os.WriteFile(filepath.Join(checkout, "new-work.txt"), []byte("important\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runCommand(t, checkout, "git", "add", "new-work.txt")
	runCommand(t, checkout, "git", "commit", "-qm", "unpublished work")
	executeWithOptions(t, options, nil, "projects", "archive", "unpublished")

	blockedPlan := executeWithOptions(t, options, nil, "projects", "remove", "unpublished")
	if !strings.Contains(blockedPlan, "Blocked:") || !strings.Contains(blockedPlan, "not on the remote") || !strings.Contains(blockedPlan, "--allow-unpublished") {
		t.Fatalf("unpublished removal plan = %q", blockedPlan)
	}

	var stdout, stderr bytes.Buffer
	applyOptions := options
	applyOptions.Stdout, applyOptions.Stderr = &stdout, &stderr
	command := cli.New(applyOptions)
	command.SetArgs([]string{"projects", "remove", "unpublished", "--apply"})
	err := command.Execute()
	if err == nil || !strings.Contains(err.Error(), "not on the remote") {
		t.Fatalf("unpublished removal error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(checkout, "new-work.txt")); err != nil {
		t.Fatalf("unpublished removal changed the checkout: %v", err)
	}

	// The plan migrates the origin fetch refspec into the bare cache, so a
	// push updates the origin tracking refs.
	caches, err := os.ReadDir(filepath.Join(root, "data", "caches"))
	if err != nil || len(caches) != 1 {
		t.Fatalf("read repository caches: %v, %v", caches, err)
	}
	cache := filepath.Join(root, "data", "caches", caches[0].Name())
	refspecs := runCommand(t, "", "git", "-C", cache, "config", "--get-all", "remote.origin.fetch")
	if !strings.Contains(refspecs, "+refs/heads/*:refs/remotes/origin/*") {
		t.Fatalf("cache origin fetch refspecs = %q", refspecs)
	}

	// After publication the plan is clean.
	branch := runCommand(t, checkout, "git", "branch", "--show-current")
	runCommand(t, checkout, "git", "push", "-q", "origin", branch)
	cleanPlan := executeWithOptions(t, options, nil, "projects", "remove", "unpublished")
	if strings.Contains(cleanPlan, "Blocked:") || !strings.Contains(cleanPlan, "Run again with --apply") {
		t.Fatalf("published removal plan = %q", cleanPlan)
	}
	executeWithOptions(t, options, nil, "projects", "remove", "unpublished", "--apply")
	if _, err := os.Stat(filepath.Join(root, "data", "projects")); err == nil {
		entries, readErr := os.ReadDir(filepath.Join(root, "data", "projects"))
		if readErr != nil || len(entries) != 0 {
			t.Fatalf("published removal kept Project data: entries=%v error=%v", entries, readErr)
		}
	}
}

func TestProjectsRemoveReportsUnknownWhenTheRemoteIsUnreachable(t *testing.T) {
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
	socket := fmt.Sprintf("twt2-test-%d", time.Now().UnixNano())
	t.Cleanup(func() { exec.Command("tmux", "-L", socket, "kill-server").Run() })
	options := cli.Options{ConfigDir: configDir, StateDir: filepath.Join(root, "state"), DataDir: filepath.Join(root, "data"), TmuxSocket: socket}
	executeWithOptions(t, options, nil, "projects", "create", "offline", "--template", "policy", "--no-open")
	project, err := store.NewProjectStore(options.StateDir).Find("offline")
	if err != nil {
		t.Fatal(err)
	}
	checkout := project.Repositories[0].Path
	runCommand(t, checkout, "git", "config", "user.name", "twt2 test")
	runCommand(t, checkout, "git", "config", "user.email", "test@example.com")
	if err := os.WriteFile(filepath.Join(checkout, "new-work.txt"), []byte("important\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runCommand(t, checkout, "git", "add", "new-work.txt")
	runCommand(t, checkout, "git", "commit", "-qm", "unpublished work")
	executeWithOptions(t, options, nil, "projects", "archive", "offline")

	// The plan cannot read the remote: the origin URL points to a missing
	// repository.
	runCommand(t, "", "git", "-C", project.Repositories[0].CachePath, "remote", "set-url", "origin", filepath.Join(root, "missing.git"))
	planJSON := executeWithOptions(t, options, nil, "projects", "remove", "offline", "--output", "json")
	var removal struct {
		Blockers []struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"blockers"`
	}
	if err := json.Unmarshal([]byte(planJSON), &removal); err != nil {
		t.Fatalf("decode removal plan JSON: %v\n%s", err, planJSON)
	}
	if len(removal.Blockers) != 1 || removal.Blockers[0].Code != "unpublished_unknown" || !strings.Contains(removal.Blockers[0].Message, "could not read the remote") {
		t.Fatalf("unreachable-remote removal blockers = %+v", removal.Blockers)
	}
	if _, err := os.Stat(filepath.Join(checkout, "new-work.txt")); err != nil {
		t.Fatalf("unreachable-remote plan changed the checkout: %v", err)
	}
}

func TestProjectsRemoveAllowUnpublishedRemovesUnpublishedWork(t *testing.T) {
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
	socket := fmt.Sprintf("twt2-test-%d", time.Now().UnixNano())
	t.Cleanup(func() { exec.Command("tmux", "-L", socket, "kill-server").Run() })
	options := cli.Options{ConfigDir: configDir, StateDir: filepath.Join(root, "state"), DataDir: filepath.Join(root, "data"), TmuxSocket: socket}
	executeWithOptions(t, options, nil, "projects", "create", "escape", "--template", "policy", "--no-open")
	project, err := store.NewProjectStore(options.StateDir).Find("escape")
	if err != nil {
		t.Fatal(err)
	}
	checkout := project.Repositories[0].Path
	runCommand(t, checkout, "git", "config", "user.name", "twt2 test")
	runCommand(t, checkout, "git", "config", "user.email", "test@example.com")
	if err := os.WriteFile(filepath.Join(checkout, "throwaway.txt"), []byte("temporary\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runCommand(t, checkout, "git", "add", "throwaway.txt")
	runCommand(t, checkout, "git", "commit", "-qm", "throwaway work")
	executeWithOptions(t, options, nil, "projects", "archive", "escape")

	plan := executeWithOptions(t, options, nil, "projects", "remove", "escape", "--allow-unpublished")
	if strings.Contains(plan, "Blocked:") || !strings.Contains(plan, "Run again with --apply") {
		t.Fatalf("allow-unpublished plan = %q", plan)
	}
	executeWithOptions(t, options, nil, "projects", "remove", "escape", "--allow-unpublished", "--apply")
	if _, err := os.Stat(project.Root); !os.IsNotExist(err) {
		t.Fatalf("allow-unpublished removal kept the Project root: %v", err)
	}
	if output := executeWithOptions(t, options, nil, "projects", "list"); output != "" {
		t.Fatalf("projects list after allow-unpublished removal = %q", output)
	}
}

func TestProjectsArchiveAndRemoveWorkFromSetupFailed(t *testing.T) {
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
	socket := fmt.Sprintf("twt2-test-%d", time.Now().UnixNano())
	t.Cleanup(func() { exec.Command("tmux", "-L", socket, "kill-server").Run() })
	options := cli.Options{ConfigDir: configDir, StateDir: filepath.Join(root, "state"), DataDir: filepath.Join(root, "data"), TmuxSocket: socket, Stdout: &bytes.Buffer{}, Stderr: &bytes.Buffer{}}

	for _, name := range []string{"fail-archive", "fail-remove"} {
		command := cli.New(options)
		command.SetArgs([]string{"projects", "create", name, "--template", "policy", "--no-open"})
		if err := command.Execute(); err == nil {
			t.Fatalf("create %q did not fail", name)
		}
		project, err := store.NewProjectStore(options.StateDir).Find(name)
		if err != nil {
			t.Fatal(err)
		}
		if project.Status != domain.ProjectSetupFailed {
			t.Fatalf("Project %q status = %q, want %q", name, project.Status, domain.ProjectSetupFailed)
		}
	}

	// Archive works from setup_failed.
	output := executeWithOptions(t, options, nil, "projects", "archive", "fail-archive")
	if !strings.Contains(output, "Archived Project \"fail-archive\"") {
		t.Fatalf("archive from setup_failed output = %q", output)
	}
	archived, err := store.NewProjectStore(options.StateDir).Find("fail-archive")
	if err != nil || archived.Status != domain.ProjectArchived {
		t.Fatalf("archive from setup_failed: status=%q error=%v", archived.Status, err)
	}

	// Removal works directly from setup_failed, also for a partial root
	// with a missing checkout and branch.
	failRemove, err := store.NewProjectStore(options.StateDir).Find("fail-remove")
	if err != nil {
		t.Fatal(err)
	}
	repository := failRemove.Repositories[0]
	runCommand(t, "", "git", "-C", repository.CachePath, "worktree", "remove", "--force", repository.Path)
	runCommand(t, "", "git", "-C", repository.CachePath, "branch", "-D", repository.Branch)
	plan := executeWithOptions(t, options, nil, "projects", "remove", "fail-remove")
	if strings.Contains(plan, "not archived") || !strings.Contains(plan, "Run again with --apply") {
		t.Fatalf("setup_failed removal plan = %q", plan)
	}
	executeWithOptions(t, options, nil, "projects", "remove", "fail-remove", "--apply")
	if _, err := os.Stat(failRemove.Root); !os.IsNotExist(err) {
		t.Fatalf("setup_failed removal kept the Project root: %v", err)
	}
}

func TestProjectsRemoveProtectsTheDefaultBranchPin(t *testing.T) {
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
	socket := fmt.Sprintf("twt2-test-%d", time.Now().UnixNano())
	t.Cleanup(func() { exec.Command("tmux", "-L", socket, "kill-server").Run() })
	stateDir := filepath.Join(root, "state")
	options := cli.Options{ConfigDir: configDir, StateDir: stateDir, DataDir: filepath.Join(root, "data"), TmuxSocket: socket}
	executeWithOptions(t, options, nil, "projects", "create", "pinned", "--template", "policy", "--no-open")
	executeWithOptions(t, options, nil, "projects", "archive", "pinned")
	stateEntries, _ := os.ReadDir(filepath.Join(stateDir, "projects"))
	statePath := filepath.Join(stateDir, "projects", stateEntries[0].Name())
	data, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatal(err)
	}
	var project domain.Project
	if err := json.Unmarshal(data, &project); err != nil {
		t.Fatal(err)
	}
	cachePath := project.Repositories[0].CachePath
	project.Repositories[0].Branch = "main"
	changed, _ := json.Marshal(project)
	if err := os.WriteFile(statePath, changed, 0o600); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	options.Stdout, options.Stderr = &stdout, &stderr
	command := cli.New(options)
	command.SetArgs([]string{"projects", "remove", "pinned", "--apply"})
	err = command.Execute()
	if err == nil || !strings.Contains(err.Error(), "default branch") {
		t.Fatalf("default-branch removal error = %v", err)
	}
	if err := exec.Command("git", "-C", cachePath, "show-ref", "--verify", "--quiet", "refs/heads/main").Run(); err != nil {
		t.Fatal("default-branch removal deleted the cache default branch")
	}
}

func TestProjectsCreateRefreshesReusedRepositoryCache(t *testing.T) {
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
	socket := fmt.Sprintf("twt2-test-%d", time.Now().UnixNano())
	t.Cleanup(func() { exec.Command("tmux", "-L", socket, "kill-server").Run() })
	options := cli.Options{ConfigDir: configDir, StateDir: filepath.Join(root, "state"), DataDir: filepath.Join(root, "data"), TmuxSocket: socket}
	executeWithOptions(t, options, nil, "projects", "create", "first", "--template", "policy", "--no-open")
	if err := os.WriteFile(filepath.Join(source, "new-source-commit.txt"), []byte("new\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runCommand(t, source, "git", "add", "new-source-commit.txt")
	runCommand(t, source, "git", "commit", "-qm", "new source commit")
	executeWithOptions(t, options, nil, "projects", "create", "second", "--template", "policy", "--no-open")
	second, err := store.NewProjectStore(options.StateDir).Find("second")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(second.Repositories[0].Path, "new-source-commit.txt")); err != nil {
		t.Fatalf("second Project did not use the refreshed cache: %v", err)
	}
}

func TestRenamedProjectSessionRemainsTheImmutableTmuxTarget(t *testing.T) {
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
	socket := fmt.Sprintf("twt2-test-%d", time.Now().UnixNano())
	t.Cleanup(func() { exec.Command("tmux", "-L", socket, "kill-server").Run() })
	options := cli.Options{ConfigDir: configDir, StateDir: filepath.Join(root, "state"), DataDir: filepath.Join(root, "data"), TmuxSocket: socket}
	executeWithOptions(t, options, nil, "projects", "create", "stable", "--template", "policy", "--no-open")
	runCommand(t, "", "tmux", "-L", socket, "rename-session", "-t", "stable", "display-name")
	openOutput := executeWithOptions(t, options, nil, "projects", "open", "stable", "--output", "json")
	if !strings.Contains(openOutput, `"status":"applied"`) {
		t.Fatalf("JSON open output = %q", openOutput)
	}
	if sessions := runCommand(t, "", "tmux", "-L", socket, "list-sessions", "-F", "#{session_name}"); sessions != "display-name" {
		t.Fatalf("projects open created or renamed a session: %q", sessions)
	}

	registration := executeWithOptions(t, options, nil, "agents", "register", "--project", "stable", "--provider", "command", "--label", "sleeper", "--", "sleep", "60")
	fields := strings.Fields(registration)
	if len(fields) < 4 {
		t.Fatalf("registration output = %q", registration)
	}
	executeWithOptions(t, options, nil, "agents", "resume", fields[3], "--output", "json")
	windows := runCommand(t, "", "tmux", "-L", socket, "list-windows", "-t", "display-name", "-F", "#{window_name}")
	if windows != "app\nsleeper" {
		t.Fatalf("Agent Session did not resume in the renamed Project session: %q", windows)
	}
}

func TestProjectsRemoveRejectsChangedStatePaths(t *testing.T) {
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
	socket := fmt.Sprintf("twt2-test-%d", time.Now().UnixNano())
	t.Cleanup(func() { exec.Command("tmux", "-L", socket, "kill-server").Run() })
	stateDir := filepath.Join(root, "state")
	options := cli.Options{ConfigDir: configDir, StateDir: stateDir, DataDir: filepath.Join(root, "data"), TmuxSocket: socket}
	executeWithOptions(t, options, nil, "projects", "create", "tampered", "--template", "policy", "--no-open")
	executeWithOptions(t, options, nil, "projects", "archive", "tampered")
	stateEntries, _ := os.ReadDir(filepath.Join(stateDir, "projects"))
	statePath := filepath.Join(stateDir, "projects", stateEntries[0].Name())
	data, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatal(err)
	}
	var project domain.Project
	if err := json.Unmarshal(data, &project); err != nil {
		t.Fatal(err)
	}
	protected := filepath.Join(root, "must-not-change")
	if err := os.WriteFile(protected, []byte("protected"), 0o644); err != nil {
		t.Fatal(err)
	}
	project.Repositories[0].Path = protected
	changed, _ := json.Marshal(project)
	if err := os.WriteFile(statePath, changed, 0o600); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	options.Stdout, options.Stderr = &stdout, &stderr
	command := cli.New(options)
	command.SetArgs([]string{"projects", "remove", "tampered", "--apply"})
	err = command.Execute()
	if err == nil || !strings.Contains(err.Error(), "outside its Project root") {
		t.Fatalf("changed state removal error = %v", err)
	}
	if data, err := os.ReadFile(protected); err != nil || string(data) != "protected" {
		t.Fatalf("changed state path was modified: data=%q error=%v", data, err)
	}
}

func TestProjectsRemoveRetriesAfterPartialDataRemoval(t *testing.T) {
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
	socket := fmt.Sprintf("twt2-test-%d", time.Now().UnixNano())
	t.Cleanup(func() { exec.Command("tmux", "-L", socket, "kill-server").Run() })
	stateDir := filepath.Join(root, "state")
	options := cli.Options{ConfigDir: configDir, StateDir: stateDir, DataDir: filepath.Join(root, "data"), TmuxSocket: socket}
	executeWithOptions(t, options, nil, "projects", "create", "partial", "--template", "policy", "--no-open")
	stateEntries, _ := os.ReadDir(filepath.Join(stateDir, "projects"))
	statePath := filepath.Join(stateDir, "projects", stateEntries[0].Name())
	data, _ := os.ReadFile(statePath)
	var project domain.Project
	if err := json.Unmarshal(data, &project); err != nil {
		t.Fatal(err)
	}
	repository := project.Repositories[0]
	runCommand(t, "", "git", "-C", repository.CachePath, "worktree", "remove", repository.Path)
	runCommand(t, "", "git", "-C", repository.CachePath, "branch", "-D", repository.Branch)
	if err := os.Remove(filepath.Join(project.Root, ".twt2-owned.json")); err != nil {
		t.Fatal(err)
	}
	project.Status = domain.ProjectRemoving
	changed, _ := json.Marshal(project)
	if err := os.WriteFile(statePath, changed, 0o600); err != nil {
		t.Fatal(err)
	}
	if project.EnvironmentID == "" {
		t.Fatal("created Project has no Prepared Environment ID")
	}
	if err := store.NewEnvironmentStore(stateDir).Delete(project.EnvironmentID); err != nil {
		t.Fatal(err)
	}

	executeWithOptions(t, options, nil, "projects", "remove", "partial", "--apply")
	if _, err := os.Stat(project.Root); !os.IsNotExist(err) {
		t.Fatalf("partial Project root still exists: %v", err)
	}
	if output := executeWithOptions(t, options, nil, "projects", "list"); output != "" {
		t.Fatalf("partial Project state still exists: %s", output)
	}
}
