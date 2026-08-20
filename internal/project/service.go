package project

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/jpugliesi/tmux-worktree/internal/domain"
	"github.com/jpugliesi/tmux-worktree/internal/store"
)

type Options struct {
	StateDir   string
	DataDir    string
	TmuxSocket string
}

type Service struct {
	options      Options
	store        store.ProjectStore
	environments store.EnvironmentStore
	now          func() time.Time
}

func NewService(options Options) *Service {
	return &Service{
		options:      options,
		store:        store.NewProjectStore(options.StateDir),
		environments: store.NewEnvironmentStore(options.StateDir),
		now:          func() time.Time { return time.Now().UTC() },
	}
}

func (s *Service) Create(name, templateName string, template domain.Template) (domain.Project, error) {
	if reserved, found, err := s.restoreReservedProject(name); err != nil {
		return domain.Project{}, err
	} else if found {
		return s.completeEnvironmentClaim(reserved.EnvironmentID, reserved.ID)
	}
	if err := s.ValidateCreate(name, templateName, template); err != nil {
		return domain.Project{}, err
	}
	environment, err := s.Prepare(templateName, template)
	if err != nil {
		return domain.Project{}, err
	}
	return s.claimPreparedEnvironment(name, templateName, template, environment.ID)
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
			return fmt.Errorf("Project %q already exists", name)
		}
	}
	return nil
}

func (s *Service) List() ([]domain.Project, error) { return s.store.List() }

func (s *Service) Find(reference string) (domain.Project, error) { return s.store.Find(reference) }

func (s *Service) CurrentFromPane(tmuxPane string) (domain.Project, error) {
	if tmuxPane == "" {
		return domain.Project{}, fmt.Errorf("run this command inside a twt2 Project tmux session")
	}
	sessionID, err := output("", "tmux", s.tmuxArgs("display-message", "-p", "-t", tmuxPane, "#{session_id}")...)
	if err != nil || sessionID == "" {
		return domain.Project{}, fmt.Errorf("find the Project tmux session for pane %q", tmuxPane)
	}
	projectID, err := output("", "tmux", s.tmuxArgs("show-options", "-t", sessionID, "-v", "@twt2_project_id")...)
	if err != nil || projectID == "" {
		return domain.Project{}, fmt.Errorf("tmux pane %q is not in a twt2 Project", tmuxPane)
	}
	p, err := s.store.Find(projectID)
	if err != nil {
		return domain.Project{}, err
	}
	if p.ID != projectID {
		return domain.Project{}, fmt.Errorf("tmux session %q does not contain an immutable Project ID", sessionID)
	}
	if p.Status != domain.ProjectActive {
		return domain.Project{}, fmt.Errorf("Project %q has status %q; quick create requires status %q", p.Name, p.Status, domain.ProjectActive)
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

func (s *Service) Current(directory, projectID, tmuxPane string) (domain.Project, error) {
	if projectID != "" {
		return s.store.Find(projectID)
	}
	if tmuxPane != "" {
		sessionID, err := output("", "tmux", s.tmuxArgs("display-message", "-p", "-t", tmuxPane, "#{session_id}")...)
		if err == nil {
			id, optionErr := output("", "tmux", s.tmuxArgs("show-options", "-t", sessionID, "-v", "@twt2_project_id")...)
			if optionErr == nil && id != "" {
				return s.store.Find(id)
			}
		}
	}
	projects, err := s.store.List()
	if err != nil {
		return domain.Project{}, err
	}
	absDirectory, err := filepath.Abs(directory)
	if err != nil {
		return domain.Project{}, fmt.Errorf("resolve current directory: %w", err)
	}
	for _, p := range projects {
		relative, err := filepath.Rel(p.Root, absDirectory)
		if err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return p, nil
		}
	}
	return domain.Project{}, fmt.Errorf("the current directory or tmux pane is not in a twt2 Project")
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
			return s.completeEnvironmentClaim(environment.ID, reserved.ID)
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
		return p, fmt.Errorf("Project %q removal is in progress; run twt2 projects remove %s --apply", p.Name, p.ID)
	}
	if p.Status == domain.ProjectArchived {
		return p, fmt.Errorf("Project %q is archived; run twt2 projects open %s", p.Name, p.ID)
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
	if p.Status != domain.ProjectActive && p.Status != domain.ProjectArchived {
		return p, fmt.Errorf("Project %q setup is not complete; run twt2 projects setup retry %s", p.Name, p.ID)
	}
	return p, nil
}

func (s *Service) runPending(p *domain.Project) error {
	for index := range p.Steps {
		if p.Steps[index].Status == domain.StepSucceeded {
			continue
		}
		step := &p.Steps[index]
		now := s.now()
		step.Status = domain.StepRunning
		step.Attempts++
		step.StartedAt = &now
		step.FinishedAt = nil
		step.Error = ""
		p.UpdatedAt = now
		if err := s.store.Save(*p); err != nil {
			return err
		}
		err := s.runStep(p, *step)
		finished := s.now()
		step.FinishedAt = &finished
		p.UpdatedAt = finished
		if err != nil {
			step.Status = domain.StepFailed
			step.Error = err.Error()
			p.Status = domain.ProjectSetupFailed
			if saveErr := s.store.Save(*p); saveErr != nil {
				return fmt.Errorf("%v; also could not save failure: %w", err, saveErr)
			}
			return err
		}
		step.Status = domain.StepSucceeded
		if err := s.store.Save(*p); err != nil {
			return err
		}
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
		return s.ensureCache(*p, step.Repository)
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
	default:
		return fmt.Errorf("unknown setup step %q", step.Kind)
	}
}
