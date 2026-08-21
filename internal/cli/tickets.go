package cli

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/user"
	"path/filepath"
	"strings"
	"text/tabwriter"

	"github.com/jpugliesi/tmux-worktree/internal/clierr"
	"github.com/jpugliesi/tmux-worktree/internal/domain"
	ticketservice "github.com/jpugliesi/tmux-worktree/internal/ticket"
	"github.com/spf13/cobra"
)

// defaultTicketTemplate seeds the interactive create editor when the Tickets
// home has no templates/ticket.md. It mirrors the init scaffold.
const defaultTicketTemplate = `---
title: "<title>"
aliases:
  - <title>
tags:
  - tickets
status: needs-triage
priority: 2
board:
blocked_by: []
claimed_by:
claimed_at:
created: YYYY-MM-DD
updated: YYYY-MM-DD
---

# <title>

## What to build

## Acceptance criteria

- [ ]

## Blocked by

None - can start immediately

## Comments
`

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
	tickets.AddCommand(newTicketsCreateCommand(options))
	tickets.AddCommand(newTicketsListCommand(options))
	tickets.AddCommand(newTicketsShowCommand(options))
	tickets.AddCommand(newTicketsEditCommand(options))
	tickets.AddCommand(newTicketsSetCommand(options))
	tickets.AddCommand(newTicketsClaimCommand(options))
	tickets.AddCommand(newTicketsUnclaimCommand(options))
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
			case len(args) > 0 || strings.TrimSpace(title) != "":
				description := strings.Join(args, " ")
				if title == "" {
					first, rest, _ := strings.Cut(description, "\n")
					request.Title = strings.TrimSpace(first)
					request.Body = rest
				} else {
					request.Body = description
				}
			default:
				if !interactiveTicketSession(command) {
					return invalidUsageWithHint(command, "Pass DESCRIPTION, --title, or --stdin.",
						"twt tickets create has no input and no terminal")
				}
				editorRequest, err := createTicketInEditor(command, options)
				if err != nil {
					return err
				}
				editorRequest.Slug = slug
				if board != "" {
					editorRequest.Board = board
				}
				if status != "" {
					editorRequest.Status = domain.TicketStatus(status)
				}
				request = editorRequest
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

// createTicketInEditor opens the editor on a temporary copy of the create
// template and parses the saved file into one CreateRequest.
func createTicketInEditor(command *cobra.Command, options Options) (ticketservice.CreateRequest, error) {
	var request ticketservice.CreateRequest
	home, err := options.resolveTicketsHome()
	if err != nil {
		return request, err
	}
	seed, err := os.ReadFile(filepath.Join(home, "templates", "ticket.md"))
	if err != nil {
		seed = []byte(defaultTicketTemplate)
	}
	temp, err := os.CreateTemp("", "twt-ticket-*.md")
	if err != nil {
		return request, fmt.Errorf("create the ticket draft file: %w", err)
	}
	path := temp.Name()
	defer os.Remove(path)
	if _, err := temp.Write(seed); err != nil {
		temp.Close()
		return request, fmt.Errorf("write the ticket draft file: %w", err)
	}
	if err := temp.Close(); err != nil {
		return request, fmt.Errorf("write the ticket draft file: %w", err)
	}
	if err := options.OpenEditor(path); err != nil {
		return request, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return request, fmt.Errorf("read the ticket draft file: %w", err)
	}
	if strings.TrimSpace(string(data)) == "" {
		return request, invalidUsageWithHint(command, "Write the ticket and save the file, or pass DESCRIPTION.",
			"the editor saved an empty ticket")
	}
	if bytes.Equal(data, seed) {
		return request, invalidUsageWithHint(command, "Set the title and save the file, or pass DESCRIPTION.",
			"the editor saved the ticket template unchanged")
	}
	return parseTicketDraft(command, path, data)
}

// parseTicketDraft maps one saved editor draft to a CreateRequest.
func parseTicketDraft(command *cobra.Command, path string, data []byte) (ticketservice.CreateRequest, error) {
	var request ticketservice.CreateRequest
	file, err := ticketservice.ParseTicketFile(path, data)
	if err != nil {
		return request, err
	}
	parsed := domain.Ticket{Priority: -1}
	if mapping := file.Mapping(); mapping != nil {
		if err := mapping.Decode(&parsed); err != nil {
			return request, invalidUsageWithHint(command, "Fix the YAML frontmatter of the draft.",
				"the ticket draft has invalid frontmatter: %v", err)
		}
	}
	bodyTitle, body := splitLeadingTitle(file.Body)
	title := strings.TrimSpace(parsed.Title)
	if title == "" || title == "<title>" {
		title = bodyTitle
	}
	if bodyTitle == "" {
		body = file.Body
	}
	if title == "" || title == "<title>" {
		return request, invalidUsageWithHint(command, "Set the title line and save the file.",
			"the ticket draft has no title")
	}
	return ticketservice.CreateRequest{
		Title:    title,
		Board:    parsed.Board,
		Body:     body,
		Status:   parsed.Status,
		Priority: parsed.Priority,
	}, nil
}

// splitLeadingTitle removes the leading H1 line from body and returns that
// title and the remaining body. A body that does not start with an H1 returns
// an empty title and the full body.
func splitLeadingTitle(body string) (string, string) {
	lines := strings.Split(body, "\n")
	for index, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if strings.HasPrefix(trimmed, "# ") {
			return strings.TrimSpace(trimmed[2:]), strings.Join(lines[index+1:], "\n")
		}
		break
	}
	return "", body
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
		_, err = command.OutOrStdout().Write(result.Content)
		return err
	}
	result, err := service.Create(request, false)
	if err != nil {
		return err
	}
	if WantsJSON(command) {
		return writeMutation(command, "tickets.create", statusApplied, result.Ticket.Slug, result.Ticket.Title)
	}
	_, err = fmt.Fprintf(command.OutOrStdout(), "Created ticket %q at %q\n", result.Ticket.Slug, result.Ticket.Path)
	return err
}

func newTicketsListCommand(options Options) *cobra.Command {
	var board, status string
	var ready bool
	var limit, offset int
	command := &cobra.Command{
		Use:   "list",
		Short: "List Tickets",
		Args:  noArgs,
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
			writer := tabwriter.NewWriter(command.OutOrStdout(), 0, 4, 2, ' ', 0)
			if _, err := fmt.Fprintln(writer, "SLUG\tSTATUS\tPRIORITY\tBOARD\tTITLE"); err != nil {
				return err
			}
			for _, ticket := range tickets {
				boardName := ticket.Board
				if boardName == "" {
					boardName = "-"
				}
				if _, err := fmt.Fprintf(writer, "%s\t%s\t%d\t%s\t%s\n", ticket.Slug, ticket.Status, ticket.Priority, boardName, ticket.Title); err != nil {
					return err
				}
			}
			return writer.Flush()
		},
	}
	command.Flags().StringVar(&board, "board", "", "List one Board; an empty value lists ungrouped Tickets")
	command.Flags().StringVar(&status, "status", "", "List one status")
	command.Flags().BoolVar(&ready, "ready", false, "List only unclaimed, unblocked, ready-for-agent Tickets")
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
			if _, err := fmt.Fprintf(command.OutOrStdout(), "Slug: %s\nTitle: %s\nStatus: %s\nPriority: %d\nBoard: %s\nPath: %s\n",
				ticket.Slug, ticket.Title, ticket.Status, ticket.Priority, ticket.Board, ticket.Path); err != nil {
				return err
			}
			if ticket.ClaimedBy != "" {
				if _, err := fmt.Fprintf(command.OutOrStdout(), "Claimed by: %s\n", ticket.ClaimedBy); err != nil {
					return err
				}
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
				if _, err := fmt.Fprintf(command.OutOrStdout(), "Blocked by open tickets: %s\n", strings.Join(blockers, ", ")); err != nil {
					return err
				}
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
		Use:   "list",
		Short: "List Boards",
		Args:  noArgs,
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
			writer := tabwriter.NewWriter(command.OutOrStdout(), 0, 4, 2, ' ', 0)
			if _, err := fmt.Fprintln(writer, "NAME\tTICKETS"); err != nil {
				return err
			}
			for _, board := range boards {
				if _, err := fmt.Fprintf(writer, "%s\t%d\n", board.Name, board.Tickets); err != nil {
					return err
				}
			}
			return writer.Flush()
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
			_, err = fmt.Fprintf(command.OutOrStdout(), "Board: %s\nPath: %s\nTickets: %d\n", board.Name, board.Path, board.Tickets)
			return err
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
