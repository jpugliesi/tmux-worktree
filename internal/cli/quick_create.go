package cli

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	agentservice "github.com/jpugliesi/tmux-worktree/internal/agent"
	"github.com/jpugliesi/tmux-worktree/internal/clierr"
	"github.com/jpugliesi/tmux-worktree/internal/domain"
	projectservice "github.com/jpugliesi/tmux-worktree/internal/project"
	ticketservice "github.com/jpugliesi/tmux-worktree/internal/ticket"
	"github.com/spf13/cobra"
)

// quickCreateRequest describes one quick-create run. Both twt start and twt
// tickets start use the same flow.
type quickCreateRequest struct {
	// Name is the new Project name. An empty value asks for it in an
	// interactive terminal.
	Name string
	// TemplateName selects the Project Template. An empty value uses the
	// template of the current Project, or infers one outside a Project.
	TemplateName string
	// KeepCurrent keeps the current Project active after the switch.
	KeepCurrent bool
	// NoFetch turns the default-branch refresh before the claim off.
	NoFetch bool
	// Branch is an optional custom Project branch name.
	Branch string
	// Ticket is the slug of the Ticket that the new Project works on.
	Ticket string
}

func newQuickCreateCommand(options Options) *cobra.Command {
	var templateName string
	var keepCurrent bool
	var noFetch bool
	var branch string
	var as string
	command := &cobra.Command{
		Use:     "start [NAME]",
		Short:   "Create a new Project and archive the current Project",
		Args:    optionalArg("NAME"),
		PreRunE: refuseJSONQuickCreate,
		RunE: func(command *cobra.Command, args []string) error {
			name := ""
			if len(args) == 1 {
				name = args[0]
			}
			return runStart(command, options, quickCreateRequest{
				Name:         name,
				TemplateName: templateName,
				KeepCurrent:  keepCurrent,
				NoFetch:      noFetch,
				Branch:       branch,
			}, as)
		},
	}
	command.Flags().StringVar(&templateName, "template", "", "Select the Project Template instead of the current Project's template")
	command.Flags().BoolVar(&keepCurrent, "keep-current", false, "Switch to the new Project and keep the current Project active")
	command.Flags().BoolVar(&noFetch, "no-fetch", false, "Do not refresh the default branch before the claim")
	command.Flags().StringVar(&branch, "branch", "", "Set a custom Project branch name")
	command.Flags().StringVar(&as, "as", "", "Set the claimant name when start claims a Ticket")
	setArguments(command, optionalArgument("name", "the Ticket picker asks for it when absent. A Ticket slug claims that Ticket. TAB offers Ticket slugs"))
	command.ValidArgsFunction = ticketSlugCompletion(options)
	_ = command.RegisterFlagCompletionFunc("template", templateFlagCompletion(options.templateStore()))
	return command
}

// runStart starts a Project. A Ticket slug claims that Ticket first. With no
// name and at least one open Ticket, it shows the Ticket picker. With no
// Tickets it asks for a Project name.
func runStart(command *cobra.Command, options Options, request quickCreateRequest, as string) error {
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
// Tickets home returns no Tickets so start can still ask for a Project name.
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
		boardName := ticket.Board
		if boardName == "" {
			boardName = "-"
		}
		lines = append(lines, fmt.Sprintf("%s\t%s\t%d\t%s\t%s", ticket.Slug, ticket.Status, ticket.Priority, boardName, ticket.Title))
	}
	pick := options.TicketPick
	if pick == nil {
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
		MissingHint: "missing Ticket; use 'twt start NAME' or 'twt tickets start TICKET' in a script",
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
		return invalidUsage(command, "quick create uses interactive text output; use 'twt projects create' for JSON automation")
	}
	return nil
}

// runQuickCreate is the shared quick-create flow: it creates the new Project,
// switches the calling tmux client to it, and archives the current Project
// unless the request keeps it. Outside a Project session, it only creates and
// opens the new Project.
func runQuickCreate(command *cobra.Command, options Options, request quickCreateRequest) error {
	service := options.projectService()
	currentPane := os.Getenv("TMUX_PANE")
	directory, err := os.Getwd()
	if err != nil {
		return err
	}
	current, err := service.CurrentForQuickCreate(directory, os.Getenv("TWT_PROJECT_ID"), currentPane)
	known := err == nil
	if err != nil && !errors.Is(err, projectservice.ErrNotInProject) {
		return err
	}
	// The tmux client switch and the archive of the current Project
	// need the calling pane. Without a pane, quick create uses the
	// outside-session flow and keeps the current Project active.
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
	if !outside && !isDryRun(command) {
		clientName, err = callingTmuxClient(options, currentPane)
		if err != nil {
			return err
		}
	}
	template, err := templateStore.Load(selected)
	if err != nil {
		return err
	}
	name := request.Name
	if name == "" {
		name, err = quickCreateName(command)
		if err != nil {
			return err
		}
	}
	createOptions := projectservice.CreateOptions{Branch: request.Branch, NoFetch: request.NoFetch, Ticket: request.Ticket}
	if isDryRun(command) {
		if err := validateCreate(options, service, name, selected, template, createOptions); err != nil {
			return err
		}
		return writeMutation(command, "projects.quick_create", statusValid, "", name)
	}

	created, err := createProject(command, options, name, selected, template, createOptions)
	if err != nil {
		return err
	}
	out := command.OutOrStdout()

	if outside {
		if _, err := fmt.Fprintf(out, "Created Project %q (%s)\n", created.Name, created.ID); err != nil {
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
	message := fmt.Sprintf("Created Project %q; switching to it and archiving Project %q\n", created.Name, current.Name)
	if request.KeepCurrent {
		message = fmt.Sprintf("Created Project %q; switching to it; Project %q stays active\n", created.Name, current.Name)
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
		return fmt.Errorf("new Project %q is active, but old Project %q was not archived: %w; run 'twt archive %s' if the archive failure window appears", created.Name, current.Name, err, current.ID)
	}
	return nil
}

// quickCreateSwitchFailure keeps the new Project active after a failed tmux
// switch and tells the user how to open it.
func quickCreateSwitchFailure(created domain.Project, cause error) error {
	return fmt.Errorf("twt could not switch to the new Project: %w. The new Project %q is active. Run 'twt projects open %s'.", cause, created.Name, created.Name)
}

func quickCreateName(command *cobra.Command) (string, error) {
	if !interactiveInput(command.InOrStdin()) {
		return "", invalidUsage(command, "missing Project name; use 'twt start NAME' in a script")
	}
	if _, err := fmt.Fprint(command.ErrOrStderr(), "Project name: "); err != nil {
		return "", err
	}
	line, err := bufio.NewReader(command.InOrStdin()).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", fmt.Errorf("read Project name: %w", err)
	}
	name := strings.TrimSpace(line)
	if name == "" {
		return "", invalidUsage(command, "Project creation was canceled; no Project name was given")
	}
	return name, nil
}
