package ticket

import (
	"fmt"
	"strings"

	"github.com/jpugliesi/tmux-worktree/internal/clierr"
	"github.com/jpugliesi/tmux-worktree/internal/domain"
)

// planHeadingName is the body section that carries a Ticket's
// implementation plan.
const planHeadingName = "Plan"

// HasPlanSection reports whether a Ticket body carries a "## Plan" section.
func HasPlanSection(body string) bool {
	_, _, ok := sectionBounds(body, planHeadingName)
	return ok
}

// RequireApprovedPlan is the implementation dispatch gate: a Ticket whose
// body has a "## Plan" section must carry the approval stamp. A Ticket
// without a plan section dispatches freely.
func RequireApprovedPlan(shown ShowResult) error {
	if !HasPlanSection(shown.Body) {
		return nil
	}
	if shown.Ticket.PlanApprovedBy != "" {
		return nil
	}
	return clierr.WithHint(
		clierr.New(clierr.PreconditionFailed, "ticket %q has a plan without an approval", shown.Ticket.Slug),
		"Review the plan, then run 'twt tickets approve %s'.", shown.Ticket.Slug)
}

// Approve stamps the human approval on a Ticket's plan. When the Ticket
// waits on input (the planning agent's "approve this plan?" ask), the
// approval also acts as the answer: it records the reply under the open
// question, restores the pre-ask status, and clears the ask key. A retry on
// an already-approved plan is a no-op.
func (s *Service) Approve(ref, approver, note string, dryRun bool) (domain.Ticket, error) {
	approver, err := validClaimant(approver)
	if err != nil {
		return domain.Ticket{}, err
	}
	return s.mutate(ref, dryRun, false, syncRequired, func() string {
		return fmt.Sprintf("twt: approve plan on %s (by %s)", ref, approver)
	}, func(m *mutation) error {
		if !HasPlanSection(m.file.Body) {
			return clierr.WithHint(
				clierr.New(clierr.PreconditionFailed, "ticket %q has no \"## Plan\" section to approve", m.ticket.Slug),
				"Write the plan first with 'twt tickets plan %s --stdin'.", m.ticket.Slug)
		}
		if m.ticket.PlanApprovedBy != "" {
			m.skipWrite = true
			return nil
		}
		setMapString(m.mapping, "plan_approved_by", approver)
		setMapString(m.mapping, "plan_approved_at", s.askTimestamp())
		reply := "Plan approved."
		if trimmed := strings.TrimSpace(note); trimmed != "" {
			reply += "\n\n" + trimmed
		}
		if m.ticket.Status == domain.TicketNeedsInfo {
			restored := domain.TicketStatus(m.ticket.AskStatus)
			if !domain.ValidTicketStatus(restored) || restored == domain.TicketNeedsInfo {
				restored = domain.TicketReadyForAgent
			}
			entry := fmt.Sprintf("### A %s\n\n%s", s.askTimestamp(), reply)
			m.file.Body = appendBodySection(m.file.Body, questionsHeadingName, entry)
			setMapString(m.mapping, "status", string(restored))
			deleteMapKey(m.mapping, "twt_ask_status")
			m.relocate = true
		} else if strings.TrimSpace(note) != "" {
			m.file.Body = appendBodySection(m.file.Body, "Comments", reply)
		}
		return nil
	})
}
