package cli

import (
	"fmt"
	"strings"

	"github.com/jpugliesi/tmux-worktree/internal/clierr"
	"github.com/jpugliesi/tmux-worktree/internal/domain"
	ticketservice "github.com/jpugliesi/tmux-worktree/internal/ticket"
	"github.com/spf13/cobra"
)

func newTicketsStartCommand(options Options) *cobra.Command {
	var name, templateName, as string
	var withAgent, detached bool
	command := &cobra.Command{
		Use:   "start [TICKET...] [--name NAME] [--template TEMPLATE] [--as NAME] [--with-agent] [--detached]",
		Short: "Claim Tickets and start one Workspace for them",
		Args:  cobra.ArbitraryArgs,
		PreRunE: func(command *cobra.Command, args []string) error {
			if WantsJSON(command) && (!detached || len(args) == 0) {
				return invalidUsage(command, "%s uses interactive text output; JSON requires explicit TICKET arguments and --detached", command.CommandPath())
			}
			return nil
		},
		RunE: func(command *cobra.Command, args []string) error {
			request := quickCreateRequest{
				Name:         name,
				TemplateName: templateName,
				KeepCurrent:  true,
				WithAgent:    withAgent,
				Detached:     detached,
			}
			if len(args) > 0 {
				tickets, err := resolveStartTicketRefs(options, args)
				if err != nil {
					return err
				}
				return startFromTickets(command, options, tickets, request, as)
			}
			tickets, err := listStartableTickets(options)
			if err != nil {
				return err
			}
			if len(tickets) == 0 {
				return invalidUsageWithHint(command, "Create a Ticket with --project, or pass TICKET.",
					"no startable Tickets")
			}
			ticket, err := pickStartTicket(command, options, tickets)
			if err != nil {
				return err
			}
			return startFromTicket(command, options, ticket, request, as)
		},
	}
	command.Flags().StringVar(&name, "name", "", "Set the Workspace name; empty uses the Ticket slug")
	command.Flags().StringVar(&templateName, "template", "", "Select the Workspace Template instead of the current Workspace's template")
	command.Flags().StringVar(&as, "as", "", "Set the claimant name")
	command.Flags().BoolVar(&withAgent, "with-agent", false, "Start one configured Ticket planning Agent Session")
	command.Flags().BoolVarP(&detached, "detached", "d", false, "Create and start the Workspace without opening or switching tmux")
	setArguments(command, variadicArgument("ticket", false, "the picker asks for one Ticket when absent; many values must be Ticket slugs from one Project"))
	command.ValidArgsFunction = ticketSlugsCompletion(options)
	_ = command.RegisterFlagCompletionFunc("template", templateFlagCompletion(options.templateStore()))
	return command
}

// startFromTicket claims one Ticket, then runs the quick-create flow and
// links the new Workspace to that Ticket.
func startFromTicket(command *cobra.Command, options Options, ticket domain.Ticket, request quickCreateRequest, as string) error {
	return startFromTickets(command, options, []domain.Ticket{ticket}, request, as)
}

// listStartableTickets lists open Tickets that start can claim. A Ticket
// without a Project cannot start a Workspace, so the picker omits it.
func listStartableTickets(options Options) ([]domain.Ticket, error) {
	service, err := options.ticketService()
	if err != nil {
		return nil, err
	}
	tickets, err := service.List(ticketservice.ListFilter{})
	if err != nil {
		return nil, err
	}
	startable := make([]domain.Ticket, 0, len(tickets))
	for _, ticket := range tickets {
		if ticket.Project != "" {
			startable = append(startable, ticket)
		}
	}
	return startable, nil
}

func resolveStartTicketRefs(options Options, refs []string) ([]domain.Ticket, error) {
	service, err := options.ticketService()
	if err != nil {
		return nil, err
	}
	tickets := make([]domain.Ticket, 0, len(refs))
	for _, ref := range refs {
		ticket, err := service.Resolve(ref)
		if err != nil {
			return nil, err
		}
		tickets = append(tickets, ticket)
	}
	return tickets, nil
}

func startFromTickets(command *cobra.Command, options Options, tickets []domain.Ticket, request quickCreateRequest, as string) error {
	project, err := validateStartTickets(tickets)
	if err != nil {
		return err
	}
	workspaceName := strings.TrimSpace(request.Name)
	if workspaceName == "" {
		workspaceName = tickets[0].Slug
	}
	request.Name = workspaceName
	request.Project = project
	request.Tickets = make([]string, 0, len(tickets))
	for _, ticket := range tickets {
		request.Tickets = append(request.Tickets, ticket.Slug)
	}
	if request.WithAgent {
		launch, err := options.ticketPlanningLaunch(request.Tickets)
		if err != nil {
			return err
		}
		request.PlanningAgent = &launch
	}
	service, err := options.ticketService()
	if err != nil {
		return err
	}
	claimant, err := resolveClaimant(command, as)
	if err != nil {
		return err
	}
	// Every claim comes first. A claim failure rolls back only the claims that
	// this command added, before any Workspace work starts.
	if err := claimStartTickets(command, service, tickets, claimant); err != nil {
		return err
	}
	// A create failure keeps the claim: the create error already tells how to
	// retry the setup.
	if err := runQuickCreate(command, options, request); err != nil {
		return err
	}
	if isDryRun(command) {
		return nil
	}
	// A start comment is best-effort: a comment failure does not fail start.
	for _, ticket := range tickets {
		var err error
		if WantsJSON(command) {
			_, err = service.Comment(ticket.Slug, fmt.Sprintf("Started Workspace %s.", workspaceName), false)
		} else {
			err = commentTicket(command, service, ticket.Slug, fmt.Sprintf("Started Workspace %s.", workspaceName))
		}
		if err != nil {
			_, _ = fmt.Fprintf(command.ErrOrStderr(), "Warning: twt could not add the start comment to Ticket %q: %v\n", ticket.Slug, err)
		}
	}
	return nil
}

func validateStartTickets(tickets []domain.Ticket) (string, error) {
	if len(tickets) == 0 {
		return "", clierr.New(clierr.InvalidUsage, "at least one Ticket is required")
	}
	project := tickets[0].Project
	if project == "" {
		return "", clierr.WithHint(clierr.New(clierr.PreconditionFailed, "Ticket %q has no Project", tickets[0].Slug),
			"Move the Ticket with 'twt tickets set %s --project PROJECT'.", tickets[0].Slug)
	}
	seen := map[string]bool{}
	for _, ticket := range tickets {
		if seen[ticket.Slug] {
			return "", clierr.New(clierr.InvalidUsage, "Ticket %q is listed more than once", ticket.Slug)
		}
		seen[ticket.Slug] = true
		if ticket.Project != project {
			return "", clierr.New(clierr.InvalidUsage, "Tickets in one Workspace must belong to one Project; %q belongs to %q and %q belongs to %q", tickets[0].Slug, project, ticket.Slug, ticket.Project)
		}
		if ticket.Status == domain.TicketDone || ticket.Status == domain.TicketWontfix {
			return "", clierr.WithHint(clierr.New(clierr.PreconditionFailed, "Ticket %q is closed", ticket.Slug),
				"Select a Ticket from 'twt tickets list --ready'.")
		}
	}
	return project, nil
}

func claimStartTickets(command *cobra.Command, service ticketservice.Store, tickets []domain.Ticket, claimant string) error {
	for _, ticket := range tickets {
		if _, err := service.Claim(ticket.Slug, claimant, true); err != nil {
			return err
		}
	}
	if isDryRun(command) {
		if WantsJSON(command) {
			return nil
		}
		for _, ticket := range tickets {
			if err := claimTicket(command, service, ticket.Slug, claimant); err != nil {
				return err
			}
		}
		return nil
	}
	claimed := make([]string, 0, len(tickets))
	for _, ticket := range tickets {
		if _, err := service.Claim(ticket.Slug, claimant, false); err != nil {
			for index := len(claimed) - 1; index >= 0; index-- {
				_, _ = service.Unclaim(claimed[index], claimant, false)
			}
			return err
		}
		if ticket.ClaimedBy == "" {
			claimed = append(claimed, ticket.Slug)
		}
	}
	for _, ticket := range tickets {
		if WantsJSON(command) {
			continue
		}
		if _, err := fmt.Fprintf(command.OutOrStdout(), "Claimed ticket %q as %q\n", ticket.Slug, claimant); err != nil {
			return err
		}
	}
	return nil
}
