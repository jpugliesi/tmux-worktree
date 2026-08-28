package workspace

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/jpugliesi/tmux-worktree/internal/agent"
	"github.com/jpugliesi/tmux-worktree/internal/clierr"
	"github.com/jpugliesi/tmux-worktree/internal/domain"
	"github.com/jpugliesi/tmux-worktree/internal/store"
)

type Options struct {
	StateDir   string
	DataDir    string
	TmuxSocket string
	// Progress receives one short user-facing message for each long step.
	// A nil value turns progress reporting off.
	Progress func(message string)
	// AfterClaimReserved runs after a Prepared Environment claim reservation
	// is saved and after the mutation lock is released. The CLI uses it to
	// start the background pool refill.
	AfterClaimReserved func()
	// AfterReleaseFinalized runs after a released Prepared Environment is
	// saved as ready again. The CLI uses it to top up the pool, which
	// replaces environments that failed or no longer match the Workspace
	// Template.
	AfterReleaseFinalized func(templateName string)
}

type Service struct {
	options      Options
	store        store.WorkspaceStore
	environments store.EnvironmentStore
	snapshots    store.SnapshotStore
	now          func() time.Time
	// pendingReleaseRefills holds one Workspace Template name for each
	// finalized release whose AfterReleaseFinalized hook has not run yet.
	// The hook takes the mutation lock, so it cannot run inside the locked
	// release and reconcile sections.
	pendingReleaseRefills []string
}

func NewService(options Options) *Service {
	return &Service{
		options:      options,
		store:        store.NewWorkspaceStore(options.StateDir),
		environments: store.NewEnvironmentStore(options.StateDir),
		snapshots:    store.NewSnapshotStore(options.StateDir),
		now:          func() time.Time { return time.Now().UTC() },
	}
}

// report sends one progress message when the service has a Progress function.
func (s *Service) report(format string, a ...any) {
	if s.options.Progress == nil {
		return
	}
	s.options.Progress(fmt.Sprintf(format, a...))
}

func (s *Service) Create(name, templateName string, template domain.Template) (domain.Workspace, error) {
	return s.CreateWithOptions(name, templateName, template, CreateOptions{})
}

func (s *Service) ValidateCreate(name, templateName string, template domain.Template) error {
	if err := store.ValidateResourceName(name); err != nil {
		return fmt.Errorf("invalid Workspace name: %w", err)
	}
	if err := template.Validate(); err != nil {
		return fmt.Errorf("invalid Workspace Template %q: %w", templateName, err)
	}
	if len(template.Repositories) == 0 {
		return fmt.Errorf("Workspace Template %q has no repositories", templateName)
	}
	workspaces, err := s.store.List()
	if err != nil {
		return err
	}
	for _, existing := range workspaces {
		if existing.Name == name {
			return clierr.New(clierr.AlreadyExists, "Workspace %q already exists", name)
		}
	}
	return nil
}

// ValidateCreateWithOptions also validates the branch selection, so a dry
// run refuses exactly what the real create refuses. The placeholder ID only
// feeds the {id8} token.
func (s *Service) ValidateCreateWithOptions(name, templateName string, template domain.Template, opts CreateOptions) error {
	if err := s.ValidateCreate(name, templateName, template); err != nil {
		return err
	}
	if err := s.validateTicketLinks(opts); err != nil {
		return err
	}
	_, err := s.validateBranchSelection(name, "00000000", template, opts)
	return err
}

func (s *Service) validateTicketLinks(opts CreateOptions) error {
	if len(opts.Tickets) == 0 {
		if opts.Project != "" {
			return clierr.New(clierr.InvalidUsage, "a Workspace without Tickets cannot name Project %q", opts.Project)
		}
		return nil
	}
	if opts.Project == "" {
		return clierr.New(clierr.InvalidUsage, "a Workspace with Tickets must name their Project")
	}
	seen := map[string]bool{}
	for _, slug := range opts.Tickets {
		if slug == "" {
			return clierr.New(clierr.InvalidUsage, "a Workspace Ticket slug is empty")
		}
		if seen[slug] {
			return clierr.New(clierr.InvalidUsage, "Ticket %q is linked more than once", slug)
		}
		seen[slug] = true
	}
	workspaces, err := s.store.List()
	if err != nil {
		return err
	}
	for _, workspace := range workspaces {
		if workspace.Status != domain.WorkspaceActive {
			continue
		}
		for _, slug := range workspace.Tickets {
			if seen[slug] {
				return clierr.WithHint(
					clierr.New(clierr.Locked, "Ticket %q is linked to active Workspace %q", slug, workspace.Name),
					"Archive or remove Workspace %q before you start another Workspace for this Ticket.", workspace.Name)
			}
		}
	}
	return nil
}

func (s *Service) List() ([]domain.Workspace, error) { return s.store.List() }

func (s *Service) Find(reference string) (domain.Workspace, error) { return s.store.Find(reference) }

// ErrNotInWorkspace marks a tmux context that is not inside a twt Workspace
// session. Callers can branch on it with errors.Is.
var ErrNotInWorkspace = errors.New("the tmux pane is not in a twt Workspace session")

type notInWorkspaceError struct{ message string }

func (e notInWorkspaceError) Error() string { return e.message }

func (e notInWorkspaceError) Is(target error) bool { return target == ErrNotInWorkspace }

// CurrentForQuickCreate resolves the current Workspace for quick create. It
// uses the tmux pane of the caller, then the workspace ID value, then the
// current directory. Quick create needs an active Workspace. When the caller
// runs inside the tmux session that the Workspace owns, that session must be
// the only session of the Workspace, because quick create switches the calling
// tmux client and then archives the Workspace.
func (s *Service) CurrentForQuickCreate(directory, workspaceID, tmuxPane string) (domain.Workspace, error) {
	p, sessionID, err := s.workspaceForPane(tmuxPane)
	if err != nil {
		return domain.Workspace{}, err
	}
	if sessionID == "" {
		p, err = s.Current(directory, workspaceID, "")
		if err != nil {
			return domain.Workspace{}, notInWorkspaceError{message: "run this command inside a twt Workspace worktree or tmux session"}
		}
	}
	if p.Status != domain.WorkspaceActive {
		return domain.Workspace{}, fmt.Errorf("Workspace %q has status %q; next requires status %q", p.Name, p.Status, domain.WorkspaceActive)
	}
	if sessionID == "" {
		return p, nil
	}
	sessions, err := s.ownedSessions(p.ID)
	if err != nil {
		return domain.Workspace{}, err
	}
	if len(sessions) != 1 || sessions[0] != sessionID {
		return domain.Workspace{}, fmt.Errorf("tmux session %q is not the unique session for Workspace %q", sessionID, p.Name)
	}
	return p, nil
}

// workspaceForPane returns the Workspace of the tmux session that owns the pane,
// with that session ID. An empty session ID means that the pane gives no
// Workspace, and the caller must use another part of the context chain.
func (s *Service) workspaceForPane(tmuxPane string) (domain.Workspace, string, error) {
	if tmuxPane == "" {
		return domain.Workspace{}, "", nil
	}
	sessionID, err := output("", "tmux", s.tmuxArgs("display-message", "-p", "-t", tmuxPane, "#{session_id}")...)
	if err != nil || sessionID == "" {
		return domain.Workspace{}, "", nil
	}
	workspaceID, err := s.tmuxWorkspaceID(sessionID)
	if err != nil || workspaceID == "" {
		return domain.Workspace{}, "", nil
	}
	p, err := s.store.Find(workspaceID)
	if err != nil {
		return domain.Workspace{}, "", err
	}
	if p.ID != workspaceID {
		return domain.Workspace{}, "", fmt.Errorf("tmux session %q does not contain an immutable Workspace ID", sessionID)
	}
	return p, sessionID, nil
}

func (s *Service) Current(directory, workspaceID, tmuxPane string) (domain.Workspace, error) {
	if workspaceID != "" {
		return s.store.Find(workspaceID)
	}
	if tmuxPane != "" {
		sessionID, err := output("", "tmux", s.tmuxArgs("display-message", "-p", "-t", tmuxPane, "#{session_id}")...)
		if err == nil {
			id, optionErr := s.tmuxWorkspaceID(sessionID)
			if optionErr == nil && id != "" {
				return s.store.Find(id)
			}
		}
	}
	return s.FindByDirectory(directory)
}

// FindByDirectory finds the Workspace that contains the directory. It matches
// the ownership marker, the Workspace root, or any repository path. Adopted
// Workspaces use repository paths, because their root is only the first pane.
func (s *Service) FindByDirectory(directory string) (domain.Workspace, error) {
	absDirectory, err := filepath.Abs(directory)
	if err != nil {
		return domain.Workspace{}, fmt.Errorf("resolve current directory: %w", err)
	}
	if workspace, ok := s.workspaceFromOwnershipMarker(absDirectory); ok {
		return workspace, nil
	}
	workspaces, err := s.store.List()
	if err != nil {
		return domain.Workspace{}, err
	}
	for _, p := range workspaces {
		if workspaceContainsDirectory(p, absDirectory) {
			return p, nil
		}
	}
	return domain.Workspace{}, fmt.Errorf("the current directory or tmux pane is not in a twt Workspace")
}

func (s *Service) workspaceFromOwnershipMarker(directory string) (domain.Workspace, bool) {
	for dir := directory; ; {
		workspaceID, ok := readOwnershipWorkspaceID(dir)
		if ok {
			workspace, err := s.store.Find(workspaceID)
			if err == nil && workspaceContainsDirectory(workspace, directory) {
				return workspace, true
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return domain.Workspace{}, false
		}
		dir = parent
	}
}

func workspaceContainsDirectory(workspace domain.Workspace, directory string) bool {
	if directoryInside(workspace.Root, directory) {
		return true
	}
	for _, repository := range workspace.Repositories {
		if directoryInside(repository.Path, directory) {
			return true
		}
	}
	return false
}

func directoryInside(root, directory string) bool {
	if root == "" {
		return false
	}
	relative, err := filepath.Rel(root, directory)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func (s *Service) Retry(reference string) (domain.Workspace, error) {
	reserved, findErr := s.store.Find(reference)
	if findErr != nil {
		var restored bool
		var restoreErr error
		reserved, restored, restoreErr = s.restoreReservedWorkspace(reference)
		if restoreErr != nil {
			return domain.Workspace{}, restoreErr
		}
		if restored {
			findErr = nil
		}
	}
	if findErr == nil && reserved.EnvironmentID != "" {
		environment, environmentErr := s.environments.Find(reserved.EnvironmentID)
		if environmentErr == nil && environment.Status == domain.EnvironmentClaiming {
			return s.completeEnvironmentClaim(environment.ID, reserved.ID, CreateOptions{})
		}
	}
	lock, err := store.AcquireMutationLock(s.options.StateDir)
	if err != nil {
		return domain.Workspace{}, err
	}
	defer lock.Release()
	p, err := s.validateRetry(reference)
	if err != nil {
		return domain.Workspace{}, err
	}
	for index := range p.Steps {
		if p.Steps[index].Status == domain.StepRunning {
			p.Steps[index].Status = domain.StepUnknown
			p.Steps[index].Error = "the earlier process stopped while this step was running"
		}
		if p.Steps[index].Status == domain.StepFailed || p.Steps[index].Status == domain.StepUnknown {
			p.Steps[index].Status = domain.StepPending
			p.Steps[index].Error = ""
		}
	}
	p.Status = domain.WorkspaceInitializing
	if err := s.store.Save(p); err != nil {
		return p, err
	}
	if err := s.runPending(&p); err != nil {
		return p, err
	}
	return p, nil
}

// restoreReservedWorkspace repairs the durable boundary between an Environment
// claim reservation and its Workspace record. The reservation is the journal of
// record and contains the complete immutable Workspace.
func (s *Service) restoreReservedWorkspace(reference string) (domain.Workspace, bool, error) {
	lock, err := store.AcquireMutationLock(s.options.StateDir)
	if err != nil {
		return domain.Workspace{}, false, err
	}
	defer lock.Release()
	environments, err := s.environments.List()
	if err != nil {
		return domain.Workspace{}, false, err
	}
	var match *domain.Workspace
	for _, environment := range environments {
		if environment.Status != domain.EnvironmentClaiming || environment.Assignment == nil {
			continue
		}
		workspace := environment.Assignment.Workspace
		if workspace.ID != reference && workspace.Name != reference {
			continue
		}
		if match != nil {
			return domain.Workspace{}, false, fmt.Errorf("Workspace claim %q is ambiguous; use a Workspace ID", reference)
		}
		copy := workspace
		match = &copy
	}
	if match == nil {
		return domain.Workspace{}, false, nil
	}
	if err := s.store.Save(*match); err != nil {
		return domain.Workspace{}, false, fmt.Errorf("restore Workspace %q from its Prepared Environment claim: %w", match.Name, err)
	}
	return *match, true, nil
}

func (s *Service) ValidateRetry(reference string) error {
	_, err := s.validateRetry(reference)
	return err
}

func (s *Service) validateRetry(reference string) (domain.Workspace, error) {
	p, err := s.store.Find(reference)
	if err != nil {
		return p, err
	}
	if p.Status == domain.WorkspaceRemoving {
		return p, clierr.New(clierr.PreconditionFailed, "Workspace %q removal is in progress; run twt workspaces remove %s --apply", p.Name, p.ID)
	}
	if p.Status == domain.WorkspaceArchived {
		return p, clierr.New(clierr.PreconditionFailed, "Workspace %q is archived; run twt workspaces open %s", p.Name, p.ID)
	}
	return p, nil
}

func (s *Service) Open(reference string) (domain.Workspace, error) {
	if err := s.Reconcile(); err != nil {
		return domain.Workspace{}, err
	}
	p, err := s.validateOpen(reference)
	if err != nil {
		return domain.Workspace{}, err
	}
	if p.Status == domain.WorkspaceArchived && !p.Adopted && !p.Materialized {
		return s.openReleasedWorkspace(p)
	}
	lock, err := store.AcquireMutationLock(s.options.StateDir)
	if err != nil {
		return domain.Workspace{}, err
	}
	defer lock.Release()
	p, err = s.validateOpen(reference)
	if err != nil {
		return domain.Workspace{}, err
	}
	if p.Materialized && p.EnvironmentID != "" {
		if err := s.restoreBoundWorkspace(&p); err != nil {
			return p, err
		}
	}
	if err := s.ensureTmux(&p, claimUnownedSession); err != nil {
		return p, err
	}
	sessionID, ownerID, exists, err := s.findSession(p.ID, p.TmuxSession)
	if err != nil || !exists || ownerID != p.ID {
		return p, fmt.Errorf("find owned tmux session for Workspace %q: %w", p.Name, err)
	}
	liveName, err := output("", "tmux", s.tmuxArgs("display-message", "-p", "-t", sessionID, "#{session_name}")...)
	if err != nil {
		return p, fmt.Errorf("find tmux session name for Workspace %q: %w", p.Name, err)
	}
	p.TmuxSession = liveName
	if p.Status == domain.WorkspaceArchived {
		p.Status = domain.WorkspaceActive
		p.ArchivedAt = nil
		p.UpdatedAt = s.now()
		if err := s.store.Save(p); err != nil {
			return p, err
		}
	}
	return p, nil
}

func (s *Service) ValidateOpen(reference string) error {
	_, err := s.validateOpen(reference)
	return err
}

func (s *Service) validateOpen(reference string) (domain.Workspace, error) {
	p, err := s.store.Find(reference)
	if err != nil {
		return p, err
	}
	switch p.Status {
	case domain.WorkspaceActive, domain.WorkspaceArchived:
		return p, nil
	case domain.WorkspaceRemoving:
		return p, clierr.New(clierr.PreconditionFailed, "Workspace %q removal is in progress. Run 'twt workspaces remove %s --apply' or 'twt workspaces remove %s --cancel'.", p.Name, p.ID, p.ID)
	default:
		return p, clierr.New(clierr.PreconditionFailed, "Workspace %q setup is not complete; run twt workspaces setup retry %s", p.Name, p.ID)
	}
}

func (s *Service) runPending(p *domain.Workspace) error {
	err := s.runSteps(p.Steps,
		func(now time.Time) error {
			p.UpdatedAt = now
			return s.store.Save(*p)
		},
		func(step domain.SetupStep) error {
			return s.runStep(p, step)
		},
		func(now time.Time, cause error) error {
			p.Status = domain.WorkspaceSetupFailed
			p.UpdatedAt = now
			if saveErr := s.store.Save(*p); saveErr != nil {
				return fmt.Errorf("%v; also could not save failure: %w", cause, saveErr)
			}
			return cause
		},
	)
	if err != nil {
		return err
	}
	p.Status = domain.WorkspaceActive
	p.UpdatedAt = s.now()
	return s.store.Save(*p)
}

func (s *Service) runStep(p *domain.Workspace, step domain.SetupStep) error {
	switch step.Kind {
	case domain.StepWorkspaceRoot:
		return s.writeOwnershipMarker(*p)
	case domain.StepCache:
		spec, repository, err := repositoryFor(*p, step.Repository)
		if err != nil {
			return err
		}
		return s.ensureCache(spec, repository.CachePath)
	case domain.StepCheckout:
		return s.ensureCheckout(*p, step.Repository)
	case domain.StepRepositoryInit:
		spec, repository, err := repositoryFor(*p, step.Repository)
		if err != nil {
			return err
		}
		return s.runInitialize(*p, repository.Path, spec.Initialize)
	case domain.StepTmux:
		return s.ensureTmux(p, preserveUnownedSession)
	case domain.StepWorkspaceInit:
		init := p.TemplateSnapshot.Initialize
		workingDirectory := filepath.Join(p.Root, filepath.FromSlash(init.WorkingDirectory))
		return s.runInitialize(*p, workingDirectory, init)
	case domain.StepAgent:
		return s.ensureTemplateAgent(*p, step.Agent)
	default:
		return fmt.Errorf("unknown setup step %q", step.Kind)
	}
}

// agentSteps returns one setup step for each Agent Session that the Workspace
// Template declares. These steps come after the tmux step and after Workspace
// initialization, so each Agent starts in a complete Workspace.
func agentSteps(template domain.Template) []domain.SetupStep {
	steps := make([]domain.SetupStep, 0, len(template.Agents))
	for _, declared := range template.Agents {
		steps = append(steps, domain.SetupStep{
			ID: "agent:" + declared.Label, Kind: domain.StepAgent,
			Agent: declared.Label, Status: domain.StepPending,
		})
	}
	return steps
}

// ensureTemplateAgent registers one declared Agent Session and starts it in
// its own Workspace window. The step is idempotent: a saved record with the
// same label makes it succeed again, so a setup retry does not make a second
// Agent Session. The caller already holds the mutation lock, so this step
// delegates to the lock-free BuildSession and StartDeclared of the Agent
// Session service.
//
// A Workspace without a live owned tmux session gets a record with no pane and
// with the declared start command as its resume command. The Agent Session
// can then start later with 'twt agents resume'.
func (s *Service) ensureTemplateAgent(p domain.Workspace, label string) error {
	var declared *domain.TemplateAgent
	for index := range p.TemplateSnapshot.Agents {
		if p.TemplateSnapshot.Agents[index].Label == label {
			declared = &p.TemplateSnapshot.Agents[index]
			break
		}
	}
	if declared == nil {
		return fmt.Errorf("the Workspace Template snapshot does not declare Agent Session %q", label)
	}
	agents := store.NewAgentStore(s.options.StateDir)
	existing, err := agents.List(p.ID)
	if err != nil {
		return err
	}
	for _, session := range existing {
		if session.Label == label {
			return nil
		}
	}
	resume := declared.Start
	if len(declared.Resume) > 0 {
		resume = declared.Resume
	}
	session, err := agent.BuildSession(p, declared.Provider, declared.Label, "", "", resume, existing, s.now())
	if err != nil {
		return err
	}
	session.PreferProviderResume = declared.PreferProviderResume
	session.Env = append([]string(nil), declared.Env...)
	_, ownerID, exists, err := s.findSession(p.ID, p.TmuxSession)
	if err != nil {
		return err
	}
	if !exists || ownerID != p.ID {
		return agents.Save(session)
	}
	if _, err := agent.NewService(s.options.StateDir, s.options.TmuxSocket).StartDeclared(p, session, declared.Start, declared.Env, declared.PreferredPane); err != nil {
		return fmt.Errorf("start Agent Session %q: %w", label, err)
	}
	return nil
}
