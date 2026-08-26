package localdispatch

import (
	"strings"

	"github.com/jpugliesi/tmux-worktree/internal/clierr"
	"github.com/jpugliesi/tmux-worktree/internal/domain"
	"github.com/jpugliesi/tmux-worktree/internal/store"
	ticketservice "github.com/jpugliesi/tmux-worktree/internal/ticket"
)

// stackInfo describes the stack parent of one stacked dispatch.
type stackInfo struct {
	// ParentSlug is the in-review blocker the dependent stacks on.
	ParentSlug string
	// Branch is the parent's Workspace branch: the dependent's base ref.
	Branch string
	// ParentPR is the parent's first pull request URL, for the worker's
	// stacked pull request.
	ParentPR string
}

// Base renders the twt_base frontmatter value.
func (i stackInfo) Base() string {
	return i.ParentSlug + "@" + i.Branch
}

// resolveStack finds the stack parent of a stack-ready Ticket on this
// machine: exactly one open blocker, in review with a pull request, whose
// dispatch session left a Workspace whose branch is the base.
func (s *Service) resolveStack(shown ticketservice.ShowResult) (stackInfo, error) {
	open := make([]string, 0, len(shown.BlockedByOpen))
	for _, blocker := range shown.BlockedByOpen {
		open = append(open, blocker.Slug)
	}
	if len(open) != 1 {
		return stackInfo{}, clierr.WithHint(
			clierr.New(clierr.PreconditionFailed, "ticket %q has %d open blockers (%s); stacking supports exactly one",
				shown.Ticket.Slug, len(open), strings.Join(open, ", ")),
			"Wait for the other blockers to close.")
	}
	parent, err := s.options.Tickets.Resolve(open[0])
	if err != nil {
		return stackInfo{}, err
	}
	if parent.Status != domain.TicketReadyForHuman || len(parent.PullRequests) == 0 {
		return stackInfo{}, clierr.WithHint(
			clierr.New(clierr.PreconditionFailed, "blocker %q is not in review with a pull request", parent.Slug),
			"A stacked dispatch starts only from a blocker whose worker completed with a pull request.")
	}
	branch, err := s.parentBranch(parent.Slug)
	if err != nil {
		return stackInfo{}, err
	}
	return stackInfo{ParentSlug: parent.Slug, Branch: branch, ParentPR: parent.PullRequests[0]}, nil
}

// parentBranch reads the Workspace branch of the blocker's newest dispatch
// session on this machine.
func (s *Service) parentBranch(slug string) (string, error) {
	sessions, err := s.sessions.List()
	if err != nil {
		return "", err
	}
	workspaceID := ""
	for _, session := range sessions {
		// List is sorted oldest first; the last match wins.
		if session.TicketSlug == slug && session.WorkspaceID != "" {
			workspaceID = session.WorkspaceID
		}
	}
	if workspaceID == "" {
		return "", clierr.WithHint(
			clierr.New(clierr.PreconditionFailed, "blocker %q has no dispatch Workspace on this machine", slug),
			"Stacking needs the blocker's branch; dispatch the stack from the machine that ran the blocker.")
	}
	workspace, err := store.NewWorkspaceStore(s.options.StateDir).Find(workspaceID)
	if err != nil {
		return "", err
	}
	if len(workspace.Repositories) == 0 || workspace.Repositories[0].Branch == "" {
		return "", clierr.New(clierr.UnsafeState, "blocker Workspace %q has no branch to stack on", workspaceID)
	}
	return workspace.Repositories[0].Branch, nil
}
