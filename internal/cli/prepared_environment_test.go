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
	"github.com/jpugliesi/tmux-worktree/internal/domain"
	"github.com/jpugliesi/tmux-worktree/internal/store"
)

func TestPreparedEnvironmentRunsRepositoryInitializationBeforeProjectClaim(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is not installed")
	}

	root := t.TempDir()
	binary := filepath.Join(root, "twt2")
	runCommand(t, filepath.Join("..", ".."), "go", "build", "-o", binary, "./cmd/twt2")
	source := filepath.Join(root, "source")
	initGitRepository(t, source)
	initLog := filepath.Join(root, "repository-init.log")
	t.Setenv("TWT2_TEST_INIT_LOG", initLog)
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
      command: ["sh", "-c", "sleep 1.2; printf 'initialized\\n' >> \"$TWT2_TEST_INIT_LOG\""]
`, source)
	templatePath := filepath.Join(configDir, "templates", "example.yaml")
	if err := os.WriteFile(templatePath, []byte(template), 0o644); err != nil {
		t.Fatal(err)
	}
	socket := fmt.Sprintf("twt2-test-%d", time.Now().UnixNano())
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
	projects, err := store.NewProjectStore(options.StateDir).List()
	if err != nil {
		t.Fatal(err)
	}
	if len(projects) != 0 {
		t.Fatalf("templates prepare created Projects: %+v", projects)
	}

	started := time.Now()
	executeWithOptions(t, options, nil, "projects", "create", "first", "--template", "example", "--no-open")
	claimTime := time.Since(started)
	if claimTime >= time.Second {
		t.Fatalf("prepared Project claim took %s; want less than 1s", claimTime)
	}
	assertFileLines(t, initLog, []string{"initialized"})
	project, err := store.NewProjectStore(options.StateDir).Find("first")
	if err != nil {
		t.Fatal(err)
	}
	if project.EnvironmentID == "" {
		t.Fatalf("Project %q has no claimed Prepared Environment", project.Name)
	}
	branch := runCommand(t, project.Repositories[0].Path, "git", "branch", "--show-current")
	if branch != project.Repositories[0].Branch {
		t.Fatalf("claimed checkout branch = %q, want %q", branch, project.Repositories[0].Branch)
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
	storageOutput := executeWithOptions(t, options, nil, "storage", "status")
	if !strings.Contains(storageOutput, "Prepared:") || !strings.Contains(storageOutput, "1 ready") {
		t.Fatalf("storage status does not show the ready Prepared Environment:\n%s", storageOutput)
	}
	changedTemplate := strings.Replace(template, "  - name: app\n", "  - name: app\n    window_name: changed\n", 1)
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
	if len(environments) != 1 || environments[0].Status != domain.EnvironmentClaimed || environments[0].ID != project.EnvironmentID {
		t.Fatalf("cleanup changed the claimed Project Environment: %+v", environments)
	}
	if _, err := os.Stat(project.Repositories[0].Path); err != nil {
		t.Fatalf("cleanup removed the active Project checkout: %v", err)
	}
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
