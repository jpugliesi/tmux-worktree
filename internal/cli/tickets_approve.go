package cli

import (
	"fmt"
	"strings"

	"github.com/jpugliesi/tmux-worktree/internal/domain"
	ticketservice "github.com/jpugliesi/tmux-worktree/internal/ticket"
	"github.com/spf13/cobra"
)

type ticketApproveOutput struct {
	SchemaVersion int          `json:"schemaVersion"`
	Operation     string       `json:"operation"`
	Status        string       `json:"status"`
	ID            string       `json:"id"`
	Name          string       `json:"name"`
	Relay         *answerRelay `json:"relay,omitempty"`
}

func newTicketsApproveCommand(options Options) *cobra.Command {
	var as, agentID string
	command := &cobra.Command{
		Use:   "approve TICKET [-]",
		Short: "Approve a Ticket's plan for implementation",
		Args:  optionalStdinAfter("TICKET"),
		RunE: func(command *cobra.Command, args []string) error {
			service, err := options.ticketService()
			if err != nil {
				return err
			}
			approver, err := resolveClaimant(command, as)
			if err != nil {
				return err
			}
			note := ""
			if len(args) == 2 {
				note, err = readTicketStdin(command)
				if err != nil {
					return err
				}
			}
			return approveTicket(command, options, service, args[0], approver, note, agentID)
		},
	}
	command.Flags().StringVar(&as, "as", "", "Set the approver name")
	command.Flags().StringVar(&agentID, "agent", "", "Relay into this Agent Session when several are live")
	setArguments(command, requiredArgument("ticket"), stdinTokenArgument(false))
	command.ValidArgsFunction = ticketSlugCompletion(options)
	return command
}

// approveTicket stamps the approval and, when the approval answered the
// planning agent's waiting ask, relays it into the live session. Both the
// tickets approve command and apply use it.
func approveTicket(command *cobra.Command, options Options, service ticketservice.Store, ref, approver, note, agentID string) error {
	if isDryRun(command) {
		ticket, err := service.Approve(ref, approver, note, true)
		if err != nil {
			return err
		}
		return writeMutation(command, "tickets.approve", statusValid, ticket.Slug, ticket.Title)
	}
	// The approval answers a waiting ask; remember whether one is open so the
	// relay fires only then.
	waiting := false
	if before, err := service.Resolve(ref); err == nil {
		waiting = before.Status == domain.TicketNeedsInfo
	}
	ticket, err := service.Approve(ref, approver, note, false)
	if err != nil {
		return err
	}
	var relay *answerRelay
	if waiting {
		reply := "Plan approved."
		if trimmed := strings.TrimSpace(note); trimmed != "" {
			reply += "\n\n" + trimmed
		}
		result := relayTicketAnswer(options, ticket, reply, agentID)
		relay = &result
	}
	if WantsJSON(command) {
		return writeJSONOutput(command, ticketApproveOutput{
			SchemaVersion: jsonSchemaVersion, Operation: "tickets.approve", Status: statusApplied,
			ID: ticket.Slug, Name: ticket.Title, Relay: relay,
		})
	}
	if _, err := fmt.Fprintf(command.OutOrStdout(), "Approved the plan on ticket %q\n", ticket.Slug); err != nil {
		return err
	}
	if relay == nil {
		return nil
	}
	if relay.Delivered {
		_, err = fmt.Fprintf(command.OutOrStdout(), "Relayed the approval to Agent Session %s\n", relay.AgentID)
		return err
	}
	_, err = fmt.Fprintf(command.ErrOrStderr(),
		"Warning: the approval is recorded on the ticket but was not relayed: %s\n", relay.Reason)
	return err
}
