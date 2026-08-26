// Tickets git sync wraps every Service write in one pull, commit, push round
// so two machines share one Tickets home through a git remote. A claim-class
// write treats the push as its cross-machine compare-and-swap: a rejected
// push drops the local commit, fast-forwards, and replays the mutation
// closure against the fresh state, so a lost claim surfaces as the normal
// locked error.
//
// Lock order: any caller-held project lock, then the blocking tickets-git
// named lock, then the per-ticket flock inside the mutation. Nothing acquires
// in the reverse direction.

package ticket

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/jpugliesi/tmux-worktree/internal/clierr"
	"github.com/jpugliesi/tmux-worktree/internal/store"
)

// SyncModeGit enables git synchronization of the Tickets home.
const SyncModeGit = "git"

// defaultSyncRemote is the git remote that sync uses when none is configured.
const defaultSyncRemote = "origin"

// SyncOptions is the resolved tickets-sync configuration. The zero value
// disables sync: the Service makes no git calls at all.
type SyncOptions struct {
	// Mode is "" or "off" for no sync, or "git".
	Mode string
	// Remote is the git remote name. The default is "origin".
	Remote string
}

// enabled reports whether git synchronization is on.
func (o SyncOptions) enabled() bool {
	return o.Mode == SyncModeGit
}

// remote returns the configured remote name or the default.
func (o SyncOptions) remote() string {
	if o.Remote == "" {
		return defaultSyncRemote
	}
	return o.Remote
}

// logf writes one best-effort sync warning when a logger is configured.
func (s *Service) logf(format string, a ...any) {
	if s.options.Logf == nil {
		return
	}
	s.options.Logf(format, a...)
}

// syncClass selects the push policy of one write.
type syncClass int

const (
	// syncBestEffort commits always and pushes best-effort. A failed push
	// keeps the local commit and warns.
	syncBestEffort syncClass = iota
	// syncRequired treats the push as the cross-machine compare-and-swap.
	// The write fails when the push cannot succeed.
	syncRequired
)

// syncRemoteTimeout bounds each git command that reaches the remote, and
// syncLocalTimeout bounds each local git command. They are variables so
// tests can widen them on loaded machines.
var (
	syncRemoteTimeout = 10 * time.Second
	syncLocalTimeout  = 5 * time.Second
)

const (
	// syncDryRunFetchTimeout bounds the best-effort dry-run fetch.
	syncDryRunFetchTimeout = 3 * time.Second
	// syncMaxReplays bounds the push-rejection replay loop.
	syncMaxReplays = 3
)

// runTicketGit runs one git command for tickets sync. It refuses interactive
// credential prompts and stops after the timeout. Tests replace it.
var runTicketGit = func(timeout time.Duration, directory string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	command := exec.CommandContext(ctx, "git", args...)
	command.Dir = directory
	command.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	data, err := command.CombinedOutput()
	if ctx.Err() != nil {
		return "", fmt.Errorf("git %s: twt could not finish the git command in %s", strings.Join(args, " "), timeout)
	}
	if err != nil {
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(data)))
	}
	return strings.TrimSpace(string(data)), nil
}

// errPushRejected marks a non-fast-forward push rejection: another machine
// moved the remote branch first.
var errPushRejected = errors.New("the tickets remote rejected the push")

// gitSync is one resolved view of the tickets git repository.
type gitSync struct {
	home     string
	toplevel string
	// pathspec limits every stage and status to the Tickets home, so
	// untracked vault notes outside it never enter a twt commit.
	pathspec string
	remote   string
	branch   string
	stateDir string
	logf     func(format string, a ...any)
}

// syncer resolves and validates the git context. It returns (nil, nil) when
// sync is disabled.
func (s *Service) syncer() (*gitSync, error) {
	if !s.options.Sync.enabled() {
		return nil, nil
	}
	home, err := s.home()
	if err != nil {
		return nil, err
	}
	if _, err := exec.LookPath("git"); err != nil {
		return nil, clierr.WithHint(
			clierr.New(clierr.PreconditionFailed, "ticketsSync.mode is git but git is not installed"),
			"Install git, or set ticketsSync.mode to off.")
	}
	// The home may not exist yet (Init creates it), so resolve the git
	// context from the nearest existing ancestor.
	base := existingAncestor(home)
	toplevel, err := runTicketGit(syncLocalTimeout, base, "rev-parse", "--show-toplevel")
	if err != nil {
		return nil, clierr.WithHint(
			clierr.New(clierr.PreconditionFailed, "the Tickets home %q is not inside a git work tree", home),
			"Clone the tickets repository first, or set ticketsSync.mode to off.")
	}
	branch, err := runTicketGit(syncLocalTimeout, toplevel, "symbolic-ref", "--short", "HEAD")
	if err != nil {
		return nil, clierr.WithHint(
			clierr.New(clierr.UnsafeState, "the tickets repository %q has a detached HEAD", toplevel),
			"Check out a branch in the tickets repository.")
	}
	remote := s.options.Sync.remote()
	if _, err := runTicketGit(syncLocalTimeout, toplevel, "remote", "get-url", remote); err != nil {
		return nil, clierr.WithHint(
			clierr.New(clierr.PreconditionFailed, "the tickets repository %q has no remote %q", toplevel, remote),
			"Add the remote, or set ticketsSync.remote to the correct name.")
	}
	pathspec, err := ticketsPathspec(toplevel, base, home)
	if err != nil {
		return nil, err
	}
	return &gitSync{
		home:     home,
		toplevel: toplevel,
		pathspec: pathspec,
		remote:   remote,
		branch:   branch,
		stateDir: s.options.StateDir,
		logf:     s.logf,
	}, nil
}

// existingAncestor returns the deepest existing ancestor of path, or path
// itself when it exists.
func existingAncestor(path string) string {
	for current := path; ; current = filepath.Dir(current) {
		if _, err := os.Stat(current); err == nil {
			return current
		}
		if current == filepath.Dir(current) {
			return current
		}
	}
}

// ticketsPathspec returns the Tickets home relative to the repository
// toplevel. It resolves symlinks on the existing base first, because git
// reports a resolved toplevel, and rejoins the not-yet-created remainder.
func ticketsPathspec(toplevel, base, home string) (string, error) {
	resolvedBase, err := filepath.EvalSymlinks(base)
	if err != nil {
		resolvedBase = base
	}
	remainder, err := filepath.Rel(base, home)
	if err != nil {
		remainder = "."
	}
	resolvedHome := filepath.Join(resolvedBase, remainder)
	pathspec, err := filepath.Rel(toplevel, resolvedHome)
	if err != nil || pathspec == ".." || strings.HasPrefix(pathspec, ".."+string(filepath.Separator)) {
		return "", clierr.New(clierr.Internal,
			"the Tickets home %q is not under the git toplevel %q", home, toplevel)
	}
	return pathspec, nil
}

// lock serializes every tickets git operation of this home on this machine.
// It blocks, so concurrent local mutations queue instead of failing.
func (g *gitSync) lock() (*store.NamedLock, error) {
	return store.AcquireNamedLockBlocking(g.stateDir, "tickets-git", g.home)
}

// dirty reports whether the Tickets home pathspec has uncommitted changes.
func (g *gitSync) dirty() (bool, error) {
	output, err := runTicketGit(syncLocalTimeout, g.toplevel, "status", "--porcelain", "--", g.pathspec)
	if err != nil {
		return false, err
	}
	return output != "", nil
}

// commit stages the Tickets home pathspec and commits it. Hooks are skipped:
// these are machine commits.
func (g *gitSync) commit(message string) error {
	if _, err := runTicketGit(syncLocalTimeout, g.toplevel, "add", "-A", "--", g.pathspec); err != nil {
		return err
	}
	if _, err := runTicketGit(syncLocalTimeout, g.toplevel, "commit", "--quiet", "--no-verify", "-m", message); err != nil {
		return err
	}
	return nil
}

// push publishes the branch. A non-fast-forward rejection returns
// errPushRejected so the caller can replay.
func (g *gitSync) push() error {
	_, err := runTicketGit(syncRemoteTimeout, g.toplevel, "push", "--quiet", "--no-verify", "-u", g.remote, g.branch)
	if err == nil {
		return nil
	}
	message := err.Error()
	if strings.Contains(message, "[rejected]") ||
		strings.Contains(message, "non-fast-forward") ||
		strings.Contains(message, "fetch first") ||
		strings.Contains(message, "stale info") {
		return errPushRejected
	}
	return err
}

// dropLastCommit removes the commit that this operation just made. The
// caller holds the tickets-git lock and swept manual edits first, so the
// dropped commit can only be twt's own.
func (g *gitSync) dropLastCommit() error {
	_, err := runTicketGit(syncLocalTimeout, g.toplevel, "reset", "--hard", "HEAD~1")
	return err
}

// sweepManualEdits commits stray changes under the Tickets home pathspec, so
// every later commit of this operation contains exactly one twt write and a
// rollback can never destroy user edits.
func (g *gitSync) sweepManualEdits() error {
	dirty, err := g.dirty()
	if err != nil {
		return err
	}
	if !dirty {
		return nil
	}
	return g.commit("twt: sync manual edits")
}

// remoteRef names the remote-tracking ref of the sync branch.
func (g *gitSync) remoteRef() string {
	return g.remote + "/" + g.branch
}

// fetch updates the remote-tracking ref. It reports whether that ref exists.
// When required is true a fetch failure fails the operation; otherwise the
// operation continues offline with a warning.
func (g *gitSync) fetch(required bool, timeout time.Duration) (bool, error) {
	if _, err := runTicketGit(timeout, g.toplevel, "fetch", g.remote, g.branch); err != nil {
		if strings.Contains(err.Error(), "couldn't find remote ref") {
			// The remote branch does not exist yet. The first push creates it.
			return false, nil
		}
		if required {
			return false, clierr.WithHint(
				clierr.New(clierr.PreconditionFailed, "twt could not reach the tickets remote %q: %v", g.remote, err),
				"This change needs the remote for the claim handshake. Check the network, then run the command again.")
		}
		g.logf("Warning: twt could not fetch the tickets remote %q. The change stays local until the next successful sync.", g.remote)
		return false, nil
	}
	if _, err := runTicketGit(syncLocalTimeout, g.toplevel, "rev-parse", "--verify", "--quiet", g.remoteRef()); err != nil {
		return false, nil
	}
	return true, nil
}

// headExists reports whether the local branch has a commit yet.
func (g *gitSync) headExists() bool {
	_, err := runTicketGit(syncLocalTimeout, g.toplevel, "rev-parse", "--verify", "--quiet", "HEAD")
	return err == nil
}

// aheadBehind counts local commits the remote lacks and remote commits the
// local branch lacks. The caller must know that the remote ref exists.
func (g *gitSync) aheadBehind() (ahead, behind int, err error) {
	counts, err := runTicketGit(syncLocalTimeout, g.toplevel, "rev-list", "--left-right", "--count", "HEAD..."+g.remoteRef())
	if err != nil {
		return 0, 0, err
	}
	if _, err := fmt.Sscanf(counts, "%d\t%d", &ahead, &behind); err != nil {
		return 0, 0, fmt.Errorf("parse rev-list counts %q: %w", counts, err)
	}
	return ahead, behind, nil
}

// advance brings the local branch up to the remote: fast-forward when the
// local branch has nothing of its own, or rebase pre-existing local commits.
// A rebase conflict aborts and reports unsafe_state.
func (g *gitSync) advance(ahead, behind int) error {
	if behind == 0 {
		return nil
	}
	if ahead == 0 {
		if _, err := runTicketGit(syncLocalTimeout, g.toplevel, "merge", "--ff-only", g.remoteRef()); err != nil {
			return err
		}
		return nil
	}
	if _, err := runTicketGit(syncRemoteTimeout, g.toplevel, "rebase", g.remoteRef()); err != nil {
		_, _ = runTicketGit(syncLocalTimeout, g.toplevel, "rebase", "--abort")
		return clierr.WithHint(
			clierr.New(clierr.UnsafeState, "the tickets repository %q diverged from %s and the rebase conflicts", g.toplevel, g.remoteRef()),
			"Resolve the conflict in %q, then run 'twt tickets git-sync'.", g.toplevel)
	}
	return nil
}

// reconcile brings the local branch up to date with the remote before a
// mutation.
func (g *gitSync) reconcile(required bool, fetchTimeout time.Duration) error {
	hasRemote, err := g.fetch(required, fetchTimeout)
	if err != nil || !hasRemote {
		return err
	}
	if !g.headExists() {
		// A fresh clone of a repository that gained commits later has an
		// unborn branch. Point it at the remote state.
		_, err := runTicketGit(syncLocalTimeout, g.toplevel, "reset", "--hard", g.remoteRef())
		return err
	}
	ahead, behind, err := g.aheadBehind()
	if err != nil {
		return err
	}
	return g.advance(ahead, behind)
}

// Sync doctor issue codes. These are findings, not errors, and they never
// block repair.
const (
	syncIssueNoRepo       = "sync_no_repo"
	syncIssueNoRemote     = "sync_no_remote"
	syncIssueDetachedHead = "sync_detached_head"
	syncIssueNoUpstream   = "sync_no_upstream"
	syncIssueDirty        = "sync_dirty"
	syncIssueUnpushed     = "sync_unpushed"
	syncIssueGitignore    = "sync_gitignore"
	syncIssueInProgress   = "sync_in_progress"
)

// syncDoctor collects the local-only git sync findings for the doctor
// report. It returns nil when sync is disabled and never reaches the remote.
func (s *Service) syncDoctor(home string) *SyncDoctorInfo {
	if !s.options.Sync.enabled() {
		return nil
	}
	info := &SyncDoctorInfo{Remote: s.options.Sync.remote(), Issues: []SyncDoctorIssue{}}
	report := func(code, message string) {
		info.Issues = append(info.Issues, SyncDoctorIssue{Code: code, Message: message})
	}
	if _, err := exec.LookPath("git"); err != nil {
		report(syncIssueNoRepo, "ticketsSync.mode is git but git is not installed")
		return info
	}
	base := existingAncestor(home)
	toplevel, err := runTicketGit(syncLocalTimeout, base, "rev-parse", "--show-toplevel")
	if err != nil {
		report(syncIssueNoRepo, fmt.Sprintf("the Tickets home %q is not inside a git work tree", home))
		return info
	}
	pathspec, err := ticketsPathspec(toplevel, base, home)
	if err != nil {
		report(syncIssueNoRepo, err.Error())
		return info
	}
	branch, err := runTicketGit(syncLocalTimeout, toplevel, "symbolic-ref", "--short", "HEAD")
	if err != nil {
		report(syncIssueDetachedHead, fmt.Sprintf("the tickets repository %q has a detached HEAD", toplevel))
		return info
	}
	info.Branch = branch
	if _, err := runTicketGit(syncLocalTimeout, toplevel, "remote", "get-url", info.Remote); err != nil {
		report(syncIssueNoRemote, fmt.Sprintf("the tickets repository %q has no remote %q", toplevel, info.Remote))
	}
	gitDir, err := runTicketGit(syncLocalTimeout, toplevel, "rev-parse", "--absolute-git-dir")
	if err == nil {
		for _, marker := range []string{"rebase-merge", "rebase-apply", "MERGE_HEAD"} {
			if _, statErr := os.Stat(filepath.Join(gitDir, marker)); statErr == nil {
				report(syncIssueInProgress, fmt.Sprintf("a git operation is in progress in %q (%s)", toplevel, marker))
				break
			}
		}
	}
	if status, err := runTicketGit(syncLocalTimeout, toplevel, "status", "--porcelain", "--", pathspec); err == nil && status != "" {
		info.Dirty = true
		report(syncIssueDirty, "the Tickets home has uncommitted changes; the next mutation or 'twt tickets git-sync' commits them")
	}
	remoteRef := info.Remote + "/" + branch
	if _, err := runTicketGit(syncLocalTimeout, toplevel, "rev-parse", "--verify", "--quiet", remoteRef); err != nil {
		report(syncIssueNoUpstream, fmt.Sprintf("the branch %q has no remote-tracking ref yet; the first push creates it", branch))
	} else if counts, err := runTicketGit(syncLocalTimeout, toplevel, "rev-list", "--count", remoteRef+"..HEAD"); err == nil {
		if _, scanErr := fmt.Sscanf(counts, "%d", &info.UnpushedCommits); scanErr == nil && info.UnpushedCommits > 0 {
			report(syncIssueUnpushed, fmt.Sprintf("%d local commit(s) are not on %q; run 'twt tickets git-sync'", info.UnpushedCommits, remoteRef))
		}
	}
	gitignore, err := os.ReadFile(filepath.Join(home, ".gitignore"))
	if err != nil || !strings.Contains(string(gitignore), ".twt-write-*") {
		report(syncIssueGitignore, "the Tickets home .gitignore does not exclude .twt-write-* temp files; run 'twt tickets init'")
	}
	return info
}

// SyncStatus reports one explicit reconcile-and-push round.
type SyncStatus struct {
	Remote               string `json:"remote"`
	Branch               string `json:"branch"`
	PulledCommits        int    `json:"pulledCommits"`
	PushedCommits        int    `json:"pushedCommits"`
	CommittedManualEdits bool   `json:"committedManualEdits"`
}

// Sync reconciles the Tickets home with its git remote in one explicit
// round: commit manual edits, pull, rebase pre-existing local commits, and
// push everything the remote lacks. It is the recovery path after offline
// work and the manual refresh for reads.
func (s *Service) Sync(dryRun bool) (SyncStatus, error) {
	g, err := s.syncer()
	if err != nil {
		return SyncStatus{}, err
	}
	if g == nil {
		return SyncStatus{}, clierr.WithHint(
			clierr.New(clierr.PreconditionFailed, "ticketsSync is off"),
			"Set ticketsSync.mode to git in ~/.config/twt/config.yaml or TWT_TICKETS_SYNC.")
	}
	lock, err := g.lock()
	if err != nil {
		return SyncStatus{}, err
	}
	defer lock.Release()
	status := SyncStatus{Remote: g.remote, Branch: g.branch}
	dirty, err := g.dirty()
	if err != nil {
		return SyncStatus{}, err
	}
	if !dryRun && dirty {
		if err := g.commit("twt: sync manual edits"); err != nil {
			return SyncStatus{}, err
		}
		status.CommittedManualEdits = true
	}
	hasRemote, err := g.fetch(true, syncRemoteTimeout)
	if err != nil {
		return SyncStatus{}, err
	}
	ahead := 0
	if g.headExists() {
		if hasRemote {
			var behind int
			ahead, behind, err = g.aheadBehind()
			if err != nil {
				return SyncStatus{}, err
			}
			status.PulledCommits = behind
		} else {
			total, err := runTicketGit(syncLocalTimeout, g.toplevel, "rev-list", "--count", "HEAD")
			if err != nil {
				return SyncStatus{}, err
			}
			if _, err := fmt.Sscanf(total, "%d", &ahead); err != nil {
				return SyncStatus{}, fmt.Errorf("parse rev-list count %q: %w", total, err)
			}
		}
	}
	if dryRun && dirty {
		ahead++
	}
	status.PushedCommits = ahead
	if dryRun {
		return status, nil
	}
	if hasRemote {
		if !g.headExists() {
			if _, err := runTicketGit(syncLocalTimeout, g.toplevel, "reset", "--hard", g.remoteRef()); err != nil {
				return SyncStatus{}, err
			}
		} else if err := g.advance(ahead, status.PulledCommits); err != nil {
			return SyncStatus{}, err
		}
	}
	if ahead > 0 {
		if err := g.push(); err != nil {
			if errors.Is(err, errPushRejected) {
				return SyncStatus{}, clierr.WithHint(
					clierr.New(clierr.Locked, "the tickets remote changed during the sync"),
					"Run 'twt tickets git-sync' again.")
			}
			return SyncStatus{}, clierr.WithHint(
				clierr.New(clierr.PreconditionFailed, "twt could not push to the tickets remote %q: %v", g.remote, err),
				"Check the network, then run 'twt tickets git-sync' again.")
		}
	}
	return status, nil
}

// syncWrite wraps one Service write in the sync round: sweep, pull, run the
// operation, commit, push, and replay on a rejected push. When sync is
// disabled it runs the operation directly.
func syncWrite[T any](s *Service, class syncClass, dryRun bool, message func() string, op func() (T, error)) (T, error) {
	var zero T
	g, err := s.syncer()
	if err != nil {
		return zero, err
	}
	if g == nil {
		return op()
	}
	lock, err := g.lock()
	if err != nil {
		return zero, err
	}
	defer lock.Release()
	if dryRun {
		// A dry run pulls best-effort so validity answers are fresh, and
		// never commits or pushes.
		if err := g.reconcile(false, syncDryRunFetchTimeout); err != nil {
			return zero, err
		}
		return op()
	}
	if err := g.sweepManualEdits(); err != nil {
		return zero, err
	}
	if err := g.reconcile(class == syncRequired, syncRemoteTimeout); err != nil {
		return zero, err
	}
	for attempt := 0; attempt < syncMaxReplays; attempt++ {
		result, err := op()
		if err != nil {
			return zero, err
		}
		dirty, err := g.dirty()
		if err != nil {
			return zero, err
		}
		if !dirty {
			// The operation changed nothing, so there is nothing to publish.
			return result, nil
		}
		if err := g.commit(message()); err != nil {
			return zero, err
		}
		pushErr := g.push()
		if pushErr == nil {
			return result, nil
		}
		if errors.Is(pushErr, errPushRejected) {
			// Another machine moved the remote. Drop our commit, pull, and
			// replay the operation against the fresh state. The replayed
			// operation's own validation decides whether the change is
			// still valid; a lost claim surfaces as locked from there.
			if err := g.dropLastCommit(); err != nil {
				return zero, err
			}
			if err := g.reconcile(true, syncRemoteTimeout); err != nil {
				return zero, err
			}
			continue
		}
		if class == syncRequired {
			// A claim that the remote never saw must not exist locally.
			if err := g.dropLastCommit(); err != nil {
				return zero, err
			}
			return zero, clierr.WithHint(
				clierr.New(clierr.PreconditionFailed, "twt could not push the ticket change to %q: %v", g.remote, pushErr),
				"This change needs the remote for the claim handshake. Check the network, then run the command again.")
		}
		g.logf("Warning: twt could not push the ticket change to %q. The commit stays local until the next successful sync.", g.remote)
		return result, nil
	}
	return zero, clierr.WithHint(
		clierr.New(clierr.Locked, "the tickets remote kept changing during this operation"),
		"Run the command again.")
}
