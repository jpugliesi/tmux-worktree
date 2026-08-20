package project

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

	"github.com/jpugliesi/tmux-worktree/internal/domain"
	"github.com/jpugliesi/tmux-worktree/internal/store"
)

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
	socket := fmt.Sprintf("twt2-project-test-%d", time.Now().UnixNano())
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

	project, err := service.CreateWithOptions("fix-auth", template.Name, template, CreateOptions{})
	<-done
	if err != nil {
		t.Fatalf("CreateWithOptions() after a failed in-flight preparation: %v", err)
	}
	if project.Status != domain.ProjectActive {
		t.Fatalf("Project status = %q, want %q", project.Status, domain.ProjectActive)
	}
	if !messageAppeared("failed. twt2 prepares a replacement.") {
		t.Fatalf("progress does not report the replacement: %v", messages)
	}
	environments, err := store.NewEnvironmentStore(stateDir).List()
	if err != nil {
		t.Fatal(err)
	}
	if len(environments) != 1 || environments[0].Status != domain.EnvironmentClaimed || environments[0].ID == environmentID {
		t.Fatalf("Prepared Environments after replacement = %+v", environments)
	}
	if _, err := os.Stat(filepath.Join(project.Root, "app", "README.md")); err != nil {
		t.Fatalf("replacement Project checkout is incomplete: %v", err)
	}
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
		{"git", "config", "user.name", "twt2 test"},
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
