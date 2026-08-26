package ticket

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jpugliesi/tmux-worktree/internal/clierr"
	"github.com/jpugliesi/tmux-worktree/internal/domain"
)

// testGit runs one git command directly, outside runTicketGit, so tests can
// simulate another machine even while an interceptor is installed.
func testGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	command := exec.Command("git", args...)
	command.Dir = dir
	command.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s in %s: %v: %s", strings.Join(args, " "), dir, err, output)
	}
	return strings.TrimSpace(string(output))
}

type syncEnv struct {
	remote   string
	cloneA   string
	cloneB   string
	serviceA *Service
	serviceB *Service
	warnA    *syncWarnings
	warnB    *syncWarnings
}

type syncWarnings struct {
	mu       sync.Mutex
	messages []string
}

func (w *syncWarnings) logf(format string, a ...any) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.messages = append(w.messages, strings.TrimSpace(strings.ReplaceAll(format, "%v", "")))
}

func (w *syncWarnings) count() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return len(w.messages)
}

// newSyncEnv builds one bare remote and two synced clones, and bootstraps the
// Tickets home from clone A.
func newSyncEnv(t *testing.T) *syncEnv {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	// Widen the git timeouts: package tests run in parallel with the whole
	// module suite and a loaded machine can push a file fetch past 10s.
	originalRemote, originalLocal := syncRemoteTimeout, syncLocalTimeout
	syncRemoteTimeout, syncLocalTimeout = 120*time.Second, 60*time.Second
	t.Cleanup(func() { syncRemoteTimeout, syncLocalTimeout = originalRemote, originalLocal })
	root := t.TempDir()
	globalConfig := filepath.Join(root, "gitconfig")
	if err := os.WriteFile(globalConfig, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_CONFIG_GLOBAL", globalConfig)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	testGit(t, root, "init", "-q", "--bare", "-b", "main", "remote.git")
	remote := filepath.Join(root, "remote.git")
	env := &syncEnv{remote: remote, warnA: &syncWarnings{}, warnB: &syncWarnings{}}
	env.cloneA = cloneSyncRepo(t, root, remote, "a")
	env.cloneB = cloneSyncRepo(t, root, remote, "b")
	env.serviceA = newSyncService(t, env.cloneA, env.warnA)
	env.serviceB = newSyncService(t, env.cloneB, env.warnB)
	if _, err := env.serviceA.Init(false); err != nil {
		t.Fatalf("bootstrap init: %v", err)
	}
	return env
}

func cloneSyncRepo(t *testing.T, root, remote, name string) string {
	t.Helper()
	testGit(t, root, "clone", "-q", remote, name)
	dir := filepath.Join(root, name)
	// Resolve symlinks so the directory compares equal to the toplevel that
	// git reports inside the sync code.
	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	testGit(t, resolved, "symbolic-ref", "HEAD", "refs/heads/main")
	testGit(t, resolved, "config", "user.name", "twt test")
	testGit(t, resolved, "config", "user.email", "twt@test.invalid")
	return resolved
}

func newSyncService(t *testing.T, clone string, warnings *syncWarnings) *Service {
	t.Helper()
	service := NewService(Options{
		Home:     filepath.Join(clone, "tickets"),
		StateDir: t.TempDir(),
		Sync:     SyncOptions{Mode: SyncModeGit},
		Logf:     warnings.logf,
	})
	service.now = func() time.Time {
		return time.Date(2026, 8, 25, 12, 0, 0, 0, time.UTC)
	}
	return service
}

func (e *syncEnv) createTicket(t *testing.T, service *Service, slug string) {
	t.Helper()
	_, err := service.Create(CreateRequest{
		Title:  slug,
		Slug:   slug,
		Status: domain.TicketReadyForAgent,
	}, false)
	if err != nil {
		t.Fatalf("create %s: %v", slug, err)
	}
}

// gitCall is one recorded runTicketGit invocation.
type gitCall struct {
	dir  string
	args []string
}

// interceptTicketGit swaps runTicketGit for a hook and restores it on
// cleanup. The hook receives the original runner.
func interceptTicketGit(t *testing.T, hook func(original func(time.Duration, string, ...string) (string, error), timeout time.Duration, dir string, args ...string) (string, error)) {
	t.Helper()
	original := runTicketGit
	runTicketGit = func(timeout time.Duration, dir string, args ...string) (string, error) {
		return hook(original, timeout, dir, args...)
	}
	t.Cleanup(func() { runTicketGit = original })
}

// recordTicketGit records every runTicketGit call and delegates.
func recordTicketGit(t *testing.T) *[]gitCall {
	t.Helper()
	calls := &[]gitCall{}
	var mu sync.Mutex
	interceptTicketGit(t, func(original func(time.Duration, string, ...string) (string, error), timeout time.Duration, dir string, args ...string) (string, error) {
		mu.Lock()
		*calls = append(*calls, gitCall{dir: dir, args: args})
		mu.Unlock()
		return original(timeout, dir, args...)
	})
	return calls
}

func callsWithSubcommand(calls []gitCall, subcommand string) []gitCall {
	var matched []gitCall
	for _, call := range calls {
		if len(call.args) > 0 && call.args[0] == subcommand {
			matched = append(matched, call)
		}
	}
	return matched
}

func remoteLogSubjects(t *testing.T, env *syncEnv) []string {
	t.Helper()
	output := testGit(t, env.remote, "log", "--format=%s", "main")
	if output == "" {
		return nil
	}
	return strings.Split(output, "\n")
}

func TestSyncDisabledMakesZeroGitCalls(t *testing.T) {
	calls := recordTicketGit(t)
	service, _ := newTestService(t)
	if _, err := service.Init(false); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Create(CreateRequest{Title: "no git", Status: domain.TicketReadyForAgent}, false); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Claim("no-git", "worker", false); err != nil {
		t.Fatal(err)
	}
	if len(*calls) != 0 {
		t.Fatalf("git calls with sync disabled: %v", *calls)
	}
}

func TestSyncClaimIsACrossCloneCompareAndSwap(t *testing.T) {
	env := newSyncEnv(t)
	env.createTicket(t, env.serviceA, "fix-auth")

	// Clone B pulls the ticket during its own claim and wins it.
	if _, err := env.serviceB.Claim("fix-auth", "worker-b", false); err != nil {
		t.Fatalf("claim from B: %v", err)
	}
	// Clone A pulls B's claim during reconcile and loses with locked.
	_, err := env.serviceA.Claim("fix-auth", "worker-a", false)
	if clierr.CodeOf(err) != clierr.Locked {
		t.Fatalf("claim from A error = %v, want locked", err)
	}
}

func TestSyncReplaysARejectedPushAndSucceeds(t *testing.T) {
	env := newSyncEnv(t)
	env.createTicket(t, env.serviceA, "fix-auth")
	env.createTicket(t, env.serviceA, "other-ticket")

	// Reject B's first push by moving the remote with an unrelated commit
	// after B reconciled.
	var once sync.Once
	interceptTicketGit(t, func(original func(time.Duration, string, ...string) (string, error), timeout time.Duration, dir string, args ...string) (string, error) {
		if len(args) > 0 && args[0] == "push" && dir == env.cloneB {
			once.Do(func() {
				path := filepath.Join(env.cloneA, "tickets", "other-ticket.md")
				content, err := os.ReadFile(path)
				if err != nil {
					t.Error(err)
					return
				}
				if err := os.WriteFile(path, append(content, []byte("\nRemote note.\n")...), 0o644); err != nil {
					t.Error(err)
					return
				}
				testGit(t, env.cloneA, "add", "-A", "--", "tickets")
				testGit(t, env.cloneA, "commit", "-q", "-m", "other machine: edit other-ticket")
				testGit(t, env.cloneA, "push", "-q", "origin", "main")
			})
		}
		return original(timeout, dir, args...)
	})

	ticket, err := env.serviceB.Claim("fix-auth", "worker-b", false)
	if err != nil {
		t.Fatalf("claim with replay: %v", err)
	}
	if ticket.ClaimedBy != "worker-b" {
		t.Fatalf("ClaimedBy = %q, want worker-b", ticket.ClaimedBy)
	}
	subjects := remoteLogSubjects(t, env)
	if subjects[0] != "twt: claim fix-auth (as worker-b)" || subjects[1] != "other machine: edit other-ticket" {
		t.Fatalf("remote log = %v", subjects)
	}
}

func TestSyncReplayLosesACompetingClaimWithLocked(t *testing.T) {
	env := newSyncEnv(t)
	env.createTicket(t, env.serviceA, "fix-auth")
	// B pulls once so its clone has the ticket before the intercept.
	testGit(t, env.cloneB, "pull", "-q", "origin", "main")

	// After B reconciles and commits its claim, another machine claims the
	// same ticket by hand and pushes first.
	var once sync.Once
	interceptTicketGit(t, func(original func(time.Duration, string, ...string) (string, error), timeout time.Duration, dir string, args ...string) (string, error) {
		if len(args) > 0 && args[0] == "push" && dir == env.cloneB {
			once.Do(func() {
				testGit(t, env.cloneA, "pull", "-q", "origin", "main")
				path := filepath.Join(env.cloneA, "tickets", "fix-auth.md")
				content, err := os.ReadFile(path)
				if err != nil {
					t.Error(err)
					return
				}
				updated := strings.Replace(string(content), "claimed_by: null", "claimed_by: worker-a", 1)
				updated = strings.Replace(updated, "claimed_by:\n", "claimed_by: worker-a\n", 1)
				if err := os.WriteFile(path, []byte(updated), 0o644); err != nil {
					t.Error(err)
					return
				}
				testGit(t, env.cloneA, "add", "-A", "--", "tickets")
				testGit(t, env.cloneA, "commit", "-q", "-m", "other machine: claim fix-auth")
				testGit(t, env.cloneA, "push", "-q", "origin", "main")
			})
		}
		return original(timeout, dir, args...)
	})

	_, err := env.serviceB.Claim("fix-auth", "worker-b", false)
	if clierr.CodeOf(err) != clierr.Locked {
		t.Fatalf("replayed claim error = %v, want locked", err)
	}
	// The rejected claim commit must not survive locally.
	if ahead := testGit(t, env.cloneB, "rev-list", "--count", "origin/main..HEAD"); ahead != "0" {
		t.Fatalf("clone B is ahead by %s commits after a lost claim", ahead)
	}
}

func TestSyncDuplicateCreateAcrossClonesReportsAlreadyExists(t *testing.T) {
	env := newSyncEnv(t)
	env.createTicket(t, env.serviceA, "fix-auth")
	_, err := env.serviceB.Create(CreateRequest{Title: "fix-auth", Slug: "fix-auth"}, false)
	if clierr.CodeOf(err) != clierr.AlreadyExists {
		t.Fatalf("duplicate create error = %v, want already_exists", err)
	}
}

func TestSyncOfflineNonClaimCommitsLocallyAndWarns(t *testing.T) {
	env := newSyncEnv(t)
	env.createTicket(t, env.serviceA, "fix-auth")

	interceptTicketGit(t, func(original func(time.Duration, string, ...string) (string, error), timeout time.Duration, dir string, args ...string) (string, error) {
		if len(args) > 0 && (args[0] == "fetch" || args[0] == "push") {
			return "", errors.New("network is unreachable")
		}
		return original(timeout, dir, args...)
	})

	if _, err := env.serviceA.Set("fix-auth", SetRequest{Priority: 3, PrioritySet: true}, false); err != nil {
		t.Fatalf("offline set: %v", err)
	}
	if ahead := testGit(t, env.cloneA, "rev-list", "--count", "origin/main..HEAD"); ahead != "1" {
		t.Fatalf("offline set left %s local commits, want 1", ahead)
	}
	if env.warnA.count() < 2 {
		t.Fatalf("offline set warnings = %d, want fetch and push warnings", env.warnA.count())
	}
}

func TestSyncOfflineClaimFailsWithPreconditionAndRollsBack(t *testing.T) {
	env := newSyncEnv(t)
	env.createTicket(t, env.serviceA, "fix-auth")

	interceptTicketGit(t, func(original func(time.Duration, string, ...string) (string, error), timeout time.Duration, dir string, args ...string) (string, error) {
		if len(args) > 0 && args[0] == "fetch" {
			return "", errors.New("network is unreachable")
		}
		return original(timeout, dir, args...)
	})

	_, err := env.serviceA.Claim("fix-auth", "worker-a", false)
	if clierr.CodeOf(err) != clierr.PreconditionFailed {
		t.Fatalf("offline claim error = %v, want precondition_failed", err)
	}
	if ahead := testGit(t, env.cloneA, "rev-list", "--count", "origin/main..HEAD"); ahead != "0" {
		t.Fatalf("offline claim left %s local commits, want 0", ahead)
	}
}

func TestSyncRejectedPushOnClaimRollsBackTheLocalCommit(t *testing.T) {
	env := newSyncEnv(t)
	env.createTicket(t, env.serviceA, "fix-auth")

	interceptTicketGit(t, func(original func(time.Duration, string, ...string) (string, error), timeout time.Duration, dir string, args ...string) (string, error) {
		if len(args) > 0 && args[0] == "push" {
			return "", errors.New("remote hung up unexpectedly")
		}
		return original(timeout, dir, args...)
	})

	_, err := env.serviceA.Claim("fix-auth", "worker-a", false)
	if clierr.CodeOf(err) != clierr.PreconditionFailed {
		t.Fatalf("failed push claim error = %v, want precondition_failed", err)
	}
	if ahead := testGit(t, env.cloneA, "rev-list", "--count", "origin/main..HEAD"); ahead != "0" {
		t.Fatalf("failed push left %s local commits, want 0", ahead)
	}
	ticket, err := env.serviceA.Resolve("fix-auth")
	if err != nil {
		t.Fatal(err)
	}
	if ticket.ClaimedBy != "" {
		t.Fatalf("ClaimedBy = %q after rollback, want empty", ticket.ClaimedBy)
	}
}

func TestSyncSkipWriteAndDryRunNeverCommitOrPush(t *testing.T) {
	env := newSyncEnv(t)
	env.createTicket(t, env.serviceA, "fix-auth")
	if _, err := env.serviceA.Claim("fix-auth", "worker-a", false); err != nil {
		t.Fatal(err)
	}

	calls := recordTicketGit(t)
	// Same-claimant claim is a skipWrite no-op.
	if _, err := env.serviceA.Claim("fix-auth", "worker-a", false); err != nil {
		t.Fatal(err)
	}
	// Dry run pulls best-effort and never writes.
	if _, err := env.serviceA.Set("fix-auth", SetRequest{Priority: 1, PrioritySet: true}, true); err != nil {
		t.Fatal(err)
	}
	if commits := callsWithSubcommand(*calls, "commit"); len(commits) != 0 {
		t.Fatalf("commit calls = %v, want none", commits)
	}
	if pushes := callsWithSubcommand(*calls, "push"); len(pushes) != 0 {
		t.Fatalf("push calls = %v, want none", pushes)
	}
	if fetches := callsWithSubcommand(*calls, "fetch"); len(fetches) == 0 {
		t.Fatal("no fetch calls recorded, want at least the dry-run fetch")
	}
}

func TestSyncSweepsManualEditsBeforeTheMutationCommit(t *testing.T) {
	env := newSyncEnv(t)
	env.createTicket(t, env.serviceA, "fix-auth")

	path := filepath.Join(env.cloneA, "tickets", "fix-auth.md")
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(content, []byte("\nHand edit.\n")...), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := env.serviceA.Comment("fix-auth", "Progress note.", false); err != nil {
		t.Fatal(err)
	}
	subjects := remoteLogSubjects(t, env)
	if subjects[0] != "twt: comment on fix-auth" || subjects[1] != "twt: sync manual edits" {
		t.Fatalf("remote log = %v", subjects)
	}
}

func TestSyncNeverCommitsAtomicWriteTempFiles(t *testing.T) {
	env := newSyncEnv(t)
	stray := filepath.Join(env.cloneA, "tickets", ".twt-write-stray")
	if err := os.WriteFile(stray, []byte("crash leftover"), 0o644); err != nil {
		t.Fatal(err)
	}
	env.createTicket(t, env.serviceA, "fix-auth")
	tracked := testGit(t, env.cloneA, "ls-files", "--", "tickets")
	if strings.Contains(tracked, ".twt-write-") {
		t.Fatalf("temp file committed: %s", tracked)
	}
}

func TestSyncRepairIsOneCommit(t *testing.T) {
	env := newSyncEnv(t)
	env.createTicket(t, env.serviceA, "one")
	env.createTicket(t, env.serviceA, "two")
	// Close both by hand so the files are mislocated in the active tree.
	for _, slug := range []string{"one", "two"} {
		path := filepath.Join(env.cloneA, "tickets", slug+".md")
		content, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		updated := strings.Replace(string(content), "status: ready-for-agent", "status: done", 1)
		if err := os.WriteFile(path, []byte(updated), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	result, err := env.serviceA.Repair(false)
	if err != nil {
		t.Fatalf("repair: %v", err)
	}
	if result.MovedCount != 2 {
		t.Fatalf("MovedCount = %d, want 2", result.MovedCount)
	}
	subjects := remoteLogSubjects(t, env)
	if subjects[0] != "twt: repair" || subjects[1] != "twt: sync manual edits" {
		t.Fatalf("remote log = %v", subjects)
	}
}

func TestSyncDivergedConflictAbortsTheRebaseAndReportsUnsafeState(t *testing.T) {
	env := newSyncEnv(t)
	env.createTicket(t, env.serviceA, "fix-auth")
	// Warm clone B, then take both clones offline and edit the same line.
	testGit(t, env.cloneB, "pull", "-q", "origin", "main")
	offline := true
	interceptTicketGit(t, func(original func(time.Duration, string, ...string) (string, error), timeout time.Duration, dir string, args ...string) (string, error) {
		if offline && len(args) > 0 && (args[0] == "fetch" || args[0] == "push") {
			return "", errors.New("network is unreachable")
		}
		return original(timeout, dir, args...)
	})
	if _, err := env.serviceA.Set("fix-auth", SetRequest{Priority: 3, PrioritySet: true}, false); err != nil {
		t.Fatal(err)
	}
	if _, err := env.serviceB.Set("fix-auth", SetRequest{Priority: 4, PrioritySet: true}, false); err != nil {
		t.Fatal(err)
	}
	offline = false
	// A pushes its offline commit first.
	if _, err := env.serviceA.Comment("fix-auth", "Back online.", false); err != nil {
		t.Fatal(err)
	}
	// B's next mutation must rebase its offline commit and conflict.
	_, err := env.serviceB.Comment("fix-auth", "Also back online.", false)
	if clierr.CodeOf(err) != clierr.UnsafeState {
		t.Fatalf("diverged conflict error = %v, want unsafe_state", err)
	}
	// The rebase must not stay in progress.
	if status := testGit(t, env.cloneB, "status", "--porcelain"); strings.Contains(status, "UU") {
		t.Fatalf("conflict left in the tree: %s", status)
	}
	gitDir := testGit(t, env.cloneB, "rev-parse", "--git-dir")
	if _, err := os.Stat(filepath.Join(env.cloneB, gitDir, "rebase-merge")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("rebase still in progress after abort")
	}
}

func TestSyncConcurrentClaimsHaveExactlyOneWinner(t *testing.T) {
	env := newSyncEnv(t)
	env.createTicket(t, env.serviceA, "race-slug")

	var group sync.WaitGroup
	results := make([]error, 2)
	claimants := []string{"worker-a", "worker-b"}
	services := []*Service{env.serviceA, env.serviceB}
	for i, service := range services {
		group.Add(1)
		go func() {
			defer group.Done()
			_, results[i] = service.Claim("race-slug", claimants[i], false)
		}()
	}
	group.Wait()

	winners := 0
	for _, err := range results {
		switch {
		case err == nil:
			winners++
		case clierr.CodeOf(err) == clierr.Locked:
		default:
			t.Fatalf("unexpected race error: %v", err)
		}
	}
	if winners != 1 {
		t.Fatalf("winners = %d, want exactly 1 (results: %v)", winners, results)
	}
}

func TestServiceSyncPushesOfflineCommitsAndReportsCounts(t *testing.T) {
	env := newSyncEnv(t)
	env.createTicket(t, env.serviceA, "fix-auth")

	offline := true
	interceptTicketGit(t, func(original func(time.Duration, string, ...string) (string, error), timeout time.Duration, dir string, args ...string) (string, error) {
		if offline && len(args) > 0 && (args[0] == "fetch" || args[0] == "push") {
			return "", errors.New("network is unreachable")
		}
		return original(timeout, dir, args...)
	})
	if _, err := env.serviceA.Set("fix-auth", SetRequest{Priority: 3, PrioritySet: true}, false); err != nil {
		t.Fatal(err)
	}
	offline = false

	status, err := env.serviceA.Sync(false)
	if err != nil {
		t.Fatalf("sync: %v", err)
	}
	if status.PushedCommits != 1 || status.PulledCommits != 0 {
		t.Fatalf("sync status = %+v, want 1 pushed and 0 pulled", status)
	}
	if ahead := testGit(t, env.cloneA, "rev-list", "--count", "origin/main..HEAD"); ahead != "0" {
		t.Fatalf("clone A is still ahead by %s after sync", ahead)
	}
}

func TestServiceSyncIsANoOpWhenSyncIsOff(t *testing.T) {
	service, _ := newTestService(t)
	status, err := service.Sync(false)
	if err != nil {
		t.Fatalf("sync-off error = %v, want a no-op", err)
	}
	if status.Enabled {
		t.Fatalf("sync-off status = %+v, want Enabled false", status)
	}
}

func TestDoctorReportsSyncFindingsWithoutBlockingRepair(t *testing.T) {
	env := newSyncEnv(t)
	env.createTicket(t, env.serviceA, "fix-auth")

	offline := true
	interceptTicketGit(t, func(original func(time.Duration, string, ...string) (string, error), timeout time.Duration, dir string, args ...string) (string, error) {
		if offline && len(args) > 0 && (args[0] == "fetch" || args[0] == "push") {
			return "", errors.New("network is unreachable")
		}
		return original(timeout, dir, args...)
	})
	if _, err := env.serviceA.Set("fix-auth", SetRequest{Priority: 3, PrioritySet: true}, false); err != nil {
		t.Fatal(err)
	}
	offline = false
	path := filepath.Join(env.cloneA, "tickets", "fix-auth.md")
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(content, []byte("\nHand edit.\n")...), 0o644); err != nil {
		t.Fatal(err)
	}

	report, err := env.serviceA.Doctor()
	if err != nil {
		t.Fatal(err)
	}
	if report.Sync == nil {
		t.Fatal("doctor report has no sync block")
	}
	codes := map[string]bool{}
	for _, issue := range report.Sync.Issues {
		codes[issue.Code] = true
	}
	if !codes[syncIssueUnpushed] || !codes[syncIssueDirty] {
		t.Fatalf("sync issues = %+v, want unpushed and dirty", report.Sync.Issues)
	}
	if !report.Healthy {
		t.Fatalf("sync findings must not make the ticket report unhealthy: %+v", report.Issues)
	}
	if report.Sync.UnpushedCommits != 1 || !report.Sync.Dirty {
		t.Fatalf("sync block = %+v", report.Sync)
	}
}

func TestSyncerReportsAHomeOutsideAWorkTree(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	service := NewService(Options{
		Home:     t.TempDir(),
		StateDir: t.TempDir(),
		Sync:     SyncOptions{Mode: SyncModeGit},
	})
	_, err := service.Init(false)
	if clierr.CodeOf(err) != clierr.PreconditionFailed {
		t.Fatalf("init outside a work tree error = %v, want precondition_failed", err)
	}
}
