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

// The clone filter of the Template controls the shared Repository Cache. An
// empty filter clones the full object set, so checkouts never lazy-fetch.
func TestEnsureCacheHonorsTheTemplateCloneFilter(t *testing.T) {
	source := gitTestRepository(t)
	for _, test := range []struct {
		name   string
		filter string
	}{
		{name: "full clone without a filter", filter: ""},
		{name: "partial clone with a filter", filter: "blob:none"},
	} {
		t.Run(test.name, func(t *testing.T) {
			service := NewService(Options{StateDir: t.TempDir(), DataDir: t.TempDir()})
			spec := domain.RepositorySpec{Name: "app", Clone: domain.CloneSpec{URL: source, Filter: test.filter}}
			cachePath := service.cachePath(spec.Name, spec.Clone.URL)
			if err := service.ensureCache(spec, cachePath); err != nil {
				t.Fatalf("ensureCache() error = %v", err)
			}
			command := exec.Command("git", "config", "--get", "remote.origin.partialclonefilter")
			command.Dir = cachePath
			data, err := command.CombinedOutput()
			got := strings.TrimSpace(string(data))
			if test.filter == "" {
				if err == nil {
					t.Fatalf("full clone has partial clone filter %q", got)
				}
				return
			}
			if err != nil || got != test.filter {
				t.Fatalf("partial clone filter = %q, %v; want %q", got, err, test.filter)
			}
		})
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
