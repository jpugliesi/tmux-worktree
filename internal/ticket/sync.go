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

const (
	// syncRemoteTimeout bounds each git command that reaches the remote.
	syncRemoteTimeout = 10 * time.Second
	// syncLocalTimeout bounds each local git command.
	syncLocalTimeout = 5 * time.Second
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

// reconcile brings the local branch up to date with the remote. When
// required is true a fetch failure fails the operation; otherwise the
// operation continues offline with a warning. Local commits that predate
// this operation rebase onto the remote; a conflict aborts the rebase and
// reports unsafe_state.
func (g *gitSync) reconcile(required bool, fetchTimeout time.Duration) error {
	if _, err := runTicketGit(fetchTimeout, g.toplevel, "fetch", g.remote, g.branch); err != nil {
		if strings.Contains(err.Error(), "couldn't find remote ref") {
			// The remote branch does not exist yet. The first push creates it.
			return nil
		}
		if required {
			return clierr.WithHint(
				clierr.New(clierr.PreconditionFailed, "twt could not reach the tickets remote %q: %v", g.remote, err),
				"This change needs the remote for the claim handshake. Check the network, then run the command again.")
		}
		g.logf("Warning: twt could not fetch the tickets remote %q. The change stays local until the next successful sync.", g.remote)
		return nil
	}
	remoteRef := g.remote + "/" + g.branch
	if _, err := runTicketGit(syncLocalTimeout, g.toplevel, "rev-parse", "--verify", "--quiet", remoteRef); err != nil {
		return nil
	}
	if _, err := runTicketGit(syncLocalTimeout, g.toplevel, "rev-parse", "--verify", "--quiet", "HEAD"); err != nil {
		// A fresh clone of a repository that gained commits later has an
		// unborn branch. Point it at the remote state.
		if _, err := runTicketGit(syncLocalTimeout, g.toplevel, "reset", "--hard", remoteRef); err != nil {
			return err
		}
		return nil
	}
	counts, err := runTicketGit(syncLocalTimeout, g.toplevel, "rev-list", "--left-right", "--count", "HEAD..."+remoteRef)
	if err != nil {
		return err
	}
	var ahead, behind int
	if _, err := fmt.Sscanf(counts, "%d\t%d", &ahead, &behind); err != nil {
		return fmt.Errorf("parse rev-list counts %q: %w", counts, err)
	}
	if behind == 0 {
		return nil
	}
	if ahead == 0 {
		if _, err := runTicketGit(syncLocalTimeout, g.toplevel, "merge", "--ff-only", remoteRef); err != nil {
			return err
		}
		return nil
	}
	if _, err := runTicketGit(syncRemoteTimeout, g.toplevel, "rebase", remoteRef); err != nil {
		_, _ = runTicketGit(syncLocalTimeout, g.toplevel, "rebase", "--abort")
		return clierr.WithHint(
			clierr.New(clierr.UnsafeState, "the tickets repository %q diverged from %s and the rebase conflicts", g.toplevel, remoteRef),
			"Run 'twt tickets git-sync', or resolve the conflict in %q.", g.toplevel)
	}
	return nil
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
