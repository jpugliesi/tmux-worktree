package cursorcloud

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/jpugliesi/tmux-worktree/internal/clierr"
	"github.com/jpugliesi/tmux-worktree/internal/domain"
	"github.com/jpugliesi/tmux-worktree/internal/store"
	ticketservice "github.com/jpugliesi/tmux-worktree/internal/ticket"
)

type Harness interface {
	Dispatch(context.Context, DispatchRequest) (DispatchResult, error)
	Sync(context.Context, SyncRequest) (SyncResult, error)
}

type ServiceOptions struct {
	StateDir  string
	Templates store.TemplateStore
	Tickets   *ticketservice.Service
	Harness   Harness
	Now       func() time.Time
	NewID     func() (string, error)
}

type Service struct {
	options  ServiceOptions
	sessions store.CursorCloudSessionStore
}

func NewService(options ServiceOptions) *Service {
	if options.Now == nil {
		options.Now = time.Now
	}
	if options.NewID == nil {
		options.NewID = randomID
	}
	return &Service{options: options, sessions: store.NewCursorCloudSessionStore(options.StateDir)}
}

type DispatchOptions struct {
	TicketRef      string
	Mode           domain.CursorCloudMode
	MaxConcurrency int
	DryRun         bool
}

func (s *Service) Dispatch(ctx context.Context, options DispatchOptions) (domain.CursorCloudSession, error) {
	if s.options.Tickets == nil {
		return domain.CursorCloudSession{}, clierr.New(clierr.PreconditionFailed, "Cursor Cloud dispatch is not configured")
	}
	if options.Mode != domain.CursorCloudModeAgent && options.Mode != domain.CursorCloudModePlan {
		return domain.CursorCloudSession{}, clierr.New(clierr.InvalidUsage, "Cursor Cloud mode %q is invalid", options.Mode)
	}
	shown, project, template, repositories, err := s.dispatchInputs(options.TicketRef)
	if err != nil {
		return domain.CursorCloudSession{}, err
	}
	id, err := s.options.NewID()
	if err != nil {
		return domain.CursorCloudSession{}, fmt.Errorf("create Cursor Cloud Session ID: %w", err)
	}
	if len(id) < 8 {
		return domain.CursorCloudSession{}, fmt.Errorf("create Cursor Cloud Session ID: ID %q is too short", id)
	}
	now := s.options.Now().UTC()
	claimant := "cursor-cloud-" + id[:8]
	prompt := buildPrompt(shown, options.Mode, template.CursorCloud.Instructions)
	session := domain.CursorCloudSession{
		Version:              domain.CursorCloudSessionVersion,
		ID:                   id,
		TicketSlug:           shown.Ticket.Slug,
		Project:              project.Name,
		TemplateName:         template.Name,
		TemplateSnapshot:     template,
		Mode:                 options.Mode,
		Status:               domain.CursorCloudCreating,
		Claimant:             claimant,
		PromptSnapshot:       prompt,
		CreateIdempotencyKey: "twt-create-" + id,
		SendIdempotencyKey:   "twt-send-" + id,
		Repositories:         repositories,
		CreatedAt:            now,
		UpdatedAt:            now,
	}
	if options.DryRun {
		if err := s.rejectActiveSession(shown.Ticket.Slug); err != nil {
			return domain.CursorCloudSession{}, err
		}
		if err := s.checkCapacity(project.Name, template.CursorCloud.EffectiveMaxConcurrency(), options.MaxConcurrency); err != nil {
			return domain.CursorCloudSession{}, err
		}
		if _, err := s.options.Tickets.ClaimReady(shown.Ticket.Slug, claimant, true); err != nil {
			return domain.CursorCloudSession{}, err
		}
		return session, nil
	}
	if s.options.Harness == nil {
		return domain.CursorCloudSession{}, clierr.New(clierr.PreconditionFailed, "Cursor Cloud dispatch is not configured")
	}
	return s.dispatchWithSessionLock(ctx, session, template.CursorCloud, options.MaxConcurrency)
}

func (s *Service) dispatchWithSessionLock(
	ctx context.Context,
	session domain.CursorCloudSession,
	configuration *domain.CursorCloudSpec,
	maximumOverride int,
) (_ domain.CursorCloudSession, returnErr error) {
	lock, err := store.AcquireNamedLock(s.options.StateDir, "cursor-cloud-session", session.ID)
	if err != nil {
		return session, err
	}
	defer func() {
		returnErr = errors.Join(returnErr, lock.Release())
	}()
	if err := s.reserveDispatch(session, configuration.EffectiveMaxConcurrency(), maximumOverride); err != nil {
		return domain.CursorCloudSession{}, err
	}
	result, dispatchErr := s.options.Harness.Dispatch(ctx, DispatchRequest{
		SessionID:            session.ID,
		TicketSlug:           session.TicketSlug,
		Project:              session.Project,
		Mode:                 string(session.Mode),
		Prompt:               session.PromptSnapshot,
		Model:                configuration.Model,
		Effort:               configuration.EffectiveEffort(),
		CreateIdempotencyKey: session.CreateIdempotencyKey,
		SendIdempotencyKey:   session.SendIdempotencyKey,
		Repositories:         harnessRepositories(session.Repositories),
	})
	if dispatchErr != nil {
		return s.recordDispatchFailure(session, dispatchErr)
	}
	now := s.options.Now().UTC()
	session.Status = domain.CursorCloudRunning
	session.CursorAgentID = result.AgentID
	session.RunID = result.RunID
	session.RequestID = result.RequestID
	session.EffectiveEffort = domain.CursorCloudEffort{Kind: result.Effort.Kind, Value: result.Effort.Value}
	session.RunHistory = append(session.RunHistory, domain.CursorCloudRun{
		ID: result.RunID, RequestID: result.RequestID, Status: domain.CursorCloudRunning, CreatedAt: now,
	})
	session.UpdatedAt = s.options.Now().UTC()
	if err := s.sessions.Save(session); err != nil {
		return session, err
	}
	return session, nil
}

func (s *Service) reserveDispatch(session domain.CursorCloudSession, configured, override int) error {
	lock, err := store.AcquireNamedLock(s.options.StateDir, "cursor-cloud-project", session.Project)
	if err != nil {
		if !errors.Is(err, store.ErrLockHeld) {
			return err
		}
		return clierr.WithHint(
			clierr.New(clierr.Locked, "Project %q has another Cursor Cloud dispatch in progress", session.Project),
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
			clierr.New(clierr.PreconditionFailed, "Project %q has %d active Cursor Cloud Sessions and a maximum of %d", project, active, maximum),
			"Run 'twt tickets cloud-sync --project %s' before you dispatch more Tickets.", project)
	}
	return nil
}

func (s *Service) dispatchInputs(ref string) (ticketservice.ShowResult, domain.Project, domain.Template, []domain.CursorCloudRepository, error) {
	shown, err := s.options.Tickets.Show(ref)
	if err != nil {
		return ticketservice.ShowResult{}, domain.Project{}, domain.Template{}, nil, err
	}
	if shown.Ticket.Project == "" {
		return ticketservice.ShowResult{}, domain.Project{}, domain.Template{}, nil,
			clierr.New(clierr.PreconditionFailed, "ticket %q does not belong to a Project", shown.Ticket.Slug)
	}
	project, err := s.options.Tickets.Project(shown.Ticket.Project)
	if err != nil {
		return ticketservice.ShowResult{}, domain.Project{}, domain.Template{}, nil, err
	}
	if project.TemplateName == "" {
		return ticketservice.ShowResult{}, domain.Project{}, domain.Template{}, nil,
			clierr.WithHint(clierr.New(clierr.PreconditionFailed, "Project %q has no Workspace Template", project.Name),
				"Run 'twt projects set %s --template TEMPLATE'.", project.Name)
	}
	template, err := s.options.Templates.Load(project.TemplateName)
	if err != nil {
		return ticketservice.ShowResult{}, domain.Project{}, domain.Template{}, nil, err
	}
	if template.CursorCloud == nil {
		return ticketservice.ShowResult{}, domain.Project{}, domain.Template{}, nil,
			clierr.New(clierr.PreconditionFailed, "Workspace Template %q has no cursor_cloud configuration", template.Name)
	}
	repositories, err := resolveRepositories(template)
	if err != nil {
		return ticketservice.ShowResult{}, domain.Project{}, domain.Template{}, nil, err
	}
	return shown, project, template, repositories, nil
}

func (s *Service) rejectActiveSession(ticketSlug string) error {
	sessions, err := s.sessions.List()
	if err != nil {
		return err
	}
	for _, session := range sessions {
		if session.TicketSlug == ticketSlug && session.Active() {
			return clierr.WithHint(
				clierr.New(clierr.Locked, "ticket %q already has active Cursor Cloud Session %q", ticketSlug, session.ID),
				"Run 'twt tickets cloud-sync --project %s'.", session.Project)
		}
	}
	return nil
}

func (s *Service) recordDispatchFailure(session domain.CursorCloudSession, dispatchErr error) (domain.CursorCloudSession, error) {
	now := s.options.Now().UTC()
	session.UpdatedAt = now
	session.Error = domainError(dispatchErr)
	var cloudErr *Error
	if !errors.As(dispatchErr, &cloudErr) || cloudErr.Uncertain() {
		session.Status = domain.CursorCloudCreatingUnknown
		if err := s.sessions.Save(session); err != nil {
			return session, errors.Join(dispatchErr, err)
		}
		return session, dispatchErr
	}
	session.Status = domain.CursorCloudFailed
	session.CompletedAt = &now
	if err := s.finishTicketTransition(&session, domain.TicketReadyForAgent, false); err != nil {
		return session, errors.Join(dispatchErr, err)
	}
	return session, dispatchErr
}

type CloudSyncResult struct {
	Project     string                      `json:"project"`
	Capacity    CloudCapacity               `json:"capacity"`
	Sessions    []domain.CursorCloudSession `json:"sessions"`
	Diagnostics []SyncDiagnostic            `json:"diagnostics,omitempty"`
}

type CloudCapacity struct {
	Maximum   int  `json:"maximum"`
	Active    int  `json:"active"`
	Available int  `json:"available"`
	Known     bool `json:"known"`
}

type SyncDiagnostic struct {
	SessionID string `json:"sessionId"`
	Ticket    string `json:"ticket"`
	Code      string `json:"code"`
	Message   string `json:"message"`
	Hint      string `json:"hint,omitempty"`
}

func (s *Service) Sync(ctx context.Context, project string, dryRun bool) (CloudSyncResult, error) {
	if s.options.Tickets == nil || s.options.Harness == nil {
		return CloudSyncResult{}, clierr.New(clierr.PreconditionFailed, "Cursor Cloud sync is not configured")
	}
	maximum, err := s.projectMaxConcurrency(project)
	if err != nil {
		return CloudSyncResult{}, err
	}
	all, err := s.sessions.List()
	if err != nil {
		return CloudSyncResult{}, err
	}
	pending := make([]domain.CursorCloudSession, 0)
	for _, session := range all {
		if session.Project == project && (session.Active() || !session.TicketTransitioned) {
			pending = append(pending, session)
		}
	}
	result := CloudSyncResult{Project: project, Sessions: make([]domain.CursorCloudSession, 0, len(pending))}
	remote := make([]domain.CursorCloudSession, 0, len(pending))
	for _, session := range pending {
		if !session.Active() {
			updated, updateErr := s.updateSavedSession(session.ID, func(current *domain.CursorCloudSession) error {
				if current.Active() || current.TicketTransitioned {
					return nil
				}
				return s.reconcileTerminal(current, dryRun)
			})
			if updated.ID != "" {
				session = updated
			}
			if updateErr != nil {
				result.Diagnostics = append(result.Diagnostics, syncDiagnostic(session, updateErr))
			}
			result.Sessions = append(result.Sessions, session)
			continue
		}
		remote = append(remote, session)
	}
	if len(remote) == 0 {
		setCloudCapacity(&result, maximum)
		return result, nil
	}
	references := make([]SessionReference, 0, len(remote))
	for _, session := range remote {
		references = append(references, SessionReference{SessionID: session.ID, AgentID: session.CursorAgentID, RunID: session.RunID})
	}
	observed, err := s.options.Harness.Sync(ctx, SyncRequest{Sessions: references})
	if err != nil {
		return result, err
	}
	byID := make(map[string]SyncObservation, len(observed.Sessions))
	for _, observation := range observed.Sessions {
		byID[observation.SessionID] = observation
	}
	for _, session := range remote {
		observation, found := byID[session.ID]
		if !found {
			observation = SyncObservation{SessionID: session.ID, Status: "unknown", Error: &Error{Kind: "unknown", Message: "Cursor sync returned no observation"}}
		}
		updated, updateErr := s.updateSavedSession(session.ID, func(current *domain.CursorCloudSession) error {
			if !current.Active() {
				if current.TicketTransitioned {
					return nil
				}
				return s.reconcileTerminal(current, dryRun)
			}
			return s.applyObservation(current, observation, dryRun)
		})
		if updated.ID != "" {
			session = updated
		}
		if updateErr != nil {
			result.Diagnostics = append(result.Diagnostics, syncDiagnostic(session, updateErr))
		}
		result.Sessions = append(result.Sessions, session)
	}
	sort.Slice(result.Sessions, func(i, j int) bool { return result.Sessions[i].ID < result.Sessions[j].ID })
	sort.Slice(result.Diagnostics, func(i, j int) bool { return result.Diagnostics[i].SessionID < result.Diagnostics[j].SessionID })
	setCloudCapacity(&result, maximum)
	return result, nil
}

func (s *Service) projectMaxConcurrency(projectName string) (int, error) {
	project, err := s.options.Tickets.Project(projectName)
	if err != nil {
		return 0, err
	}
	if project.TemplateName == "" {
		return 0, clierr.New(clierr.PreconditionFailed, "Project %q has no Workspace Template", projectName)
	}
	template, err := s.options.Templates.Load(project.TemplateName)
	if err != nil {
		return 0, err
	}
	if template.CursorCloud == nil {
		return 0, clierr.New(clierr.PreconditionFailed, "Workspace Template %q has no cursor_cloud configuration", template.Name)
	}
	return template.CursorCloud.EffectiveMaxConcurrency(), nil
}

func setCloudCapacity(result *CloudSyncResult, maximum int) {
	active := 0
	for _, session := range result.Sessions {
		if session.Active() {
			active++
		}
	}
	available := maximum - active
	if available < 0 {
		available = 0
	}
	known := len(result.Diagnostics) == 0
	if !known {
		available = 0
	}
	result.Capacity = CloudCapacity{Maximum: maximum, Active: active, Available: available, Known: known}
}

func syncDiagnostic(session domain.CursorCloudSession, err error) SyncDiagnostic {
	return SyncDiagnostic{
		SessionID: session.ID,
		Ticket:    session.TicketSlug,
		Code:      string(clierr.CodeOf(err)),
		Message:   err.Error(),
		Hint:      clierr.HintOf(err),
	}
}

func (s *Service) updateSavedSession(
	id string,
	update func(*domain.CursorCloudSession) error,
) (session domain.CursorCloudSession, returnErr error) {
	lock, err := store.AcquireNamedLock(s.options.StateDir, "cursor-cloud-session", id)
	if err != nil {
		return domain.CursorCloudSession{}, err
	}
	defer func() {
		returnErr = errors.Join(returnErr, lock.Release())
	}()
	session, err = s.sessions.Find(id)
	if err != nil {
		return session, err
	}
	if err := update(&session); err != nil {
		return session, err
	}
	return session, nil
}

// Abandon makes one local Cloud Session terminal and stops future Ticket
// reconciliation. It releases the Ticket only when the saved Cloud claimant
// still owns it. A different claim is user state and stays unchanged.
func (s *Service) Abandon(reference string, dryRun bool) (domain.CursorCloudSession, error) {
	if s.options.Tickets == nil {
		return domain.CursorCloudSession{}, clierr.New(clierr.PreconditionFailed, "Cursor Cloud recovery is not configured")
	}
	matched, err := s.sessions.Find(reference)
	if err != nil {
		return domain.CursorCloudSession{}, err
	}
	return s.updateSavedSession(matched.ID, func(session *domain.CursorCloudSession) error {
		if session.TicketTransitioned {
			return clierr.New(clierr.PreconditionFailed,
				"Cursor Cloud Session %q has no pending Ticket transition", session.ID)
		}
		now := s.options.Now().UTC()
		if session.Active() {
			session.Status = domain.CursorCloudCancelled
			session.CompletedAt = &now
		}
		session.Error = &domain.CursorCloudError{
			Kind: "abandoned", Message: "The operator abandoned local Cursor Cloud Session recovery.",
		}
		session.UpdatedAt = now
		if !dryRun {
			if err := s.sessions.Save(*session); err != nil {
				return err
			}
		}
		ticket, resolveErr := s.options.Tickets.Resolve(session.TicketSlug)
		if resolveErr != nil && clierr.CodeOf(resolveErr) != clierr.NotFound {
			return resolveErr
		}
		if resolveErr == nil && ticket.ClaimedBy == session.Claimant {
			if _, err := s.options.Tickets.CompleteClaim(
				session.TicketSlug, session.Claimant, domain.TicketReadyForAgent, dryRun,
			); err != nil {
				return err
			}
		}
		session.TicketTransitioned = true
		session.UpdatedAt = s.options.Now().UTC()
		if !dryRun {
			return s.sessions.Save(*session)
		}
		return nil
	})
}

func (s *Service) applyObservation(session *domain.CursorCloudSession, observation SyncObservation, dryRun bool) error {
	now := s.options.Now().UTC()
	session.UpdatedAt = now
	if observation.RequestID != "" {
		session.RequestID = observation.RequestID
	}
	if observation.AgentID != "" {
		session.CursorAgentID = observation.AgentID
	}
	if observation.RunID != "" {
		session.RunID = observation.RunID
	}
	session.Result = observation.Result
	session.Error = domainError(observation.Error)
	mergeRepositoryResults(session, observation.Repositories)
	switch observation.Status {
	case "running":
		session.Status = domain.CursorCloudRunning
		updateRunHistory(session, domain.CursorCloudRunning)
		if !dryRun {
			return s.sessions.Save(*session)
		}
		return nil
	case "unknown":
		session.Status = domain.CursorCloudRunUnknown
		updateRunHistory(session, domain.CursorCloudRunUnknown)
		if !dryRun {
			return s.sessions.Save(*session)
		}
		return nil
	case "finished":
		session.Status = domain.CursorCloudFinished
		session.CompletedAt = &now
		if session.Mode == domain.CursorCloudModeAgent && hasRepositoryWithoutPullRequest(session.Repositories) {
			session.HandoffIncomplete = true
		}
		updateRunHistory(session, domain.CursorCloudFinished)
		return s.finishTicketTransition(session, domain.TicketReadyForHuman, dryRun)
	case "error":
		session.Status = domain.CursorCloudFailed
		session.CompletedAt = &now
		updateRunHistory(session, domain.CursorCloudFailed)
		return s.finishTicketTransition(session, domain.TicketReadyForAgent, dryRun)
	case "cancelled":
		session.Status = domain.CursorCloudCancelled
		session.CompletedAt = &now
		updateRunHistory(session, domain.CursorCloudCancelled)
		return s.finishTicketTransition(session, domain.TicketReadyForAgent, dryRun)
	default:
		return fmt.Errorf("Cursor sync status %q is invalid", observation.Status)
	}
}

func (s *Service) reconcileTerminal(session *domain.CursorCloudSession, dryRun bool) error {
	target := domain.TicketReadyForHuman
	if session.Status == domain.CursorCloudFailed || session.Status == domain.CursorCloudCancelled {
		target = domain.TicketReadyForAgent
	}
	return s.finishTicketTransition(session, target, dryRun)
}

func (s *Service) finishTicketTransition(session *domain.CursorCloudSession, target domain.TicketStatus, dryRun bool) error {
	if dryRun {
		// A dry run validates the claim completion and changes nothing, on
		// disk or in memory.
		_, err := s.options.Tickets.CompleteWork(session.TicketSlug, session.Claimant, target, pullRequestURLs(session.Repositories), true)
		return err
	}
	if err := s.sessions.Save(*session); err != nil {
		return err
	}
	// Record the pull requests on the Ticket in the same write that
	// completes the claim, so the coordinator can read them without the
	// session store. A failed run can also have opened pull requests;
	// recording them is strictly more information.
	if _, err := s.options.Tickets.CompleteWork(session.TicketSlug, session.Claimant, target, pullRequestURLs(session.Repositories), false); err != nil {
		return err
	}
	session.TicketTransitioned = true
	session.UpdatedAt = s.options.Now().UTC()
	return s.sessions.Save(*session)
}

// pullRequestURLs collects the non-empty pull request URLs of a session.
func pullRequestURLs(repositories []domain.CursorCloudRepository) []string {
	urls := make([]string, 0, len(repositories))
	for _, repository := range repositories {
		if strings.TrimSpace(repository.PRURL) != "" {
			urls = append(urls, repository.PRURL)
		}
	}
	return urls
}

func resolveRepositories(template domain.Template) ([]domain.CursorCloudRepository, error) {
	byName := make(map[string]domain.RepositorySpec, len(template.Repositories))
	for _, repository := range template.Repositories {
		byName[repository.Name] = repository
	}
	selected := template.CursorCloud.Repositories
	if len(selected) == 0 {
		selected = make([]domain.CursorCloudRepositorySpec, 0, len(template.Repositories))
		for _, repository := range template.Repositories {
			selected = append(selected, domain.CursorCloudRepositorySpec{Name: repository.Name})
		}
	}
	result := make([]domain.CursorCloudRepository, 0, len(selected))
	for _, cloudRepository := range selected {
		repository := byName[cloudRepository.Name]
		repositoryURL := repository.Clone.URL
		if cloudRepository.URL != "" {
			repositoryURL = cloudRepository.URL
		}
		if err := validateCloudRepositoryURL(repositoryURL); err != nil {
			return nil, clierr.WithHint(
				clierr.New(clierr.PreconditionFailed, "Cursor Cloud repository %q: %v", cloudRepository.Name, err),
				"Set cursor_cloud.repositories[].url to the HTTPS URL of a repository connected to Cursor.")
		}
		startingRef := repository.DefaultBranch
		if cloudRepository.StartingRef != "" {
			startingRef = cloudRepository.StartingRef
		}
		result = append(result, domain.CursorCloudRepository{
			Name: cloudRepository.Name, URL: repositoryURL, StartingRef: startingRef,
		})
	}
	return result, nil
}

func validateCloudRepositoryURL(value string) error {
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" {
		return fmt.Errorf("URL %q is not an HTTPS repository URL", value)
	}
	return nil
}

func buildPrompt(shown ticketservice.ShowResult, mode domain.CursorCloudMode, instructions string) string {
	task := fmt.Sprintf("Implement twt Ticket %s. Run the relevant tests and create a pull request for each repository that changes.", shown.Ticket.Slug)
	if mode == domain.CursorCloudModePlan {
		task = fmt.Sprintf("Create a plan to implement twt Ticket %s.", shown.Ticket.Slug)
	}
	blockers := "None"
	if len(shown.Ticket.BlockedBy) > 0 {
		blockers = strings.Join(shown.Ticket.BlockedBy, ", ")
	}
	parts := []string{}
	if text := strings.TrimSpace(instructions); text != "" {
		parts = append(parts, text)
	}
	parts = append(parts, task, fmt.Sprintf(
		"Ticket title: %s\nProject: %s\nPriority: p%d\nStatus: %s\nBlocked by: %s\n\nTicket body:\n%s",
		shown.Ticket.Title, shown.Ticket.Project, shown.Ticket.Priority, shown.Ticket.Status, blockers, strings.TrimSpace(shown.Body)))
	return strings.Join(parts, "\n\n")
}

func harnessRepositories(repositories []domain.CursorCloudRepository) []Repository {
	result := make([]Repository, 0, len(repositories))
	for _, repository := range repositories {
		result = append(result, Repository{Name: repository.Name, URL: repository.URL, StartingRef: repository.StartingRef})
	}
	return result
}

func mergeRepositoryResults(session *domain.CursorCloudSession, results []RepositoryResult) {
	for _, result := range results {
		matched := false
		for index := range session.Repositories {
			if canonicalRepositoryURL(session.Repositories[index].URL) == canonicalRepositoryURL(result.URL) {
				session.Repositories[index].Branch = result.Branch
				session.Repositories[index].PRURL = result.PRURL
				matched = true
				break
			}
		}
		if !matched {
			session.Repositories = append(session.Repositories, domain.CursorCloudRepository{URL: result.URL, Branch: result.Branch, PRURL: result.PRURL})
		}
	}
}

func canonicalRepositoryURL(value string) string {
	return strings.TrimSuffix(strings.TrimSuffix(strings.ToLower(strings.TrimSpace(value)), "/"), ".git")
}

func hasRepositoryWithoutPullRequest(repositories []domain.CursorCloudRepository) bool {
	for _, repository := range repositories {
		if repository.Branch != "" && repository.PRURL == "" {
			return true
		}
	}
	return false
}

func updateRunHistory(session *domain.CursorCloudSession, status domain.CursorCloudStatus) {
	for index := range session.RunHistory {
		if session.RunHistory[index].ID == session.RunID {
			session.RunHistory[index].Status = status
			return
		}
	}
}

func domainError(err error) *domain.CursorCloudError {
	if err == nil {
		return nil
	}
	var cloudErr *Error
	if errors.As(err, &cloudErr) {
		if cloudErr == nil {
			return nil
		}
		return &domain.CursorCloudError{
			Kind: cloudErr.Kind, Code: cloudErr.Code, Message: cloudErr.Message, Retryable: cloudErr.Retryable,
			HelpURL: cloudErr.HelpURL, RequestID: cloudErr.RequestID,
		}
	}
	return &domain.CursorCloudError{Kind: "unknown", Message: err.Error()}
}

func randomID() (string, error) {
	data := make([]byte, 16)
	if _, err := rand.Read(data); err != nil {
		return "", err
	}
	return hex.EncodeToString(data), nil
}
