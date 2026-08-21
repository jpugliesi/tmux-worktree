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

type boardsListOutput struct {
	SchemaVersion int            `json:"schemaVersion"`
	Boards        []domain.Board `json:"boards"`
	TotalCount    int            `json:"totalCount"`
	Truncated     bool           `json:"truncated,omitempty"`
}

type boardShowOutput struct {
	SchemaVersion int          `json:"schemaVersion"`
	Board         domain.Board `json:"board"`
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
	tickets.AddCommand(newTicketsShowCommand(options))
	tickets.AddCommand(newTicketsEditCommand(options))
	tickets.AddCommand(newTicketsSetCommand(options))
	tickets.AddCommand(newTicketsClaimCommand(options))
	tickets.AddCommand(newTicketsStartCommand(options))
	tickets.AddCommand(newTicketsUnclaimCommand(options))
	tickets.AddCommand(newTicketsCloseCommand(options))
	tickets.AddCommand(newTicketsCommentCommand(options))
	tickets.AddCommand(newTicketsBoardsCommand(options))
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
func initializeTicketsHome(command *cobra.Command, service *ticketservice.Service) error {
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
			return reportScaffold(out, result.WroteTemplate, filepath.Join(result.Home, "templates", "ticket.md"))
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
	var board, title, slug, status string
	var fromStdin bool
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
				Title:    title,
				Slug:     slug,
				Board:    board,
				Status:   domain.TicketStatus(status),
				Priority: -1,
			}
			switch {
			case fromStdin:
				if strings.TrimSpace(title) == "" {
					return invalidUsageWithHint(command, "Pass --title together with --stdin.",
						"--stdin reads only the body and needs a title")
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
				return invalidUsageWithHint(command, "Pass DESCRIPTION, --title, or --stdin.",
					"twt tickets create has no input and no terminal")
			}
			return createTicket(command, service, request)
		},
	}
	command.Flags().StringVar(&board, "board", "", "Put the Ticket in this Board")
	command.Flags().StringVar(&title, "title", "", "Set the Ticket title")
	command.Flags().StringVar(&slug, "slug", "", "Set the file slug; empty derives it from the title")
	command.Flags().StringVar(&status, "status", "", "Set the initial status; empty selects needs-triage")
	command.Flags().BoolVar(&fromStdin, "stdin", false, "Read the Ticket body from standard input; requires --title")
	setArguments(command, variadicArgument("description", false, "the body; the first line becomes the title when --title is absent"))
	setFlagEnum(command, "status", domain.TicketStatuses()...)
	registerBoardFlagCompletion(command, options)
	return command
}

// createTicket writes one Ticket. Both the tickets create command and apply
// use it. A text dry run prints the file that twt would write.
func createTicket(command *cobra.Command, service *ticketservice.Service, request ticketservice.CreateRequest) error {
	if isDryRun(command) {
		result, err := service.Create(request, true)
		if err != nil {
			return err
		}
		if WantsJSON(command) {
			return writeMutation(command, "tickets.create", statusValid, result.Ticket.Slug, result.Ticket.Title)
		}
		out := command.OutOrStdout()
		if request.EnsureBoard && request.Board != "" {
			if _, err := fmt.Fprintf(out, "Would create Board %q\n", request.Board); err != nil {
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
	if request.EnsureBoard && request.Board != "" {
		if _, err := fmt.Fprintf(out, "Created Board %q\n", request.Board); err != nil {
			return err
		}
	}
	_, err = fmt.Fprintf(out, "Created ticket %q at %q\n", result.Ticket.Slug, result.Ticket.Path)
	return err
}

func newTicketsListCommand(options Options) *cobra.Command {
	var board, status string
	var ready, all bool
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
			tickets, err := service.List(ticketservice.ListFilter{
				Board:    board,
				BoardSet: command.Flags().Changed("board"),
				Status:   status,
				Ready:    ready,
				All:      all,
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
			rows := make([][]string, 0, len(tickets))
			for _, ticket := range tickets {
				boardName := ticket.Board
				if boardName == "" {
					boardName = "-"
				}
				rows = append(rows, []string{ticket.Slug, string(ticket.Status), fmt.Sprintf("%d", ticket.Priority), boardName, ticket.Title})
			}
			return writeTable(command.OutOrStdout(), []string{"SLUG", "STATUS", "PRIORITY", "BOARD", "TITLE"}, rows)
		},
	}
	command.Flags().StringVar(&board, "board", "", "List one Board; an empty value lists ungrouped Tickets")
	command.Flags().StringVar(&status, "status", "", "List one status")
	command.Flags().BoolVar(&ready, "ready", false, "List only unclaimed, unblocked, ready-for-agent Tickets")
	command.Flags().BoolVar(&all, "all", false, "Include closed tickets")
	addListReadFlags(command, &limit, &offset, domain.Ticket{})
	setFlagEnum(command, "status", domain.TicketStatuses()...)
	registerBoardFlagCompletion(command, options)
	return command
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
				{"Board", ticket.Board},
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

func newTicketsEditCommand(options Options) *cobra.Command {
	var fromStdin bool
	command := &cobra.Command{
		Use:   "edit TICKET [--stdin]",
		Short: "Replace the body of a Ticket",
		Args:  exactArgs("TICKET"),
		RunE: func(command *cobra.Command, args []string) error {
			service, err := options.ticketService()
			if err != nil {
				return err
			}
			if fromStdin {
				body, err := readTicketStdin(command)
				if err != nil {
					return err
				}
				return editTicket(command, service, args[0], body)
			}
			if !interactiveTicketSession(command) {
				return invalidUsageWithHint(command, "Pass --stdin when twt runs without a terminal.",
					"twt tickets edit has no terminal")
			}
			resolved, err := service.Resolve(args[0])
			if err != nil {
				return err
			}
			return runMutation(command, "tickets.edit",
				func() (string, string, error) {
					return resolved.Slug, resolved.Title, nil
				},
				func() (string, string, error) {
					if err := options.OpenEditor(resolved.Path); err != nil {
						return "", "", err
					}
					if _, err := service.Show(resolved.Slug); err != nil {
						return "", "", clierr.WithHint(clierr.Wrap(clierr.UnsafeState, err),
							"Fix the file %q, then run 'twt tickets show %s'.", resolved.Path, resolved.Slug)
					}
					return resolved.Slug, resolved.Title, nil
				},
				func(out io.Writer, id, _ string) error {
					_, err := fmt.Fprintf(out, "Ticket %q is valid\n", id)
					return err
				})
		},
	}
	command.Flags().BoolVar(&fromStdin, "stdin", false, "Read the new body from standard input")
	setArguments(command, requiredArgument("ticket"))
	command.ValidArgsFunction = ticketSlugCompletion(options)
	return command
}

// editTicket replaces the body of one Ticket from stdin text.
func editTicket(command *cobra.Command, service *ticketservice.Service, ref, body string) error {
	return runMutation(command, "tickets.edit",
		func() (string, string, error) {
			ticket, err := service.Edit(ref, body, true)
			return ticket.Slug, ticket.Title, err
		},
		func() (string, string, error) {
			ticket, err := service.Edit(ref, body, false)
			return ticket.Slug, ticket.Title, err
		},
		func(out io.Writer, id, _ string) error {
			_, err := fmt.Fprintf(out, "Replaced the body of ticket %q\n", id)
			return err
		})
}

func newTicketsSetCommand(options Options) *cobra.Command {
	var status, board string
	var priority int
	command := &cobra.Command{
		Use:   "set TICKET [--status STATUS] [--priority N] [--board BOARD]",
		Short: "Change Ticket fields",
		Args:  exactArgs("TICKET"),
		RunE: func(command *cobra.Command, args []string) error {
			service, err := options.ticketService()
			if err != nil {
				return err
			}
			request := ticketservice.SetRequest{
				Status:      status,
				StatusSet:   command.Flags().Changed("status"),
				Priority:    priority,
				PrioritySet: command.Flags().Changed("priority"),
				Board:       board,
				BoardSet:    command.Flags().Changed("board"),
			}
			if !request.StatusSet && !request.PrioritySet && !request.BoardSet {
				return invalidUsage(command, "pass at least one of --status, --priority, or --board")
			}
			return setTicket(command, service, args[0], request)
		},
	}
	command.Flags().StringVar(&status, "status", "", "Set the status")
	command.Flags().IntVar(&priority, "priority", 2, "Set the priority, 0 (highest) to 4 (lowest)")
	command.Flags().StringVar(&board, "board", "", "Move the Ticket to this Board")
	setArguments(command, requiredArgument("ticket"))
	setFlagEnum(command, "status", domain.TicketStatuses()...)
	registerBoardFlagCompletion(command, options)
	command.ValidArgsFunction = ticketSlugCompletion(options)
	return command
}

// setTicket changes the fields of one Ticket. Both the tickets set command
// and apply use it.
func setTicket(command *cobra.Command, service *ticketservice.Service, ref string, request ticketservice.SetRequest) error {
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
	var as string
	command := &cobra.Command{
		Use:   "claim TICKET [--as NAME]",
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
			return claimTicket(command, service, args[0], claimant)
		},
	}
	command.Flags().StringVar(&as, "as", "", "Set the claimant name")
	setArguments(command, requiredArgument("ticket"))
	command.ValidArgsFunction = ticketSlugCompletion(options)
	return command
}

func newTicketsStartCommand(options Options) *cobra.Command {
	var name, templateName, as string
	var keepCurrent bool
	command := &cobra.Command{
		Use:     "start TICKET [--name NAME] [--template TEMPLATE] [--as NAME]",
		Short:   "Claim a Ticket and start a Project for it",
		Args:    exactArgs("TICKET"),
		PreRunE: refuseJSONQuickCreate,
		RunE: func(command *cobra.Command, args []string) error {
			service, err := options.ticketService()
			if err != nil {
				return err
			}
			ticket, err := service.Resolve(args[0])
			if err != nil {
				return err
			}
			if ticket.Status == domain.TicketDone || ticket.Status == domain.TicketWontfix {
				return clierr.WithHint(
					clierr.New(clierr.PreconditionFailed, "the Ticket is closed"),
					"Select a Ticket from 'twt tickets list --ready'.")
			}
			claimant, err := resolveClaimant(command, as)
			if err != nil {
				return err
			}
			// The claim comes first: a Ticket that a different claimant
			// holds aborts before any Project work.
			if err := claimTicket(command, service, ticket.Slug, claimant); err != nil {
				return err
			}
			projectName := strings.TrimSpace(name)
			if projectName == "" {
				projectName = ticket.Slug
			}
			// A create failure keeps the claim: the create error already
			// tells how to retry the setup.
			if err := runQuickCreate(command, options, quickCreateRequest{
				Name:         projectName,
				TemplateName: templateName,
				KeepCurrent:  keepCurrent,
				Ticket:       ticket.Slug,
			}); err != nil {
				return err
			}
			if isDryRun(command) {
				return nil
			}
			// The start comment is best-effort: a comment failure must not
			// fail the start.
			if err := commentTicket(command, service, ticket.Slug, fmt.Sprintf("Started Project %s.", projectName)); err != nil {
				_, _ = fmt.Fprintf(command.ErrOrStderr(), "Warning: twt could not add the start comment to Ticket %q: %v\n", ticket.Slug, err)
			}
			return nil
		},
	}
	command.Flags().StringVar(&name, "name", "", "Set the Project name; empty uses the Ticket slug")
	command.Flags().StringVar(&templateName, "template", "", "Select the Project Template instead of the current Project's template")
	command.Flags().BoolVar(&keepCurrent, "keep-current", false, "Switch to the new Project and keep the current Project active")
	command.Flags().StringVar(&as, "as", "", "Set the claimant name")
	setArguments(command, requiredArgument("ticket"))
	command.ValidArgsFunction = ticketSlugCompletion(options)
	_ = command.RegisterFlagCompletionFunc("template", templateFlagCompletion(options.templateStore()))
	return command
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
func claimTicket(command *cobra.Command, service *ticketservice.Service, ref, claimant string) error {
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
func unclaimTicket(command *cobra.Command, service *ticketservice.Service, ref, claimant string) error {
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
func closeTicket(command *cobra.Command, service *ticketservice.Service, ref, claimant string) error {
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
	var fromStdin bool
	command := &cobra.Command{
		Use:   "comment TICKET --stdin",
		Short: "Append a comment to a Ticket",
		Args:  exactArgs("TICKET"),
		RunE: func(command *cobra.Command, args []string) error {
			if !fromStdin {
				return invalidUsageWithHint(command, "Pass the comment text on standard input with --stdin.",
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
			return commentTicket(command, service, args[0], text)
		},
	}
	command.Flags().BoolVar(&fromStdin, "stdin", false, "Read the comment text from standard input")
	setArguments(command, requiredArgument("ticket"))
	command.ValidArgsFunction = ticketSlugCompletion(options)
	return command
}

// commentTicket appends one comment. Both the tickets comment command and
// apply use it.
func commentTicket(command *cobra.Command, service *ticketservice.Service, ref, text string) error {
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

func newTicketsBoardsCommand(options Options) *cobra.Command {
	boards := groupCommand(&cobra.Command{
		Use:   "boards",
		Short: "Manage Ticket Boards",
	})
	boards.AddCommand(newTicketsBoardsCreateCommand(options))
	boards.AddCommand(newTicketsBoardsListCommand(options))
	boards.AddCommand(newTicketsBoardsShowCommand(options))
	return boards
}

func newTicketsBoardsCreateCommand(options Options) *cobra.Command {
	command := &cobra.Command{
		Use:   "create NAME",
		Short: "Create a Board directory",
		Args:  exactArgs("NAME"),
		RunE: func(command *cobra.Command, args []string) error {
			service, err := options.ticketService()
			if err != nil {
				return err
			}
			return createBoard(command, service, args[0])
		},
	}
	setArguments(command, requiredArgument("name"))
	return command
}

// createBoard creates one Board. Both the boards create command and apply use
// it.
func createBoard(command *cobra.Command, service *ticketservice.Service, name string) error {
	return runMutation(command, "tickets.boards.create",
		func() (string, string, error) {
			board, err := service.CreateBoard(name, true)
			return "", board.Name, err
		},
		func() (string, string, error) {
			board, err := service.CreateBoard(name, false)
			return "", board.Name, err
		},
		func(out io.Writer, _, boardName string) error {
			_, err := fmt.Fprintf(out, "Created Board %q\n", boardName)
			return err
		})
}

func newTicketsBoardsListCommand(options Options) *cobra.Command {
	var limit, offset int
	command := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List Boards",
		Args:    noArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			service, err := options.ticketService()
			if err != nil {
				return err
			}
			boards, err := service.Boards()
			if err != nil {
				return err
			}
			boards, total, truncated, err := applyWindow(boards, offset, limit)
			if err != nil {
				return err
			}
			if format := resolvedOutputFormat(command); format != outputText {
				if format == outputNDJSON {
					return writeNDJSONList(command, boards, total, truncated)
				}
				return writeReadJSON(command, boardsListOutput{SchemaVersion: jsonSchemaVersion, Boards: boards, TotalCount: total, Truncated: truncated}, "boards")
			}
			if total == 0 {
				_, err = fmt.Fprintln(command.ErrOrStderr(), "No Boards exist. Run 'twt tickets boards create NAME'.")
				return err
			}
			rows := make([][]string, 0, len(boards))
			for _, board := range boards {
				rows = append(rows, []string{board.Name, fmt.Sprintf("%d", board.Tickets)})
			}
			return writeTable(command.OutOrStdout(), []string{"NAME", "TICKETS"}, rows)
		},
	}
	addListReadFlags(command, &limit, &offset, domain.Board{})
	return command
}

func newTicketsBoardsShowCommand(options Options) *cobra.Command {
	command := &cobra.Command{
		Use:   "show NAME",
		Short: "Show a Board",
		Args:  exactArgs("NAME"),
		RunE: func(command *cobra.Command, args []string) error {
			service, err := options.ticketService()
			if err != nil {
				return err
			}
			board, err := service.Board(args[0])
			if err != nil {
				return err
			}
			if WantsJSON(command) {
				return writeReadJSON(command, boardShowOutput{SchemaVersion: jsonSchemaVersion, Board: board}, "board")
			}
			return writeFields(command.OutOrStdout(), [][2]string{
				{"Board", board.Name},
				{"Path", board.Path},
				{"Tickets", fmt.Sprintf("%d", board.Tickets)},
			})
		},
	}
	setArguments(command, requiredArgument("name"))
	addFieldsFlag(command, domain.Board{})
	command.ValidArgsFunction = ticketBoardNameCompletion(options)
	return command
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

// ticketBoardNames lists every Board name. A missing or unset Tickets home
// completes to nothing.
func ticketBoardNames(options Options, toComplete string) []string {
	service, err := options.ticketService()
	if err != nil {
		return nil
	}
	boards, err := service.Boards()
	if err != nil {
		return nil
	}
	names := make([]string, 0, len(boards))
	for _, board := range boards {
		names = append(names, board.Name)
	}
	return matching(names, toComplete)
}

// ticketBoardNameCompletion completes the first positional Board name.
func ticketBoardNameCompletion(options Options) completionFunc {
	return func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) > 0 {
			return nil, noFileCompletion
		}
		return ticketBoardNames(options, toComplete), noFileCompletion
	}
}

// registerBoardFlagCompletion completes a --board flag value.
func registerBoardFlagCompletion(command *cobra.Command, options Options) {
	_ = command.RegisterFlagCompletionFunc("board", func(_ *cobra.Command, _ []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		return ticketBoardNames(options, toComplete), noFileCompletion
	})
}
