package cli

import (
	"fmt"
	"time"

	"github.com/jpugliesi/tmux-worktree/internal/domain"
	"github.com/jpugliesi/tmux-worktree/internal/localdispatch"
	"github.com/jpugliesi/tmux-worktree/internal/store"
	workspaceservice "github.com/jpugliesi/tmux-worktree/internal/workspace"
	"github.com/spf13/cobra"
)

const (
	dispatchBackendLocal       = "local"
	dispatchBackendCursorCloud = "cursor-cloud"
)

type localDispatchSessionOutput struct {
	ID                 string                     `json:"id"`
	Ticket             string                     `json:"ticket"`
	Project            string                     `json:"project"`
	Template           string                     `json:"template"`
	Mode               domain.CursorCloudMode     `json:"mode"`
	Provider           string                     `json:"provider"`
	Status             domain.LocalDispatchStatus `json:"status"`
	Claimant           string                     `json:"claimant"`
	WorkspaceID        string                     `json:"workspaceId,omitempty"`
	WorkspaceName      string                     `json:"workspaceName,omitempty"`
	TmuxSession        string                     `json:"tmuxSession,omitempty"`
	AgentSessionID     string                     `json:"agentSessionId,omitempty"`
	AgentLabel         string                     `json:"agentLabel,omitempty"`
	Error              *domain.CursorCloudError   `json:"error,omitempty"`
	TicketTransitioned bool                       `json:"ticketTransitioned,omitempty"`
	CreatedAt          string                     `json:"createdAt"`
	UpdatedAt          string                     `json:"updatedAt"`
}

type localDispatchMutationOutput struct {
	SchemaVersion int                         `json:"schemaVersion"`
	Operation     string                      `json:"operation"`
	Status        string                      `json:"status"`
	Backend       string                      `json:"backend"`
	Session       *localDispatchSessionOutput `json:"session,omitempty"`
}

// localDispatchService builds the local dispatch service. The launcher is
// wired for dispatch; read-only paths pass withLauncher false.
func (o Options) localDispatchService(command *cobra.Command, withLauncher bool) (*localdispatch.Service, error) {
	tickets, err := o.ticketService()
	if err != nil {
		return nil, err
	}
	config, err := store.LoadConfig(o.ConfigDir)
	if err != nil {
		return nil, err
	}
	resolved := resolvedTicketAgentConfig(config.TicketAgent)
	if err := validateTicketAgentConfig(resolved); err != nil {
		return nil, err
	}
	serviceOptions := localdispatch.ServiceOptions{
		StateDir:  o.StateDir,
		Templates: o.templateStore(),
		Tickets:   tickets,
		Config:    resolved,
	}
	serviceOptions.Observer = cliWorkspaceObserver{options: o}
	if withLauncher {
		serviceOptions.Launcher = &cliWorkspaceLauncher{command: command, options: o}
	}
	return localdispatch.NewService(serviceOptions), nil
}

// cliWorkspaceLauncher adapts the CLI workspace-create helpers (branch
// prefix, progress, pool refill, ticket stamping) to the launcher seam.
type cliWorkspaceLauncher struct {
	command *cobra.Command
	options Options
}

func (l *cliWorkspaceLauncher) createOptions(request localdispatch.LaunchRequest) workspaceservice.CreateOptions {
	return workspaceservice.CreateOptions{Tickets: request.Tickets, Project: request.Project, BaseRef: request.BaseRef}
}

func (l *cliWorkspaceLauncher) Validate(request localdispatch.LaunchRequest) error {
	service := workspaceservice.NewService(l.options.workspaceServiceOptions())
	return validateCreate(l.options, service, request.Name, request.TemplateName, request.Template, l.createOptions(request))
}

func (l *cliWorkspaceLauncher) Launch(request localdispatch.LaunchRequest) (localdispatch.LaunchResult, error) {
	workspace, err := createWorkspace(l.command, l.options, request.Name, request.TemplateName, request.Template, l.createOptions(request))
	result := localdispatch.LaunchResult{
		WorkspaceID:   workspace.ID,
		WorkspaceName: workspace.Name,
		TmuxSession:   workspace.TmuxSession,
	}
	if err != nil {
		return result, err
	}
	agents, err := store.NewAgentStore(l.options.StateDir).List(workspace.ID)
	if err != nil {
		return result, err
	}
	for _, agent := range agents {
		if agent.Label == request.AgentLabel {
			result.AgentSessionID = agent.ID
			// A started agent has a claimed pane; the degraded save during
			// setup keeps the record paneless.
			result.AgentStarted = agent.TmuxPane != ""
			break
		}
	}
	return result, nil
}

// resolveDispatchBackend selects the backend when --backend is omitted:
// cursor-cloud when the Project Template configures it, otherwise local.
func resolveDispatchBackend(options Options, ticketRef string) (string, error) {
	tickets, err := options.ticketService()
	if err != nil {
		return "", err
	}
	shown, err := tickets.Show(ticketRef)
	if err != nil {
		return "", err
	}
	if shown.Ticket.Project == "" {
		return dispatchBackendLocal, nil
	}
	project, err := tickets.Project(shown.Ticket.Project)
	if err != nil {
		return "", err
	}
	if project.TemplateName == "" {
		return dispatchBackendLocal, nil
	}
	template, err := options.templateStore().Load(project.TemplateName)
	if err != nil {
		return "", err
	}
	if template.CursorCloud != nil {
		return dispatchBackendCursorCloud, nil
	}
	return dispatchBackendLocal, nil
}

func runLocalDispatch(command *cobra.Command, options Options, ticket string, plan bool, maxConcurrency int) error {
	service, err := options.localDispatchService(command, true)
	if err != nil {
		return err
	}
	mode := domain.CursorCloudModeAgent
	if plan {
		mode = domain.CursorCloudModePlan
	}
	session, err := service.Dispatch(localdispatch.DispatchOptions{
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
		output := toLocalDispatchSessionOutput(session)
		return writeJSONOutput(command, localDispatchMutationOutput{
			SchemaVersion: jsonSchemaVersion, Operation: "tickets.dispatch", Status: status,
			Backend: dispatchBackendLocal, Session: &output,
		})
	}
	verb := "Started"
	if isDryRun(command) {
		verb = "Would start"
	}
	_, err = fmt.Fprintf(command.OutOrStdout(), "%s local Session %q for Ticket %q in Workspace %q\n",
		verb, session.ID, session.TicketSlug, session.WorkspaceName)
	return err
}

func toLocalDispatchSessionOutput(session domain.LocalDispatchSession) localDispatchSessionOutput {
	return localDispatchSessionOutput{
		ID: session.ID, Ticket: session.TicketSlug, Project: session.Project, Template: session.TemplateName,
		Mode: session.Mode, Provider: session.Provider, Status: session.Status, Claimant: session.Claimant,
		WorkspaceID: session.WorkspaceID, WorkspaceName: session.WorkspaceName, TmuxSession: session.TmuxSession,
		AgentSessionID: session.AgentSessionID, AgentLabel: session.AgentLabel,
		Error: session.Error, TicketTransitioned: session.TicketTransitioned,
		CreatedAt: session.CreatedAt.Format(time.RFC3339), UpdatedAt: session.UpdatedAt.Format(time.RFC3339),
	}
}
