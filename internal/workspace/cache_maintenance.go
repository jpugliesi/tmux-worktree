package workspace

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

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
		if _, err := os.Stat(filepath.Join(cachePath, "twt-ownership.json")); err != nil {
			removeStrayFiles(cachePath, time.Now())
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

// strayFileAge is the age at which the daemon deletes a file in the caches
// directory that is not part of a Repository Cache.
const strayFileAge = 7 * 24 * time.Hour

// removeStrayFiles deletes the files under path that are older than
// strayFileAge, and then the directories that it emptied. path is an entry of
// the caches directory that is not a Repository Cache.
//
// Tools in a Checkout Lease write there. Turborepo shares its local cache
// between the worktrees of a repository in the parent of the common Git
// directory, and for a Checkout Lease that is the caches directory. Nothing
// deletes old entries. Measured: the everysphere checkouts left 9.8 GB in
// caches/.turbo in two months. An in-progress clone (.twt-cache-*) holds
// only new files, so the age rule keeps it.
func removeStrayFiles(path string, now time.Time) {
	var directories []string
	_ = filepath.WalkDir(path, func(entryPath string, entry os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if entry.IsDir() {
			directories = append(directories, entryPath)
			return nil
		}
		info, err := entry.Info()
		if err == nil && now.Sub(info.ModTime()) >= strayFileAge {
			_ = os.Remove(entryPath)
		}
		return nil
	})
	// WalkDir lists a directory before its contents, so the reverse order
	// removes the deepest directories first. A directory that still holds
	// files stays, because os.Remove fails on it.
	for index := len(directories) - 1; index >= 0; index-- {
		_ = os.Remove(directories[index])
	}
}

// maintainRepositoryCache removes the growth settings and the foreign
// worktrees of one cache, deletes its prefetch refs, and repacks it.
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
	removeGrowthSettings(cachePath)
	if err := s.removeForeignWorktrees(cachePath); err != nil {
		return fmt.Errorf("remove the foreign worktrees of Repository Cache %s: %w", name, err)
	}
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

// foreignWorktreeIdleAge is the time without Git activity after which the
// daemon removes a worktree that twt does not own.
const foreignWorktreeIdleAge = 72 * time.Hour

// cacheWorktree is one entry of "git worktree list --porcelain".
type cacheWorktree struct {
	path   string
	branch string
	bare   bool
}

// removeForeignWorktrees removes the worktrees of one cache that no twt
// Workspace or Prepared Environment owns, and their branches.
//
// Coding agents make such worktrees. Claude Code puts its subagent
// worktrees under <cache>/.claude/worktrees, because it takes the common
// Git directory for the repository root. Nothing removes a worktree with
// changes, and each one holds a full checkout with its build output.
// Measured on the everysphere cache: 27 such worktrees used 498 GB.
//
// Workspaces and their branches are disposable, so the removal discards
// changes and commits. It keeps a worktree with Git activity in the last
// foreignWorktreeIdleAge, and a worktree that a process uses as its working
// directory.
func (s *Service) removeForeignWorktrees(cachePath string) error {
	owned, err := s.ownedCheckouts()
	if err != nil {
		return err
	}
	listing, err := output(cachePath, "git", "worktree", "list", "--porcelain")
	if err != nil {
		return err
	}
	busy, err := processDirectories()
	if err != nil {
		return err
	}
	var branches []string
	for _, worktree := range parseWorktrees(listing) {
		if worktree.bare || owned[filepath.Clean(worktree.path)] {
			continue
		}
		if _, err := os.Stat(worktree.path); err == nil {
			if s.now().Sub(lastGitActivity(worktree.path)) < foreignWorktreeIdleAge || usesDirectory(busy, worktree.path) {
				continue
			}
			// The second --force removes a locked worktree. Claude Code locks
			// the worktrees of its subagents.
			if err := run(cachePath, "git", "worktree", "remove", "--force", "--force", worktree.path); err != nil {
				s.report("Could not remove worktree %s: %v", worktree.path, err)
				continue
			}
		} else if errors.Is(err, os.ErrNotExist) {
			// prune keeps the record of a locked worktree, even when its
			// directory is gone.
			_ = run(cachePath, "git", "worktree", "unlock", worktree.path)
		} else {
			continue
		}
		s.report("Removed worktree %s: no twt Workspace owns it, and it had no Git activity for %s.", worktree.path, foreignWorktreeIdleAge)
		if worktree.branch != "" {
			branches = append(branches, worktree.branch)
		}
	}
	if err := run(cachePath, "git", "worktree", "prune"); err != nil {
		return err
	}
	for _, branch := range branches {
		// Best effort: another worktree can hold the same branch.
		_ = run(cachePath, "git", "branch", "-D", branch)
	}
	return nil
}

// ownedCheckouts returns the checkout path of each repository of every
// Prepared Environment and every Workspace record.
func (s *Service) ownedCheckouts() (map[string]bool, error) {
	owned := map[string]bool{}
	add := func(path string) {
		owned[filepath.Clean(path)] = true
		if resolved, err := filepath.EvalSymlinks(path); err == nil {
			owned[resolved] = true
		}
	}
	environments, err := s.environments.List()
	if err != nil {
		return nil, err
	}
	for _, environment := range environments {
		for _, repository := range environment.Repositories {
			add(repository.Path)
		}
	}
	workspaces, err := s.store.List()
	if err != nil {
		return nil, err
	}
	for _, workspace := range workspaces {
		for _, repository := range workspace.Repositories {
			add(repository.Path)
		}
	}
	return owned, nil
}

// parseWorktrees reads the output of "git worktree list --porcelain".
func parseWorktrees(listing string) []cacheWorktree {
	var worktrees []cacheWorktree
	for _, line := range strings.Split(listing, "\n") {
		switch {
		case strings.HasPrefix(line, "worktree "):
			worktrees = append(worktrees, cacheWorktree{path: strings.TrimPrefix(line, "worktree ")})
		case len(worktrees) == 0:
		case line == "bare":
			worktrees[len(worktrees)-1].bare = true
		case strings.HasPrefix(line, "branch refs/heads/"):
			worktrees[len(worktrees)-1].branch = strings.TrimPrefix(line, "branch refs/heads/")
		}
	}
	return worktrees
}

// lastGitActivity returns the newest change time of the worktree directory
// and of the files in its Git administrative directory: HEAD, the index, and
// the HEAD reflog. A commit, a checkout, or a status refresh changes them.
func lastGitActivity(worktreePath string) time.Time {
	var newest time.Time
	consider := func(path string) {
		if info, err := os.Stat(path); err == nil && info.ModTime().After(newest) {
			newest = info.ModTime()
		}
	}
	consider(worktreePath)
	data, err := os.ReadFile(filepath.Join(worktreePath, ".git"))
	if err != nil {
		return newest
	}
	admin := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(string(data)), "gitdir:"))
	for _, name := range []string{"HEAD", "index", filepath.Join("logs", "HEAD")} {
		consider(filepath.Join(admin, name))
	}
	return newest
}

// processDirectories returns the working directory of every process of the
// current user.
func processDirectories() ([]string, error) {
	data, err := exec.Command("lsof", "-w", "-d", "cwd", "-Fn").Output()
	if err != nil && len(data) == 0 {
		return nil, fmt.Errorf("list process working directories: %w", err)
	}
	var directories []string
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "n") {
			directories = append(directories, strings.TrimPrefix(line, "n"))
		}
	}
	return directories, nil
}

// usesDirectory reports whether a process works in path or below it.
func usesDirectory(directories []string, path string) bool {
	prefixes := []string{filepath.Clean(path) + string(filepath.Separator)}
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		prefixes = append(prefixes, resolved+string(filepath.Separator))
	}
	for _, directory := range directories {
		if hasAnyPrefix(filepath.Clean(directory)+string(filepath.Separator), prefixes) {
			return true
		}
	}
	return false
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
