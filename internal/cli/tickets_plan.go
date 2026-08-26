package cli

import (
	"fmt"
	"io"

	ticketservice "github.com/jpugliesi/tmux-worktree/internal/ticket"
	"github.com/spf13/cobra"
)

func newTicketsPlanCommand(options Options) *cobra.Command {
	var fromStdin bool
	var as string
	command := &cobra.Command{
		Use:   "plan TICKET --stdin [--as NAME]",
		Short: "Replace the ## Plan section of a Ticket",
		Args:  exactArgs("TICKET"),
		RunE: func(command *cobra.Command, args []string) error {
			if !fromStdin {
				return invalidUsageWithHint(command, "Pass the plan text on standard input with --stdin.",
					"missing required flag --stdin")
			}
			service, err := options.ticketService()
			if err != nil {
				return err
			}
			text, err := readTicketStdin(command)
			if err != nil {
				return err
			}
			return planTicket(command, service, args[0], as, text)
		},
	}
	command.Flags().BoolVar(&fromStdin, "stdin", false, "Read the plan text from standard input")
	command.Flags().StringVar(&as, "as", "", "Set the claimant name of a claimed Ticket")
	setArguments(command, requiredArgument("ticket"))
	command.ValidArgsFunction = ticketSlugCompletion(options)
	return command
}

// planTicket replaces the Plan section. Both the tickets plan command and
// apply use it.
func planTicket(command *cobra.Command, service ticketservice.Store, ref, claimant, text string) error {
	return runMutation(command, "tickets.plan",
		func() (string, string, error) {
			ticket, err := service.SetPlanSection(ref, claimant, text, true)
			return ticket.Slug, ticket.Title, err
		},
		func() (string, string, error) {
			ticket, err := service.SetPlanSection(ref, claimant, text, false)
			return ticket.Slug, ticket.Title, err
		},
		func(out io.Writer, id, _ string) error {
			_, err := fmt.Fprintf(out, "Wrote the plan of ticket %q\n", id)
			return err
		})
}


