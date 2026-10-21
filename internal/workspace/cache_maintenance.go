package workspace

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/jpugliesi/tmux-worktree/internal/store"
)

// MaintainRepositoryCaches keeps every twt-owned Repository Cache compact.
// The daemon runs it after the pool refresh, so no create waits for it.
//
// twt is the only maintainer of its caches. Git's scheduled maintenance
// treats each registered Checkout Lease as its own repository, so it runs
// the same prefetch and repack once for each Checkout Lease against one
// shared object store. Measured on the everysphere cache: five registered
// Checkout Leases added 6,204 packs in three days and 109,987 prefetch refs.
// A failed cache does not stop the others.
func (s *Service) MaintainRepositoryCaches() error {
	s.removeGitMaintenanceRegistrations()
	root := filepath.Join(s.options.DataDir, "caches")
	entries, err := os.ReadDir(root)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("list Repository Caches: %w", err)
	}
	var failures []string
	for _, entry := range entries {
		cachePath := filepath.Join(root, entry.Name())
		if !entry.IsDir() {
			continue
		}
		if _, err := os.Stat(filepath.Join(cachePath, "twt-ownership.json")); err != nil {
			continue
		}
		if err := s.maintainRepositoryCache(cachePath); err != nil {
			failures = append(failures, err.Error())
		}
	}
	if len(failures) > 0 {
		return errors.New(strings.Join(failures, "; "))
	}
	return nil
}

// maintainRepositoryCache deletes the prefetch refs of one cache and
// repacks it.
//
// The repack runs outside the cache lock, because every create waits on
// that lock and the first repack of a bloated cache takes hours. Git lets
// other commands read and fetch while a repack runs, and the repack never
// deletes a pack that it did not merge. A separate repack lock stops a
// second daemon pass from a second repack of the same cache.
func (s *Service) maintainRepositoryCache(cachePath string) error {
	name := filepath.Base(cachePath)
	lock, err := store.AcquireNamedLock(s.options.StateDir, "cache-repack", cachePath)
	if errors.Is(err, store.ErrLockHeld) {
		s.report("Skipped Repository Cache %s: another repack of it runs.", name)
		return nil
	}
	if err != nil {
		return err
	}
	defer lock.Release()
	if err := s.withCacheLock(cachePath, func() error { return deletePrefetchRefs(cachePath) }); err != nil {
		return fmt.Errorf("delete the prefetch refs of Repository Cache %s: %w", name, err)
	}
	packs, packBytes := packFiles(cachePath)
	if packs <= maintainCachePackLimit {
		return nil
	}
	// The repack writes its new packs before it deletes the old ones, so
	// it can need as much free disk as the packs use now.
	if free, err := freeDiskBytes(cachePath); err == nil && free < packBytes {
		return fmt.Errorf("skipped the repack of Repository Cache %s: it can need %d GB of free disk, but only %d GB are free",
			name, packBytes/1_000_000_000, free/1_000_000_000)
	}
	s.report("Repacking Repository Cache %s: %d pack files.", name, packs)
	if err := run(cachePath, "nice", "-n", "10", "git", "repack", "--geometric=2", "-d", "--write-midx", "--quiet"); err != nil {
		return fmt.Errorf("repack Repository Cache %s: %w", name, err)
	}
	return nil
}

// removeGitMaintenanceRegistrations removes the entries of the global
// maintenance.repo list that point into the twt data directory. Entries for
// other repositories stay. Maintenance is best effort, so a failed read or
// write only leaves the entry for the next pass.
func (s *Service) removeGitMaintenanceRegistrations() {
	values, err := output("", "git", "config", "--global", "--get-all", "maintenance.repo")
	if err != nil || values == "" {
		return
	}
	prefixes := []string{filepath.Clean(s.options.DataDir) + string(filepath.Separator)}
	if resolved, err := filepath.EvalSymlinks(s.options.DataDir); err == nil {
		prefixes = append(prefixes, filepath.Clean(resolved)+string(filepath.Separator))
	}
	for _, value := range strings.Split(values, "\n") {
		if !hasAnyPrefix(filepath.Clean(value), prefixes) {
			continue
		}
		if err := run("", "git", "config", "--global", "--fixed-value", "--unset-all", "maintenance.repo", value); err != nil {
			continue
		}
		s.report("Removed the Git maintenance registration of %s. twt maintains its Repository Caches.", value)
	}
}

func hasAnyPrefix(value string, prefixes []string) bool {
	for _, prefix := range prefixes {
		if strings.HasPrefix(value, prefix) {
			return true
		}
	}
	return false
}

// deletePrefetchRefs deletes the refs/prefetch refs that Git's scheduled
// maintenance writes. twt fetches only what a create needs, and every Git
// command that enumerates refs pays for each extra ref.
func deletePrefetchRefs(cachePath string) error {
	refs, err := output(cachePath, "git", "for-each-ref", "--format=delete %(refname)", "refs/prefetch/")
	if err != nil || refs == "" {
		return err
	}
	command := exec.Command("git", "update-ref", "--stdin")
	command.Dir = cachePath
	command.Stdin = strings.NewReader(refs + "\n")
	if data, err := command.CombinedOutput(); err != nil {
		return fmt.Errorf("git update-ref --stdin: %w: %s", err, strings.TrimSpace(string(data)))
	}
	return nil
}

// packFiles returns the pack count and the pack bytes of one cache.
func packFiles(cachePath string) (int, uint64) {
	packs, err := filepath.Glob(filepath.Join(cachePath, "objects", "pack", "*.pack"))
	if err != nil {
		return 0, 0
	}
	var total uint64
	for _, pack := range packs {
		if info, err := os.Stat(pack); err == nil {
			total += uint64(info.Size())
		}
	}
	return len(packs), total
}

// freeDiskBytes returns the free bytes that the current user can use on the
// file system of path.
func freeDiskBytes(path string) (uint64, error) {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(path, &stat); err != nil {
		return 0, err
	}
	return uint64(stat.Bavail) * uint64(stat.Bsize), nil
}
