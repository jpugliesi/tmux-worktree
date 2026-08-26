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
	"github.com/jpugliesi/tmux-worktree/internal/clierr"
	"github.com/jpugliesi/tmux-worktree/internal/domain"
	"github.com/jpugliesi/tmux-worktree/internal/store"
)

func TestPreparedEnvironmentRunsRepositoryInitializationBeforeWorkspaceClaim(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is not installed")
	}

	root := t.TempDir()
	binary := filepath.Join(root, "twt")
	runCommand(t, filepath.Join("..", ".."), "go", "build", "-o", binary, "./cmd/twt")
	source := filepath.Join(root, "source")
	initGitRepository(t, source)
	initLog := filepath.Join(root, "repository-init.log")
	t.Setenv("TWT_TEST_INIT_LOG", initLog)
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
    initialize:
      command: ["sh", "-c", "sleep 1.2; printf 'initialized\\n' >> \"$TWT_TEST_INIT_LOG\""]
`, source)
	templatePath := filepath.Join(configDir, "templates", "example.yaml")
	if err := os.WriteFile(templatePath, []byte(template), 0o644); err != nil {
		t.Fatal(err)
	}
	socket := fmt.Sprintf("twt-test-%d", time.Now().UnixNano())
	t.Cleanup(func() { _ = exec.Command("tmux", "-L", socket, "kill-server").Run() })
	options := cli.Options{
		ConfigDir:             configDir,
		StateDir:              filepath.Join(root, "state"),
		DataDir:               filepath.Join(root, "data"),
		TmuxSocket:            socket,
		PreparationExecutable: binary,
	}

	prepareOutput := executeWithOptions(t, options, nil, "templates", "prepare", "example")
	if !strings.Contains(prepareOutput, "Prepared Environment") || !strings.Contains(prepareOutput, "example") {
		t.Fatalf("templates prepare output = %q", prepareOutput)
	}
	assertFileLines(t, initLog, []string{"initialized"})
	workspaces, err := store.NewWorkspaceStore(options.StateDir).List()
	if err != nil {
		t.Fatal(err)
	}
	if len(workspaces) != 0 {
		t.Fatalf("templates prepare created Workspaces: %+v", workspaces)
	}

	executeWithOptions(t, options, nil, "workspaces", "create", "first", "--template", "example", "--no-open")
	// No immediate init-log assert here: the claim triggers the background
	// refill, whose own init can land at any moment. The stable wait below
	// carries the invariant (the claim itself never reruns init: the total
	// stays at two).
	workspace, err := store.NewWorkspaceStore(options.StateDir).Find("first")
	if err != nil {
		t.Fatal(err)
	}
	if workspace.EnvironmentID == "" {
		t.Fatalf("Workspace %q has no claimed Prepared Environment", workspace.Name)
	}
	branch := runCommand(t, workspace.Repositories[0].Path, "git", "branch", "--show-current")
	if branch != workspace.Repositories[0].Branch {
		t.Fatalf("claimed checkout branch = %q, want %q", branch, workspace.Repositories[0].Branch)
	}
	waitFor(t, 4*time.Second, func() bool {
		data, err := os.ReadFile(initLog)
		if err != nil || len(strings.Fields(string(data))) != 2 {
			return false
		}
		environments, err := store.NewEnvironmentStore(options.StateDir).List()
		if err != nil {
			return false
		}
		for _, environment := range environments {
			if environment.Status == domain.EnvironmentReady {
				return true
			}
		}
		return false
	}, "background refill did not prepare the next environment")
	assertFileLines(t, initLog, []string{"initialized", "initialized"})
	environments, err := store.NewEnvironmentStore(options.StateDir).List()
	if err != nil {
		t.Fatal(err)
	}
	ready, claimed := 0, 0
	for _, environment := range environments {
		switch environment.Status {
		case domain.EnvironmentReady:
			ready++
		case domain.EnvironmentClaimed:
			claimed++
		}
	}
	if ready != 1 || claimed != 1 {
		t.Fatalf("Prepared Environment pool has ready=%d claimed=%d, want 1 and 1", ready, claimed)
	}
	storageOutput := executeWithOptions(t, options, nil, "storage", "show")
	if !strings.Contains(storageOutput, "Prepared") || !strings.Contains(storageOutput, "1 ready") {
		t.Fatalf("storage show does not show the ready Prepared Environment:\n%s", storageOutput)
	}
	changedTemplate := strings.Replace(template, "    clone:\n", "    clone:\n      depth: 1\n", 1)
	if err := os.WriteFile(templatePath, []byte(changedTemplate), 0o644); err != nil {
		t.Fatal(err)
	}
	cleanupPlan := executeWithOptions(t, options, nil, "storage", "clean")
	if !strings.Contains(cleanupPlan, "obsolete Prepared Environment") || !strings.Contains(cleanupPlan, "Run again with --apply") {
		t.Fatalf("prepared cleanup plan = %q", cleanupPlan)
	}
	executeWithOptions(t, options, nil, "storage", "clean", "--apply")
	environments, err = store.NewEnvironmentStore(options.StateDir).List()
	if err != nil {
		t.Fatal(err)
	}
	if len(environments) != 1 || environments[0].Status != domain.EnvironmentClaimed || environments[0].ID != workspace.EnvironmentID {
		t.Fatalf("cleanup changed the claimed Workspace Environment: %+v", environments)
	}
	if _, err := os.Stat(workspace.Repositories[0].Path); err != nil {
		t.Fatalf("cleanup removed the active Workspace checkout: %v", err)
	}
}

func TestPreparedEnvironmentClaimSurvivesAWindowNameEdit(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is not installed")
	}

	root := t.TempDir()
	binary := filepath.Join(root, "twt")
	runCommand(t, filepath.Join("..", ".."), "go", "build", "-o", binary, "./cmd/twt")
	source := filepath.Join(root, "source")
	initGitRepository(t, source)
	initLog := filepath.Join(root, "repository-init.log")
	t.Setenv("TWT_TEST_INIT_LOG", initLog)
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
    initialize:
      command: ["sh", "-c", "sleep 1.2; printf 'initialized\\n' >> \"$TWT_TEST_INIT_LOG\""]
`, source)
	templatePath := filepath.Join(configDir, "templates", "example.yaml")
	if err := os.WriteFile(templatePath, []byte(template), 0o644); err != nil {
		t.Fatal(err)
	}
	socket := fmt.Sprintf("twt-test-%d", time.Now().UnixNano())
	t.Cleanup(func() { _ = exec.Command("tmux", "-L", socket, "kill-server").Run() })
	options := cli.Options{
		ConfigDir:             configDir,
		StateDir:              filepath.Join(root, "state"),
		DataDir:               filepath.Join(root, "data"),
		TmuxSocket:            socket,
		PreparationExecutable: binary,
	}

	executeWithOptions(t, options, nil, "templates", "prepare", "example")
	assertFileLines(t, initLog, []string{"initialized"})

	// A window name does not change the physical worktrees, so the ready
	// Prepared Environment stays usable.
	changedTemplate := strings.Replace(template, "  - name: app\n", "  - name: app\n    window_name: changed\n", 1)
	if err := os.WriteFile(templatePath, []byte(changedTemplate), 0o644); err != nil {
		t.Fatal(err)
	}

	executeWithOptions(t, options, nil, "workspaces", "create", "first", "--template", "example", "--no-open")
	// No immediate init-log assert here: the claim triggers the background
	// refill, whose own init can land at any moment. The stable wait below
	// carries the invariant (the claim itself never reruns init: the total
	// stays at two).
	workspace, err := store.NewWorkspaceStore(options.StateDir).Find("first")
	if err != nil {
		t.Fatal(err)
	}
	if workspace.EnvironmentID == "" {
		t.Fatalf("Workspace %q has no claimed Prepared Environment", workspace.Name)
	}
	waitFor(t, 6*time.Second, func() bool {
		data, err := os.ReadFile(initLog)
		if err != nil || len(strings.Fields(string(data))) != 2 {
			return false
		}
		environments, err := store.NewEnvironmentStore(options.StateDir).List()
		if err != nil {
			return false
		}
		for _, environment := range environments {
			if environment.Status == domain.EnvironmentReady {
				return true
			}
		}
		return false
	}, "background refill did not prepare the next environment")
}

func TestWorkspacesCreateRefreshesTheBaseBranchAndPath(t *testing.T) {
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
	t.Cleanup(func() { _ = exec.Command("tmux", "-L", socket, "kill-server").Run() })
	options := cli.Options{ConfigDir: configDir, StateDir: filepath.Join(root, "state"), DataDir: filepath.Join(root, "data"), TmuxSocket: socket}

	executeWithOptions(t, options, nil, "templates", "prepare", "example")
	newTip := addOriginCommit(t, source, "second.txt")

	stdout, stderr, err := executeCollectingOutput(t, options, "workspaces", "create", "fresh", "--branch", "feature/custom", "--no-open")
	if err != nil {
		t.Fatalf("workspaces create with a stale environment: %v\n%s", err, stderr)
	}
	if !strings.Contains(stdout, "Root: ") {
		t.Fatalf("create output has no Root line: %q", stdout)
	}
	if !strings.Contains(stderr, "Base: origin/main @ ") {
		t.Fatalf("create progress has no Base line: %q", stderr)
	}
	fresh, err := store.NewWorkspaceStore(options.StateDir).Find("fresh")
	if err != nil {
		t.Fatal(err)
	}
	head := runCommand(t, fresh.Repositories[0].Path, "git", "rev-parse", "HEAD")
	if head != newTip {
		t.Fatalf("stale claim landed on %s, want the new origin tip %s", head, newTip)
	}
	branch := runCommand(t, fresh.Repositories[0].Path, "git", "branch", "--show-current")
	if branch != "feature/custom" || fresh.Repositories[0].Branch != "feature/custom" {
		t.Fatalf("custom branch = %q (record %q), want feature/custom", branch, fresh.Repositories[0].Branch)
	}

	rootPath := executeWithOptions(t, options, nil, "workspaces", "path", "fresh")
	if rootPath != fresh.Root+"\n" {
		t.Fatalf("workspaces path = %q, want %q", rootPath, fresh.Root+"\n")
	}
	repositoryPath := executeWithOptions(t, options, nil, "workspaces", "path", "fresh", "app")
	if repositoryPath != fresh.Repositories[0].Path+"\n" {
		t.Fatalf("workspaces path with repository = %q, want %q", repositoryPath, fresh.Repositories[0].Path+"\n")
	}
	if _, _, err := executeCollectingOutput(t, options, "workspaces", "path", "fresh", "missing"); err == nil || !strings.Contains(err.Error(), "is not in Workspace") {
		t.Fatalf("workspaces path with an unknown repository = %v", err)
	}
	showOutput := executeWithOptions(t, options, nil, "workspaces", "show", "fresh")
	if !strings.Contains(showOutput, "Root") || !strings.Contains(showOutput, fresh.Root) {
		t.Fatalf("workspaces show has no Root line:\n%s", showOutput)
	}
	showJSON := executeWithOptions(t, options, nil, "workspaces", "show", "fresh", "--output", "json")
	if !strings.Contains(showJSON, `"root":`) {
		t.Fatalf("workspaces show JSON has no root field: %s", showJSON)
	}

	executeWithOptions(t, options, nil, "templates", "prepare", "example")
	addOriginCommit(t, source, "third.txt")
	if _, _, err := executeCollectingOutput(t, options, "workspaces", "create", "stale", "--no-fetch", "--no-open"); err != nil {
		t.Fatalf("workspaces create --no-fetch: %v", err)
	}
	stale, err := store.NewWorkspaceStore(options.StateDir).Find("stale")
	if err != nil {
		t.Fatal(err)
	}
	staleHead := runCommand(t, stale.Repositories[0].Path, "git", "rev-parse", "HEAD")
	if staleHead != newTip {
		t.Fatalf("--no-fetch claim landed on %s, want the old base %s", staleHead, newTip)
	}
	if stale.Repositories[0].Branch != "stale" {
		t.Fatalf("default branch = %q, want the Workspace name %q", stale.Repositories[0].Branch, "stale")
	}

	_, stderr, err = executeCollectingOutput(t, options, "workspaces", "create", "third", "--branch", "feature/custom", "--no-open")
	if err != nil {
		t.Fatalf("workspaces create with a branch collision: %v", err)
	}
	if !strings.Contains(stderr, `Branch "feature/custom" exists.`) {
		t.Fatalf("branch collision progress = %q", stderr)
	}
	third, err := store.NewWorkspaceStore(options.StateDir).Find("third")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(third.Repositories[0].Branch, "twt/third-") {
		t.Fatalf("collision fallback branch = %q", third.Repositories[0].Branch)
	}

	if _, _, err := executeCollectingOutput(t, options, "workspaces", "create", "fourth", "--branch", "main", "--no-open"); err == nil || !strings.Contains(err.Error(), "default branch") {
		t.Fatalf("workspaces create with the default branch name = %v", err)
	}

	// A Workspace whose name equals the repository default branch must fail at
	// create time, because its default Workspace branch is its name.
	if _, _, err := executeCollectingOutput(t, options, "workspaces", "create", "main", "--no-open"); err == nil || !strings.Contains(err.Error(), "default branch") {
		t.Fatalf("workspaces create named after the default branch = %v", err)
	}

	t.Setenv("TWT_BRANCH_PREFIX", "jpugliesi/")
	if _, _, err := executeCollectingOutput(t, options, "workspaces", "create", "prefixed", "--no-open"); err != nil {
		t.Fatalf("workspaces create with a branch prefix: %v", err)
	}
	prefixed, err := store.NewWorkspaceStore(options.StateDir).Find("prefixed")
	if err != nil {
		t.Fatal(err)
	}
	if prefixed.Repositories[0].Branch != "jpugliesi/prefixed" {
		t.Fatalf("prefixed branch = %q, want %q", prefixed.Repositories[0].Branch, "jpugliesi/prefixed")
	}
}

func TestPreparedEnvironmentPoolDepthTopUp(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is not installed")
	}

	root := t.TempDir()
	binary := filepath.Join(root, "twt")
	runCommand(t, filepath.Join("..", ".."), "go", "build", "-o", binary, "./cmd/twt")
	source := filepath.Join(root, "source")
	initGitRepository(t, source)
	configDir := filepath.Join(root, "config")
	if err := os.MkdirAll(filepath.Join(configDir, "templates"), 0o755); err != nil {
		t.Fatal(err)
	}
	template := fmt.Sprintf("version: 1\nname: example\npool_depth: 2\nrepositories:\n  - name: app\n    clone:\n      url: %s\n", source)
	if err := os.WriteFile(filepath.Join(configDir, "templates", "example.yaml"), []byte(template), 0o644); err != nil {
		t.Fatal(err)
	}
	socket := fmt.Sprintf("twt-test-%d", time.Now().UnixNano())
	t.Cleanup(func() { _ = exec.Command("tmux", "-L", socket, "kill-server").Run() })
	options := cli.Options{
		ConfigDir:             configDir,
		StateDir:              filepath.Join(root, "state"),
		DataDir:               filepath.Join(root, "data"),
		TmuxSocket:            socket,
		PreparationExecutable: binary,
	}

	prepareOutput := executeWithOptions(t, options, nil, "templates", "prepare", "example")
	if strings.Count(prepareOutput, "Prepared Environment ") != 2 {
		t.Fatalf("templates prepare with pool_depth 2 = %q", prepareOutput)
	}
	if got := countEnvironments(t, options.StateDir, domain.EnvironmentReady); got != 2 {
		t.Fatalf("ready Prepared Environments after prepare = %d, want 2", got)
	}
	fullJSON := executeWithOptions(t, options, nil, "templates", "prepare", "example", "--output", "json")
	if !strings.Contains(fullJSON, `"environments":[]`) {
		t.Fatalf("templates prepare JSON with a full pool = %s", fullJSON)
	}

	executeWithOptions(t, options, nil, "workspaces", "create", "first", "--template", "example", "--no-open")
	waitFor(t, 10*time.Second, func() bool {
		return countEnvironments(t, options.StateDir, domain.EnvironmentReady) == 2 &&
			countEnvironments(t, options.StateDir, domain.EnvironmentClaimed) == 1
	}, "the pool was not filled back to depth 2 after the claim")
}

func TestWorkspacesCreateInfersTheTemplateAndHintsSetupRetry(t *testing.T) {
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
	failingTemplate := fmt.Sprintf("version: 1\nname: example\nrepositories:\n  - name: app\n    clone:\n      url: %s\ninitialize:\n  command: [\"false\"]\n  working_directory: app\n", source)
	templatePath := filepath.Join(configDir, "templates", "example.yaml")
	if err := os.WriteFile(templatePath, []byte(failingTemplate), 0o644); err != nil {
		t.Fatal(err)
	}
	socket := fmt.Sprintf("twt-test-%d", time.Now().UnixNano())
	t.Cleanup(func() { _ = exec.Command("tmux", "-L", socket, "kill-server").Run() })
	options := cli.Options{ConfigDir: configDir, StateDir: filepath.Join(root, "state"), DataDir: filepath.Join(root, "data"), TmuxSocket: socket}

	// The only template is inferred, and a kept failed Workspace hints retry.
	_, stderr, err := executeCollectingOutput(t, options, "workspaces", "create", "broken", "--no-open")
	if err == nil {
		t.Fatal("workspaces create with a failing Workspace initialization did not fail")
	}
	if !strings.Contains(stderr, "Template: example (only template)") {
		t.Fatalf("inference message = %q", stderr)
	}
	if hint := clierr.HintOf(err); !strings.Contains(hint, "twt workspaces setup retry broken") {
		t.Fatalf("create failure hint = %q from error %v", hint, err)
	}
	broken, findErr := store.NewWorkspaceStore(options.StateDir).Find("broken")
	if findErr != nil || broken.Status != domain.WorkspaceSetupFailed {
		t.Fatalf("kept Workspace after failure: %+v, %v", broken, findErr)
	}

	workingTemplate := fmt.Sprintf("version: 1\nname: example\nrepositories:\n  - name: app\n    clone:\n      url: %s\n", source)
	if err := os.WriteFile(templatePath, []byte(workingTemplate), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := executeCollectingOutput(t, options, "workspaces", "create", "works", "--no-open"); err != nil {
		t.Fatalf("workspaces create with the fixed template: %v", err)
	}

	// A second template exists, so inference uses the last-used record.
	otherTemplate := "version: 1\nname: zeta\nrepositories:\n  - name: app\n    clone:\n      url: " + source + "\n"
	if err := os.WriteFile(filepath.Join(configDir, "templates", "zeta.yaml"), []byte(otherTemplate), 0o644); err != nil {
		t.Fatal(err)
	}
	_, stderr, err = executeCollectingOutput(t, options, "workspaces", "create", "second", "--no-open")
	if err != nil {
		t.Fatalf("workspaces create with a last-used template: %v", err)
	}
	if !strings.Contains(stderr, "Template: example (last used)") {
		t.Fatalf("last-used inference message = %q", stderr)
	}

	// Without a last-used record, two templates need an explicit selection.
	if err := os.Remove(filepath.Join(options.StateDir, "last-template.json")); err != nil {
		t.Fatal(err)
	}
	_, _, err = executeCollectingOutput(t, options, "workspaces", "create", "third", "--no-open")
	if err == nil || !strings.Contains(err.Error(), "--template TEMPLATE") || !strings.Contains(err.Error(), "example") || !strings.Contains(err.Error(), "zeta") {
		t.Fatalf("workspaces create without a template selection = %v", err)
	}
}

func executeCollectingOutput(t *testing.T, options cli.Options, args ...string) (string, string, error) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	options.Stdout = &stdout
	options.Stderr = &stderr
	command := cli.New(options)
	command.SetArgs(forceTextOutput(args))
	err := command.Execute()
	return stdout.String(), stderr.String(), err
}

func addOriginCommit(t *testing.T, path, name string) string {
	t.Helper()
	if err := os.WriteFile(filepath.Join(path, name), []byte(name+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runCommand(t, path, "git", "add", name)
	runCommand(t, path, "git", "commit", "-qm", "add "+name)
	return runCommand(t, path, "git", "rev-parse", "HEAD")
}

func countEnvironments(t *testing.T, stateDir string, status domain.PreparedEnvironmentStatus) int {
	t.Helper()
	environments, err := store.NewEnvironmentStore(stateDir).List()
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, environment := range environments {
		if environment.Status == status {
			count++
		}
	}
	return count
}

func assertFileLines(t *testing.T, path string, want []string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Fields(string(data))
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("%s lines = %v, want %v", path, got, want)
	}
}
