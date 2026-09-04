package cli

import (
	"fmt"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/jpugliesi/tmux-worktree/internal/domain"
	"github.com/jpugliesi/tmux-worktree/internal/prstate"
	ticketservice "github.com/jpugliesi/tmux-worktree/internal/ticket"
)

// addFreshFlag registers --fresh on a read command.
func addFreshFlag(command *cobra.Command, fresh *bool) {
	command.Flags().BoolVar(fresh, "fresh", false,
		"Sync the Tickets home with its remote before the read")
}

// freshenTicketStore syncs the store before a read when --fresh is set. A
// failure never blocks the read; it degrades to a warning on stderr.
func freshenTicketStore(command *cobra.Command, service ticketservice.Store, fresh bool) {
	if !fresh {
		return
	}
	if _, err := service.Sync(false); err != nil {
		fmt.Fprintf(command.ErrOrStderr(), "Warning: --fresh sync failed; the read uses local state: %v\n", err)
	}
}

const (
	ticketStateNeedsInput = "needs-input"
	ticketStateInProgress = "in-progress"
	ticketStateInReview   = "in-review"
	ticketStateReady      = "ready"
	ticketStateBlocked    = "blocked"
)

// ticketListStatusValues is the closed set for tickets list --status:
// stored statuses plus the derived STATUS column values.
func ticketListStatusValues() []string {
	values := append([]string{}, domain.TicketStatuses()...)
	values = append(values, ticketStateInProgress, ticketStateNeedsInput, ticketStateInReview)
	sort.Strings(values)
	return values
}

// ticketListDisplayStatus reports whether value is a derived list STATUS
// that is not also a stored status.
func ticketListDisplayStatus(value string) bool {
	switch value {
	case ticketStateInProgress, ticketStateNeedsInput, ticketStateInReview:
		return true
	}
	return false
}

func ticketMatchesListStatus(ticket domain.Ticket, status string) bool {
	if status == "" {
		return true
	}
	if string(ticket.Status) == status {
		return true
	}
	return ticketDisplayState(ticket) == status
}

func filterTicketsByListStatus(tickets []domain.Ticket, status string) []domain.Ticket {
	if status == "" {
		return tickets
	}
	matched := make([]domain.Ticket, 0, len(tickets))
	for _, ticket := range tickets {
		if ticketMatchesListStatus(ticket, status) {
			matched = append(matched, ticket)
		}
	}
	return matched
}

// sortTicketsByAction orders Tickets by the STATUS column, most actionable
// first, then by priority, then by slug.
func sortTicketsByAction(tickets []domain.Ticket) {
	sort.SliceStable(tickets, func(i, j int) bool {
		left, right := ticketActionRank(tickets[i]), ticketActionRank(tickets[j])
		if left != right {
			return left < right
		}
		if tickets[i].Priority != tickets[j].Priority {
			return tickets[i].Priority < tickets[j].Priority
		}
		return tickets[i].Slug < tickets[j].Slug
	})
}

func ticketActionRank(ticket domain.Ticket) int {
	switch ticketDisplayState(ticket) {
	case ticketStateNeedsInput:
		return 0
	case ticketStateInProgress:
		return 1
	case ticketStateInReview:
		return 2
	case string(domain.TicketReadyForHuman):
		return 3
	case ticketStateReady, string(domain.TicketReadyForAgent):
		return 4
	case string(domain.TicketNeedsInfo):
		return 5
	case string(domain.TicketNeedsTriage):
		return 6
	case ticketStateBlocked:
		return 7
	case string(domain.TicketDone):
		return 8
	case string(domain.TicketWontfix):
		return 9
	default:
		return 10
	}
}

// deriveTicketState folds status, claim, PRs, and (when available) live PR
// state into one display state. prStates may be nil: offline callers derive
// in-review from URL presence and status only.
func deriveTicketState(status domain.TicketStatus, claimedBy string, prs []string,
	prStates map[string]prstate.PRState, ready bool) string {
	if status == domain.TicketDone || status == domain.TicketWontfix {
		return string(status)
	}
	if claimedBy != "" && status == domain.TicketNeedsInfo {
		return ticketStateNeedsInput
	}
	if len(prs) > 0 {
		if status == domain.TicketReadyForHuman || allMerged(prs, prStates) {
			return ticketStateInReview
		}
	}
	if claimedBy != "" {
		return ticketStateInProgress
	}
	if ready {
		return ticketStateReady
	}
	if status == domain.TicketReadyForAgent {
		return ticketStateBlocked
	}
	return string(status)
}

// allMerged is true only when every PR has a FETCHED merged state.
func allMerged(prs []string, prStates map[string]prstate.PRState) bool {
	if len(prStates) == 0 {
		return false
	}
	for _, url := range prs {
		if prStates[url].State != prstate.StateMerged {
			return false
		}
	}
	return true
}

// prBadge renders the PR summary of one ticket, such as "[PR: merged ✓]" or
// "[2 PRs: 1 merged 1 open ⧗]". Without fetched states it counts URLs only.
func prBadge(prs []string, prStates map[string]prstate.PRState) string {
	if len(prs) == 0 {
		return ""
	}
	if len(prStates) == 0 {
		if len(prs) == 1 {
			return "[PR]"
		}
		return fmt.Sprintf("[%d PRs]", len(prs))
	}
	counts := map[string]int{}
	order := []string{}
	worstChecks := prstate.ChecksNone
	changesRequested := false
	for _, url := range prs {
		state := prStates[url]
		key := string(state.State)
		if state.State == "" {
			key = string(prstate.StateUnknown)
		}
		if counts[key] == 0 {
			order = append(order, key)
		}
		counts[key]++
		switch state.Checks {
		case prstate.ChecksFail:
			worstChecks = prstate.ChecksFail
		case prstate.ChecksPending:
			if worstChecks != prstate.ChecksFail {
				worstChecks = prstate.ChecksPending
			}
		}
		if state.ReviewDecision == prstate.ReviewChangesRequested {
			changesRequested = true
		}
	}
	suffix := ""
	switch worstChecks {
	case prstate.ChecksFail:
		suffix = " ✗"
	case prstate.ChecksPending:
		suffix = " ⧗"
	default:
		if counts[string(prstate.StateMerged)] == len(prs) {
			suffix = " ✓"
		}
	}
	if changesRequested {
		suffix = " changes requested ✗"
	}
	if len(prs) == 1 {
		return fmt.Sprintf("[PR: %s%s]", order[0], suffix)
	}
	parts := make([]string, 0, len(order))
	for _, key := range order {
		parts = append(parts, fmt.Sprintf("%d %s", counts[key], key))
	}
	return fmt.Sprintf("[%d PRs: %s%s]", len(prs), strings.Join(parts, " "), suffix)
}
