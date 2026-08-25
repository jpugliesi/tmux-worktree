package cli

import (
	"fmt"
	"time"

	"github.com/jpugliesi/tmux-worktree/internal/cursorcloud"
	"github.com/jpugliesi/tmux-worktree/internal/domain"
	"github.com/spf13/cobra"
)

type cursorCloudSessionOutput struct {
	ID                 string                         `json:"id"`
	Ticket             string                         `json:"ticket"`
	Project            string                         `json:"project"`
	Template           string                         `json:"template"`
	Mode               domain.CursorCloudMode         `json:"mode"`
	Status             domain.CursorCloudStatus       `json:"status"`
	CursorAgentID      string                         `json:"cursorAgentId,omitempty"`
	RunID              string                         `json:"runId,omitempty"`
	RequestID          string                         `json:"requestId,omitempty"`
	EffectiveEffort    domain.CursorCloudEffort       `json:"effectiveEffort,omitempty"`
	Repositories       []domain.CursorCloudRepository `json:"repositories"`
	Result             string                         `json:"result,omitempty"`
	Error              *domain.CursorCloudError       `json:"error,omitempty"`
	HandoffIncomplete  bool                           `json:"handoffIncomplete,omitempty"`
	TicketTransitioned bool                           `json:"ticketTransitioned,omitempty"`
	CreatedAt          string                         `json:"createdAt"`
	UpdatedAt          string                         `json:"updatedAt"`
}

type cursorCloudMutationOutput struct {
	SchemaVersion int                          `json:"schemaVersion"`
	Operation     string                       `json:"operation"`
	Status        string                       `json:"status"`
	Capacity      *cursorcloud.CloudCapacity   `json:"capacity,omitempty"`
	Session       *cursorCloudSessionOutput    `json:"session,omitempty"`
	Sessions      []cursorCloudSessionOutput   `json:"sessions,omitempty"`
	Diagnostics   []cursorcloud.SyncDiagnostic `json:"diagnostics,omitempty"`
}

func newTicketsDispatchCommand(options Options) *cobra.Command {
	var plan bool
	var maxConcurrency int
	command := &cobra.Command{
		Use:   "dispatch TICKET",
		Short: "Start a Cursor Cloud Session for one ready Ticket",
		Args:  exactArgs("TICKET"),
		RunE: func(command *cobra.Command, args []string) error {
			return runCursorCloudDispatch(command, options, args[0], plan, maxConcurrency)
		},
	}
	command.Flags().BoolVar(&plan, "plan", false, "Ask the Cloud Agent to create a plan without changing code")
	command.Flags().IntVar(&maxConcurrency, "max-concurrency", 0, "Override the Project-wide active Cloud Session limit")
	setArguments(command, requiredArgument("ticket"))
	command.ValidArgsFunction = ticketFlagCompletion(options)
	return command
}

func newTicketsCloudSyncCommand(options Options) *cobra.Command {
	var project string
	command := &cobra.Command{
		Use:   "cloud-sync",
		Short: "Sync Cursor Cloud runs and Ticket states",
		Args:  noArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			return runCursorCloudSync(command, options, project)
		},
	}
	command.Flags().StringVar(&project, "project", "", "Sync Cloud Sessions for this Project")
	_ = command.MarkFlagRequired("project")
	registerProjectFlagCompletion(command, options)
	return command
}

func newTicketsCloudAbandonCommand(options Options) *cobra.Command {
	var force bool
	command := &cobra.Command{
		Use:   "cloud-abandon SESSION",
		Short: "Stop recovery for one Cursor Cloud Session",
		Args:  exactArgs("SESSION"),
		RunE: func(command *cobra.Command, args []string) error {
			if !force {
				return invalidUsageWithHint(command,
					"Add --force only after you confirm that Cursor can still run the remote Agent.",
					"missing required flag --force")
			}
			return runCursorCloudAbandon(command, options, args[0])
		},
	}
	command.Flags().BoolVar(&force, "force", false, "Permit a remote Agent to continue after local recovery stops")
	setArguments(command, requiredArgument("session"))
	command.ValidArgsFunction = cursorCloudSessionCompletion(options.StateDir)
	return command
}

func runCursorCloudDispatch(command *cobra.Command, options Options, ticket string, plan bool, maxConcurrency int) error {
	service, err := options.cursorCloudService(!isDryRun(command))
	if err != nil {
		return err
	}
	mode := domain.CursorCloudModeAgent
	if plan {
		mode = domain.CursorCloudModePlan
	}
	session, err := service.Dispatch(command.Context(), cursorcloud.DispatchOptions{
		TicketRef: ticket, Mode: mode, MaxConcurrency: maxConcurrency, DryRun: isDryRun(command),
	})
	if err != nil {
		return err
	}
	status := statusApplied
	if isDryRun(command) {
		status = statusValid
	}
	if WantsJSON(command) {
		output := toCursorCloudSessionOutput(session)
		return writeJSONOutput(command, cursorCloudMutationOutput{
			SchemaVersion: jsonSchemaVersion, Operation: "tickets.dispatch", Status: status, Session: &output,
		})
	}
	verb := "Started"
	if isDryRun(command) {
		verb = "Would start"
	}
	_, err = fmt.Fprintf(command.OutOrStdout(), "%s Cursor Cloud Session %q for Ticket %q\n", verb, session.ID, session.TicketSlug)
	return err
}

func runCursorCloudSync(command *cobra.Command, options Options, project string) error {
	service, err := options.cursorCloudService(true)
	if err != nil {
		return err
	}
	result, err := service.Sync(command.Context(), project, isDryRun(command))
	if err != nil {
		return err
	}
	status := statusApplied
	if isDryRun(command) {
		status = statusValid
	} else if len(result.Diagnostics) > 0 {
		status = statusPartial
	}
	sessions := make([]cursorCloudSessionOutput, 0, len(result.Sessions))
	for _, session := range result.Sessions {
		sessions = append(sessions, toCursorCloudSessionOutput(session))
	}
	if WantsJSON(command) {
		return writeJSONOutput(command, cursorCloudMutationOutput{
			SchemaVersion: jsonSchemaVersion, Operation: "tickets.cloud-sync", Status: status,
			Capacity: &result.Capacity, Sessions: sessions, Diagnostics: result.Diagnostics,
		})
	}
	if _, err = fmt.Fprintf(command.OutOrStdout(),
		"Synced %d Cursor Cloud Sessions for Project %q. Capacity: %d active, %d available, %d maximum.\n",
		len(sessions), project, result.Capacity.Active, result.Capacity.Available, result.Capacity.Maximum); err != nil {
		return err
	}
	for _, diagnostic := range result.Diagnostics {
		if _, err = fmt.Fprintf(command.ErrOrStderr(), "Could not sync Session %q for Ticket %q: %s\n", diagnostic.SessionID, diagnostic.Ticket, diagnostic.Message); err != nil {
			return err
		}
	}
	return nil
}

func runCursorCloudAbandon(command *cobra.Command, options Options, reference string) error {
	service, err := options.cursorCloudService(false)
	if err != nil {
		return err
	}
	session, err := service.Abandon(reference, isDryRun(command))
	if err != nil {
		return err
	}
	status := statusApplied
	verb := "Abandoned"
	if isDryRun(command) {
		status = statusValid
		verb = "Would abandon"
	}
	if WantsJSON(command) {
		output := toCursorCloudSessionOutput(session)
		return writeJSONOutput(command, cursorCloudMutationOutput{
			SchemaVersion: jsonSchemaVersion, Operation: "tickets.cloud-abandon", Status: status, Session: &output,
		})
	}
	_, err = fmt.Fprintf(command.OutOrStdout(), "%s Cursor Cloud Session %q for Ticket %q\n", verb, session.ID, session.TicketSlug)
	return err
}

func toCursorCloudSessionOutput(session domain.CursorCloudSession) cursorCloudSessionOutput {
	return cursorCloudSessionOutput{
		ID: session.ID, Ticket: session.TicketSlug, Project: session.Project, Template: session.TemplateName,
		Mode: session.Mode, Status: session.Status, CursorAgentID: session.CursorAgentID, RunID: session.RunID,
		RequestID: session.RequestID, EffectiveEffort: session.EffectiveEffort,
		Repositories: session.Repositories, Result: session.Result, Error: session.Error,
		HandoffIncomplete: session.HandoffIncomplete, TicketTransitioned: session.TicketTransitioned,
		CreatedAt: session.CreatedAt.Format(time.RFC3339), UpdatedAt: session.UpdatedAt.Format(time.RFC3339),
	}
}
