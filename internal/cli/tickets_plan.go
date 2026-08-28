package cli

import (
	"fmt"
	"io"

	ticketservice "github.com/jpugliesi/tmux-worktree/internal/ticket"
	"github.com/spf13/cobra"
)

func newTicketsPlanCommand(options Options) *cobra.Command {
	var as string
	command := &cobra.Command{
		Use:   "plan TICKET [-]",
		Short: "Replace the ## Plan section of a Ticket",
		Args:  optionalStdinAfter("TICKET"),
		RunE: func(command *cobra.Command, args []string) error {
			service, err := options.ticketService()
			if err != nil {
				return err
			}
			text, err := readTicketPlanText(command, options, service, args[0], len(args) == 2)
			if err != nil {
				return err
			}
			return planTicket(command, service, args[0], as, text)
		},
	}
	command.Flags().StringVar(&as, "as", "", "Set the claimant name of a claimed Ticket")
	setArguments(command, requiredArgument("ticket"), stdinTokenArgument(false))
	command.ValidArgsFunction = ticketSlugCompletion(options)
	return command
}

// readTicketPlanText reads the plan from standard input or from VISUAL or
// EDITOR. The editor path seeds a draft with the current ## Plan section.
func readTicketPlanText(command *cobra.Command, options Options, service ticketservice.Store, ref string, fromStdin bool) (string, error) {
	if fromStdin {
		return readTicketStdin(command)
	}
	if !interactiveTicketSession(command) {
		return "", invalidUsageWithHint(command, "Pass - to read the plan text from standard input.",
			"%s has no terminal", command.CommandPath())
	}
	shown, err := service.Show(ref)
	if err != nil {
		return "", err
	}
	return readPlanDraftInEditor(command, options, ticketservice.PlanSection(shown.Body),
		"Write the plan and save the file, or pass -.")
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
