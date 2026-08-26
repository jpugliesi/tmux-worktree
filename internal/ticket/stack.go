package ticket

import (
	"fmt"

	"github.com/jpugliesi/tmux-worktree/internal/clierr"
	"github.com/jpugliesi/tmux-worktree/internal/domain"
)

// blockerInReview reports whether the blocker slug resolves to work that is
// in review with pull requests attached: a stacked dependent may start from
// its unmerged branch. A missing, duplicated, or unparsable blocker blocks.
func (x *index) blockerInReview(slug string) bool {
	paths := x.bySlug[slug]
	if len(paths) == 0 {
		return false
	}
	for _, path := range paths {
		ticket, ok := x.tickets[path]
		if !ok {
			return false
		}
		if ticket.Status != domain.TicketReadyForHuman || len(ticket.PullRequests) == 0 {
			return false
		}
	}
	return true
}

// stackReady reports whether a Ticket may start as a stacked dependent:
// unclaimed ready-for-agent work whose blockers are each closed or in review
// with a pull request, with at least one still in review. A true-ready
// Ticket is not stack-ready.
func (x *index) stackReady(ticket domain.Ticket) bool {
	if ticket.Status != domain.TicketReadyForAgent || ticket.ClaimedBy != "" {
		return false
	}
	inReview := false
	for _, blocker := range ticket.BlockedBy {
		if x.blockerClosed(blocker) {
			continue
		}
		if !x.blockerInReview(blocker) {
			return false
		}
		inReview = true
	}
	return inReview
}

// ClaimStackReady claims a stack-ready Ticket: its open blockers are all in
// review with pull requests. stackBase records the stack parent
// ("blocker-slug@branch") in the twt_base frontmatter when non-empty, so any
// vault reader can babysit the stack. A same-claimant retry is a no-op.
func (s *Service) ClaimStackReady(ref, claimant, stackBase string, dryRun bool) (domain.Ticket, error) {
	claimant, err := validClaimant(claimant)
	if err != nil {
		return domain.Ticket{}, err
	}
	return s.mutate(ref, dryRun, false, syncRequired, func() string {
		return fmt.Sprintf("twt: stack-claim %s (as %s)", ref, claimant)
	}, func(m *mutation) error {
		if m.ticket.ClaimedBy == claimant {
			m.skipWrite = true
			return nil
		}
		if m.ticket.ClaimedBy != "" {
			return claimedByOther(m.ticket.Slug, m.ticket.ClaimedBy)
		}
		if !m.index.stackReady(m.ticket) && !m.index.ready(m.ticket) {
			return clierr.WithHint(
				clierr.New(clierr.PreconditionFailed, "ticket %q is not stack-ready to claim", m.ticket.Slug),
				"A stacked dependent needs every open blocker in review with a pull request.")
		}
		setMapString(m.mapping, "claimed_by", claimant)
		setMapDate(m.mapping, "claimed_at", s.today())
		if stackBase != "" {
			setMapString(m.mapping, "twt_base", stackBase)
		}
		return nil
	})
}
