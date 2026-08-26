package ticket

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/jpugliesi/tmux-worktree/internal/clierr"
	"github.com/jpugliesi/tmux-worktree/internal/domain"
)

// questionsHeadingName is the body section that carries the ask/answer
// exchange of a Ticket.
const questionsHeadingName = "Questions"

var questionEntryPattern = regexp.MustCompile(`(?m)^### (Q|A) .*$`)

// askTimestamp formats the ask/answer entry time.
func (s *Service) askTimestamp() string {
	return s.now().UTC().Format("2006-01-02 15:04") + " UTC"
}

// openQuestion returns the text of the trailing unanswered question in the
// Questions section, when one exists.
func openQuestion(body string) (string, bool) {
	start, end, ok := sectionBounds(body, questionsHeadingName)
	if !ok {
		return "", false
	}
	section := body[start:end]
	entries := questionEntryPattern.FindAllStringIndex(section, -1)
	if len(entries) == 0 {
		return "", false
	}
	last := entries[len(entries)-1]
	header := section[last[0]:last[1]]
	if !strings.HasPrefix(header, "### Q") {
		return "", false
	}
	return strings.TrimSpace(section[last[1]:]), true
}

// Ask records one question from the claimant and parks the Ticket on
// needs-info, keeping the claim. The prior status lands in twt_ask_status
// for Answer to restore. A retry of the same open question is a no-op.
func (s *Service) Ask(ref, claimant, text string, dryRun bool) (domain.Ticket, error) {
	claimant, err := validClaimant(claimant)
	if err != nil {
		return domain.Ticket{}, err
	}
	if strings.TrimSpace(text) == "" {
		return domain.Ticket{}, clierr.WithHint(
			clierr.New(clierr.InvalidUsage, "the question text is empty"),
			"Pass the question text on stdin.")
	}
	return s.mutate(ref, dryRun, false, syncRequired, func() string {
		return fmt.Sprintf("twt: ask on %s (as %s)", ref, claimant)
	}, func(m *mutation) error {
		if m.ticket.ClaimedBy == "" {
			return clierr.WithHint(
				clierr.New(clierr.UnsafeState, "ticket %q is not claimed; ask requires an active claim", m.ticket.Slug),
				"Claim the ticket first, or record the note with 'twt tickets comment'.")
		}
		if m.ticket.ClaimedBy != claimant {
			return claimedByOther(m.ticket.Slug, m.ticket.ClaimedBy)
		}
		if m.ticket.Status == domain.TicketNeedsInfo {
			if open, ok := openQuestion(m.file.Body); ok && open == strings.TrimSpace(text) {
				m.skipWrite = true
				return nil
			}
		}
		if m.ticket.Status != domain.TicketNeedsInfo {
			// Only the first ask stores the working status; a follow-up ask
			// must not overwrite it with needs-info.
			setMapString(m.mapping, "twt_ask_status", string(m.ticket.Status))
		}
		entry := fmt.Sprintf("### Q %s by %s\n\n%s", s.askTimestamp(), claimant, strings.Trim(text, "\n"))
		m.file.Body = appendBodySection(m.file.Body, questionsHeadingName, entry)
		setMapString(m.mapping, "status", string(domain.TicketNeedsInfo))
		m.relocate = true
		return nil
	})
}

// Answer records the reply under the open question, restores the pre-ask
// status, and keeps the claim. Anyone can answer: the human normally, or the
// agent itself when the human replied in its pane instead. Relay to a live
// session is the CLI's job.
func (s *Service) Answer(ref, text string, dryRun bool) (domain.Ticket, error) {
	if strings.TrimSpace(text) == "" {
		return domain.Ticket{}, clierr.WithHint(
			clierr.New(clierr.InvalidUsage, "the answer text is empty"),
			"Pass the answer text on stdin.")
	}
	return s.mutate(ref, dryRun, false, syncRequired, func() string {
		return fmt.Sprintf("twt: answer on %s", ref)
	}, func(m *mutation) error {
		if m.ticket.Status != domain.TicketNeedsInfo {
			return clierr.WithHint(
				clierr.New(clierr.PreconditionFailed, "ticket %q is not waiting on input", m.ticket.Slug),
				"Record a plain note with 'twt tickets comment' instead.")
		}
		restored := domain.TicketStatus(m.ticket.AskStatus)
		if !domain.ValidTicketStatus(restored) || restored == domain.TicketNeedsInfo {
			restored = domain.TicketReadyForAgent
		}
		entry := fmt.Sprintf("### A %s\n\n%s", s.askTimestamp(), strings.Trim(text, "\n"))
		m.file.Body = appendBodySection(m.file.Body, questionsHeadingName, entry)
		setMapString(m.mapping, "status", string(restored))
		deleteMapKey(m.mapping, "twt_ask_status")
		m.relocate = true
		return nil
	})
}
