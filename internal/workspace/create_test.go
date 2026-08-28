package workspace

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jpugliesi/tmux-worktree/internal/clierr"
	"github.com/jpugliesi/tmux-worktree/internal/domain"
	"github.com/jpugliesi/tmux-worktree/internal/store"
)

func TestValidateCreateRejectsATicketInAnotherActiveWorkspace(t *testing.T) {
	stateDir := t.TempDir()
	now := time.Now().UTC()
	existing := domain.Workspace{
		Version: domain.WorkspaceVersion, ID: "existing-workspace", Name: "existing",
		Status: domain.WorkspaceActive, Project: "core", Tickets: []string{"fix-auth"},
		CreatedAt: now, UpdatedAt: now,
	}
	if err := store.NewWorkspaceStore(stateDir).Save(existing); err != nil {
		t.Fatal(err)
	}
	template := domain.Template{
		Version: domain.TemplateVersion,
		Name:    "example",
		Repositories: []domain.RepositorySpec{{
			Name: "app", Clone: domain.CloneSpec{URL: "https://example.com/app.git"},
		}},
	}
	service := NewService(Options{StateDir: stateDir, DataDir: t.TempDir()})
	err := service.ValidateCreateWithOptions("second", template.Name, template, CreateOptions{
		Project: "core", Tickets: []string{"fix-auth"},
	})
	if clierr.CodeOf(err) != clierr.Locked {
		t.Fatalf("ValidateCreateWithOptions() = %v (code %q), want locked", err, clierr.CodeOf(err))
	}
}

func TestPrepareQueuedReportsAFailedEnvironment(t *testing.T) {
	stateDir := t.TempDir()
	dataDir := t.TempDir()
	now := time.Now().UTC()
	template := domain.Template{
		Version: domain.TemplateVersion,
		Name:    "example",
		Repositories: []domain.RepositorySpec{{
			Name:  "app",
			Clone: domain.CloneSpec{URL: "https://example.com/app.git"},
		}},
	}
	digest, err := store.EnvironmentDigest(template)
	if err != nil {
		t.Fatal(err)
	}
	environment := domain.PreparedEnvironment{
		Version: domain.PreparedEnvironmentVersion, FormatVersion: domain.PreparationFormatVersion,
		ID: "failed-env", TemplateName: template.Name, TemplateDigest: digest,
		TemplateSnapshot: template, Status: domain.EnvironmentFailed,
		Root: filepath.Join(dataDir, "projects", "failed-env"), QueueToken: "queue-token",
		QueuedAt: now, CreatedAt: now, UpdatedAt: now, Failure: "init exploded",
	}
	if err := store.NewEnvironmentStore(stateDir).Save(environment); err != nil {
		t.Fatal(err)
	}

	service := NewService(Options{StateDir: stateDir, DataDir: dataDir})
	_, err = service.PrepareQueued("failed-env", "queue-token")
	if !errors.Is(err, ErrEnvironmentFailed) {
		t.Fatalf("PrepareQueued() error = %v, want ErrEnvironmentFailed", err)
	}
	logPath := PrepareLogPath(stateDir, "failed-env")
	if !strings.Contains(err.Error(), "init exploded") || !strings.Contains(err.Error(), logPath) {
		t.Fatalf("failed environment error = %q, want the failure text and log path %q", err, logPath)
	}
}

func TestCreateReplacesAFailedInFlightPreparation(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is not installed")
	}

	stateDir := t.TempDir()
	dataDir := t.TempDir()
	source := filepath.Join(t.TempDir(), "source")
	initCreateTestRepository(t, source)
	template := domain.Template{
		Version: domain.TemplateVersion,
		Name:    "example",
		Repositories: []domain.RepositorySpec{{
			Name:  "app",
			Clone: domain.CloneSpec{URL: source},
		}},
	}
	socket := fmt.Sprintf("twt-workspace-test-%d", time.Now().UnixNano())
	t.Cleanup(func() { _ = exec.Command("tmux", "-L", socket, "kill-server").Run() })

	var mu sync.Mutex
	var messages []string
	service := NewService(Options{
		StateDir: stateDir, DataDir: dataDir, TmuxSocket: socket,
		Progress: func(message string) {
			mu.Lock()
			messages = append(messages, message)
			mu.Unlock()
		},
	})
	queued, err := service.TopUpPool(template.Name, template, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(queued) != 1 {
		t.Fatalf("TopUpPool() queued %d Prepared Environments, want 1", len(queued))
	}
	environmentID := queued[0].ID
	lock, err := store.AcquireEnvironmentLock(stateDir, environmentID)
	if err != nil {
		t.Fatal(err)
	}
	messageAppeared := func(want string) bool {
		mu.Lock()
		defer mu.Unlock()
		for _, message := range messages {
			if strings.Contains(message, want) {
				return true
			}
		}
		return false
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		deadline := time.Now().Add(5 * time.Second)
		for !messageAppeared("Waiting for the background preparation") && time.Now().Before(deadline) {
			time.Sleep(10 * time.Millisecond)
		}
		environmentStore := store.NewEnvironmentStore(stateDir)
		failed, findErr := environmentStore.Find(environmentID)
		if findErr == nil {
			failed.Status = domain.EnvironmentFailed
			failed.Failure = "the background worker failed"
			failed.UpdatedAt = time.Now().UTC()
			_ = environmentStore.Save(failed)
		}
		_ = lock.Release()
	}()

	workspace, err := service.CreateWithOptions("fix-auth", template.Name, template, CreateOptions{})
	<-done
	if err != nil {
		t.Fatalf("CreateWithOptions() after a failed in-flight preparation: %v", err)
	}
	if workspace.Status != domain.WorkspaceActive {
		t.Fatalf("Workspace status = %q, want %q", workspace.Status, domain.WorkspaceActive)
	}
	if !messageAppeared("failed. twt prepares a replacement.") {
		t.Fatalf("progress does not report the replacement: %v", messages)
	}
	environments, err := store.NewEnvironmentStore(stateDir).List()
	if err != nil {
		t.Fatal(err)
	}
	if len(environments) != 1 || environments[0].Status != domain.EnvironmentClaimed || environments[0].ID == environmentID {
		t.Fatalf("Prepared Environments after replacement = %+v", environments)
	}
	if _, err := os.Stat(filepath.Join(workspace.Root, "app", "README.md")); err != nil {
		t.Fatalf("replacement Workspace checkout is incomplete: %v", err)
	}
}

func TestCreateWithBaseRefStartsFromTheStackParentBranch(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is not installed")
	}
	stateDir := t.TempDir()
	dataDir := t.TempDir()
	source := filepath.Join(t.TempDir(), "source")
	initCreateTestRepository(t, source)
	// The stack parent branch carries a file main does not have.
	for _, argv := range [][]string{
		{"git", "-C", source, "checkout", "-q", "-b", "twt/parent-branch"},
		{"git", "-C", source, "commit", "-q", "--allow-empty", "-m", "parent work"},
		{"git", "-C", source, "checkout", "-q", "main"},
	} {
		command := exec.Command(argv[0], argv[1:]...)
		if data, err := command.CombinedOutput(); err != nil {
			t.Fatalf("%v: %v\n%s", argv, err, data)
		}
	}
	parentFile := filepath.Join(source, "PARENT.md")
	for _, argv := range [][]string{
		{"git", "-C", source, "checkout", "-q", "twt/parent-branch"},
	} {
		command := exec.Command(argv[0], argv[1:]...)
		if data, err := command.CombinedOutput(); err != nil {
			t.Fatalf("%v: %v\n%s", argv, err, data)
		}
	}
	if err := os.WriteFile(parentFile, []byte("parent work\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, argv := range [][]string{
		{"git", "-C", source, "add", "PARENT.md"},
		{"git", "-C", source, "commit", "-q", "-m", "parent file"},
		{"git", "-C", source, "checkout", "-q", "main"},
	} {
		command := exec.Command(argv[0], argv[1:]...)
		if data, err := command.CombinedOutput(); err != nil {
			t.Fatalf("%v: %v\n%s", argv, err, data)
		}
	}
	template := domain.Template{
		Version: domain.TemplateVersion,
		Name:    "example",
		Repositories: []domain.RepositorySpec{{
			Name:  "app",
			Clone: domain.CloneSpec{URL: source},
		}},
	}
	socket := fmt.Sprintf("twt-workspace-test-%d", time.Now().UnixNano())
	t.Cleanup(func() { _ = exec.Command("tmux", "-L", socket, "kill-server").Run() })
	service := NewService(Options{StateDir: stateDir, DataDir: dataDir, TmuxSocket: socket})

	workspace, err := service.CreateWithOptions("stacked", template.Name, template, CreateOptions{
		BaseRef: "twt/parent-branch",
	})
	if err != nil {
		t.Fatalf("CreateWithOptions with BaseRef: %v", err)
	}
	if workspace.BaseRef != "twt/parent-branch" {
		t.Fatalf("Workspace.BaseRef = %q", workspace.BaseRef)
	}
	if _, err := os.Stat(filepath.Join(workspace.Root, "app", "PARENT.md")); err != nil {
		t.Fatalf("checkout does not start from the stack parent: %v", err)
	}
}

func TestCreateRepairsShallowCacheAncestryAndKeepsItConnected(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is not installed")
	}
	stateDir := t.TempDir()
	dataDir := t.TempDir()
	source := filepath.Join(t.TempDir(), "source")
	initCreateTestRepository(t, source)
	template := domain.Template{
		Version: domain.TemplateVersion,
		Name:    "shallow",
		Repositories: []domain.RepositorySpec{{
			Name: "app", Clone: domain.CloneSpec{URL: "file://" + source, Depth: 1},
			DefaultBranch: "main",
		}},
	}
	socket := fmt.Sprintf("twt-workspace-test-%d", time.Now().UnixNano())
	t.Cleanup(func() { _ = exec.Command("tmux", "-L", socket, "kill-server").Run() })
	service := NewService(Options{StateDir: stateDir, DataDir: dataDir, TmuxSocket: socket})

	environment, err := service.Prepare(template.Name, template)
	if err != nil {
		t.Fatal(err)
	}
	commitCreateTestFile(t, source, "second.txt")
	repository := environment.Repositories[0]
	legacyFetch := exec.Command("git", "-C", repository.CachePath, "fetch", "--depth", "1", "origin", "+refs/heads/main:refs/remotes/origin/main")
	if data, err := legacyFetch.CombinedOutput(); err != nil {
		t.Fatalf("create the legacy shallow-cache state: %v\n%s", err, data)
	}
	if command := exec.Command("git", "-C", repository.CachePath, "merge-base", environment.Repositories[0].BaseCommit, "refs/remotes/origin/main"); command.Run() == nil {
		t.Fatal("test setup did not create disconnected shallow history")
	}

	workspace, err := service.CreateWithOptions("fix-shallow", template.Name, template, CreateOptions{Fresh: true})
	if err != nil {
		t.Fatal(err)
	}
	checkout := workspace.Repositories[0].Path
	base := testGitOutput(t, checkout, "merge-base", "HEAD", "refs/remotes/origin/main")
	if head := testGitOutput(t, checkout, "rev-parse", "HEAD"); base != head {
		t.Fatalf("Workspace branch starts at %s, but its origin/main merge base is %s", head, base)
	}
	if shallow := testGitOutput(t, checkout, "rev-parse", "--is-shallow-repository"); shallow != "false" {
		t.Fatalf("Workspace Repository Cache is still shallow: %s", shallow)
	}

	commitCreateTestFile(t, source, "third.txt")
	if _, err := service.Prepare(template.Name, template); err != nil {
		t.Fatal(err)
	}
	if base := testGitOutput(t, checkout, "merge-base", "HEAD", "refs/remotes/origin/main"); base == "" {
		t.Fatal("a later cache refresh disconnected the Workspace branch from origin/main")
	}
}

func commitCreateTestFile(t *testing.T, repository, name string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(repository, name), []byte(name+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, argv := range [][]string{{"add", name}, {"commit", "-qm", "add " + name}} {
		command := exec.Command("git", argv...)
		command.Dir = repository
		if data, err := command.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", argv, err, data)
		}
	}
}

func testGitOutput(t *testing.T, repository string, args ...string) string {
	t.Helper()
	command := exec.Command("git", append([]string{"-C", repository}, args...)...)
	data, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, data)
	}
	return strings.TrimSpace(string(data))
}

func initCreateTestRepository(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
	commands := [][]string{
		{"git", "init", "-q", "-b", "main", path},
	}
	for _, argv := range commands {
		command := exec.Command(argv[0], argv[1:]...)
		if data, err := command.CombinedOutput(); err != nil {
			t.Fatalf("%v: %v\n%s", argv, err, data)
		}
	}
	if err := os.WriteFile(filepath.Join(path, "README.md"), []byte("test repository\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, argv := range [][]string{
		{"git", "config", "user.name", "twt test"},
		{"git", "config", "user.email", "test@example.com"},
		{"git", "add", "README.md"},
		{"git", "commit", "-qm", "initial commit"},
	} {
		command := exec.Command(argv[0], argv[1:]...)
		command.Dir = path
		if data, err := command.CombinedOutput(); err != nil {
			t.Fatalf("%v: %v\n%s", argv, err, data)
		}
	}
}

// A failed Prepared Environment from an earlier session keeps its finished
// steps. The next create retries it instead of preparing a new environment.
func TestCreateRetriesAFailedEnvironmentFromAnEarlierSession(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is not installed")
	}

	stateDir := t.TempDir()
	dataDir := t.TempDir()
	source := filepath.Join(t.TempDir(), "source")
	initCreateTestRepository(t, source)
	template := domain.Template{
		Version: domain.TemplateVersion,
		Name:    "example",
		Repositories: []domain.RepositorySpec{{
			Name:  "app",
			Clone: domain.CloneSpec{URL: source},
			// The initialization fails on the first run and succeeds on the
			// retry, like an initialization stopped by a signal.
			Initialize: &domain.InitializeSpec{Command: []string{"sh", "-c",
				`test -f "$TWT_ENVIRONMENT_ROOT/.tried" && exit 0; touch "$TWT_ENVIRONMENT_ROOT/.tried"; exit 1`}},
		}},
	}
	socket := fmt.Sprintf("twt-workspace-retry-test-%d", time.Now().UnixNano())
	t.Cleanup(func() { _ = exec.Command("tmux", "-L", socket, "kill-server").Run() })

	var mu sync.Mutex
	var messages []string
	service := NewService(Options{
		StateDir: stateDir, DataDir: dataDir, TmuxSocket: socket,
		Progress: func(message string) {
			mu.Lock()
			messages = append(messages, message)
			mu.Unlock()
		},
	})
	queued, err := service.TopUpPool(template.Name, template, 1)
	if err != nil {
		t.Fatal(err)
	}
	environmentID := queued[0].ID
	if _, err := service.PrepareQueued(environmentID, queued[0].QueueToken); err == nil {
		t.Fatal("PrepareQueued() succeeded, want the initialization failure")
	}

	workspace, err := service.CreateWithOptions("retry", template.Name, template, CreateOptions{})
	if err != nil {
		t.Fatalf("CreateWithOptions() after a failed preparation: %v", err)
	}
	if workspace.Status != domain.WorkspaceActive || workspace.EnvironmentID != environmentID {
		t.Fatalf("Workspace = status %q, environment %q; want an active claim of %q",
			workspace.Status, workspace.EnvironmentID, environmentID)
	}
	mu.Lock()
	defer mu.Unlock()
	retried := false
	for _, message := range messages {
		if strings.Contains(message, "Retrying failed Prepared Environment "+environmentID) {
			retried = true
		}
	}
	if !retried {
		t.Fatalf("progress does not report the retry: %v", messages)
	}
}

func TestRunningStepLineDescribesTheRunningStep(t *testing.T) {
	now := time.Now()
	steps := []domain.SetupStep{
		{ID: "environment_root", Status: domain.StepSucceeded},
		{ID: "cache:app", Status: domain.StepRunning, StartedAt: &now},
		{ID: "checkout:app", Status: domain.StepPending},
	}
	if line := runningStepLine(steps); line != "cache:app (step 2 of 3)" {
		t.Fatalf("runningStepLine() = %q", line)
	}
	if line := runningStepLine([]domain.SetupStep{{ID: "cache:app", Status: domain.StepPending}}); line != "" {
		t.Fatalf("runningStepLine() with no running step = %q", line)
	}
}

// Repository initialization may initialize submodules and install ignored
// files. Only tracked changes, a moved HEAD, or an active Git operation fail
// the preparation.
func TestPreparedInitializationAcceptsSubmoduleInitialization(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	stateDir := t.TempDir()
	dataDir := t.TempDir()
	submoduleSource := filepath.Join(t.TempDir(), "library")
	initCreateTestRepository(t, submoduleSource)
	source := filepath.Join(t.TempDir(), "source")
	initCreateTestRepository(t, source)
	for _, argv := range [][]string{
		{"git", "-C", source, "-c", "protocol.file.allow=always", "submodule", "add", submoduleSource, "library"},
		{"git", "-C", source, "commit", "-qm", "add the library submodule"},
	} {
		command := exec.Command(argv[0], argv[1:]...)
		if data, err := command.CombinedOutput(); err != nil {
			t.Fatalf("%v: %v\n%s", argv, err, data)
		}
	}
	template := domain.Template{
		Version: domain.TemplateVersion,
		Name:    "example",
		Repositories: []domain.RepositorySpec{{
			Name:  "app",
			Clone: domain.CloneSpec{URL: source},
			Initialize: &domain.InitializeSpec{Command: []string{"sh", "-c",
				"git -c protocol.file.allow=always submodule update --init && exclude=\"$(git rev-parse --git-path info/exclude)\" && mkdir -p \"$(dirname \"$exclude\")\" && echo .ignored-local > \"$exclude\" && echo local > .ignored-local"}},
		}},
	}
	service := NewService(Options{StateDir: stateDir, DataDir: dataDir})
	queued, err := service.TopUpPool(template.Name, template, 1)
	if err != nil {
		t.Fatal(err)
	}
	environment, err := service.PrepareQueued(queued[0].ID, queued[0].QueueToken)
	if err != nil {
		t.Fatalf("PrepareQueued() with a submodule-initializing initialization: %v", err)
	}
	if environment.Status != domain.EnvironmentReady {
		t.Fatalf("Prepared Environment status = %q, want %q", environment.Status, domain.EnvironmentReady)
	}
}

func TestPreparedInitializationRejectsTrackedChangesAndAMovedHead(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	for _, test := range []struct {
		name    string
		command string
		want    string
	}{
		{name: "tracked change", command: "echo changed > README.md", want: "tracked or nonignored changes"},
		{name: "moved HEAD", command: "git reset -q --hard HEAD~1", want: "moved prepared repository"},
	} {
		t.Run(test.name, func(t *testing.T) {
			source := filepath.Join(t.TempDir(), "source")
			initCreateTestRepository(t, source)
			command := exec.Command("git", "-C", source, "commit", "-q", "--allow-empty", "-m", "second")
			if data, err := command.CombinedOutput(); err != nil {
				t.Fatalf("git commit: %v\n%s", err, data)
			}
			template := domain.Template{
				Version: domain.TemplateVersion,
				Name:    "example",
				Repositories: []domain.RepositorySpec{{
					Name:       "app",
					Clone:      domain.CloneSpec{URL: source},
					Initialize: &domain.InitializeSpec{Command: []string{"sh", "-c", test.command}},
				}},
			}
			service := NewService(Options{StateDir: t.TempDir(), DataDir: t.TempDir()})
			queued, err := service.TopUpPool(template.Name, template, 1)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := service.PrepareQueued(queued[0].ID, queued[0].QueueToken); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("PrepareQueued() error = %v, want %q", err, test.want)
			}
		})
	}
}
