package cli

import (
	"fmt"
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

// deriveTicketState folds status, claim, PRs, and (when available) live PR
// state into one display state. prStates may be nil: offline callers derive
// in-review from URL presence and status only.
func deriveTicketState(status domain.TicketStatus, claimedBy string, prs []string,
	prStates map[string]prstate.PRState, ready bool) string {
	if status == domain.TicketDone || status == domain.TicketWontfix {
		return string(status)
	}
	if claimedBy != "" && status == domain.TicketNeedsInfo {
		return "needs-input"
	}
	if len(prs) > 0 {
		if status == domain.TicketReadyForHuman || allMerged(prs, prStates) {
			return "in-review"
		}
	}
	if claimedBy != "" {
		return "in-progress"
	}
	if ready {
		return "ready"
	}
	if status == domain.TicketReadyForAgent {
		return "blocked"
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
