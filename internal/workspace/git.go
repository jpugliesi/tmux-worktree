package workspace

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/jpugliesi/tmux-worktree/internal/domain"
	"github.com/jpugliesi/tmux-worktree/internal/store"
)

func (s *Service) ensureCache(spec domain.RepositorySpec, cachePath string) error {
	return s.withCacheLock(cachePath, func() error {
		return s.ensureCacheLocked(spec, cachePath)
	})
}

func (s *Service) ensureCacheLocked(spec domain.RepositorySpec, cachePath string) error {
	if info, statErr := os.Stat(cachePath); statErr == nil && info.IsDir() {
		if err := validateCacheMarker(cachePath, spec.Clone.URL); err != nil {
			return err
		}
		origin, err := output(cachePath, "git", "remote", "get-url", "origin")
		if err != nil {
			return fmt.Errorf("read origin for cache %q: %w", spec.Name, err)
		}
		if origin != spec.Clone.URL {
			return fmt.Errorf("cache %q has origin %q, but the Workspace requires %q", spec.Name, origin, spec.Clone.URL)
		}
		if err := s.ensureRemotes(cachePath, spec.Remotes); err != nil {
			return err
		}
		return refreshCache(cachePath, spec)
	} else if statErr != nil && !errors.Is(statErr, os.ErrNotExist) {
		return fmt.Errorf("inspect repository cache: %w", statErr)
	}
	if err := os.MkdirAll(filepath.Dir(cachePath), 0o755); err != nil {
		return fmt.Errorf("create cache directory: %w", err)
	}
	temporary, err := os.MkdirTemp(filepath.Dir(cachePath), ".twt-cache-*")
	if err != nil {
		return fmt.Errorf("create temporary cache path: %w", err)
	}
	if err := os.Remove(temporary); err != nil {
		return fmt.Errorf("prepare temporary cache path: %w", err)
	}
	defer os.RemoveAll(temporary)
	args := []string{"clone", "--bare"}
	if spec.Clone.Filter != "" {
		args = append(args, "--filter="+spec.Clone.Filter)
	}
	args = append(args, spec.Clone.URL, temporary)
	if err := run("", "git", args...); err != nil {
		return fmt.Errorf("clone repository %q: %w", spec.Name, err)
	}
	marker := map[string]string{"owner": "twt", "url": spec.Clone.URL}
	if err := writeJSON(filepath.Join(temporary, "twt-ownership.json"), marker, 0o600); err != nil {
		return err
	}
	if err := os.Rename(temporary, cachePath); err != nil {
		return fmt.Errorf("publish repository cache: %w", err)
	}
	if err := s.ensureRemotes(cachePath, spec.Remotes); err != nil {
		return err
	}
	return refreshCache(cachePath, spec)
}

func (s *Service) ensureRemotes(cachePath string, remotes map[string]string) error {
	names := make([]string, 0, len(remotes))
	for name := range remotes {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		url := remotes[name]
		existing, err := output(cachePath, "git", "remote", "get-url", name)
		if err == nil {
			if existing != url {
				return fmt.Errorf("cache remote %q is %q, but the Workspace requires %q", name, existing, url)
			}
			continue
		}
		if err := run(cachePath, "git", "remote", "add", name, url); err != nil {
			return fmt.Errorf("add cache remote %q: %w", name, err)
		}
	}
	return ensureOriginFetchRefspec(cachePath)
}

const originFetchRefspec = "+refs/heads/*:refs/remotes/origin/*"

// ensureOriginFetchRefspec makes sure the bare cache tracks origin branches.
// A cache from "git clone --bare" has no origin fetch refspec, so a push from
// a worktree does not update refs/remotes/origin/*. This function lazily
// migrates existing caches because it runs on every cache ensure and on the
// removal validation path.
func ensureOriginFetchRefspec(cachePath string) error {
	existing, err := output(cachePath, "git", "config", "--get-all", "remote.origin.fetch")
	if err == nil {
		for _, line := range strings.Split(existing, "\n") {
			if strings.TrimSpace(line) == originFetchRefspec {
				return nil
			}
		}
	}
	if err := run(cachePath, "git", "config", "--add", "remote.origin.fetch", originFetchRefspec); err != nil {
		return fmt.Errorf("add the origin fetch refspec to cache %q: %w", cachePath, err)
	}
	return nil
}

func (s *Service) ensureCheckout(p domain.Workspace, repositoryName string) error {
	_, repository, err := repositoryFor(p, repositoryName)
	if err != nil {
		return err
	}
	return s.withCacheLock(repository.CachePath, func() error {
		return s.ensureCheckoutLocked(p, repositoryName)
	})
}

func (s *Service) ensureCheckoutLocked(p domain.Workspace, repositoryName string) error {
	spec, repository, err := repositoryFor(p, repositoryName)
	if err != nil {
		return err
	}
	if info, statErr := os.Stat(repository.Path); statErr == nil && info.IsDir() {
		if err := worktreeUsesCache(repository.Path, repository.CachePath); err != nil {
			return err
		}
		branch, err := output(repository.Path, "git", "branch", "--show-current")
		if err != nil || branch != repository.Branch {
			return fmt.Errorf("checkout path %q is on branch %q, expected %q", repository.Path, branch, repository.Branch)
		}
		tuneCheckoutPerformance(repository.Path)
		return nil
	} else if statErr != nil && !errors.Is(statErr, os.ErrNotExist) {
		return fmt.Errorf("inspect checkout path: %w", statErr)
	}
	if err := os.MkdirAll(filepath.Dir(repository.Path), 0o755); err != nil {
		return fmt.Errorf("create Workspace directory: %w", err)
	}
	branch, err := defaultBranch(repository.CachePath, spec)
	if err != nil {
		return err
	}
	startPoint := "refs/remotes/origin/" + branch
	if p.BaseRef != "" {
		// A stacked Workspace starts from the blocker's pull request branch.
		// The prepared cache refreshed only the default branch, so fetch the
		// base ref now, inside the same cache lock.
		if err := fetchOrigin(repository.CachePath, p.BaseRef); err != nil {
			return fmt.Errorf("fetch stack base %q: %w", p.BaseRef, err)
		}
		startPoint = "refs/remotes/origin/" + p.BaseRef
	}
	if err := run(repository.CachePath, "git", "worktree", "add", "-b", repository.Branch, repository.Path, startPoint); err != nil {
		return fmt.Errorf("create checkout for repository %q: %w", repositoryName, err)
	}
	tuneCheckoutPerformance(repository.Path)
	return nil
}

func (s *Service) withCacheLock(cachePath string, operation func() error) error {
	lock, err := store.AcquireNamedLockBlocking(s.options.StateDir, "cache", cachePath)
	if err != nil {
		return err
	}
	defer lock.Release()
	return operation()
}

// defaultBranch returns the default branch of one repository. It uses the
// declared branch of the spec first, then reads HEAD of the Repository Cache
// at cachePath.
func defaultBranch(cachePath string, spec domain.RepositorySpec) (string, error) {
	if spec.DefaultBranch != "" {
		return spec.DefaultBranch, nil
	}
	branch, err := output(cachePath, "git", "symbolic-ref", "--short", "HEAD")
	if err != nil {
		return "", fmt.Errorf("find default branch for Repository Cache %q: %w", cachePath, err)
	}
	return branch, nil
}

// refreshCache resolves the default branch of the cache, applies the
// large-repository settings, and refreshes the branch from origin. It then
// runs cache maintenance, because a partial clone gains one lazy-fetch pack
// per checkout, and hundreds of packs make every later Git command slow.
func refreshCache(cachePath string, spec domain.RepositorySpec) error {
	branch, err := defaultBranch(cachePath, spec)
	if err != nil {
		return err
	}
	tuneCachePerformance(cachePath)
	if err := fetchOrigin(cachePath, branch); err != nil {
		return err
	}
	// Maintenance is best effort: a failed repack must not block Workspace
	// preparation, and the next refresh retries it.
	maintainCache(cachePath)
	return nil
}

// maintainCachePackLimit is the pack count that triggers an incremental
// repack of one Repository Cache.
const maintainCachePackLimit = 32

// RepackCeiling is the pack count above which an incremental repack stops
// making progress, because one large base pack never enters the
// batch. Measured on a 20 GiB monorepo cache: 219 packs stayed at 219 after a
// 95-second incremental run, and the run added one more pack. A multi-pack
// index keeps object lookup fast at that pack count, so twt writes one and
// leaves the packs alone. A full repack costs tens of minutes and belongs to
// a deliberate repair, not to Workspace preparation.
const RepackCeiling = 128

// staleTemporaryPackAge is the age at which an interrupted fetch pack
// (objects/pack/tmp_pack_*) counts as garbage.
const staleTemporaryPackAge = time.Hour

// maintainCache keeps one shared Repository Cache fast. It removes stale
// temporary packs from interrupted fetches, packs the ref store, folds loose
// objects into a pack, keeps the commit-graph current, and consolidates
// lazy-fetch packs when the pack count passes the limit.
//
// pack-refs is the task that matters most. A cache that tracks a monorepo
// gains one loose file for each remote branch and tag, and then every Git
// command that enumerates refs opens all of them. Measured on a cache with
// 135,857 loose refs: "git for-each-ref --count=1" took 15.3 seconds before
// pack-refs and 0.02 seconds after, and a single-branch fetch fell from 27
// seconds to 4 seconds. Neither commit-graph nor incremental-repack touches
// the ref store, so a cache without this task degrades without limit.
func maintainCache(cachePath string) {
	sweepStaleTemporaryPacks(cachePath, time.Now())
	packs, err := filepath.Glob(filepath.Join(cachePath, "objects", "pack", "*.pack"))
	if err != nil {
		packs = nil
	}
	for _, command := range maintainCacheCommands(len(packs)) {
		// Maintenance is best effort: a failed task must not block Workspace
		// preparation, and the next refresh retries it. 'twt doctor' reports
		// a cache that maintenance no longer keeps compact.
		_ = run(cachePath, "git", command...)
	}
}

// maintainCacheCommands returns the Git commands that keep one Repository
// Cache with packs pack files fast. The first three always run: they are
// cheap and they hold the ref store, the loose objects, and the commit-graph
// in shape. The pack strategy then depends on the count.
func maintainCacheCommands(packs int) [][]string {
	commands := [][]string{
		{"maintenance", "run", "--quiet", "--task=pack-refs"},
		{"maintenance", "run", "--quiet", "--task=loose-objects"},
		{"maintenance", "run", "--quiet", "--task=commit-graph"},
	}
	switch {
	case packs > RepackCeiling:
		commands = append(commands, []string{"multi-pack-index", "write"})
	case packs > maintainCachePackLimit:
		commands = append(commands, []string{"maintenance", "run", "--quiet", "--task=incremental-repack"})
	}
	return commands
}

// largeCheckoutFileCount is the tracked-file count above which twt turns on
// the built-in file system monitor for a Checkout Lease. Below it the daemon
// costs more than the directory scan that it replaces. Tests lower it.
var largeCheckoutFileCount = 10000

// tuneCachePerformance sets the large-repository Git settings on one shared
// Repository Cache. Every Checkout Lease reads the config of the common
// directory, so one write serves all of them. Index version 4 shrinks the
// index, and the untracked cache removes a full directory scan from
// "git status". It runs on every cache ensure, so it also migrates a cache
// that an older twt created.
func tuneCachePerformance(cachePath string) {
	settings := [][2]string{
		{"core.untrackedCache", "true"},
		{"index.version", "4"},
	}
	for _, setting := range settings {
		// Best effort: an old Git that rejects one setting must not block
		// Workspace preparation.
		_ = run(cachePath, "git", "config", setting[0], setting[1])
	}
}

// tuneCheckoutPerformance turns on the built-in file system monitor for one
// Checkout Lease that holds many files. "git status" then reads daemon events
// instead of calling stat on every tracked file. Measured on a 150,194-file
// monorepo checkout: 1.46 seconds before, 0.14 seconds after. A small
// checkout keeps the daemon off.
func tuneCheckoutPerformance(worktreePath string) {
	files, err := output(worktreePath, "git", "ls-files")
	if err != nil || files == "" {
		return
	}
	if strings.Count(files, "\n")+1 < largeCheckoutFileCount {
		return
	}
	_ = run(worktreePath, "git", "config", "core.fsmonitor", "true")
}

// sweepStaleTemporaryPacks removes objects/pack/tmp_pack_* files that are
// older than staleTemporaryPackAge. An interrupted lazy fetch leaves such a
// file behind, and Git never removes it in a repository without auto gc.
func sweepStaleTemporaryPacks(cachePath string, now time.Time) {
	entries, err := filepath.Glob(filepath.Join(cachePath, "objects", "pack", "tmp_pack_*"))
	if err != nil {
		return
	}
	for _, entry := range entries {
		info, err := os.Stat(entry)
		if err != nil || now.Sub(info.ModTime()) < staleTemporaryPackAge {
			continue
		}
		_ = os.Remove(entry)
	}
}

// fetchOrigin refreshes one branch of the cache from origin. Repository
// Caches keep full commit history because all Workspace branches share them.
func fetchOrigin(cachePath, branch string) error {
	if err := ensureFullHistory(cachePath, branch); err != nil {
		return err
	}
	args := []string{"fetch", "--prune", "--no-tags"}
	args = append(args, "origin", "+refs/heads/"+branch+":refs/remotes/origin/"+branch)
	if err := run(cachePath, "git", args...); err != nil {
		return fmt.Errorf("refresh repository cache from origin: %w", err)
	}
	return nil
}

// ensureFullHistory repairs a cache made by a version of twt that honored a
// Template clone depth. A shallow shared cache can disconnect active Workspace
// branches when its remote-tracking branch moves.
func ensureFullHistory(cachePath, branch string) error {
	value, err := output(cachePath, "git", "rev-parse", "--is-shallow-repository")
	if err != nil {
		return fmt.Errorf("inspect Repository Cache history: %w", err)
	}
	if value != "true" {
		return nil
	}
	refspec := "+refs/heads/" + branch + ":refs/remotes/origin/" + branch
	if err := run(cachePath, "git", "fetch", "--unshallow", "--filter=tree:0", "--prune", "--no-tags", "origin", refspec); err != nil {
		return fmt.Errorf("repair shallow Repository Cache history: %w", err)
	}
	return nil
}

// worktreeUsesCache checks that the checkout at worktreePath is a Git
// worktree of the Repository Cache at cachePath.
func worktreeUsesCache(worktreePath, cachePath string) error {
	commonDir, err := output(worktreePath, "git", "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return fmt.Errorf("checkout path %q is not a Git worktree", worktreePath)
	}
	if !sameDirectory(commonDir, cachePath) {
		return fmt.Errorf("checkout path %q does not use Repository Cache %q", worktreePath, cachePath)
	}
	return nil
}

// sameDirectory reports whether the two paths name the same directory.
func sameDirectory(first, second string) bool {
	firstInfo, firstErr := os.Stat(first)
	secondInfo, secondErr := os.Stat(second)
	return firstErr == nil && secondErr == nil && os.SameFile(firstInfo, secondInfo)
}

// isAncestor reports whether ancestor is reachable from descendant.
func isAncestor(cachePath, ancestor, descendant string) (bool, error) {
	command := exec.Command("git", "merge-base", "--is-ancestor", ancestor, descendant)
	command.Dir = cachePath
	if err := command.Run(); err == nil {
		return true, nil
	} else if exitError, ok := err.(*exec.ExitError); ok && exitError.ExitCode() == 1 {
		return false, nil
	} else {
		return false, fmt.Errorf("compare commits in Repository Cache %q: %w", cachePath, err)
	}
}

func shortCommit(commit string) string {
	if len(commit) > 12 {
		return commit[:12]
	}
	return commit
}

func (s *Service) cachePath(name, url string) string {
	digest := sha256.Sum256([]byte(url))
	return filepath.Join(s.options.DataDir, "caches", name+"-"+hex.EncodeToString(digest[:6])+".git")
}

func repositoryFor(p domain.Workspace, name string) (domain.RepositorySpec, domain.WorkspaceRepository, error) {
	var spec *domain.RepositorySpec
	var repository *domain.WorkspaceRepository
	for index := range p.TemplateSnapshot.Repositories {
		if p.TemplateSnapshot.Repositories[index].Name == name {
			copy := p.TemplateSnapshot.Repositories[index]
			spec = &copy
			break
		}
	}
	for index := range p.Repositories {
		if p.Repositories[index].Name == name {
			copy := p.Repositories[index]
			repository = &copy
			break
		}
	}
	if spec == nil || repository == nil {
		return domain.RepositorySpec{}, domain.WorkspaceRepository{}, fmt.Errorf("repository %q is not in the saved Workspace snapshot", name)
	}
	return *spec, *repository, nil
}

func validateCacheMarker(cachePath, expectedURL string) error {
	data, err := os.ReadFile(filepath.Join(cachePath, "twt-ownership.json"))
	if err != nil {
		return fmt.Errorf("cache %q has no valid twt ownership marker", cachePath)
	}
	var marker struct {
		Owner string `json:"owner"`
		URL   string `json:"url"`
	}
	if err := json.Unmarshal(data, &marker); err != nil || marker.Owner != "twt" || marker.URL != expectedURL {
		return fmt.Errorf("cache %q has a conflicting twt ownership marker", cachePath)
	}
	return nil
}

// remoteProbeTimeout bounds each Git command that reads the remote during a
// publication check.
const remoteProbeTimeout = 10 * time.Second

// remoteGitOutput runs one Git command that reads the remote. It refuses
// interactive credential prompts and stops after remoteProbeTimeout. Tests
// replace it to observe or block remote access.
var remoteGitOutput = func(directory string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), remoteProbeTimeout)
	defer cancel()
	command := exec.CommandContext(ctx, "git", args...)
	command.Dir = directory
	command.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	data, err := command.CombinedOutput()
	if ctx.Err() != nil {
		return "", fmt.Errorf("git %s: twt could not reach the remote in %s", strings.Join(args, " "), remoteProbeTimeout)
	}
	if err != nil {
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(data)))
	}
	return strings.TrimSpace(string(data)), nil
}

// branchPublished reports whether the branch commits are safe on the remote.
// It returns unknown=true when twt could not read the remote, so the caller
// can refuse instead of a silent pass. The check reads the remote-tracking
// refs of the local cache first and uses at most one remote round trip:
// one ls-remote probe answers both whether the branch is on the remote and
// where the remote default branch points. A second round trip (a targeted
// fetch of the default branch) happens only when the cache does not contain
// the remote default tip.
func branchPublished(cachePath, branch string) (published bool, unknown bool, err error) {
	branchRef := "refs/heads/" + branch
	published, err = branchOnRemoteRefs(cachePath, branch)
	if err != nil || published {
		return published, false, err
	}
	defaultRef := ""
	if defaultBranch, headErr := output(cachePath, "git", "symbolic-ref", "--short", "HEAD"); headErr == nil && defaultBranch != "" {
		defaultRef = "refs/heads/" + defaultBranch
	}
	probe := []string{"ls-remote", "--heads", "origin", branchRef}
	if defaultRef != "" && defaultRef != branchRef {
		probe = append(probe, defaultRef)
	}
	heads, lsErr := remoteGitOutput(cachePath, probe...)
	if lsErr != nil {
		return false, true, nil
	}
	remoteDefaultTip := ""
	for _, line := range strings.Split(heads, "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 {
			continue
		}
		if fields[1] == branchRef {
			return true, false, nil
		}
		if fields[1] == defaultRef {
			remoteDefaultTip = fields[0]
		}
	}
	if remoteDefaultTip == "" {
		return false, false, nil
	}
	exists, err := commitExists(cachePath, remoteDefaultTip)
	if err != nil {
		return false, false, err
	}
	if !exists {
		// The cache is stale. Fetch the remote default branch once, then
		// check again. A fetch failure keeps the ls-remote result: the
		// branch is not on the remote.
		defaultBranch := strings.TrimPrefix(defaultRef, "refs/heads/")
		if _, fetchErr := remoteGitOutput(cachePath, "fetch", "--no-tags", "origin", "+"+defaultRef+":refs/remotes/origin/"+defaultBranch); fetchErr != nil {
			return false, false, nil
		}
		exists, err = commitExists(cachePath, remoteDefaultTip)
		if err != nil || !exists {
			return false, false, err
		}
	}
	published, err = isAncestor(cachePath, branchRef, remoteDefaultTip)
	if err != nil {
		return false, false, fmt.Errorf("compare branch %q with the remote default tip %q: %w", branch, shortCommit(remoteDefaultTip), err)
	}
	return published, false, nil
}

// branchOnRemoteRefs reports whether a remote-tracking ref in the local cache
// already contains the branch. Only refs/remotes/* count as published: a
// local sibling branch can never vouch for the commits. It does not read the
// remote. One negated rev-list answers reachability in a single traversal: a
// monorepo cache tracks thousands of remote branches, so a walk per ref does
// not scale.
func branchOnRemoteRefs(cachePath, branch string) (bool, error) {
	branchRef := "refs/heads/" + branch
	missing, err := output(cachePath, "git", "rev-list", "-1", branchRef, "--not", "--remotes")
	if err != nil {
		return false, fmt.Errorf("compare branch %q with the remote-tracking refs: %w", branch, err)
	}
	return strings.TrimSpace(missing) == "", nil
}

// commitExists reports whether the commit is in the local cache.
func commitExists(cachePath, commit string) (bool, error) {
	command := exec.Command("git", "cat-file", "-e", commit+"^{commit}")
	command.Dir = cachePath
	if err := command.Run(); err == nil {
		return true, nil
	} else if _, ok := err.(*exec.ExitError); ok {
		return false, nil
	} else {
		return false, fmt.Errorf("inspect commit %q in Repository Cache %q: %w", shortCommit(commit), cachePath, err)
	}
}

func refExists(cachePath, ref string) (bool, error) {
	command := exec.Command("git", "show-ref", "--verify", "--quiet", ref)
	command.Dir = cachePath
	if err := command.Run(); err == nil {
		return true, nil
	} else if exitError, ok := err.(*exec.ExitError); ok && exitError.ExitCode() == 1 {
		return false, nil
	} else {
		return false, fmt.Errorf("inspect Git ref %q: %w", ref, err)
	}
}
