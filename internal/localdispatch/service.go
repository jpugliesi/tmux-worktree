// Package localdispatch starts and tracks local implementation runs for
// Tickets: one Workspace plus one autonomous implementation Agent Session
// per Ticket, recorded as a durable session so a coordinator can dispatch,
// observe, and recover them like Cursor Cloud Sessions.
package localdispatch

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/jpugliesi/tmux-worktree/internal/agentprovider"
	"github.com/jpugliesi/tmux-worktree/internal/clierr"
	"github.com/jpugliesi/tmux-worktree/internal/domain"
	"github.com/jpugliesi/tmux-worktree/internal/store"
	ticketservice "github.com/jpugliesi/tmux-worktree/internal/ticket"
)

// LaunchRequest asks the launcher for one Workspace that runs one ticket
// agent. The Template already carries the injected agent.
type LaunchRequest struct {
	Name         string
	TemplateName string
	Template     domain.Template
	AgentLabel   string
	Tickets      []string
	Project      string
}

// LaunchResult reports what the launcher created. WorkspaceID is set even on
// a partial failure so the caller can hand the Workspace to a human.
type LaunchResult struct {
	WorkspaceID    string
	WorkspaceName  string
	TmuxSession    string
	AgentSessionID string
	// AgentStarted is false when the agent record was saved without a
	// running pane. Dispatch treats that as a failure: its purpose is a
	// running agent.
	AgentStarted bool
}

// WorkspaceLauncher creates local Workspaces. The CLI implements it; tests
// fake it.
type WorkspaceLauncher interface {
	// Validate rejects a request without changing state. It runs before the
	// ticket claim so a lingering active Workspace never burns a claim.
	Validate(request LaunchRequest) error
	// Launch creates the Workspace and starts the agent. It may return a
	// partial result together with an error.
	Launch(request LaunchRequest) (LaunchResult, error)
}

type ServiceOptions struct {
	StateDir  string
	Templates store.TemplateStore
	Tickets   ticketservice.Store
	// Launcher is required for a real dispatch. Read-only paths (sync,
	// abandon) leave it nil.
	Launcher WorkspaceLauncher
	// Observer is required for Sync. Dispatch and Abandon leave it nil.
	Observer WorkspaceObserver
	// Config is the resolved machine ticketAgent config: the fallback for
	// provider, effort, and instructions when the Template sets none.
	Config store.TicketAgentConfig
	// LookPath resolves provider executables. Nil uses exec.LookPath.
	LookPath func(string) (string, error)
	Now      func() time.Time
	NewID    func() (string, error)
}

type Service struct {
	options  ServiceOptions
	sessions store.LocalDispatchSessionStore
}

func NewService(options ServiceOptions) *Service {
	if options.Now == nil {
		options.Now = time.Now
	}
	if options.NewID == nil {
		options.NewID = randomID
	}
	return &Service{options: options, sessions: store.NewLocalDispatchSessionStore(options.StateDir)}
}

type DispatchOptions struct {
	TicketRef      string
	Mode           domain.CursorCloudMode
	MaxConcurrency int
	DryRun         bool
}

func (s *Service) Dispatch(options DispatchOptions) (domain.LocalDispatchSession, error) {
	if s.options.Tickets == nil {
		return domain.LocalDispatchSession{}, clierr.New(clierr.PreconditionFailed, "local dispatch is not configured")
	}
	if options.Mode != domain.CursorCloudModeAgent && options.Mode != domain.CursorCloudModePlan {
		return domain.LocalDispatchSession{}, clierr.New(clierr.InvalidUsage, "local dispatch mode %q is invalid", options.Mode)
	}
	shown, project, template, err := s.dispatchInputs(options.TicketRef)
	if err != nil {
		return domain.LocalDispatchSession{}, err
	}
	id, err := s.options.NewID()
	if err != nil {
		return domain.LocalDispatchSession{}, fmt.Errorf("create local dispatch Session ID: %w", err)
	}
	if len(id) < 8 {
		return domain.LocalDispatchSession{}, fmt.Errorf("create local dispatch Session ID: ID %q is too short", id)
	}
	claimant := "twt-local-" + id[:8]
	provider := template.LocalDispatch.EffectiveProvider(s.options.Config.Provider)
	if strings.TrimSpace(provider) == "" {
		provider = agentprovider.DefaultTicketPlanningProvider
	}
	effort := template.LocalDispatch.EffectiveEffort(s.options.Config.Effort)
	if strings.TrimSpace(effort) == "" {
		effort = string(agentprovider.DefaultTicketPlanningEffort)
	}
	instructions := template.LocalDispatch.EffectiveInstructions(s.options.Config.Instructions)
	launch, label, err := s.buildLaunch(options.Mode, provider, effort, instructions, shown.Ticket.Slug, claimant)
	if err != nil {
		return domain.LocalDispatchSession{}, err
	}
	home, err := s.options.Tickets.HomePath()
	if err != nil {
		return domain.LocalDispatchSession{}, err
	}
	launchTemplate, label := appendTicketAgent(template, label, launch, []string{"TWT_TICKETS_HOME=" + home})
	now := s.options.Now().UTC()
	session := domain.LocalDispatchSession{
		Version:        domain.LocalDispatchSessionVersion,
		ID:             id,
		TicketSlug:     shown.Ticket.Slug,
		Project:        project.Name,
		TemplateName:   template.Name,
		Mode:           options.Mode,
		Provider:       provider,
		Status:         domain.LocalDispatchCreating,
		Claimant:       claimant,
		PromptSnapshot: launch.Start[len(launch.Start)-1],
		AgentLabel:     label,
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	request := LaunchRequest{
		Name:         shown.Ticket.Slug,
		TemplateName: template.Name,
		Template:     launchTemplate,
		AgentLabel:   label,
		Tickets:      []string{shown.Ticket.Slug},
		Project:      project.Name,
	}
	if s.options.Launcher == nil {
		return domain.LocalDispatchSession{}, clierr.New(clierr.PreconditionFailed, "local dispatch is not configured with a Workspace launcher")
	}
	if options.DryRun {
		if err := s.rejectActiveSession(shown.Ticket.Slug); err != nil {
			return domain.LocalDispatchSession{}, err
		}
		if err := s.checkCapacity(project.Name, template.LocalDispatch.EffectiveMaxConcurrency(), options.MaxConcurrency); err != nil {
			return domain.LocalDispatchSession{}, err
		}
		if err := s.options.Launcher.Validate(request); err != nil {
			return domain.LocalDispatchSession{}, err
		}
		if _, err := s.options.Tickets.ClaimReady(shown.Ticket.Slug, claimant, true); err != nil {
			return domain.LocalDispatchSession{}, err
		}
		return session, nil
	}
	return s.dispatchWithSessionLock(session, request, template.LocalDispatch, options.MaxConcurrency)
}

func (s *Service) buildLaunch(mode domain.CursorCloudMode, provider, effort, instructions, slug, claimant string) (agentprovider.TicketPlanningLaunch, string, error) {
	var launch agentprovider.TicketPlanningLaunch
	var label string
	var err error
	if mode == domain.CursorCloudModePlan {
		label = "ticket-plan"
		launch, err = agentprovider.BuildTicketPlanningLaunch(agentprovider.TicketPlanningRequest{
			Provider:     provider,
			Effort:       agentprovider.TicketPlanningEffort(effort),
			Instructions: instructions,
			Tickets:      []string{slug},
		}, s.options.LookPath)
	} else {
		label = "ticket-impl"
		launch, err = agentprovider.BuildTicketImplementationLaunch(agentprovider.TicketImplementationRequest{
			Provider:     provider,
			Effort:       agentprovider.TicketPlanningEffort(effort),
			Instructions: instructions,
			Ticket:       slug,
			Claimant:     claimant,
		}, s.options.LookPath)
	}
	if err != nil {
		return agentprovider.TicketPlanningLaunch{}, "", clierr.WithHint(
			clierr.New(clierr.PreconditionFailed, "%v", err),
			"Install the provider on this machine, or set ticketAgent.provider or the Template local_dispatch.provider to an installed provider.")
	}
	return launch, label, nil
}

func (s *Service) dispatchWithSessionLock(
	session domain.LocalDispatchSession,
	request LaunchRequest,
	configuration *domain.LocalDispatchSpec,
	maximumOverride int,
) (_ domain.LocalDispatchSession, returnErr error) {
	lock, err := store.AcquireNamedLock(s.options.StateDir, "local-dispatch-session", session.ID)
	if err != nil {
		return session, err
	}
	defer func() {
		returnErr = errors.Join(returnErr, lock.Release())
	}()
	if err := s.reserveDispatch(session, request, configuration.EffectiveMaxConcurrency(), maximumOverride); err != nil {
		return domain.LocalDispatchSession{}, err
	}
	result, launchErr := s.options.Launcher.Launch(request)
	session.WorkspaceID = result.WorkspaceID
	session.WorkspaceName = result.WorkspaceName
	session.TmuxSession = result.TmuxSession
	session.AgentSessionID = result.AgentSessionID
	if launchErr != nil {
		return s.recordDispatchFailure(session, launchErr)
	}
	if !result.AgentStarted {
		return s.recordDispatchFailure(session, clierr.New(clierr.UnsafeState,
			"the Workspace was created but the %q agent did not start", session.AgentLabel))
	}
	session.Status = domain.LocalDispatchRunning
	session.UpdatedAt = s.options.Now().UTC()
	if err := s.sessions.Save(session); err != nil {
		return session, err
	}
	return session, nil
}

func (s *Service) reserveDispatch(session domain.LocalDispatchSession, request LaunchRequest, configured, override int) error {
	lock, err := store.AcquireNamedLock(s.options.StateDir, "local-dispatch-project", session.Project)
	if err != nil {
		if !errors.Is(err, store.ErrLockHeld) {
			return err
		}
		return clierr.WithHint(
			clierr.New(clierr.Locked, "Project %q has another local dispatch in progress", session.Project),
			"Run the queue command again after the other dispatch completes.")
	}
	rollback := func(cause error) error {
		deleteErr := s.sessions.Delete(session.ID)
		_, ticketErr := s.options.Tickets.CompleteClaim(session.TicketSlug, session.Claimant, domain.TicketReadyForAgent, false)
		return errors.Join(cause, deleteErr, ticketErr)
	}
	if err := s.rejectActiveSession(session.TicketSlug); err != nil {
		return errors.Join(err, lock.Release())
	}
	if err := s.checkCapacity(session.Project, configured, override); err != nil {
		return errors.Join(err, lock.Release())
	}
	if err := s.options.Launcher.Validate(request); err != nil {
		return errors.Join(err, lock.Release())
	}
	if err := s.sessions.Save(session); err != nil {
		return errors.Join(err, lock.Release())
	}
	if _, err := s.options.Tickets.ClaimReady(session.TicketSlug, session.Claimant, false); err != nil {
		_ = s.sessions.Delete(session.ID)
		return errors.Join(err, lock.Release())
	}
	if err := lock.Release(); err != nil {
		return rollback(err)
	}
	return nil
}

func (s *Service) checkCapacity(project string, configured, override int) error {
	if override < 0 {
		return clierr.New(clierr.InvalidUsage, "maximum concurrency %d is negative", override)
	}
	maximum := configured
	if override > 0 {
		maximum = override
	}
	sessions, err := s.sessions.List()
	if err != nil {
		return err
	}
	active := 0
	for _, session := range sessions {
		if session.Project == project && session.Active() {
			active++
		}
	}
	if active >= maximum {
		return clierr.WithHint(
			clierr.New(clierr.PreconditionFailed, "Project %q has %d active local dispatch Sessions and a maximum of %d", project, active, maximum),
			"Run 'twt tickets sync --project %s' before you dispatch more Tickets.", project)
	}
	return nil
}

func (s *Service) dispatchInputs(ref string) (ticketservice.ShowResult, domain.Project, domain.Template, error) {
	shown, err := s.options.Tickets.Show(ref)
	if err != nil {
		return ticketservice.ShowResult{}, domain.Project{}, domain.Template{}, err
	}
	if shown.Ticket.Project == "" {
		return ticketservice.ShowResult{}, domain.Project{}, domain.Template{},
			clierr.New(clierr.PreconditionFailed, "ticket %q does not belong to a Project", shown.Ticket.Slug)
	}
	project, err := s.options.Tickets.Project(shown.Ticket.Project)
	if err != nil {
		return ticketservice.ShowResult{}, domain.Project{}, domain.Template{}, err
	}
	if project.TemplateName == "" {
		return ticketservice.ShowResult{}, domain.Project{}, domain.Template{},
			clierr.WithHint(clierr.New(clierr.PreconditionFailed, "Project %q has no Workspace Template", project.Name),
				"Run 'twt projects set %s --template TEMPLATE'.", project.Name)
	}
	template, err := s.options.Templates.Load(project.TemplateName)
	if err != nil {
		return ticketservice.ShowResult{}, domain.Project{}, domain.Template{}, err
	}
	return shown, project, template, nil
}

func (s *Service) rejectActiveSession(ticketSlug string) error {
	sessions, err := s.sessions.List()
	if err != nil {
		return err
	}
	for _, session := range sessions {
		if session.TicketSlug == ticketSlug && session.Active() {
			return clierr.WithHint(
				clierr.New(clierr.Locked, "ticket %q already has active local dispatch Session %q", ticketSlug, session.ID),
				"Run 'twt tickets sync --project %s'.", session.Project)
		}
	}
	return nil
}

// recordDispatchFailure makes the session terminal and returns the Ticket to
// the queue. Local failures are always definite, so the claim never stays
// behind.
func (s *Service) recordDispatchFailure(session domain.LocalDispatchSession, cause error) (domain.LocalDispatchSession, error) {
	now := s.options.Now().UTC()
	session.UpdatedAt = now
	session.Status = domain.LocalDispatchFailed
	session.CompletedAt = &now
	session.Error = &domain.CursorCloudError{Kind: "launch", Message: cause.Error()}
	if err := s.finishTicketTransition(&session, domain.TicketReadyForAgent, false); err != nil {
		return session, errors.Join(cause, err)
	}
	if session.WorkspaceID != "" {
		cause = clierr.WithHint(clierr.Wrap(clierr.CodeOf(cause), cause),
			"A partially created Workspace %q remains. Inspect it, then run 'twt done %s'.", session.WorkspaceID, session.WorkspaceID)
	}
	return session, cause
}

// finishTicketTransition saves the session, completes the claim, and marks
// the transition done. A dry run changes nothing, on disk or in memory.
func (s *Service) finishTicketTransition(session *domain.LocalDispatchSession, target domain.TicketStatus, dryRun bool) error {
	if dryRun {
		return nil
	}
	if err := s.sessions.Save(*session); err != nil {
		return err
	}
	if _, err := s.options.Tickets.CompleteClaim(session.TicketSlug, session.Claimant, target, false); err != nil {
		return err
	}
	session.TicketTransitioned = true
	session.UpdatedAt = s.options.Now().UTC()
	return s.sessions.Save(*session)
}

// appendTicketAgent copies the Template and appends the ticket agent under a
// label that no declared agent uses. The env pairs land in the agent's tmux
// window, so the worker's twt commands see the right Tickets home even when
// the tmux server environment differs.
func appendTicketAgent(template domain.Template, wantLabel string, launch agentprovider.TicketPlanningLaunch, env []string) (domain.Template, string) {
	template.Agents = append([]domain.TemplateAgent(nil), template.Agents...)
	used := make(map[string]bool, len(template.Agents))
	for _, declared := range template.Agents {
		used[declared.Label] = true
	}
	label := wantLabel
	for number := 2; used[label]; number++ {
		label = wantLabel + "-" + strconv.Itoa(number)
	}
	template.Agents = append(template.Agents, domain.TemplateAgent{
		Label: label, Provider: launch.Provider,
		Start: append([]string(nil), launch.Start...), Resume: append([]string(nil), launch.Resume...), PreferProviderResume: true,
		Env: append([]string(nil), env...),
	})
	return template, label
}

func randomID() (string, error) {
	data := make([]byte, 16)
	if _, err := rand.Read(data); err != nil {
		return "", err
	}
	return hex.EncodeToString(data), nil
}
