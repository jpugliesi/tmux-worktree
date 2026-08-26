package cli

import (
	"fmt"

	agentservice "github.com/jpugliesi/tmux-worktree/internal/agent"
	"github.com/jpugliesi/tmux-worktree/internal/clierr"
	"github.com/jpugliesi/tmux-worktree/internal/cursorcloud"
	"github.com/jpugliesi/tmux-worktree/internal/domain"
	"github.com/jpugliesi/tmux-worktree/internal/localdispatch"
	"github.com/jpugliesi/tmux-worktree/internal/store"
	"github.com/spf13/cobra"
)

type ticketsSyncLocalOutput struct {
	Capacity    localdispatch.Capacity       `json:"capacity"`
	Sessions    []localDispatchSessionOutput `json:"sessions"`
	Diagnostics []localdispatch.Diagnostic   `json:"diagnostics,omitempty"`
}

type ticketsSyncCloudOutput struct {
	Capacity    cursorcloud.CloudCapacity    `json:"capacity"`
	Sessions    []cursorCloudSessionOutput   `json:"sessions"`
	Diagnostics []cursorcloud.SyncDiagnostic `json:"diagnostics,omitempty"`
}

type ticketsSyncBackendsOutput struct {
	Local       *ticketsSyncLocalOutput `json:"local,omitempty"`
	CursorCloud *ticketsSyncCloudOutput `json:"cursor-cloud,omitempty"`
}

type ticketsSyncOutput struct {
	SchemaVersion int                       `json:"schemaVersion"`
	Operation     string                    `json:"operation"`
	Status        string                    `json:"status"`
	Project       string                    `json:"project"`
	Backends      ticketsSyncBackendsOutput `json:"backends"`
}

func newTicketsSyncCommand(options Options) *cobra.Command {
	var project string
	command := &cobra.Command{
		Use:   "sync",
		Short: "Sync local and Cursor Cloud Sessions with Ticket states",
		Args:  noArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			return runTicketsSync(command, options, project)
		},
	}
	command.Flags().StringVar(&project, "project", "", "Sync the Sessions of this Project")
	_ = command.MarkFlagRequired("project")
	registerProjectFlagCompletion(command, options)
	return command
}

func runTicketsSync(command *cobra.Command, options Options, project string) error {
	dryRun := isDryRun(command)
	localService, err := options.localDispatchService(command, false)
	if err != nil {
		return err
	}
	localResult, err := localService.Sync(project, dryRun)
	if err != nil {
		return err
	}
	localSessions := make([]localDispatchSessionOutput, 0, len(localResult.Sessions))
	for _, session := range localResult.Sessions {
		localSessions = append(localSessions, toLocalDispatchSessionOutput(session))
	}
	output := ticketsSyncOutput{
		SchemaVersion: jsonSchemaVersion,
		Operation:     "tickets.sync",
		Project:       project,
		Backends: ticketsSyncBackendsOutput{
			Local: &ticketsSyncLocalOutput{
				Capacity: localResult.Capacity, Sessions: localSessions, Diagnostics: localResult.Diagnostics,
			},
		},
	}
	diagnostics := len(localResult.Diagnostics)

	cloudOutput, cloudDiagnostics, err := runTicketsSyncCloudHalf(command, options, project, dryRun)
	if err != nil {
		return err
	}
	output.Backends.CursorCloud = cloudOutput
	diagnostics += cloudDiagnostics

	output.Status = statusApplied
	if dryRun {
		output.Status = statusValid
	} else if diagnostics > 0 {
		output.Status = statusPartial
	}
	if WantsJSON(command) {
		return writeJSONOutput(command, output)
	}
	if err := writeTicketsSyncCapacityLine(command, "local", output.Backends.Local.Capacity.Active,
		output.Backends.Local.Capacity.Available, output.Backends.Local.Capacity.Maximum, project); err != nil {
		return err
	}
	if cloudOutput != nil {
		if err := writeTicketsSyncCapacityLine(command, "cursor-cloud", cloudOutput.Capacity.Active,
			cloudOutput.Capacity.Available, cloudOutput.Capacity.Maximum, project); err != nil {
			return err
		}
	}
	for _, diagnostic := range localResult.Diagnostics {
		if _, err := fmt.Fprintf(command.ErrOrStderr(), "local Session %q for Ticket %q: %s: %s\n",
			diagnostic.SessionID, diagnostic.Ticket, diagnostic.Code, diagnostic.Message); err != nil {
			return err
		}
	}
	if cloudOutput != nil {
		for _, diagnostic := range cloudOutput.Diagnostics {
			if _, err := fmt.Fprintf(command.ErrOrStderr(), "cursor-cloud Session %q for Ticket %q: %s\n",
				diagnostic.SessionID, diagnostic.Ticket, diagnostic.Message); err != nil {
				return err
			}
		}
	}
	return nil
}

// runTicketsSyncCloudHalf syncs the cloud backend. The harness is required
// only when pending cloud Sessions exist; a Template without cursor_cloud
// and without pending Sessions omits the backend entirely.
func runTicketsSyncCloudHalf(command *cobra.Command, options Options, project string, dryRun bool) (*ticketsSyncCloudOutput, int, error) {
	sessions, err := store.NewCursorCloudSessionStore(options.StateDir).List()
	if err != nil {
		return nil, 0, err
	}
	pending := 0
	for _, session := range sessions {
		if session.Project == project && (session.Active() || !session.TicketTransitioned) {
			pending++
		}
	}
	cloudSpec, err := projectCursorCloudSpec(options, project)
	if err != nil {
		return nil, 0, err
	}
	if pending == 0 {
		if cloudSpec == nil {
			return nil, 0, nil
		}
		maximum := cloudSpec.EffectiveMaxConcurrency()
		return &ticketsSyncCloudOutput{
			Capacity: cursorcloud.CloudCapacity{Maximum: maximum, Active: 0, Available: maximum, Known: true},
			Sessions: []cursorCloudSessionOutput{},
		}, 0, nil
	}
	service, err := options.cursorCloudService(true)
	if err != nil {
		return nil, 0, err
	}
	result, err := service.Sync(command.Context(), project, dryRun)
	if err != nil {
		return nil, 0, err
	}
	outputs := make([]cursorCloudSessionOutput, 0, len(result.Sessions))
	for _, session := range result.Sessions {
		outputs = append(outputs, toCursorCloudSessionOutput(session))
	}
	return &ticketsSyncCloudOutput{
		Capacity: result.Capacity, Sessions: outputs, Diagnostics: result.Diagnostics,
	}, len(result.Diagnostics), nil
}

// projectCursorCloudSpec loads the cursor_cloud block of a Project Template,
// or nil when the Project or Template does not configure it.
func projectCursorCloudSpec(options Options, project string) (*domain.CursorCloudSpec, error) {
	tickets, err := options.ticketService()
	if err != nil {
		return nil, err
	}
	resolved, err := tickets.Project(project)
	if err != nil {
		return nil, err
	}
	if resolved.TemplateName == "" {
		return nil, nil
	}
	template, err := options.templateStore().Load(resolved.TemplateName)
	if err != nil {
		return nil, err
	}
	return template.CursorCloud, nil
}

func writeTicketsSyncCapacityLine(command *cobra.Command, backend string, active, available, maximum int, project string) error {
	_, err := fmt.Fprintf(command.OutOrStdout(),
		"%s: %d active, %d available, %d maximum for Project %q.\n",
		backend, active, available, maximum, project)
	return err
}

func newTicketsAbandonCommand(options Options) *cobra.Command {
	var force bool
	command := &cobra.Command{
		Use:   "abandon SESSION",
		Short: "Stop recovery for one local dispatch Session",
		Args:  exactArgs("SESSION"),
		RunE: func(command *cobra.Command, args []string) error {
			if !force {
				return invalidUsageWithHint(command,
					"Add --force only after you confirm no one needs the running local agent.",
					"missing required flag --force")
			}
			return runLocalDispatchAbandon(command, options, args[0])
		},
	}
	command.Flags().BoolVar(&force, "force", false, "Permit the Workspace and its agent to keep running after recovery stops")
	setArguments(command, requiredArgument("session"))
	command.ValidArgsFunction = localDispatchSessionCompletion(options.StateDir)
	return command
}

func runLocalDispatchAbandon(command *cobra.Command, options Options, reference string) error {
	service, err := options.localDispatchService(command, false)
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
		output := toLocalDispatchSessionOutput(session)
		return writeJSONOutput(command, localDispatchMutationOutput{
			SchemaVersion: jsonSchemaVersion, Operation: "tickets.abandon", Status: status,
			Backend: dispatchBackendLocal, Session: &output,
		})
	}
	if _, err := fmt.Fprintf(command.OutOrStdout(), "%s local Session %q for Ticket %q\n", verb, session.ID, session.TicketSlug); err != nil {
		return err
	}
	if !isDryRun(command) && session.WorkspaceID != "" {
		_, err = fmt.Fprintf(command.OutOrStdout(),
			"The Workspace %q and its agent keep running. Inspect them, then run 'twt done %s'.\n",
			session.WorkspaceName, session.WorkspaceID)
		return err
	}
	return nil
}

// localDispatchSessionCompletion completes recoverable local Session IDs.
func localDispatchSessionCompletion(stateDir string) completionFunc {
	return func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) > 0 {
			return nil, noFileCompletion
		}
		sessions, err := store.NewLocalDispatchSessionStore(stateDir).List()
		if err != nil {
			return nil, noFileCompletion
		}
		ids := make([]string, 0, len(sessions))
		for _, session := range sessions {
			if session.Active() || !session.TicketTransitioned {
				ids = append(ids, session.ID)
			}
		}
		return matching(ids, toComplete), noFileCompletion
	}
}

// cliWorkspaceObserver adapts the workspace store and the agent catalog to
// the local dispatch observer seam.
type cliWorkspaceObserver struct {
	options Options
}

func (o cliWorkspaceObserver) Observe(workspaceID, agentSessionID, agentLabel string) (localdispatch.AgentObservation, bool, error) {
	if workspaceID == "" {
		return localdispatch.AgentObservation{Complete: true}, true, nil
	}
	workspace, err := store.NewWorkspaceStore(o.options.StateDir).Find(workspaceID)
	if err != nil {
		if clierr.CodeOf(err) == clierr.NotFound {
			return localdispatch.AgentObservation{Complete: true}, true, nil
		}
		return localdispatch.AgentObservation{}, false, err
	}
	if workspace.Status == domain.WorkspaceArchived || workspace.Status == domain.WorkspaceRemoving {
		return localdispatch.AgentObservation{Complete: true}, true, nil
	}
	catalog, err := agentservice.NewService(o.options.StateDir, o.options.TmuxSocket).Catalog(workspace)
	if err != nil {
		return localdispatch.AgentObservation{}, false, err
	}
	observation := localdispatch.AgentObservation{
		Complete:    catalog.Complete,
		Diagnostics: catalog.Diagnostics,
	}
	for _, entry := range catalog.Entries {
		if entry.AgentSessionID == agentSessionID || (agentSessionID == "" && entry.Label == agentLabel) {
			observation.Found = true
			observation.Live = entry.Status == "live"
			break
		}
	}
	return observation, false, nil
}
