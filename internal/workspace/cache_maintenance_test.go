package workspace

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jpugliesi/tmux-worktree/internal/domain"
	"github.com/jpugliesi/tmux-worktree/internal/store"
)

// cacheMaintenanceSetup makes one twt-owned Repository Cache with more packs
// than maintainCachePackLimit and one prefetch ref.
func cacheMaintenanceSetup(t *testing.T) (*Service, string, *[]string) {
	t.Helper()
	repository := gitTestRepository(t)
	testGit(t, repository, "config", "gc.auto", "0")
	testGit(t, repository, "config", "maintenance.auto", "false")
	for index := 0; index <= maintainCachePackLimit; index++ {
		testGit(t, repository, "commit", "-q", "--allow-empty", "-m", "pack")
		testGit(t, repository, "repack", "-q")
	}
	testGit(t, repository, "update-ref", "refs/prefetch/remotes/origin/feature", "HEAD")
	testGit(t, repository, "config", "remote.origin.partialclonefilter", "blob:none")
	dataDir := t.TempDir()
	cachePath := filepath.Join(dataDir, "caches", "app.git")
	if err := os.MkdirAll(filepath.Dir(cachePath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(repository, ".git"), cachePath); err != nil {
		t.Fatal(err)
	}
	testGit(t, cachePath, "config", "core.bare", "true")
	if err := os.WriteFile(filepath.Join(cachePath, "twt-ownership.json"), []byte(`{"owner":"twt"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if packs, _ := packFiles(cachePath); packs <= maintainCachePackLimit {
		t.Fatalf("the test cache has %d packs, want more than %d", packs, maintainCachePackLimit)
	}
	messages := &[]string{}
	service := NewService(Options{StateDir: t.TempDir(), DataDir: dataDir})
	service.options.Progress = func(message string) { *messages = append(*messages, message) }
	return service, cachePath, messages
}

func TestMaintainRepositoryCachesRepacksWithoutTheCacheLock(t *testing.T) {
	service, cachePath, _ := cacheMaintenanceSetup(t)
	repacked := false
	service.options.Progress = func(message string) {
		if !strings.Contains(message, "Repacking Repository Cache") {
			return
		}
		repacked = true
		// A create waits on the cache lock, so the repack must not hold it.
		lock, err := store.AcquireCacheLock(service.options.StateDir, cachePath)
		if err != nil {
			t.Errorf("the repack holds the cache lock: %v", err)
			return
		}
		_ = lock.Release()
	}

	if err := service.MaintainRepositoryCaches(); err != nil {
		t.Fatalf("MaintainRepositoryCaches(): %v", err)
	}
	if !repacked {
		t.Fatal("MaintainRepositoryCaches() did not repack the cache")
	}
	if packs, _ := packFiles(cachePath); packs > 2 {
		t.Fatalf("the cache has %d packs after the repack, want at most 2", packs)
	}
	if refs := testGit(t, cachePath, "for-each-ref", "refs/prefetch/"); refs != "" {
		t.Fatalf("the prefetch refs remain: %s", refs)
	}
	if filter, err := exec.Command("git", "-C", cachePath, "config", "--get", "remote.origin.partialclonefilter").Output(); err == nil {
		t.Fatalf("the cache keeps the partial clone filter %s", filter)
	}
	testGit(t, cachePath, "fsck", "--connectivity-only", "--no-progress")
}

func TestMaintainRepositoryCachesSkipsACacheThatAnotherRepackHolds(t *testing.T) {
	service, cachePath, messages := cacheMaintenanceSetup(t)
	before, _ := packFiles(cachePath)
	lock, err := store.AcquireNamedLock(service.options.StateDir, "cache-repack", cachePath)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Release()

	if err := service.MaintainRepositoryCaches(); err != nil {
		t.Fatalf("MaintainRepositoryCaches(): %v", err)
	}
	if after, _ := packFiles(cachePath); after != before {
		t.Fatalf("the cache has %d packs, want the %d packs of the held cache", after, before)
	}
	if !strings.Contains(strings.Join(*messages, "\n"), "another repack of it runs") {
		t.Fatalf("progress does not report the skip: %v", *messages)
	}
}

// Git's scheduled maintenance must not run on a Checkout Lease, but the
// registrations of other repositories stay.
func TestMaintainRepositoryCachesRemovesOnlyTwtMaintenanceRegistrations(t *testing.T) {
	service, _, _ := cacheMaintenanceSetup(t)
	globalConfig := filepath.Join(t.TempDir(), "gitconfig")
	t.Setenv("GIT_CONFIG_GLOBAL", globalConfig)
	lease := filepath.Join(service.options.DataDir, "projects", "0123", "app")
	other := filepath.Join(t.TempDir(), "other")
	testGit(t, "", "config", "--global", "--add", "maintenance.repo", lease)
	testGit(t, "", "config", "--global", "--add", "maintenance.repo", other)

	if err := service.MaintainRepositoryCaches(); err != nil {
		t.Fatalf("MaintainRepositoryCaches(): %v", err)
	}
	if got := testGit(t, "", "config", "--global", "--get-all", "maintenance.repo"); got != other {
		t.Fatalf("maintenance.repo = %q, want only %q", got, other)
	}
}

// The daemon removes an idle worktree that no twt record owns, with its
// branch, and keeps owned, recent, and busy worktrees.
func TestMaintainRepositoryCachesRemovesIdleForeignWorktrees(t *testing.T) {
	service, cachePath, _ := cacheMaintenanceSetup(t)
	// Git lists the resolved path, for example /private/var on macOS.
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	add := func(name string) string {
		path := filepath.Join(root, name)
		testGit(t, cachePath, "worktree", "add", "-q", "-b", name, path, "HEAD")
		return path
	}
	age := func(path string) {
		old := time.Now().Add(-2 * foreignWorktreeIdleAge)
		admin := filepath.Join(cachePath, "worktrees", filepath.Base(path))
		for _, target := range []string{path, filepath.Join(admin, "HEAD"), filepath.Join(admin, "index"), filepath.Join(admin, "logs", "HEAD")} {
			if err := os.Chtimes(target, old, old); err != nil && !errors.Is(err, os.ErrNotExist) {
				t.Fatal(err)
			}
		}
	}
	owned := add("owned")
	idle := add("idle")
	locked := add("locked")
	recent := add("recent")
	busy := add("busy")
	gone := add("gone")
	for _, path := range []string{owned, idle, locked, busy} {
		age(path)
	}
	testGit(t, cachePath, "worktree", "lock", locked)
	testGit(t, cachePath, "worktree", "lock", gone)
	if err := os.RemoveAll(gone); err != nil {
		t.Fatal(err)
	}
	template := domain.Template{
		Version: domain.TemplateVersion, Name: "app",
		Repositories: []domain.RepositorySpec{{Name: "app", Clone: domain.CloneSpec{URL: "https://example.com/app.git"}}},
	}
	digest, err := store.EnvironmentDigest(template)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	if err := service.environments.Save(domain.PreparedEnvironment{
		Version: domain.PreparedEnvironmentVersion, FormatVersion: domain.PreparationFormatVersion,
		ID: "owner", TemplateName: template.Name, TemplateDigest: digest, TemplateSnapshot: template,
		Status: domain.EnvironmentReady, Root: root, QueueToken: "queue-token", QueuedAt: now, ReadyAt: &now,
		Repositories: []domain.PreparedRepository{{Name: "app", CachePath: cachePath, Path: owned, BaseCommit: "base-commit"}},
		Steps:        []domain.SetupStep{{ID: "environment_root", Kind: domain.StepWorkspaceRoot, Status: domain.StepSucceeded}},
		CreatedAt:    now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	process := exec.Command("sleep", "60")
	process.Dir = busy
	if err := process.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = process.Process.Kill(); _ = process.Wait() })

	if err := service.MaintainRepositoryCaches(); err != nil {
		t.Fatalf("MaintainRepositoryCaches(): %v", err)
	}
	listing := testGit(t, cachePath, "worktree", "list", "--porcelain")
	for _, path := range []string{owned, recent, busy} {
		if !strings.Contains(listing, "worktree "+path+"\n") {
			t.Errorf("the daemon removed worktree %s", path)
		}
	}
	for _, path := range []string{idle, locked, gone} {
		if strings.Contains(listing, "worktree "+path+"\n") {
			t.Errorf("the daemon kept worktree %s", path)
		}
		if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("the directory of worktree %s remains: %v", path, err)
		}
		if branches := testGit(t, cachePath, "branch", "--list", filepath.Base(path)); branches != "" {
			t.Errorf("the branch of worktree %s remains", path)
		}
	}
}
