package cli

import (
	"fmt"
	"io"
	"os"
	"os/user"
	"path/filepath"
	"strings"

	"github.com/jpugliesi/tmux-worktree/internal/clierr"
	"github.com/jpugliesi/tmux-worktree/internal/domain"
	ticketservice "github.com/jpugliesi/tmux-worktree/internal/ticket"
	"github.com/spf13/cobra"
)

type ticketsListOutput struct {
	SchemaVersion int             `json:"schemaVersion"`
	Tickets       []domain.Ticket `json:"tickets"`
	TotalCount    int             `json:"totalCount"`
	Truncated     bool            `json:"truncated,omitempty"`
}

// ticketShowDetail is the show envelope body: the Ticket plus its body, its
// readiness, and each blocker that keeps it out of the ready queue.
type ticketShowDetail struct {
	domain.Ticket
	Body          string                      `json:"body"`
	Ready         bool                        `json:"ready"`
	BlockedByOpen []ticketservice.OpenBlocker `json:"blockedByOpen"`
}

type ticketShowOutput struct {
	SchemaVersion int              `json:"schemaVersion"`
	Ticket        ticketShowDetail `json:"ticket"`
}

func newTicketsCommand(options Options) *cobra.Command {
	tickets := groupCommand(&cobra.Command{
		Use:   "tickets",
		Short: "Manage Markdown tickets",
	})
	tickets.AddCommand(newTicketsInitCommand(options))
	tickets.AddCommand(newTicketsHomeCommand(options))
	tickets.AddCommand(newTicketsCreateCommand(options))
	tickets.AddCommand(newTicketsListCommand(options))
	tickets.AddCommand(newTicketsQueueCommand(options))
	tickets.AddCommand(newTicketsTreeCommand(options))
	tickets.AddCommand(newTicketsDispatchCommand(options))
	tickets.AddCommand(newTicketsSyncCommand(options))
	tickets.AddCommand(newTicketsAbandonCommand(options))
	tickets.AddCommand(newTicketsShowCommand(options))
	tickets.AddCommand(newTicketsSetCommand(options))
	tickets.AddCommand(newTicketsClaimCommand(options))
	tickets.AddCommand(newTicketsStartCommand(options))
	tickets.AddCommand(newTicketsUnclaimCommand(options))
	tickets.AddCommand(newTicketsCompleteCommand(options))
	tickets.AddCommand(newTicketsCloseCommand(options))
	tickets.AddCommand(newTicketsCommentCommand(options))
	tickets.AddCommand(newTicketsPlanCommand(options))
	tickets.AddCommand(newTicketsAskCommand(options))
	tickets.AddCommand(newTicketsAnswerCommand(options))
	tickets.AddCommand(newTicketsApproveCommand(options))
	tickets.AddCommand(newTicketsPRCommand(options))
	tickets.AddCommand(newTicketsDoctorCommand(options))
	tickets.AddCommand(newTicketsRepairCommand(options))
	return tickets
}

// invalidUsageWithHint builds an invalid_usage error with a recovery hint and
// the help command of the failing command.
func invalidUsageWithHint(command *cobra.Command, hint, format string, values ...any) error {
	return usageError{
		err:         clierr.WithHint(clierr.New(clierr.InvalidUsage, format, values...), hint),
		helpCommand: command.CommandPath() + " --help",
	}
}

// interactiveTicketSession reports whether a tickets command can open the
// editor or default the claimant. A replaced test input drives an interactive
// session; on the real standard input, both the input and the output must be
// terminals.
func interactiveTicketSession(command *cobra.Command) bool {
	if command.InOrStdin() != os.Stdin {
		return true
	}
	if !terminalTicketFile(os.Stdin) {
		return false
	}
	file, ok := command.OutOrStdout().(*os.File)
	return ok && terminalTicketFile(file)
}

// terminalTicketFile reports whether the file is a real terminal. The null
// device is a character device but never a terminal, so a null redirect does
// not open the editor.
func terminalTicketFile(file *os.File) bool {
	info, err := file.Stat()
	if err != nil || info.Mode()&os.ModeCharDevice == 0 {
		return false
	}
	if null, err := os.Stat(os.DevNull); err == nil && os.SameFile(info, null) {
		return false
	}
	return true
}

// readTicketStdin reads one body or comment text from standard input.
func readTicketStdin(command *cobra.Command) (string, error) {
	data, err := io.ReadAll(io.LimitReader(command.InOrStdin(), 1024*1024))
	if err != nil {
		return "", fmt.Errorf("read standard input: %w", err)
	}
	return string(data), nil
}

func newTicketsInitCommand(options Options) *cobra.Command {
	return &cobra.Command{
		Use:   "init",
		Short: "Create the Tickets home scaffold",
		Args:  noArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			service, err := options.ticketService()
			if err != nil {
				return err
			}
			return initializeTicketsHome(command, service)
		},
	}
}

// initializeTicketsHome writes the Tickets home scaffold. Both the tickets
// init command and apply use it.
func initializeTicketsHome(command *cobra.Command, service ticketservice.Store) error {
	var result ticketservice.InitResult
	return runMutation(command, "tickets.init",
		func() (string, string, error) {
			preview, err := service.Init(true)
			return "", preview.Home, err
		},
		func() (string, string, error) {
			var applyErr error
			result, applyErr = service.Init(false)
			return "", result.Home, applyErr
		},
		func(out io.Writer, _, _ string) error {
			if err := reportScaffold(out, result.WroteIndex, filepath.Join(result.Home, "index.md")); err != nil {
				return err
			}
			if err := reportScaffold(out, result.WroteTemplate, filepath.Join(result.Home, "templates", "ticket.md")); err != nil {
				return err
			}
			return reportScaffold(out, result.WroteClosedMarker, filepath.Join(result.Home, "closed", ".twt-closed"))
		})
}

// reportScaffold writes one init result line: Wrote for a new file, Kept for
// an existing note that init did not touch.
func reportScaffold(out io.Writer, wrote bool, path string) error {
	verb := "Kept"
	if wrote {
		verb = "Wrote"
	}
	_, err := fmt.Fprintf(out, "%s %q\n", verb, path)
	return err
}

func newTicketsHomeCommand(options Options) *cobra.Command {
	return &cobra.Command{
		Use:   "home",
		Short: "Open the Tickets home in your editor",
		Args:  noArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			// The editor is an interactive escape: it opens only for a person
			// at a terminal, so a piped call never blocks in an editor.
			if !interactiveTicketSession(command) {
				return invalidUsageWithHint(command,
					"Run 'twt tickets home' in a terminal.",
					"twt tickets home has no terminal")
			}
			home, err := options.resolveTicketsHome()
			if err != nil {
				return err
			}
			if home == "" {
				return clierr.WithHint(
					clierr.New(clierr.PreconditionFailed, "no Tickets home is set"),
					"Set ticketsHome in ~/.config/twt/config.yaml or TWT_TICKETS_HOME.")
			}
			home = filepath.Clean(home)
			return runMutation(command, "tickets.home",
				func() (string, string, error) {
					return "", home, nil
				},
				func() (string, string, error) {
					if err := options.OpenEditor(home); err != nil {
						return "", "", err
					}
					return "", home, nil
				},
				func(out io.Writer, _, path string) error {
					_, err := fmt.Fprintf(out, "Opened Tickets home %q\n", path)
					return err
				})
		},
	}
}

func newTicketsCreateCommand(options Options) *cobra.Command {
	var project, title, slug, status string
	var blockedBy []string
	command := &cobra.Command{
		Use:   "create [DESCRIPTION...]",
		Short: "Create a Ticket",
		Args:  cobra.ArbitraryArgs,
		RunE: func(command *cobra.Command, args []string) error {
			service, err := options.ticketService()
			if err != nil {
				return err
			}
			request := ticketservice.CreateRequest{
				Title:     title,
				Slug:      slug,
				Project:   project,
				Status:    domain.TicketStatus(status),
				Priority:  -1,
				BlockedBy: blockedBy,
			}
			switch {
			case len(args) == 1 && isStdinToken(args[0]):
				if strings.TrimSpace(title) == "" {
					return invalidUsageWithHint(command, "Pass --title together with -.",
						"- reads only the body and needs a title")
				}
				body, err := readTicketStdin(command)
				if err != nil {
					return err
				}
				request.Body = body
			case len(args) > 0:
				description := strings.Join(args, " ")
				if title == "" {
					first, rest, _ := strings.Cut(description, "\n")
					request.Title = strings.TrimSpace(first)
					request.Body = rest
				} else {
					request.Body = description
				}
			case interactiveTicketSession(command):
				wizardRequest, err := createTicketWizard(command, options, service, request)
				if err != nil {
					return err
				}
				request = wizardRequest
			case strings.TrimSpace(title) != "":
				// --title without DESCRIPTION outside a terminal keeps the
				// skeleton body. The wizard is TTY-only.
			default:
				return invalidUsageWithHint(command, "Pass DESCRIPTION, --title, or -.",
					"twt tickets create has no input and no terminal")
			}
			return createTicket(command, service, request)
		},
	}
	command.Flags().StringVar(&project, "project", "", "Put the Ticket in this Project")
	command.Flags().StringVar(&title, "title", "", "Set the Ticket title")
	command.Flags().StringVar(&slug, "slug", "", "Set the file slug; empty derives it from the title")
	command.Flags().StringVar(&status, "status", "", "Set the initial status; empty selects needs-triage")
	command.Flags().StringArrayVar(&blockedBy, "blocked-by", nil, "Add a blocker slug or wiki-link; repeat for more blockers")
	setArguments(command, variadicArgument("description", false, "the body, or the literal \"-\" to read standard input; the first line becomes the title when --title is absent"))
	setFlagEnum(command, "status", domain.TicketStatuses()...)
	registerProjectFlagCompletion(command, options)
	_ = command.RegisterFlagCompletionFunc("blocked-by", ticketFlagCompletion(options))
	return command
}

// createTicket writes one Ticket. Both the tickets create command and apply
// use it. A text dry run prints the file that twt would write.
func createTicket(command *cobra.Command, service ticketservice.Store, request ticketservice.CreateRequest) error {
	if isDryRun(command) {
		result, err := service.Create(request, true)
		if err != nil {
			return err
		}
		if WantsJSON(command) {
			return writeMutation(command, "tickets.create", statusValid, result.Ticket.Slug, result.Ticket.Title)
		}
		out := command.OutOrStdout()
		if request.EnsureProject && request.Project != "" {
			if _, err := fmt.Fprintf(out, "Would create Project %q\n", request.Project); err != nil {
				return err
			}
		}
		_, err = out.Write(result.Content)
		return err
	}
	result, err := service.Create(request, false)
	if err != nil {
		return err
	}
	if WantsJSON(command) {
		return writeMutation(command, "tickets.create", statusApplied, result.Ticket.Slug, result.Ticket.Title)
	}
	out := command.OutOrStdout()
	if request.EnsureProject && request.Project != "" {
		if _, err := fmt.Fprintf(out, "Created Project %q\n", request.Project); err != nil {
			return err
		}
	}
	_, err = fmt.Fprintf(out, "Created ticket %q at %q\n", result.Ticket.Slug, result.Ticket.Path)
	return err
}

func newTicketsListCommand(options Options) *cobra.Command {
	var project, status string
	var ready, claimed, needsInput, all, allProjects, fresh bool
	var limit, offset int
	command := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List Tickets",
		Args:    noArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			service, err := options.ticketService()
			if err != nil {
				return err
			}
			scope, err := resolveTicketProject(command, options, project, command.Flags().Changed("project"), allProjects)
			if err != nil {
				return err
			}
			freshenTicketStore(command, service, fresh)
			tickets, err := service.List(ticketservice.ListFilter{
				Project:    scope.Project,
				ProjectSet: scope.Set,
				Status:     status,
				Ready:      ready,
				Claimed:    claimed,
				NeedsInput: needsInput,
				All:        all,
			})
			if err != nil {
				return err
			}
			tickets, total, truncated, err := applyWindow(tickets, offset, limit)
			if err != nil {
				return err
			}
			if format := resolvedOutputFormat(command); format != outputText {
				if format == outputNDJSON {
					return writeNDJSONList(command, tickets, total, truncated)
				}
				return writeReadJSON(command, ticketsListOutput{SchemaVersion: jsonSchemaVersion, Tickets: tickets, TotalCount: total, Truncated: truncated}, "tickets")
			}
			if total == 0 {
				_, err = fmt.Fprintln(command.ErrOrStderr(), "No tickets match. Run 'twt tickets create DESCRIPTION'.")
				return err
			}
			return writeTicketList(command.OutOrStdout(), tickets, !scope.Set)
		},
	}
	command.Flags().StringVar(&project, "project", "", "List one Project; an empty value lists ungrouped Tickets")
	command.Flags().BoolVar(&allProjects, "all-projects", false, "List Tickets from every Project")
	command.Flags().StringVar(&status, "status", "", "List one status")
	command.Flags().BoolVar(&ready, "ready", false, "List only unclaimed, unblocked, ready-for-agent Tickets")
	command.Flags().BoolVar(&claimed, "claimed", false, "List only Tickets that have a claimant")
	command.Flags().BoolVar(&needsInput, "needs-input", false, "List only Tickets whose agent waits on the human")
	command.Flags().BoolVar(&all, "all", false, "Include closed tickets")
	addFreshFlag(command, &fresh)
	addListReadFlags(command, &limit, &offset, domain.Ticket{})
	setFlagEnum(command, "status", domain.TicketStatuses()...)
	registerProjectFlagCompletion(command, options)
	return command
}

// writeTicketList writes one Ticket table. A wide list includes a PROJECT
// column. A scoped list is a simple table.
func writeTicketList(out io.Writer, tickets []domain.Ticket, includeProject bool) error {
	headers := []string{"SLUG", "STATE", "CLAIMED_BY", "PRIORITY", "TITLE"}
	if includeProject {
		headers = append([]string{"PROJECT"}, headers...)
	}
	rows := make([][]string, 0, len(tickets))
	for _, ticket := range tickets {
		row := []string{ticket.Slug, ticketDisplayState(ticket), ticket.ClaimedBy, fmt.Sprintf("%d", ticket.Priority), ticket.Title}
		if includeProject {
			project := ticket.Project
			if project == "" {
				project = ungroupedProjectSentinel
			}
			row = append([]string{project}, row...)
		}
		rows = append(rows, row)
	}
	return writeTable(out, headers, rows)
}

// ticketDisplayState folds the claim and PR presence into the human table
// state. It stays offline: in-review derives from URL presence and status
// only; tree and board pass fetched states to deriveTicketState directly.
func ticketDisplayState(ticket domain.Ticket) string {
	state := deriveTicketState(ticket.Status, ticket.ClaimedBy, ticket.PullRequests, nil, false)
	if state == "blocked" {
		// The flat list has no dependency snapshot; keep the raw status.
		return string(ticket.Status)
	}
	return state
}

func newTicketsShowCommand(options Options) *cobra.Command {
	command := &cobra.Command{
		Use:   "show TICKET",
		Short: "Show a Ticket and its body",
		Args:  exactArgs("TICKET"),
		RunE: func(command *cobra.Command, args []string) error {
			service, err := options.ticketService()
			if err != nil {
				return err
			}
			result, err := service.Show(args[0])
			if err != nil {
				return err
			}
			if WantsJSON(command) {
				return writeReadJSON(command, ticketShowOutput{
					SchemaVersion: jsonSchemaVersion,
					Ticket: ticketShowDetail{
						Ticket:        result.Ticket,
						Body:          result.Body,
						Ready:         result.Ready,
						BlockedByOpen: result.BlockedByOpen,
					},
				}, "ticket")
			}
			ticket := result.Ticket
			fields := [][2]string{
				{"Slug", ticket.Slug},
				{"Title", ticket.Title},
				{"Status", string(ticket.Status)},
				{"Priority", fmt.Sprintf("%d", ticket.Priority)},
				{"Project", ticket.Project},
				{"Path", ticket.Path},
			}
			if ticket.ClaimedBy != "" {
				fields = append(fields, [2]string{"Claimed by", ticket.ClaimedBy})
			}
			if len(result.BlockedByOpen) > 0 {
				blockers := make([]string, 0, len(result.BlockedByOpen))
				for _, blocker := range result.BlockedByOpen {
					name := blocker.Slug
					if blocker.Missing {
						name += " (missing)"
					}
					blockers = append(blockers, name)
				}
				fields = append(fields, [2]string{"Blocked by", strings.Join(blockers, ", ")})
			}
			if err := writeFields(command.OutOrStdout(), fields); err != nil {
				return err
			}
			_, err = fmt.Fprintf(command.OutOrStdout(), "\n%s", strings.TrimLeft(result.Body, "\n"))
			return err
		},
	}
	setArguments(command, requiredArgument("ticket"))
	addFieldsFlag(command, ticketShowDetail{})
	command.ValidArgsFunction = ticketSlugCompletion(options)
	return command
}

func newTicketsSetCommand(options Options) *cobra.Command {
	var status, project string
	var blockedBy []string
	var priority int
	command := &cobra.Command{
		Use:   "set TICKET [--status STATUS] [--priority N] [--project PROJECT] [--blocked-by SLUG]",
		Short: "Change Ticket fields",
		Args:  exactArgs("TICKET"),
		RunE: func(command *cobra.Command, args []string) error {
			service, err := options.ticketService()
			if err != nil {
				return err
			}
			request := ticketservice.SetRequest{
				Status:       status,
				StatusSet:    command.Flags().Changed("status"),
				Priority:     priority,
				PrioritySet:  command.Flags().Changed("priority"),
				Project:      project,
				ProjectSet:   command.Flags().Changed("project"),
				BlockedBy:    blockedBy,
				BlockedBySet: command.Flags().Changed("blocked-by"),
			}
			if !request.StatusSet && !request.PrioritySet && !request.ProjectSet && !request.BlockedBySet {
				return invalidUsage(command, "pass at least one of --status, --priority, --project, or --blocked-by")
			}
			return setTicket(command, service, args[0], request)
		},
	}
	command.Flags().StringVar(&status, "status", "", "Set the status")
	command.Flags().IntVar(&priority, "priority", 2, "Set the priority, 0 (highest) to 4 (lowest)")
	command.Flags().StringVar(&project, "project", "", "Move the Ticket to this Project")
	command.Flags().StringArrayVar(&blockedBy, "blocked-by", nil, "Replace blockers with this slug or wiki-link; repeat for more; pass an empty value to clear")
	setArguments(command, requiredArgument("ticket"))
	setFlagEnum(command, "status", domain.TicketStatuses()...)
	registerProjectFlagCompletion(command, options)
	_ = command.RegisterFlagCompletionFunc("blocked-by", ticketFlagCompletion(options))
	command.ValidArgsFunction = ticketSlugCompletion(options)
	return command
}

// setTicket changes the fields of one Ticket. Both the tickets set command
// and apply use it.
func setTicket(command *cobra.Command, service ticketservice.Store, ref string, request ticketservice.SetRequest) error {
	return runMutation(command, "tickets.set",
		func() (string, string, error) {
			ticket, err := service.Set(ref, request, true)
			return ticket.Slug, ticket.Title, err
		},
		func() (string, string, error) {
			ticket, err := service.Set(ref, request, false)
			return ticket.Slug, ticket.Title, err
		},
		func(out io.Writer, id, _ string) error {
			_, err := fmt.Fprintf(out, "Set ticket %q\n", id)
			return err
		})
}

func newTicketsClaimCommand(options Options) *cobra.Command {
	var as, workspaceRef string
	command := &cobra.Command{
		Use:   "claim TICKET [--as NAME] [--workspace WORKSPACE]",
		Short: "Claim a Ticket",
		Args:  exactArgs("TICKET"),
		RunE: func(command *cobra.Command, args []string) error {
			service, err := options.ticketService()
			if err != nil {
				return err
			}
			claimant, err := resolveClaimant(command, as)
			if err != nil {
				return err
			}
			if err := claimTicket(command, service, args[0], claimant); err != nil {
				return err
			}
			if !command.Flags().Changed("workspace") {
				return nil
			}
			return stampClaimedWorkspace(command, options, service, args[0], workspaceRef)
		},
	}
	command.Flags().StringVar(&as, "as", "", "Set the claimant name")
	command.Flags().StringVar(&workspaceRef, "workspace", "", "Stamp the Workspace ID on the Ticket")
	setArguments(command, requiredArgument("ticket"))
	command.ValidArgsFunction = ticketSlugCompletion(options)
	return command
}

func stampClaimedWorkspace(command *cobra.Command, options Options, service ticketservice.Store, ticketRef, workspaceRef string) error {
	workspace, err := resolveWorkspace(options.workspaceService(), workspaceRef)
	if err != nil {
		return err
	}
	if isDryRun(command) {
		return nil
	}
	if _, err := service.SetWorkspace(ticketRef, workspace.ID, false); err != nil {
		return err
	}
	if WantsJSON(command) {
		return nil
	}
	_, err = fmt.Fprintf(command.OutOrStdout(), "Linked ticket %q to Workspace %q\n", ticketRef, workspace.ID)
	return err
}

func newTicketsUnclaimCommand(options Options) *cobra.Command {
	var as string
	command := &cobra.Command{
		Use:   "unclaim TICKET [--as NAME]",
		Short: "Remove the claim on a Ticket",
		Args:  exactArgs("TICKET"),
		RunE: func(command *cobra.Command, args []string) error {
			service, err := options.ticketService()
			if err != nil {
				return err
			}
			claimant, err := resolveClaimant(command, as)
			if err != nil {
				return err
			}
			return unclaimTicket(command, service, args[0], claimant)
		},
	}
	command.Flags().StringVar(&as, "as", "", "Set the claimant name")
	setArguments(command, requiredArgument("ticket"))
	command.ValidArgsFunction = ticketSlugCompletion(options)
	return command
}

func newTicketsCompleteCommand(options Options) *cobra.Command {
	var as string
	var status string
	var pullRequests []string
	command := &cobra.Command{
		Use:   "complete TICKET [--as NAME] [--status STATUS] [--pr URL]...",
		Short: "Record pull requests and release the claim in one write",
		Args:  exactArgs("TICKET"),
		RunE: func(command *cobra.Command, args []string) error {
			service, err := options.ticketService()
			if err != nil {
				return err
			}
			claimant, err := resolveClaimant(command, as)
			if err != nil {
				return err
			}
			return completeTicketWork(command, service, args[0], claimant, domain.TicketStatus(status), pullRequests)
		},
	}
	command.Flags().StringVar(&as, "as", "", "Set the claimant name")
	command.Flags().StringVar(&status, "status", string(domain.TicketReadyForHuman), "Completion status: ready-for-human or ready-for-agent")
	command.Flags().StringArrayVar(&pullRequests, "pr", nil, "Record one pull request URL; repeat for more")
	setFlagEnum(command, "status", string(domain.TicketReadyForHuman), string(domain.TicketReadyForAgent))
	setArguments(command, requiredArgument("ticket"))
	command.ValidArgsFunction = ticketSlugCompletion(options)
	return command
}

// completeTicketWork runs the shared complete mutation for the command and
// apply.
func completeTicketWork(command *cobra.Command, service ticketservice.Store, ref, claimant string, status domain.TicketStatus, pullRequests []string) error {
	return runMutation(command, "tickets.complete",
		func() (string, string, error) {
			ticket, err := service.CompleteWork(ref, claimant, status, pullRequests, true)
			return ticket.Slug, ticket.Title, err
		},
		func() (string, string, error) {
			ticket, err := service.CompleteWork(ref, claimant, status, pullRequests, false)
			return ticket.Slug, ticket.Title, err
		},
		func(out io.Writer, id, _ string) error {
			_, err := fmt.Fprintf(out, "Completed ticket %q (%s, %d pull request(s))\n", id, status, len(pullRequests))
			return err
		})
}

func newTicketsCloseCommand(options Options) *cobra.Command {
	var as string
	command := &cobra.Command{
		Use:   "close TICKET [--as NAME]",
		Short: "Resolve a Ticket: set the status done and drop the claim",
		Args:  exactArgs("TICKET"),
		RunE: func(command *cobra.Command, args []string) error {
			service, err := options.ticketService()
			if err != nil {
				return err
			}
			claimant, err := resolveClaimant(command, as)
			if err != nil {
				return err
			}
			return closeTicket(command, service, args[0], claimant)
		},
	}
	command.Flags().StringVar(&as, "as", "", "Set the claimant name")
	setArguments(command, requiredArgument("ticket"))
	command.ValidArgsFunction = ticketSlugCompletion(options)
	return command
}

// resolveClaimant resolves the claimant name: --as, then TWT_CLAIMANT, then
// the OS username. The OS username applies only in an interactive terminal,
// so two agents can never both succeed as the same default name.
func resolveClaimant(command *cobra.Command, as string) (string, error) {
	if value := strings.TrimSpace(as); value != "" {
		return value, nil
	}
	if value := os.Getenv("TWT_CLAIMANT"); value != "" {
		return value, nil
	}
	if !interactiveTicketSession(command) {
		return "", clierr.WithHint(
			clierr.New(clierr.InvalidUsage, "no claimant is set"),
			"Pass --as NAME when twt runs without a terminal.")
	}
	if current, err := user.Current(); err == nil && current.Username != "" {
		return current.Username, nil
	}
	if value := os.Getenv("USER"); value != "" {
		return value, nil
	}
	return "", clierr.WithHint(
		clierr.New(clierr.InvalidUsage, "no claimant is set"),
		"Pass --as NAME or set TWT_CLAIMANT.")
}

// claimTicket claims one Ticket. Both the tickets claim command and apply use
// it.
func claimTicket(command *cobra.Command, service ticketservice.Store, ref, claimant string) error {
	return runMutation(command, "tickets.claim",
		func() (string, string, error) {
			ticket, err := service.Claim(ref, claimant, true)
			return ticket.Slug, ticket.Title, err
		},
		func() (string, string, error) {
			ticket, err := service.Claim(ref, claimant, false)
			return ticket.Slug, ticket.Title, err
		},
		func(out io.Writer, id, _ string) error {
			_, err := fmt.Fprintf(out, "Claimed ticket %q as %q\n", id, claimant)
			return err
		})
}

// unclaimTicket removes the claim on one Ticket. Both the tickets unclaim
// command and apply use it.
func unclaimTicket(command *cobra.Command, service ticketservice.Store, ref, claimant string) error {
	return runMutation(command, "tickets.unclaim",
		func() (string, string, error) {
			ticket, err := service.Unclaim(ref, claimant, true)
			return ticket.Slug, ticket.Title, err
		},
		func() (string, string, error) {
			ticket, err := service.Unclaim(ref, claimant, false)
			return ticket.Slug, ticket.Title, err
		},
		func(out io.Writer, id, _ string) error {
			_, err := fmt.Fprintf(out, "Removed the claim on ticket %q\n", id)
			return err
		})
}

// closeTicket resolves one Ticket. Both the tickets close command and apply
// use it.
func closeTicket(command *cobra.Command, service ticketservice.Store, ref, claimant string) error {
	return runMutation(command, "tickets.close",
		func() (string, string, error) {
			ticket, err := service.Close(ref, claimant, true)
			return ticket.Slug, ticket.Title, err
		},
		func() (string, string, error) {
			ticket, err := service.Close(ref, claimant, false)
			return ticket.Slug, ticket.Title, err
		},
		func(out io.Writer, id, _ string) error {
			_, err := fmt.Fprintf(out, "Closed Ticket %q\n", id)
			return err
		})
}

func newTicketsCommentCommand(options Options) *cobra.Command {
	command := &cobra.Command{
		Use:   "comment TICKET -",
		Short: "Append a comment to a Ticket",
		Args:  requireResourceThenStdin("TICKET"),
		RunE: func(command *cobra.Command, args []string) error {
			service, err := options.ticketService()
			if err != nil {
				return err
			}
			text, err := readTicketStdin(command)
			if err != nil {
				return err
			}
			return commentTicket(command, service, args[0], text)
		},
	}
	setArguments(command, requiredArgument("ticket"), stdinTokenArgument(true))
	command.ValidArgsFunction = ticketSlugCompletion(options)
	return command
}

// commentTicket appends one comment. Both the tickets comment command and
// apply use it.
func commentTicket(command *cobra.Command, service ticketservice.Store, ref, text string) error {
	return runMutation(command, "tickets.comment",
		func() (string, string, error) {
			ticket, err := service.Comment(ref, text, true)
			return ticket.Slug, ticket.Title, err
		},
		func() (string, string, error) {
			ticket, err := service.Comment(ref, text, false)
			return ticket.Slug, ticket.Title, err
		},
		func(out io.Writer, id, _ string) error {
			_, err := fmt.Fprintf(out, "Added a comment to ticket %q\n", id)
			return err
		})
}

// ticketSlugCompletion completes the first positional TICKET reference. A
// missing or unset Tickets home completes to nothing.
func ticketSlugCompletion(options Options) completionFunc {
	return func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) > 0 {
			return nil, noFileCompletion
		}
		service, err := options.ticketService()
		if err != nil {
			return nil, noFileCompletion
		}
		slugs, err := service.Slugs()
		if err != nil {
			return nil, noFileCompletion
		}
		return matching(slugs, toComplete), noFileCompletion
	}
}

func ticketSlugsCompletion(options Options) completionFunc {
	return func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		service, err := options.ticketService()
		if err != nil {
			return nil, noFileCompletion
		}
		slugs, err := service.Slugs()
		if err != nil {
			return nil, noFileCompletion
		}
		used := map[string]bool{}
		for _, arg := range args {
			used[arg] = true
		}
		candidates := make([]string, 0, len(slugs))
		for _, slug := range matching(slugs, toComplete) {
			if !used[slug] {
				candidates = append(candidates, slug)
			}
		}
		return candidates, noFileCompletion
	}
}

// ticketProjectNames lists every Project name. A missing or unset Tickets home
// completes to nothing.
func ticketProjectNames(options Options, toComplete string) []string {
	service, err := options.ticketService()
	if err != nil {
		return nil
	}
	projects, err := service.Projects()
	if err != nil {
		return nil
	}
	names := make([]string, 0, len(projects))
	for _, project := range projects {
		names = append(names, project.Name)
	}
	return matching(names, toComplete)
}

// ticketProjectNameCompletion completes the first positional Project name.
func ticketProjectNameCompletion(options Options) completionFunc {
	return func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) > 0 {
			return nil, noFileCompletion
		}
		return ticketProjectNames(options, toComplete), noFileCompletion
	}
}

// registerProjectFlagCompletion completes a --project flag value.
func registerProjectFlagCompletion(command *cobra.Command, options Options) {
	_ = command.RegisterFlagCompletionFunc("project", func(_ *cobra.Command, _ []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		return ticketProjectNames(options, toComplete), noFileCompletion
	})
}
