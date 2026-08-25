package cli

import (
	"errors"
	"fmt"
	"os"
	"strings"

	agentservice "github.com/jpugliesi/tmux-worktree/internal/agent"
	"github.com/jpugliesi/tmux-worktree/internal/agentprovider"
	"github.com/jpugliesi/tmux-worktree/internal/clierr"
	"github.com/jpugliesi/tmux-worktree/internal/domain"
	ticketservice "github.com/jpugliesi/tmux-worktree/internal/ticket"
	workspaceservice "github.com/jpugliesi/tmux-worktree/internal/workspace"
	"github.com/spf13/cobra"
)

// quickCreateRequest describes one quick-create run. Both twt next and twt
// tickets start use the same flow.
type quickCreateRequest struct {
	// Name is the new Workspace name. An empty value asks for it in an
	// interactive terminal.
	Name string
	// TemplateName selects the Workspace Template. An empty value uses the
	// template of the current Workspace, or infers one outside a Workspace.
	TemplateName string
	// KeepCurrent keeps the current Workspace active after the switch.
	KeepCurrent bool
	// NoFetch turns the default-branch refresh before the claim off.
	NoFetch bool
	// Branch is an optional custom Workspace branch name.
	Branch string
	// Tickets are the Ticket slugs that the new Workspace works on.
	Tickets []string
	// Project is the durable Project of Tickets.
	Project string
	// Detached creates and starts the Workspace without opening or switching
	// the calling tmux client.
	Detached bool
	// WithAgent asks tickets start to add one planning Agent Session.
	WithAgent bool
	// PlanningAgent is the validated provider launch for the generated Agent.
	PlanningAgent *agentprovider.TicketPlanningLaunch
}

func newNextCommand(options Options) *cobra.Command {
	var templateName string
	var noFetch bool
	var branch string
	var as string
	command := &cobra.Command{
		Use:     "next [NAME|TICKET...]",
		Short:   "Create the next Workspace and archive the current Workspace",
		Args:    func(_ *cobra.Command, _ []string) error { return nil },
		PreRunE: refuseJSONQuickCreate,
		RunE: func(command *cobra.Command, args []string) error {
			if err := requireNextContext(command, options); err != nil {
				return err
			}
			request := quickCreateRequest{
				TemplateName: templateName,
				NoFetch:      noFetch,
				Branch:       branch,
			}
			if len(args) > 1 {
				tickets, err := resolveStartTicketRefs(options, args)
				if err != nil {
					return err
				}
				return startFromTickets(command, options, tickets, request, as)
			}
			if len(args) == 1 {
				request.Name = args[0]
			}
			return runNext(command, options, request, as)
		},
	}
	command.Flags().StringVar(&templateName, "template", "", "Select the Workspace Template instead of the current Workspace's template")
	command.Flags().BoolVar(&noFetch, "no-fetch", false, "Do not fetch the default branch before the claim")
	command.Flags().StringVar(&branch, "branch", "", "Set a custom Workspace branch name")
	command.Flags().StringVar(&as, "as", "", "Set the claimant name when next claims a Ticket")
	setArguments(command, variadicArgument("name_or_ticket", false, "one value can be a Workspace name or Ticket slug; many values must be Ticket slugs from one Project"))
	command.ValidArgsFunction = ticketSlugsCompletion(options)
	_ = command.RegisterFlagCompletionFunc("template", templateFlagCompletion(options.templateStore()))
	return command
}

// requireNextContext checks the context before next claims a Ticket or creates
// a Workspace. A real next run needs the current Workspace tmux pane because
// it moves that client before it archives the Workspace.
func requireNextContext(command *cobra.Command, options Options) error {
	directory, err := os.Getwd()
	if err != nil {
		return err
	}
	currentPane := os.Getenv("TMUX_PANE")
	_, err = options.workspaceService().CurrentForQuickCreate(directory, workspaceIDFromEnvironment(), currentPane)
	if errors.Is(err, workspaceservice.ErrNotInWorkspace) {
		return invalidUsage(command, "no current Workspace; use 'twt create NAME' to create one")
	}
	if err != nil {
		return err
	}
	if currentPane == "" && !isDryRun(command) {
		return invalidUsage(command, "next must run inside the current Workspace tmux session")
	}
	return nil
}

// runNext creates the next Workspace. A Ticket slug claims that Ticket first. With no
// name and at least one open Ticket, it shows the Ticket picker. With no
// Tickets it asks for a Workspace name.
func runNext(command *cobra.Command, options Options, request quickCreateRequest, as string) error {
	name := strings.TrimSpace(request.Name)
	if name != "" {
		ticket, ok, err := resolveStartTicket(options, name)
		if err != nil {
			return err
		}
		if ok {
			request.Name = ""
			return startFromTicket(command, options, ticket, request, as)
		}
		return runQuickCreate(command, options, request)
	}
	tickets, err := listOpenStartTickets(options)
	if err != nil {
		return err
	}
	if len(tickets) == 0 {
		return runQuickCreate(command, options, request)
	}
	ticket, err := pickStartTicket(command, options, tickets)
	if err != nil {
		return err
	}
	return startFromTicket(command, options, ticket, request, as)
}

// resolveStartTicket maps NAME to a Ticket when a Tickets home is set and
// the name matches. A missing home or an unknown name is not an error.
func resolveStartTicket(options Options, name string) (domain.Ticket, bool, error) {
	service, err := options.ticketService()
	if err != nil {
		if clierr.CodeOf(err) == clierr.PreconditionFailed {
			return domain.Ticket{}, false, nil
		}
		return domain.Ticket{}, false, err
	}
	ticket, err := service.Resolve(name)
	if err != nil {
		if clierr.CodeOf(err) == clierr.NotFound {
			return domain.Ticket{}, false, nil
		}
		return domain.Ticket{}, false, err
	}
	return ticket, true, nil
}

// listOpenStartTickets lists open Tickets for the start picker. A missing
// Tickets home returns no Tickets so start can still ask for a Workspace name.
func listOpenStartTickets(options Options) ([]domain.Ticket, error) {
	service, err := options.ticketService()
	if err != nil {
		if clierr.CodeOf(err) == clierr.PreconditionFailed {
			return nil, nil
		}
		return nil, err
	}
	return service.List(ticketservice.ListFilter{})
}

// pickStartTicket shows the interactive Ticket picker and returns the
// selected Ticket.
func pickStartTicket(command *cobra.Command, options Options, tickets []domain.Ticket) (domain.Ticket, error) {
	lines := make([]string, 0, len(tickets))
	for _, ticket := range tickets {
		projectName := ticket.Project
		if projectName == "" {
			projectName = "-"
		}
		lines = append(lines, fmt.Sprintf("%s\t%s\t%d\t%s\t%s", ticket.Slug, ticket.Status, ticket.Priority, projectName, ticket.Title))
	}
	pick := options.TicketPick
	if pick == nil {
		if !interactiveTicketSession(command) {
			return domain.Ticket{}, invalidUsage(command, "missing Ticket; use 'twt next NAME' or 'twt tickets start TICKET' in a script")
		}
		pick = realTicketPick
	}
	index, err := pick(command, lines)
	if err != nil {
		return domain.Ticket{}, err
	}
	if index < 0 || index >= len(tickets) {
		return domain.Ticket{}, fmt.Errorf("the Ticket picker returned an invalid selection")
	}
	return tickets[index], nil
}

// realTicketPick selects one picker line with fzf when it is installed, or
// with a numbered list on the terminal. The fzf preview writes the Ticket
// show text.
func realTicketPick(command *cobra.Command, lines []string) (int, error) {
	return pickLine(command, lines, pickOptions{
		Noun:        "Ticket",
		MissingHint: "missing Ticket; use 'twt next NAME' or 'twt tickets start TICKET' in a script",
		FzfArgs: []string{
			"--delimiter", "\t",
			"--preview", ticketStartPreviewCommand(),
			"--preview-window", "right:60%:wrap",
		},
	})
}

// ticketStartPreviewCommand is the fzf --preview command. fzf runs it with a
// shell and replaces {1} with the Ticket slug of the highlighted line.
func ticketStartPreviewCommand() string {
	executable, err := os.Executable()
	if err != nil {
		executable = os.Args[0]
	}
	return fmt.Sprintf("%s tickets show --output text %s", shellQuote(executable), "'{1}'")
}

// refuseJSONQuickCreate refuses --output json before the quick-create flow
// starts. The flow moves the calling tmux client, so it is interactive.
func refuseJSONQuickCreate(command *cobra.Command, _ []string) error {
	if WantsJSON(command) {
		return invalidUsage(command, "%s uses interactive text output; use 'twt create' for JSON automation", command.CommandPath())
	}
	return nil
}

// runQuickCreate is the shared quick-create flow: it creates the new Workspace,
// switches the calling tmux client to it, and archives the current Workspace
// unless the request keeps it. Outside a Workspace session, it only creates and
// opens the new Workspace.
func runQuickCreate(command *cobra.Command, options Options, request quickCreateRequest) error {
	service := options.workspaceService()
	currentPane := os.Getenv("TMUX_PANE")
	directory, err := os.Getwd()
	if err != nil {
		return err
	}
	current, err := service.CurrentForQuickCreate(directory, workspaceIDFromEnvironment(), currentPane)
	known := err == nil
	if err != nil && !errors.Is(err, workspaceservice.ErrNotInWorkspace) {
		return err
	}
	// The tmux client switch and the archive of the current Workspace
	// need the calling pane. Without a pane, quick create uses the
	// outside-session flow and keeps the current Workspace active.
	outside := !known || currentPane == ""
	templateStore := options.templateStore()
	selected := strings.TrimSpace(request.TemplateName)
	if selected == "" {
		if known {
			selected = current.TemplateName
		} else {
			inferred, source, err := inferTemplateName(command, options, templateStore)
			if err != nil {
				return err
			}
			selected = inferred
			_, _ = fmt.Fprintf(command.ErrOrStderr(), "Template: %s (%s)\n", selected, source)
		}
	}
	clientName := ""
	if !request.Detached && !outside && !isDryRun(command) {
		clientName, err = callingTmuxClient(options, currentPane)
		if err != nil {
			return err
		}
	}
	template, err := templateStore.Load(selected)
	if err != nil {
		return err
	}
	if request.PlanningAgent != nil {
		template = addTicketPlanningAgent(template, *request.PlanningAgent)
	}
	name := request.Name
	if name == "" {
		name, err = quickCreateName(command)
		if err != nil {
			return err
		}
	}
	createOptions := workspaceservice.CreateOptions{Branch: request.Branch, NoFetch: request.NoFetch, Tickets: request.Tickets, Project: request.Project}
	if isDryRun(command) {
		if err := validateCreate(options, service, name, selected, template, createOptions); err != nil {
			return err
		}
		return writeMutation(command, quickCreateOperation(request), statusValid, "", name)
	}

	created, err := createWorkspace(command, options, name, selected, template, createOptions)
	if err != nil {
		return err
	}
	out := command.OutOrStdout()
	if request.Detached {
		if WantsJSON(command) {
			return writeMutation(command, quickCreateOperation(request), statusApplied, created.ID, created.Name)
		}
		_, err := fmt.Fprintf(out, "Created Workspace %q (%s) in detached mode\n", created.Name, created.ID)
		return err
	}

	if outside {
		if _, err := fmt.Fprintf(out, "Created Workspace %q (%s)\n", created.Name, created.ID); err != nil {
			return err
		}
		if err := options.QuickCreateSwitch("", created.TmuxSession); err != nil {
			return quickCreateSwitchFailure(created, err)
		}
		return nil
	}

	if !request.KeepCurrent {
		if liveAgents, liveErr := agentservice.NewService(options.StateDir, options.TmuxSocket).Live(current.ID); liveErr == nil {
			if err := printStoppedAgents(out, liveAgents); err != nil {
				return err
			}
		}
	}
	message := fmt.Sprintf("Created Workspace %q; switching to it and archiving Workspace %q\n", created.Name, current.Name)
	if request.KeepCurrent {
		message = fmt.Sprintf("Created Workspace %q; switching to it; Workspace %q stays active\n", created.Name, current.Name)
	}
	if _, err := fmt.Fprint(out, message); err != nil {
		return quickCreateSwitchFailure(created, fmt.Errorf("write quick create result: %w", err))
	}
	if err := options.QuickCreateSwitch(clientName, created.TmuxSession); err != nil {
		return quickCreateSwitchFailure(created, err)
	}
	if request.KeepCurrent {
		return nil
	}
	if err := options.QuickCreateArchive(clientName, current.ID, created.ID); err != nil {
		return fmt.Errorf("new Workspace %q is active, but old Workspace %q was not archived: %w; run 'twt archive %s' if the archive failure window appears", created.Name, current.Name, err, current.ID)
	}
	return nil
}

func quickCreateOperation(request quickCreateRequest) string {
	if request.Detached {
		return "tickets.start"
	}
	return "workspaces.next"
}

// quickCreateSwitchFailure keeps the new Workspace active after a failed tmux
// switch and tells the user how to open it.
func quickCreateSwitchFailure(created domain.Workspace, cause error) error {
	return fmt.Errorf("twt could not switch to the new Workspace: %w. The new Workspace %q is active. Run 'twt workspaces open %s'.", cause, created.Name, created.Name)
}

func quickCreateName(command *cobra.Command) (string, error) {
	return promptWorkspaceName(command, "missing Workspace name; use 'twt next NAME' in a script")
}

// canPromptWorkspaceName reports whether create or next can ask for a
// Workspace name. JSON output and a non-terminal session fail immediately.
func canPromptWorkspaceName(command *cobra.Command) bool {
	return resolvedOutputFormat(command) == outputText && interactiveTicketSession(command)
}

// promptWorkspaceName asks for a Workspace name on a person terminal. A
// script, a pipe, and JSON output return invalid_usage.
func promptWorkspaceName(command *cobra.Command, missingHint string) (string, error) {
	if !canPromptWorkspaceName(command) {
		return "", invalidUsage(command, "%s", missingHint)
	}
	name, err := promptTicketLine(command, "Workspace name: ")
	if err != nil {
		return "", err
	}
	if name == "" {
		return "", invalidUsage(command, "Workspace creation was canceled; no Workspace name was given")
	}
	return name, nil
}
