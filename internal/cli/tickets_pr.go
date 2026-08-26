package cli

import (
	"fmt"
	"io"

	"github.com/jpugliesi/tmux-worktree/internal/domain"
	ticketservice "github.com/jpugliesi/tmux-worktree/internal/ticket"
	"github.com/spf13/cobra"
)

func newTicketsPRCommand(options Options) *cobra.Command {
	pr := groupCommand(&cobra.Command{
		Use:   "pr",
		Short: "Manage the pull requests linked to a Ticket",
	})
	pr.AddCommand(newTicketsPRAddCommand(options))
	pr.AddCommand(newTicketsPRRemoveCommand(options))
	return pr
}

func newTicketsPRAddCommand(options Options) *cobra.Command {
	var urls []string
	var as string
	command := &cobra.Command{
		Use:   "add TICKET --pr URL [--pr URL]... [--as CLAIMANT]",
		Short: "Attach pull request URLs to a Ticket",
		Args:  exactArgs("TICKET"),
		RunE: func(command *cobra.Command, args []string) error {
			service, err := options.ticketService()
			if err != nil {
				return err
			}
			return mutateTicketPRs(command, service, "tickets.pr.add", args[0], as, urls, service.AddPullRequests,
				"Attached %d pull request(s) to ticket %q\n")
		},
	}
	command.Flags().StringArrayVar(&urls, "pr", nil, "Pull request URL (repeatable)")
	command.Flags().StringVar(&as, "as", "", "Set the claimant name of a claimed Ticket")
	setArguments(command, requiredArgument("ticket"))
	command.ValidArgsFunction = ticketSlugCompletion(options)
	return command
}

func newTicketsPRRemoveCommand(options Options) *cobra.Command {
	var urls []string
	var as string
	command := &cobra.Command{
		Use:   "rm TICKET --pr URL [--as CLAIMANT]",
		Short: "Detach pull request URLs from a Ticket",
		Args:  exactArgs("TICKET"),
		RunE: func(command *cobra.Command, args []string) error {
			service, err := options.ticketService()
			if err != nil {
				return err
			}
			return mutateTicketPRs(command, service, "tickets.pr.rm", args[0], as, urls, service.RemovePullRequests,
				"Detached %d pull request(s) from ticket %q\n")
		},
	}
	command.Flags().StringArrayVar(&urls, "pr", nil, "Pull request URL (repeatable)")
	command.Flags().StringVar(&as, "as", "", "Set the claimant name of a claimed Ticket")
	setArguments(command, requiredArgument("ticket"))
	command.ValidArgsFunction = ticketSlugCompletion(options)
	return command
}

// mutateTicketPRs runs one attach or detach through the shared mutation
// envelope. Both the pr commands and apply use it.
func mutateTicketPRs(command *cobra.Command, service ticketservice.Store, operation, ref, claimant string,
	urls []string, run func(string, string, []string, bool) (domain.Ticket, error), successFormat string) error {
	return runMutation(command, operation,
		func() (string, string, error) {
			ticket, err := run(ref, claimant, urls, true)
			return ticket.Slug, ticket.Title, err
		},
		func() (string, string, error) {
			ticket, err := run(ref, claimant, urls, false)
			return ticket.Slug, ticket.Title, err
		},
		func(out io.Writer, id, _ string) error {
			_, err := fmt.Fprintf(out, successFormat, len(urls), id)
			return err
		})
}
