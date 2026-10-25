package workspace

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jpugliesi/tmux-worktree/internal/domain"
)

func gitTestRepository(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	directory := t.TempDir()
	testGit(t, directory, "init", "-q", "-b", "main")
	testGit(t, directory, "config", "user.name", "twt test")
	testGit(t, directory, "config", "user.email", "test@example.com")
	testGit(t, directory, "commit", "-q", "--allow-empty", "-m", "first")
	return directory
}

func testGit(t *testing.T, directory string, args ...string) string {
	t.Helper()
	command := exec.Command("git", args...)
	command.Dir = directory
	data, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, data)
	}
	return strings.TrimSpace(string(data))
}

// interceptRemoteGit replaces the remote Git runner for one test and records
// each remote command.
func interceptRemoteGit(t *testing.T, stub func(directory string, args ...string) (string, error)) *[]string {
	t.Helper()
	original := remoteGitOutput
	t.Cleanup(func() { remoteGitOutput = original })
	calls := &[]string{}
	remoteGitOutput = func(directory string, args ...string) (string, error) {
		*calls = append(*calls, strings.Join(args, " "))
		return stub(directory, args...)
	}
	return calls
}

func TestBranchPublishedUsesRemoteTrackingRefsWithoutARemoteProbe(t *testing.T) {
	repository := gitTestRepository(t)
	testGit(t, repository, "branch", "feature")
	testGit(t, repository, "update-ref", "refs/remotes/origin/main", "HEAD")
	calls := interceptRemoteGit(t, func(directory string, args ...string) (string, error) {
		return "", fmt.Errorf("the test forbids remote access")
	})
	published, unknown, err := branchPublished(repository, "feature")
	if err != nil {
		t.Fatalf("branchPublished() error = %v", err)
	}
	if !published || unknown {
		t.Fatalf("branchPublished() = published %v, unknown %v; want published, known", published, unknown)
	}
	if len(*calls) != 0 {
		t.Fatalf("the local fast path read the remote: %v", *calls)
	}
}

// A local sibling branch that contains the commits must not count as
// published. Only remote-tracking refs and the remote itself can vouch for
// the commits.
func TestBranchPublishedIgnoresLocalSiblingBranches(t *testing.T) {
	repository := gitTestRepository(t)
	testGit(t, repository, "switch", "-qc", "feature")
	testGit(t, repository, "commit", "-q", "--allow-empty", "-m", "unpublished")
	testGit(t, repository, "branch", "sibling")
	testGit(t, repository, "switch", "-q", "main")
	calls := interceptRemoteGit(t, func(directory string, args ...string) (string, error) {
		if args[0] != "ls-remote" {
			return "", fmt.Errorf("the test permits only one ls-remote probe, got git %s", strings.Join(args, " "))
		}
		// The remote has neither the branch nor a default tip.
		return "", nil
	})
	published, unknown, err := branchPublished(repository, "feature")
	if err != nil {
		t.Fatalf("branchPublished() error = %v", err)
	}
	if published || unknown {
		t.Fatalf("branchPublished() = published %v, unknown %v; want unpublished, known", published, unknown)
	}
	if len(*calls) != 1 {
		t.Fatalf("remote round trips = %v, want one ls-remote probe", *calls)
	}
}

func TestBranchPublishedComparesTheRemoteDefaultTipWithoutAFetch(t *testing.T) {
	repository := gitTestRepository(t)
	testGit(t, repository, "switch", "-qc", "feature")
	testGit(t, repository, "commit", "-q", "--allow-empty", "-m", "merged on the remote")
	tip := testGit(t, repository, "rev-parse", "HEAD")
	testGit(t, repository, "switch", "-q", "main")
	calls := interceptRemoteGit(t, func(directory string, args ...string) (string, error) {
		if args[0] != "ls-remote" {
			return "", fmt.Errorf("the test permits only one ls-remote probe, got git %s", strings.Join(args, " "))
		}
		// The branch is not on the remote; the remote default tip is a
		// commit the cache already has.
		return tip + "\trefs/heads/main", nil
	})
	published, unknown, err := branchPublished(repository, "feature")
	if err != nil {
		t.Fatalf("branchPublished() error = %v", err)
	}
	if !published || unknown {
		t.Fatalf("branchPublished() = published %v, unknown %v; want published, known", published, unknown)
	}
	if len(*calls) != 1 {
		t.Fatalf("remote round trips = %v, want one ls-remote probe", *calls)
	}
}

func TestBranchPublishedReportsUnknownWhenTheRemoteIsUnreachable(t *testing.T) {
	repository := gitTestRepository(t)
	testGit(t, repository, "remote", "add", "origin", filepath.Join(t.TempDir(), "missing.git"))
	testGit(t, repository, "switch", "-qc", "feature")
	testGit(t, repository, "commit", "-q", "--allow-empty", "-m", "unpublished")
	testGit(t, repository, "switch", "-q", "main")
	published, unknown, err := branchPublished(repository, "feature")
	if err != nil {
		t.Fatalf("branchPublished() error = %v", err)
	}
	if published || !unknown {
		t.Fatalf("branchPublished() = published %v, unknown %v; want unpublished, unknown", published, unknown)
	}
}

// A new cache starts with one commit and deepens to the full history in
// bounded rounds, because the servers drop one long full-history transfer.
func TestEnsureCacheDeepensANewCacheToTheFullHistory(t *testing.T) {
	source := gitTestRepository(t)
	for _, name := range []string{"second.txt", "third.txt", "fourth.txt"} {
		commitCreateTestFile(t, source, name)
	}
	previous := historyDeepenStep
	historyDeepenStep = 1
	t.Cleanup(func() { historyDeepenStep = previous })
	service := NewService(Options{StateDir: t.TempDir(), DataDir: t.TempDir()})
	spec := domain.RepositorySpec{Name: "app", Clone: domain.CloneSpec{URL: "file://" + source}, DefaultBranch: "main"}
	cachePath := service.cachePath(spec.Name, spec.Clone.URL)
	if err := service.ensureCache(spec, cachePath); err != nil {
		t.Fatalf("ensureCache() error = %v", err)
	}
	if shallow := testGitOutput(t, cachePath, "rev-parse", "--is-shallow-repository"); shallow != "false" {
		t.Fatalf("the new cache is still shallow: %s", shallow)
	}
	if count := testGitOutput(t, cachePath, "rev-list", "--count", "refs/remotes/origin/main"); count != testGitOutput(t, source, "rev-list", "--count", "main") {
		t.Fatalf("the new cache has %s commits on origin/main", count)
	}
}

// A cache that an older twt or another tool made partial, or that aborts
// slow fetches, loses those settings on the next ensure.
func TestEnsureCacheRemovesTheSettingsThatGrowTheCache(t *testing.T) {
	source := gitTestRepository(t)
	service := NewService(Options{StateDir: t.TempDir(), DataDir: t.TempDir()})
	spec := domain.RepositorySpec{Name: "app", Clone: domain.CloneSpec{URL: source}}
	cachePath := service.cachePath(spec.Name, spec.Clone.URL)
	if err := service.ensureCache(spec, cachePath); err != nil {
		t.Fatalf("ensureCache() error = %v", err)
	}
	for _, setting := range [][2]string{
		{"remote.origin.partialclonefilter", "tree:0"},
		{"http.lowSpeedLimit", "1000"},
		{"http.lowSpeedTime", "120"},
	} {
		testGitOutput(t, cachePath, "config", setting[0], setting[1])
	}
	if err := service.ensureCache(spec, cachePath); err != nil {
		t.Fatalf("ensureCache() error = %v", err)
	}
	for _, key := range []string{"remote.origin.partialclonefilter", "http.lowSpeedLimit", "http.lowSpeedTime"} {
		command := exec.Command("git", "config", "--get", key)
		command.Dir = cachePath
		if data, err := command.CombinedOutput(); err == nil {
			t.Fatalf("the cache keeps %s = %s", key, strings.TrimSpace(string(data)))
		}
	}
}

// An interrupted lazy fetch leaves a temporary pack behind, and nothing else
// removes it in a repository without auto gc.
func TestSweepStaleTemporaryPacksRemovesOnlyOldPacks(t *testing.T) {
	cachePath := t.TempDir()
	packDirectory := filepath.Join(cachePath, "objects", "pack")
	if err := os.MkdirAll(packDirectory, 0o755); err != nil {
		t.Fatal(err)
	}
	stale := filepath.Join(packDirectory, "tmp_pack_stale")
	fresh := filepath.Join(packDirectory, "tmp_pack_fresh")
	keep := filepath.Join(packDirectory, "pack-keep.pack")
	for _, path := range []string{stale, fresh, keep} {
		if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	old := time.Now().Add(-2 * staleTemporaryPackAge)
	if err := os.Chtimes(stale, old, old); err != nil {
		t.Fatal(err)
	}
	sweepStaleTemporaryPacks(cachePath, time.Now())
	if _, err := os.Stat(stale); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the sweep kept the stale temporary pack: %v", err)
	}
	for _, path := range []string{fresh, keep} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("the sweep removed %q: %v", path, err)
		}
	}
}

// maintainCache must always pack the ref store. A cache that tracks a
// monorepo gains one loose file for each remote branch, and then every Git
// command that enumerates refs opens all of them.
func TestMaintainCachePacksTheRefStore(t *testing.T) {
	repository := gitTestRepository(t)
	gitDirectory := testGit(t, repository, "rev-parse", "--absolute-git-dir")
	for index := 0; index < 40; index++ {
		testGit(t, repository, "update-ref", fmt.Sprintf("refs/remotes/origin/branch-%d", index), "HEAD")
	}
	if _, err := os.Stat(filepath.Join(gitDirectory, "packed-refs")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the test repository already has a packed ref store: %v", err)
	}

	maintainCache(repository)

	if _, err := os.Stat(filepath.Join(gitDirectory, "packed-refs")); err != nil {
		t.Fatalf("maintainCache left the ref store unpacked: %v", err)
	}
	loose := 0
	_ = filepath.WalkDir(filepath.Join(gitDirectory, "refs"), func(_ string, entry os.DirEntry, err error) error {
		if err == nil && !entry.IsDir() {
			loose++
		}
		return nil
	})
	if loose != 0 {
		t.Fatalf("maintainCache left %d loose refs, want zero", loose)
	}
}

// The create path runs only the cheap tasks. A repack can take hours on a
// bloated cache, so only the daemon runs it, outside the cache lock.
func TestMaintainCacheCommandsNeverRepack(t *testing.T) {
	want := []string{
		"maintenance run --quiet --task=pack-refs",
		"maintenance run --quiet --task=loose-objects",
		"maintenance run --quiet --task=commit-graph",
	}
	commands := maintainCacheCommands()
	if len(commands) != len(want) {
		t.Fatalf("maintainCacheCommands() = %v, want %v", commands, want)
	}
	for index := range want {
		if got := strings.Join(commands[index], " "); got != want[index] {
			t.Fatalf("command %d = %q, want %q", index, got, want[index])
		}
	}
}

func TestTuneCheckoutPerformanceFollowsTheTrackedFileCount(t *testing.T) {
	tests := []struct {
		name      string
		threshold int
		want      string
	}{
		{name: "small checkout", threshold: 100, want: ""},
		{name: "large checkout", threshold: 2, want: "true"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			repository := gitTestRepository(t)
			for _, name := range []string{"a.txt", "b.txt", "c.txt"} {
				if err := os.WriteFile(filepath.Join(repository, name), []byte("x"), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			testGit(t, repository, "add", ".")
			testGit(t, repository, "commit", "-q", "-m", "files")
			original := largeCheckoutFileCount
			largeCheckoutFileCount = test.threshold
			t.Cleanup(func() { largeCheckoutFileCount = original })

			tuneCheckoutPerformance(repository)

			got, err := output(repository, "git", "config", "--get", "core.fsmonitor")
			if test.want == "" {
				if err == nil {
					t.Fatalf("tuneCheckoutPerformance set core.fsmonitor = %q on a small checkout", got)
				}
				return
			}
			if err != nil || got != test.want {
				t.Fatalf("core.fsmonitor = %q, %v; want %q", got, err, test.want)
			}
		})
	}
}
