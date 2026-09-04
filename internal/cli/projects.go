package cli

import (
	"context"
	"fmt"
	"io"
	"sort"
	"time"

	"github.com/jpugliesi/tmux-worktree/internal/domain"
	"github.com/jpugliesi/tmux-worktree/internal/prstate"
	"github.com/jpugliesi/tmux-worktree/internal/store"
	ticketservice "github.com/jpugliesi/tmux-worktree/internal/ticket"
	"github.com/spf13/cobra"
)

type projectsListOutput struct {
	SchemaVersion int              `json:"schemaVersion"`
	Projects      []projectListRow `json:"projects"`
	TotalCount    int              `json:"totalCount"`
	Truncated     bool             `json:"truncated,omitempty"`
}

// projectListRow is one Projects list row: the Project plus derived STATUS
// and Ticket counts that match the board sections.
type projectListRow struct {
	domain.Project
	Status   string `json:"status"`
	Waiting  int    `json:"waiting"`
	Progress int    `json:"progress"`
	Review   int    `json:"review"`
	Ready    int    `json:"ready"`
	Blocked  int    `json:"blocked"`
	Todo     int    `json:"todo"`
	Done     int    `json:"done"`
}

type projectShowOutput struct {
	SchemaVersion int             `json:"schemaVersion"`
	Project       domain.Project  `json:"project"`
	Ready         []domain.Ticket `json:"ready"`
	InFlight      []domain.Ticket `json:"inFlight"`
	// WaitingOnYou are the claimed needs-info Tickets: an agent asked and
	// waits on the human.
	WaitingOnYou []domain.Ticket `json:"waitingOnYou"`
	// Board sections derived from status, claim, and PR state.
	InProgress []domain.Ticket            `json:"inProgress"`
	InReview   []domain.Ticket            `json:"inReview"`
	Blocked    []domain.Ticket            `json:"blocked"`
	Done       []domain.Ticket            `json:"done"`
	Sessions   []boardSessionOutput       `json:"sessions"`
	PRStates   map[string]prstate.PRState `json:"prStates,omitempty"`
	// StoreAsOf is the last successful exchange with the tickets remote on
	// this machine (empty when sync is off).
	StoreAsOf  string            `json:"storeAsOf,omitempty"`
	Workspaces []workspaceOutput `json:"workspaces"`
}

// boardSessionOutput is the newest dispatch session per Ticket, read from
// the local and cloud stores without any probing.
type boardSessionOutput struct {
	Backend     string `json:"backend"`
	ID          string `json:"id"`
	Ticket      string `json:"ticket"`
	Status      string `json:"status"`
	WorkspaceID string `json:"workspaceId,omitempty"`
	UpdatedAt   string `json:"updatedAt"`
}

func newProjectsCommand(options Options) *cobra.Command {
	projects := groupCommand(&cobra.Command{Use: "projects", Short: "Manage Ticket Projects"})
	projects.AddCommand(newProjectsCreateCommand(options))
	projects.AddCommand(newProjectsCloseCommand(options))
	projects.AddCommand(newProjectsRemoveCommand(options))
	projects.AddCommand(newProjectsRenameCommand(options))
	projects.AddCommand(newProjectsSetCommand(options))
	projects.AddCommand(newProjectsListCommand(options))
	projects.AddCommand(newProjectsShowCommand(options))
	projects.AddCommand(newProjectsPlanCommand(options))
	return projects
}

func newProjectsCreateCommand(options Options) *cobra.Command {
	var templateName string
	command := &cobra.Command{
		Use:   "create [NAME]",
		Short: "Create a Project directory",
		Args:  optionalArg("NAME"),
		RunE: func(command *cobra.Command, args []string) error {
			service, err := options.ticketService()
			if err != nil {
				return err
			}
			name := ""
			if len(args) > 0 {
				name = args[0]
			}
			return runProjectsCreate(command, options, service, name, templateName)
		},
	}
	command.Flags().StringVar(&templateName, "template", "", "Save the Workspace Template for this Project")
	setArguments(command, optionalArgument("name", "the prompt asks for it when stdin is a terminal and output is text"))
	_ = command.RegisterFlagCompletionFunc("template", templateFlagCompletion(options.templateStore()))
	return command
}

// createProject creates one Project. Both the command and apply use it.
func createProject(command *cobra.Command, options Options, service ticketservice.Store, name, templateName string) error {
	if templateName != "" {
		if _, err := options.templateStore().Load(templateName); err != nil {
			return err
		}
	}
	return runMutation(command, "projects.create",
		func() (string, string, error) {
			project, err := service.CreateProjectWithTemplate(name, templateName, true)
			return project.Name, project.Name, err
		},
		func() (string, string, error) {
			project, err := service.CreateProjectWithTemplate(name, templateName, false)
			return project.Name, project.Name, err
		},
		func(out io.Writer, _, projectName string) error {
			_, err := fmt.Fprintf(out, "Created Project %q\n", projectName)
			return err
		})
}

func newProjectsSetCommand(options Options) *cobra.Command {
	var templateName string
	command := &cobra.Command{
		Use:   "set NAME --template TEMPLATE",
		Short: "Set Project configuration",
		Args:  exactArgs("NAME"),
		RunE: func(command *cobra.Command, args []string) error {
			if !command.Flags().Changed("template") || templateName == "" {
				return invalidUsage(command, "pass --template TEMPLATE")
			}
			if _, err := options.templateStore().Load(templateName); err != nil {
				return err
			}
			service, err := options.ticketService()
			if err != nil {
				return err
			}
			return setProjectTemplate(command, service, args[0], templateName)
		},
	}
	command.Flags().StringVar(&templateName, "template", "", "Set the Workspace Template for this Project")
	setArguments(command, requiredArgument("name"))
	command.ValidArgsFunction = ticketProjectNameCompletion(options)
	_ = command.RegisterFlagCompletionFunc("template", templateFlagCompletion(options.templateStore()))
	return command
}

func setProjectTemplate(command *cobra.Command, service ticketservice.Store, name, templateName string) error {
	return runMutation(command, "projects.set",
		func() (string, string, error) {
			project, err := service.SetProjectTemplate(name, templateName, true)
			return project.Name, project.Name, err
		},
		func() (string, string, error) {
			project, err := service.SetProjectTemplate(name, templateName, false)
			return project.Name, project.Name, err
		},
		func(out io.Writer, _, projectName string) error {
			_, err := fmt.Fprintf(out, "Set Workspace Template %q on Project %q\n", templateName, projectName)
			return err
		})
}

func newProjectsListCommand(options Options) *cobra.Command {
	var limit, offset int
	var all bool
	command := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List Projects",
		Args:    noArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			service, err := options.ticketService()
			if err != nil {
				return err
			}
			var projects []domain.Project
			if all {
				projects, err = service.AllProjects()
			} else {
				projects, err = service.Projects()
			}
			if err != nil {
				return err
			}
			tickets, err := service.List(ticketservice.ListFilter{All: true})
			if err != nil {
				return err
			}
			ready, err := service.List(ticketservice.ListFilter{Ready: true})
			if err != nil {
				return err
			}
			rows := projectListRows(projects, tickets, ready)
			rows, total, truncated, err := applyWindow(rows, offset, limit)
			if err != nil {
				return err
			}
			if format := resolvedOutputFormat(command); format != outputText {
				if format == outputNDJSON {
					return writeNDJSONList(command, rows, total, truncated)
				}
				return writeReadJSON(command, projectsListOutput{SchemaVersion: jsonSchemaVersion, Projects: rows, TotalCount: total, Truncated: truncated}, "projects")
			}
			if total == 0 {
				_, err = fmt.Fprintln(command.ErrOrStderr(), "No Projects exist. Run 'twt projects create NAME'.")
				return err
			}
			table := make([][]string, 0, len(rows))
			for _, row := range rows {
				table = append(table, []string{
					row.Name, row.Status,
					fmt.Sprintf("%d", row.Tickets),
					fmt.Sprintf("%d", row.Waiting),
					fmt.Sprintf("%d", row.Progress),
					fmt.Sprintf("%d", row.Review),
					fmt.Sprintf("%d", row.Ready),
					fmt.Sprintf("%d", row.Blocked),
					fmt.Sprintf("%d", row.Todo),
					fmt.Sprintf("%d", row.Done),
				})
			}
			return writeTable(command.OutOrStdout(), []string{
				"NAME", "STATUS", "TICKETS", "WAITING", "PROGRESS", "REVIEW", "READY", "BLOCKED", "TODO", "DONE",
			}, table)
		},
	}
	command.Flags().BoolVar(&all, "all", false, "Include closed Projects")
	addListReadFlags(command, &limit, &offset, projectListRow{})
	return command
}

func projectListStatus(project domain.Project) string {
	if project.Closed {
		return "closed"
	}
	return "active"
}

func projectListRows(projects []domain.Project, tickets []domain.Ticket, ready []domain.Ticket) []projectListRow {
	readySlugs := make(map[string]bool, len(ready))
	for _, ticket := range ready {
		readySlugs[ticket.Slug] = true
	}
	byProject := make(map[string][]domain.Ticket)
	for _, ticket := range tickets {
		if ticket.Project == "" {
			continue
		}
		byProject[ticket.Project] = append(byProject[ticket.Project], ticket)
	}
	rows := make([]projectListRow, 0, len(projects))
	for _, project := range projects {
		row := projectListRow{Project: project, Status: projectListStatus(project)}
		for _, ticket := range byProject[project.Name] {
			switch deriveTicketState(ticket.Status, ticket.ClaimedBy, ticket.PullRequests, nil, readySlugs[ticket.Slug]) {
			case ticketStateNeedsInput:
				row.Waiting++
			case ticketStateInProgress:
				row.Progress++
			case ticketStateInReview:
				row.Review++
			case ticketStateReady:
				row.Ready++
			case ticketStateBlocked:
				row.Blocked++
			case string(domain.TicketDone), string(domain.TicketWontfix):
				row.Done++
			default:
				row.Todo++
			}
		}
		rows = append(rows, row)
	}
	return rows
}

func newProjectsShowCommand(options Options) *cobra.Command {
	var noFetch, fresh bool
	command := &cobra.Command{
		Use:   "get [NAME]",
		Short: "Get a Project",
		Args:  optionalArg("NAME"),
		RunE: func(command *cobra.Command, args []string) error {
			name := ""
			if len(args) == 1 {
				name = args[0]
			} else {
				scope, err := resolveTicketProject(command, options, "", false, false)
				if err != nil {
					return err
				}
				if !scope.Set || scope.Project == "" {
					return invalidUsageWithHint(command,
						"Pass NAME, set TWT_PROJECT, or run this from a Workspace with a Project.",
						"no Project is in scope")
				}
				name = scope.Project
			}
			service, err := options.ticketService()
			if err != nil {
				return err
			}
			freshenTicketStore(command, service, fresh)
			project, err := service.Project(name)
			if err != nil {
				return err
			}
			board, err := projectBoard(command.Context(), options, service, project, noFetch)
			if err != nil {
				return err
			}
			if WantsJSON(command) {
				return writeReadJSON(command, board, "project")
			}
			return writeProjectBoard(command.OutOrStdout(), board, time.Now())
		},
	}
	command.Flags().BoolVar(&noFetch, "no-fetch", false, "Use only cached PR state; never call the forge")
	addFreshFlag(command, &fresh)
	setArguments(command, optionalArgument("name", "the current Project when absent"))
	addFieldsFlag(command, projectShowOutput{})
	command.ValidArgsFunction = ticketProjectNameCompletion(options)
	return command
}

// projectBoard is the coordinator read for one Project: pickable Tickets,
// claimed Tickets, board sections with PR state, sessions, and the
// Workspaces that belong to the Project. It never probes tmux or calls a
// backend; sessions come from the last sync's records.
func projectBoard(ctx context.Context, options Options, service ticketservice.Store, project domain.Project, offline bool) (projectShowOutput, error) {
	ready, err := service.List(ticketservice.ListFilter{Project: project.Name, ProjectSet: true, Ready: true})
	if err != nil {
		return projectShowOutput{}, err
	}
	if ready == nil {
		ready = []domain.Ticket{}
	}
	claimed, err := service.List(ticketservice.ListFilter{Project: project.Name, ProjectSet: true, Claimed: true})
	if err != nil {
		return projectShowOutput{}, err
	}
	if claimed == nil {
		claimed = []domain.Ticket{}
	}
	waiting, err := service.List(ticketservice.ListFilter{Project: project.Name, ProjectSet: true, NeedsInput: true})
	if err != nil {
		return projectShowOutput{}, err
	}
	if waiting == nil {
		waiting = []domain.Ticket{}
	}
	workspaces, err := options.workspaceService().List()
	if err != nil {
		return projectShowOutput{}, err
	}
	linked := filterWorkspaces(workspaces, project.Name, "", "", true, false)
	outputs := make([]workspaceOutput, 0, len(linked))
	for _, workspace := range linked {
		outputs = append(outputs, toWorkspaceOutput(workspace))
	}
	all, err := service.List(ticketservice.ListFilter{Project: project.Name, ProjectSet: true, All: true})
	if err != nil {
		return projectShowOutput{}, err
	}
	readySlugs := map[string]bool{}
	for _, ticket := range ready {
		readySlugs[ticket.Slug] = true
	}
	urls := []string{}
	for _, ticket := range all {
		if ticket.Status != domain.TicketDone && ticket.Status != domain.TicketWontfix {
			urls = append(urls, ticket.PullRequests...)
		}
	}
	var prStates map[string]prstate.PRState
	if len(urls) > 0 {
		prStates = options.prStateService().GetAll(ctx, urls, offline)
	}
	board := projectShowOutput{
		SchemaVersion: jsonSchemaVersion,
		Project:       project,
		Ready:         ready,
		InFlight:      claimed,
		WaitingOnYou:  waiting,
		InProgress:    []domain.Ticket{},
		InReview:      []domain.Ticket{},
		Blocked:       []domain.Ticket{},
		Done:          []domain.Ticket{},
		Sessions:      boardSessions(options, project.Name),
		PRStates:      prStates,
		Workspaces:    outputs,
	}
	for _, ticket := range all {
		state := deriveTicketState(ticket.Status, ticket.ClaimedBy, ticket.PullRequests, prStates, readySlugs[ticket.Slug])
		switch state {
		case "done", "wontfix":
			board.Done = append(board.Done, ticket)
		case "in-review":
			board.InReview = append(board.InReview, ticket)
		case "in-progress":
			board.InProgress = append(board.InProgress, ticket)
		case "blocked":
			board.Blocked = append(board.Blocked, ticket)
		}
	}
	sort.Slice(board.Done, func(i, j int) bool { return board.Done[i].Updated > board.Done[j].Updated })
	if len(board.Done) > 5 {
		board.Done = board.Done[:5]
	}
	if reconciled := ticketservice.LastReconciledAt(options.StateDir); !reconciled.IsZero() {
		board.StoreAsOf = reconciled.Format(time.RFC3339)
	}
	return board, nil
}

// boardSessions reads the newest dispatch session per Ticket from both
// backend stores.
func boardSessions(options Options, project string) []boardSessionOutput {
	newest := map[string]boardSessionOutput{}
	newestAt := map[string]time.Time{}
	if sessions, err := store.NewLocalDispatchSessionStore(options.StateDir).List(); err == nil {
		for _, session := range sessions {
			if session.Project != project {
				continue
			}
			if session.UpdatedAt.After(newestAt[session.TicketSlug]) {
				newestAt[session.TicketSlug] = session.UpdatedAt
				newest[session.TicketSlug] = boardSessionOutput{
					Backend: "local", ID: session.ID, Ticket: session.TicketSlug,
					Status: string(session.Status), WorkspaceID: session.WorkspaceID,
					UpdatedAt: session.UpdatedAt.Format(time.RFC3339),
				}
			}
		}
	}
	outputs := make([]boardSessionOutput, 0, len(newest))
	for _, session := range newest {
		outputs = append(outputs, session)
	}
	sort.Slice(outputs, func(i, j int) bool { return outputs[i].Ticket < outputs[j].Ticket })
	return outputs
}

// writeProjectBoard renders the sectioned text board. Empty sections are
// omitted. now is injected so tests stay deterministic.
func writeProjectBoard(out io.Writer, board projectShowOutput, now time.Time) error {
	header := fmt.Sprintf("Project: %s", board.Project.Name)
	if board.Project.HasPlan {
		header += fmt.Sprintf("    Plan: plan.md (updated %s)", board.Project.PlanUpdatedAt)
	}
	if _, err := fmt.Fprintln(out, header); err != nil {
		return err
	}
	sessionsByTicket := map[string]boardSessionOutput{}
	for _, session := range board.Sessions {
		sessionsByTicket[session.Ticket] = session
	}
	section := func(title string, tickets []domain.Ticket, line func(domain.Ticket) string) error {
		if len(tickets) == 0 {
			return nil
		}
		if _, err := fmt.Fprintf(out, "\n%s (%d)\n", title, len(tickets)); err != nil {
			return err
		}
		for _, ticket := range tickets {
			if _, err := fmt.Fprintf(out, "  %s\n", line(ticket)); err != nil {
				return err
			}
		}
		return nil
	}
	plain := func(ticket domain.Ticket) string {
		return fmt.Sprintf("%s  p%d  %s", ticket.Slug, ticket.Priority, ticket.Title)
	}
	withSession := func(ticket domain.Ticket) string {
		line := fmt.Sprintf("%s  @%s", ticket.Slug, ticket.ClaimedBy)
		if session, found := sessionsByTicket[ticket.Slug]; found {
			line += fmt.Sprintf("  session %s (%s)", session.Status, session.Backend)
		}
		if badge := prBadge(ticket.PullRequests, board.PRStates); badge != "" {
			line += "  " + badge
		}
		return line
	}
	waiting := func(ticket domain.Ticket) string {
		return withSession(ticket) + fmt.Sprintf("  <- answer: twt tickets answer %s -", ticket.Slug)
	}
	review := func(ticket domain.Ticket) string {
		line := fmt.Sprintf("%s  %s", ticket.Slug, string(ticket.Status))
		if badge := prBadge(ticket.PullRequests, board.PRStates); badge != "" {
			line += "  " + badge
		}
		if allMerged(ticket.PullRequests, board.PRStates) {
			line += "  <- all PRs merged; close it"
		}
		return line
	}
	if err := section("WAITING ON YOU", board.WaitingOnYou, waiting); err != nil {
		return err
	}
	if err := section("IN PROGRESS", board.InProgress, withSession); err != nil {
		return err
	}
	if err := section("IN REVIEW", board.InReview, review); err != nil {
		return err
	}
	if err := section("READY", board.Ready, plain); err != nil {
		return err
	}
	if err := section("BLOCKED", board.Blocked, plain); err != nil {
		return err
	}
	if err := section("DONE (last 5)", board.Done, plain); err != nil {
		return err
	}
	footer := "\nSessions as of the last sync; run 'twt tickets sync --project " + board.Project.Name + "' to refresh."
	if board.StoreAsOf != "" {
		if reconciled, err := time.Parse(time.RFC3339, board.StoreAsOf); err == nil {
			footer = fmt.Sprintf("\nStore as of %s. ", relativeAge(reconciled, now)) + footer[1:]
		}
	}
	_, err := fmt.Fprintln(out, footer)
	return err
}

// relativeAge renders how long ago t was, at minute resolution.
func relativeAge(t, now time.Time) string {
	age := now.Sub(t)
	switch {
	case age < time.Minute:
		return "less than 1m ago"
	case age < time.Hour:
		return fmt.Sprintf("%dm ago", int(age.Minutes()))
	case age < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(age.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(age.Hours()/24))
	}
}
