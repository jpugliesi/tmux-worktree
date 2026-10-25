package workspace

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/jpugliesi/tmux-worktree/internal/domain"
)

func poolTestTemplate(t *testing.T) domain.Template {
	t.Helper()
	source := filepath.Join(t.TempDir(), "source")
	initCreateTestRepository(t, source)
	return domain.Template{
		Version: domain.TemplateVersion, Name: "pool",
		Repositories: []domain.RepositorySpec{{
			Name: "app", Clone: domain.CloneSpec{URL: "file://" + source}, DefaultBranch: "main",
		}},
	}
}

// A release returns its Prepared Environment to the pool, so the pool can
// hold more ready environments than pool_depth. TrimPool removes the extra
// ones and keeps the most recently updated.
func TestTrimPoolRemovesTheOldestReadyEnvironmentsAboveTheDepth(t *testing.T) {
	template := poolTestTemplate(t)
	service := NewService(Options{StateDir: t.TempDir(), DataDir: t.TempDir()})
	queued, err := service.TopUpPool(template.Name, template, 3)
	if err != nil {
		t.Fatal(err)
	}
	var prepared []domain.PreparedEnvironment
	for _, entry := range queued {
		environment, err := service.PrepareQueued(entry.ID, entry.QueueToken)
		if err != nil {
			t.Fatal(err)
		}
		prepared = append(prepared, environment)
	}

	if err := service.TrimPool(template, 1); err != nil {
		t.Fatalf("TrimPool() error = %v", err)
	}
	environments, err := service.environments.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(environments) != 1 || environments[0].ID != prepared[2].ID {
		t.Fatalf("TrimPool() kept %d environments, want only the newest %s", len(environments), prepared[2].ID)
	}
	for _, environment := range prepared[:2] {
		if _, err := os.Stat(environment.Root); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("the root of removed environment %s remains: %v", environment.ID, err)
		}
	}
	listing := testGitOutput(t, prepared[0].Repositories[0].CachePath, "worktree", "list", "--porcelain")
	if worktrees := len(parseWorktrees(listing)); worktrees != 2 {
		t.Fatalf("the cache lists %d worktrees, want the cache and one checkout:\n%s", worktrees, listing)
	}
}

// Each Prepared Environment holds a full checkout, so the refill stops when
// the disk is almost full.
func TestTopUpPoolCreatesNothingBelowTheFreeDiskFloor(t *testing.T) {
	template := poolTestTemplate(t)
	service := NewService(Options{StateDir: t.TempDir(), DataDir: t.TempDir(), PoolRefillFreeDiskFloor: 1 << 62})
	queued, err := service.TopUpPool(template.Name, template, 2)
	if err != nil {
		t.Fatalf("TopUpPool() error = %v", err)
	}
	environments, err := service.environments.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(queued) != 0 || len(environments) != 0 {
		t.Fatalf("TopUpPool() queued %d environments below the floor", len(environments))
	}
}
