package project

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
}

type Service struct {
	options      Options
	store        store.ProjectStore
	environments store.EnvironmentStore
	snapshots    store.SnapshotStore
	now          func() time.Time
}

func NewService(options Options) *Service {
	return &Service{
		options:      options,
		store:        store.NewProjectStore(options.StateDir),
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

func (s *Service) Create(name, templateName string, template domain.Template) (domain.Project, error) {
	return s.CreateWithOptions(name, templateName, template, CreateOptions{})
}

func (s *Service) ValidateCreate(name, templateName string, template domain.Template) error {
	if err := store.ValidateResourceName(name); err != nil {
		return fmt.Errorf("invalid Project name: %w", err)
	}
	if err := template.Validate(); err != nil {
		return fmt.Errorf("invalid Project Template %q: %w", templateName, err)
	}
	if len(template.Repositories) == 0 {
		return fmt.Errorf("Project Template %q has no repositories", templateName)
	}
	projects, err := s.store.List()
	if err != nil {
		return err
	}
	for _, existing := range projects {
		if existing.Name == name {
			return clierr.New(clierr.AlreadyExists, "Project %q already exists", name)
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
	_, err := s.validateBranchSelection(name, "00000000", template, opts)
	return err
}

func (s *Service) List() ([]domain.Project, error) { return s.store.List() }

func (s *Service) Find(reference string) (domain.Project, error) { return s.store.Find(reference) }

// ErrNotInProject marks a tmux context that is not inside a twt Project
// session. Callers can branch on it with errors.Is.
var ErrNotInProject = errors.New("the tmux pane is not in a twt Project session")

type notInProjectError struct{ message string }

func (e notInProjectError) Error() string { return e.message }

func (e notInProjectError) Is(target error) bool { return target == ErrNotInProject }

// CurrentForQuickCreate resolves the current Project for quick create. It
// uses the tmux pane of the caller, then the project ID value, then the
// current directory. Quick create needs an active Project. When the caller
// runs inside the tmux session that the Project owns, that session must be
// the only session of the Project, because quick create switches the calling
// tmux client and then archives the Project.
func (s *Service) CurrentForQuickCreate(directory, projectID, tmuxPane string) (domain.Project, error) {
	p, sessionID, err := s.projectForPane(tmuxPane)
	if err != nil {
		return domain.Project{}, err
	}
	if sessionID == "" {
		p, err = s.Current(directory, projectID, "")
		if err != nil {
			return domain.Project{}, notInProjectError{message: "run this command inside a twt Project worktree or tmux session"}
		}
	}
	if p.Status != domain.ProjectActive {
		return domain.Project{}, fmt.Errorf("Project %q has status %q; quick create requires status %q", p.Name, p.Status, domain.ProjectActive)
	}
	if sessionID == "" {
		return p, nil
	}
	sessions, err := s.ownedSessions(p.ID)
	if err != nil {
		return domain.Project{}, err
	}
	if len(sessions) != 1 || sessions[0] != sessionID {
		return domain.Project{}, fmt.Errorf("tmux session %q is not the unique session for Project %q", sessionID, p.Name)
	}
	return p, nil
}

// projectForPane returns the Project of the tmux session that owns the pane,
// with that session ID. An empty session ID means that the pane gives no
// Project, and the caller must use another part of the context chain.
func (s *Service) projectForPane(tmuxPane string) (domain.Project, string, error) {
	if tmuxPane == "" {
		return domain.Project{}, "", nil
	}
	sessionID, err := output("", "tmux", s.tmuxArgs("display-message", "-p", "-t", tmuxPane, "#{session_id}")...)
	if err != nil || sessionID == "" {
		return domain.Project{}, "", nil
	}
	projectID, err := output("", "tmux", s.tmuxArgs("show-options", "-t", sessionID, "-v", "@twt_project_id")...)
	if err != nil || projectID == "" {
		return domain.Project{}, "", nil
	}
	p, err := s.store.Find(projectID)
	if err != nil {
		return domain.Project{}, "", err
	}
	if p.ID != projectID {
		return domain.Project{}, "", fmt.Errorf("tmux session %q does not contain an immutable Project ID", sessionID)
	}
	return p, sessionID, nil
}

func (s *Service) Current(directory, projectID, tmuxPane string) (domain.Project, error) {
	if projectID != "" {
		return s.store.Find(projectID)
	}
	if tmuxPane != "" {
		sessionID, err := output("", "tmux", s.tmuxArgs("display-message", "-p", "-t", tmuxPane, "#{session_id}")...)
		if err == nil {
			id, optionErr := output("", "tmux", s.tmuxArgs("show-options", "-t", sessionID, "-v", "@twt_project_id")...)
			if optionErr == nil && id != "" {
				return s.store.Find(id)
			}
		}
	}
	return s.FindByDirectory(directory)
}

// FindByDirectory finds the Project that contains the directory. It matches
// the ownership marker, the Project root, or any repository path. Adopted
// Projects use repository paths, because their root is only the first pane.
func (s *Service) FindByDirectory(directory string) (domain.Project, error) {
	absDirectory, err := filepath.Abs(directory)
	if err != nil {
		return domain.Project{}, fmt.Errorf("resolve current directory: %w", err)
	}
	if project, ok := s.projectFromOwnershipMarker(absDirectory); ok {
		return project, nil
	}
	projects, err := s.store.List()
	if err != nil {
		return domain.Project{}, err
	}
	for _, p := range projects {
		if projectContainsDirectory(p, absDirectory) {
			return p, nil
		}
	}
	return domain.Project{}, fmt.Errorf("the current directory or tmux pane is not in a twt Project")
}

func (s *Service) projectFromOwnershipMarker(directory string) (domain.Project, bool) {
	for dir := directory; ; {
		projectID, ok := readOwnershipProjectID(dir)
		if ok {
			project, err := s.store.Find(projectID)
			if err == nil && projectContainsDirectory(project, directory) {
				return project, true
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return domain.Project{}, false
		}
		dir = parent
	}
}

func projectContainsDirectory(project domain.Project, directory string) bool {
	if directoryInside(project.Root, directory) {
		return true
	}
	for _, repository := range project.Repositories {
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

func (s *Service) Retry(reference string) (domain.Project, error) {
	reserved, findErr := s.store.Find(reference)
	if findErr != nil {
		var restored bool
		var restoreErr error
		reserved, restored, restoreErr = s.restoreReservedProject(reference)
		if restoreErr != nil {
			return domain.Project{}, restoreErr
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
		return domain.Project{}, err
	}
	defer lock.Release()
	p, err := s.validateRetry(reference)
	if err != nil {
		return domain.Project{}, err
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
	p.Status = domain.ProjectInitializing
	if err := s.store.Save(p); err != nil {
		return p, err
	}
	if err := s.runPending(&p); err != nil {
		return p, err
	}
	return p, nil
}

// restoreReservedProject repairs the durable boundary between an Environment
// claim reservation and its Project record. The reservation is the journal of
// record and contains the complete immutable Project.
func (s *Service) restoreReservedProject(reference string) (domain.Project, bool, error) {
	lock, err := store.AcquireMutationLock(s.options.StateDir)
	if err != nil {
		return domain.Project{}, false, err
	}
	defer lock.Release()
	environments, err := s.environments.List()
	if err != nil {
		return domain.Project{}, false, err
	}
	var match *domain.Project
	for _, environment := range environments {
		if environment.Status != domain.EnvironmentClaiming || environment.ClaimReservation == nil {
			continue
		}
		project := environment.ClaimReservation.Project
		if project.ID != reference && project.Name != reference {
			continue
		}
		if match != nil {
			return domain.Project{}, false, fmt.Errorf("Project claim %q is ambiguous; use a Project ID", reference)
		}
		copy := project
		match = &copy
	}
	if match == nil {
		return domain.Project{}, false, nil
	}
	if err := s.store.Save(*match); err != nil {
		return domain.Project{}, false, fmt.Errorf("restore Project %q from its Prepared Environment claim: %w", match.Name, err)
	}
	return *match, true, nil
}

func (s *Service) ValidateRetry(reference string) error {
	_, err := s.validateRetry(reference)
	return err
}

func (s *Service) validateRetry(reference string) (domain.Project, error) {
	p, err := s.store.Find(reference)
	if err != nil {
		return p, err
	}
	if p.Status == domain.ProjectRemoving {
		return p, clierr.New(clierr.PreconditionFailed, "Project %q removal is in progress; run twt projects remove %s --apply", p.Name, p.ID)
	}
	if p.Status == domain.ProjectArchived {
		return p, clierr.New(clierr.PreconditionFailed, "Project %q is archived; run twt projects open %s", p.Name, p.ID)
	}
	return p, nil
}

func (s *Service) Open(reference string) (domain.Project, error) {
	lock, err := store.AcquireMutationLock(s.options.StateDir)
	if err != nil {
		return domain.Project{}, err
	}
	defer lock.Release()
	p, err := s.validateOpen(reference)
	if err != nil {
		return domain.Project{}, err
	}
	if err := s.ensureTmux(&p); err != nil {
		return p, err
	}
	sessionID, ownerID, exists, err := s.findSession(p.ID, p.TmuxSession)
	if err != nil || !exists || ownerID != p.ID {
		return p, fmt.Errorf("find owned tmux session for Project %q: %w", p.Name, err)
	}
	liveName, err := output("", "tmux", s.tmuxArgs("display-message", "-p", "-t", sessionID, "#{session_name}")...)
	if err != nil {
		return p, fmt.Errorf("find tmux session name for Project %q: %w", p.Name, err)
	}
	p.TmuxSession = liveName
	if p.Status == domain.ProjectArchived {
		p.Status = domain.ProjectActive
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

func (s *Service) validateOpen(reference string) (domain.Project, error) {
	p, err := s.store.Find(reference)
	if err != nil {
		return p, err
	}
	switch p.Status {
	case domain.ProjectActive, domain.ProjectArchived:
		return p, nil
	case domain.ProjectRemoving:
		return p, clierr.New(clierr.PreconditionFailed, "Project %q removal is in progress. Run 'twt projects remove %s --apply' or 'twt projects remove %s --cancel'.", p.Name, p.ID, p.ID)
	default:
		return p, clierr.New(clierr.PreconditionFailed, "Project %q setup is not complete; run twt projects setup retry %s", p.Name, p.ID)
	}
}

func (s *Service) runPending(p *domain.Project) error {
	err := s.runSteps(p.Steps,
		func(now time.Time) error {
			p.UpdatedAt = now
			return s.store.Save(*p)
		},
		func(step domain.SetupStep) error {
			return s.runStep(p, step)
		},
		func(now time.Time, cause error) error {
			p.Status = domain.ProjectSetupFailed
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
	p.Status = domain.ProjectActive
	p.UpdatedAt = s.now()
	return s.store.Save(*p)
}

func (s *Service) runStep(p *domain.Project, step domain.SetupStep) error {
	switch step.Kind {
	case domain.StepProjectRoot:
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
		return s.ensureTmux(p)
	case domain.StepProjectInit:
		init := p.TemplateSnapshot.Initialize
		workingDirectory := filepath.Join(p.Root, filepath.FromSlash(init.WorkingDirectory))
		return s.runInitialize(*p, workingDirectory, init)
	case domain.StepAgent:
		return s.ensureTemplateAgent(*p, step.Agent)
	default:
		return fmt.Errorf("unknown setup step %q", step.Kind)
	}
}

// agentSteps returns one setup step for each Agent Session that the Project
// Template declares. These steps come after the tmux step and after Project
// initialization, so each Agent starts in a complete Project.
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
// its own Project window. The step is idempotent: a saved record with the
// same label makes it succeed again, so a setup retry does not make a second
// Agent Session. The caller already holds the mutation lock, so this step
// delegates to the lock-free BuildSession and StartDeclared of the Agent
// Session service.
//
// A Project without a live owned tmux session gets a record with no pane and
// with the declared start command as its resume command. The Agent Session
// can then start later with 'twt agents resume'.
func (s *Service) ensureTemplateAgent(p domain.Project, label string) error {
	var declared *domain.TemplateAgent
	for index := range p.TemplateSnapshot.Agents {
		if p.TemplateSnapshot.Agents[index].Label == label {
			declared = &p.TemplateSnapshot.Agents[index]
			break
		}
	}
	if declared == nil {
		return fmt.Errorf("the Project Template snapshot does not declare Agent Session %q", label)
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
	session, err := agent.BuildSession(p, declared.Provider, declared.Label, "", "", declared.Start, existing, s.now())
	if err != nil {
		return err
	}
	_, ownerID, exists, err := s.findSession(p.ID, p.TmuxSession)
	if err != nil {
		return err
	}
	if !exists || ownerID != p.ID {
		return agents.Save(session)
	}
	if _, err := agent.NewService(s.options.StateDir, s.options.TmuxSocket).StartDeclared(p, session, declared.Start); err != nil {
		return fmt.Errorf("start Agent Session %q: %w", label, err)
	}
	return nil
}
