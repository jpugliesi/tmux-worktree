package workspace

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
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
