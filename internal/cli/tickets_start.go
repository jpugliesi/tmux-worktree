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
	var keepCurrent bool
	command := &cobra.Command{
		Use:     "start TICKET... [--name NAME] [--template TEMPLATE] [--as NAME]",
		Short:   "Claim Tickets and start one Workspace for them",
		Args:    oneOrMoreArgs("TICKET"),
		PreRunE: refuseJSONQuickCreate,
		RunE: func(command *cobra.Command, args []string) error {
			tickets, err := resolveStartTicketRefs(options, args)
			if err != nil {
				return err
			}
			return startFromTickets(command, options, tickets, quickCreateRequest{
				Name:         name,
				TemplateName: templateName,
				KeepCurrent:  keepCurrent,
			}, as)
		},
	}
	command.Flags().StringVar(&name, "name", "", "Set the Workspace name; empty uses the Ticket slug")
	command.Flags().StringVar(&templateName, "template", "", "Select the Workspace Template instead of the current Workspace's template")
	command.Flags().BoolVar(&keepCurrent, "keep-current", false, "Switch to the new Workspace and keep the current Workspace active")
	command.Flags().StringVar(&as, "as", "", "Set the claimant name")
	setArguments(command, variadicArgument("ticket", true, "all Tickets must be open and belong to one Project"))
	command.ValidArgsFunction = ticketSlugsCompletion(options)
	_ = command.RegisterFlagCompletionFunc("template", templateFlagCompletion(options.templateStore()))
	return command
}

// startFromTicket claims one Ticket, then runs the quick-create flow and
// links the new Workspace to that Ticket.
func startFromTicket(command *cobra.Command, options Options, ticket domain.Ticket, request quickCreateRequest, as string) error {
	return startFromTickets(command, options, []domain.Ticket{ticket}, request, as)
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
		if err := commentTicket(command, service, ticket.Slug, fmt.Sprintf("Started Workspace %s.", workspaceName)); err != nil {
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

func claimStartTickets(command *cobra.Command, service *ticketservice.Service, tickets []domain.Ticket, claimant string) error {
	for _, ticket := range tickets {
		if _, err := service.Claim(ticket.Slug, claimant, true); err != nil {
			return err
		}
	}
	if isDryRun(command) {
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
		if _, err := fmt.Fprintf(command.OutOrStdout(), "Claimed ticket %q as %q\n", ticket.Slug, claimant); err != nil {
			return err
		}
	}
	return nil
}
