package localdispatch

import (
	"github.com/jpugliesi/tmux-worktree/internal/clierr"
	"github.com/jpugliesi/tmux-worktree/internal/domain"
)

// Abandon makes one local dispatch Session terminal and stops future Ticket
// reconciliation. It releases the Ticket only when the saved claimant still
// owns it; a different claim is user state and stays unchanged. Abandon
// never touches tmux: the Workspace and any live agent keep running until
// 'twt done'.
func (s *Service) Abandon(reference string, dryRun bool) (domain.LocalDispatchSession, error) {
	if s.options.Tickets == nil {
		return domain.LocalDispatchSession{}, clierr.New(clierr.PreconditionFailed, "local dispatch recovery is not configured")
	}
	matched, err := s.sessions.Find(reference)
	if err != nil {
		return domain.LocalDispatchSession{}, err
	}
	return s.updateSavedSession(matched.ID, func(session *domain.LocalDispatchSession) error {
		if session.TicketTransitioned {
			return clierr.New(clierr.PreconditionFailed,
				"local dispatch Session %q has no pending Ticket transition", session.ID)
		}
		now := s.options.Now().UTC()
		if session.Active() {
			session.Status = domain.LocalDispatchCancelled
			session.CompletedAt = &now
		}
		session.Error = &domain.DispatchError{
			Kind: "abandoned", Message: "The operator abandoned local dispatch Session recovery.",
		}
		session.UpdatedAt = now
		if !dryRun {
			if err := s.sessions.Save(*session); err != nil {
				return err
			}
		}
		ticket, resolveErr := s.options.Tickets.Resolve(session.TicketSlug)
		if resolveErr != nil && clierr.CodeOf(resolveErr) != clierr.NotFound {
			return resolveErr
		}
		if resolveErr == nil && ticket.ClaimedBy == session.Claimant {
			if _, err := s.options.Tickets.CompleteClaim(
				session.TicketSlug, session.Claimant, domain.TicketReadyForAgent, dryRun,
			); err != nil {
				return err
			}
		}
		session.TicketTransitioned = true
		session.UpdatedAt = s.options.Now().UTC()
		if !dryRun {
			return s.sessions.Save(*session)
		}
		return nil
	})
}
