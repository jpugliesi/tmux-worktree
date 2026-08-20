package project

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/jpugliesi/tmux-worktree/internal/domain"
	"github.com/jpugliesi/tmux-worktree/internal/store"
)

const environmentMarkerName = ".twt2-environment.json"

const preparationLaunchLease = 10 * time.Second

type PreparationQueue struct {
	Environment domain.PreparedEnvironment
	ShouldStart bool
}

func (s *Service) Prepare(templateName string, template domain.Template) (domain.PreparedEnvironment, error) {
	queued, err := s.QueuePreparation(templateName, template)
	if err != nil {
		return domain.PreparedEnvironment{}, err
	}
	if queued.Environment.Status == domain.EnvironmentReady {
		return queued.Environment, nil
	}
	return s.prepareEnvironment(queued.Environment.ID, queued.Environment.QueueToken)
}

func (s *Service) QueuePreparation(templateName string, template domain.Template) (PreparationQueue, error) {
	if err := template.Validate(); err != nil {
		return PreparationQueue{}, fmt.Errorf("invalid Project Template %q: %w", templateName, err)
	}
	digest, err := store.TemplateDigest(template)
	if err != nil {
		return PreparationQueue{}, err
	}
	lock, err := store.AcquireMutationLock(s.options.StateDir)
	if err != nil {
		return PreparationQueue{}, err
	}
	environments, err := s.environments.List()
	if err != nil {
		lock.Release()
		return PreparationQueue{}, err
	}
	for _, environment := range environments {
		if environment.FormatVersion == domain.PreparationFormatVersion && environment.TemplateDigest == digest && environment.Status == domain.EnvironmentReady {
			lock.Release()
			return PreparationQueue{Environment: environment}, nil
		}
	}
	for _, environment := range environments {
		if environment.FormatVersion != domain.PreparationFormatVersion || environment.TemplateDigest != digest {
			continue
		}
		if environment.Status == domain.EnvironmentQueued {
			shouldStart := s.now().Sub(environment.QueuedAt) >= preparationLaunchLease
			if shouldStart {
				token, tokenErr := newID()
				if tokenErr != nil {
					lock.Release()
					return PreparationQueue{}, tokenErr
				}
				environment.QueueToken = token
				environment.QueuedAt = s.now()
				environment.UpdatedAt = environment.QueuedAt
				if saveErr := s.environments.Save(environment); saveErr != nil {
					lock.Release()
					return PreparationQueue{}, saveErr
				}
			}
			lock.Release()
			return PreparationQueue{Environment: environment, ShouldStart: shouldStart}, nil
		}
		if environment.Status == domain.EnvironmentPreparing {
			environmentLock, lockErr := store.AcquireEnvironmentLock(s.options.StateDir, environment.ID)
			if lockErr == nil {
				environmentLock.Release()
				lock.Release()
				return PreparationQueue{Environment: environment, ShouldStart: true}, nil
			}
			if errors.Is(lockErr, store.ErrLockHeld) {
				lock.Release()
				return PreparationQueue{Environment: environment}, nil
			}
			lock.Release()
			return PreparationQueue{}, lockErr
		}
	}
	environment, err := s.newPreparedEnvironment(templateName, digest, template)
	if err == nil {
		err = s.environments.Save(environment)
	}
	if releaseErr := lock.Release(); err == nil && releaseErr != nil {
		err = releaseErr
	}
	if err != nil {
		return PreparationQueue{}, err
	}
	return PreparationQueue{Environment: environment, ShouldStart: true}, nil
}

func (s *Service) PrepareQueued(environmentID, token string) (domain.PreparedEnvironment, error) {
	return s.prepareEnvironment(environmentID, token)
}

func (s *Service) FailQueuedPreparation(environmentID, token string, cause error) error {
	lock, err := store.AcquireEnvironmentLock(s.options.StateDir, environmentID)
	if err != nil {
		return err
	}
	defer lock.Release()
	environment, err := s.environments.Find(environmentID)
	if err != nil {
		return err
	}
	if environment.Status != domain.EnvironmentQueued || environment.QueueToken != token {
		return nil
	}
	_, err = s.failEnvironment(&environment, cause)
	return err
}

func (s *Service) claimPreparedEnvironment(name, templateName string, template domain.Template, environmentID string) (domain.Project, error) {
	digest, err := store.TemplateDigest(template)
	if err != nil {
		return domain.Project{}, err
	}
	lock, err := store.AcquireMutationLock(s.options.StateDir)
	if err != nil {
		return domain.Project{}, err
	}
	environment, err := s.environments.Find(environmentID)
	if err == nil && (environment.Status != domain.EnvironmentReady || environment.TemplateDigest != digest || environment.FormatVersion != domain.PreparationFormatVersion) {
		err = fmt.Errorf("Prepared Environment %q is not ready for this Project Template revision", environment.ID)
	}
	if err == nil {
		err = s.requireProjectNameAvailable(name)
	}
	var project domain.Project
	if err == nil {
		project, err = s.projectForEnvironment(name, templateName, template, environment)
	}
	if err == nil {
		environment.Status = domain.EnvironmentClaiming
		environment.ClaimReservation = &domain.EnvironmentClaim{Project: project, ReservedAt: s.now()}
		environment.UpdatedAt = s.now()
		err = s.environments.Save(environment)
	}
	if err == nil {
		err = s.store.Save(project)
	}
	if releaseErr := lock.Release(); err == nil && releaseErr != nil {
		err = releaseErr
	}
	if err != nil {
		return project, err
	}
	return s.completeEnvironmentClaim(environment.ID, project.ID)
}

func (s *Service) requireProjectNameAvailable(name string) error {
	projects, err := s.store.List()
	if err != nil {
		return err
	}
	for _, project := range projects {
		if project.Name == name {
			return fmt.Errorf("Project %q already exists", name)
		}
	}
	environments, err := s.environments.List()
	if err != nil {
		return err
	}
	for _, environment := range environments {
		if environment.ClaimReservation != nil && environment.ClaimReservation.Project.Name == name {
			return fmt.Errorf("Project %q is already reserved by a Prepared Environment claim", name)
		}
	}
	return nil
}

func (s *Service) projectForEnvironment(name, templateName string, template domain.Template, environment domain.PreparedEnvironment) (domain.Project, error) {
	id, err := newID()
	if err != nil {
		return domain.Project{}, err
	}
	now := s.now()
	project := domain.Project{
		Version: domain.ProjectVersion, ID: id, Name: name, TemplateName: templateName,
		TemplateSnapshot: template, EnvironmentID: environment.ID, Status: domain.ProjectInitializing,
		Root: environment.Root, TmuxSession: name, CreatedAt: now, UpdatedAt: now,
	}
	project.Steps = append(project.Steps, newStep("project_root", domain.StepProjectRoot, ""))
	for _, repository := range environment.Repositories {
		project.Repositories = append(project.Repositories, domain.ProjectRepository{
			Name: repository.Name, CachePath: repository.CachePath, Path: repository.Path,
			Branch: "twt2/" + name + "-" + id[:8], WindowName: repository.WindowName,
		})
		project.Steps = append(project.Steps,
			successfulStep("cache:"+repository.Name, domain.StepCache, repository.Name, now),
			newStep("checkout:"+repository.Name, domain.StepCheckout, repository.Name),
		)
		if repositorySpecHasInitialize(template, repository.Name) {
			project.Steps = append(project.Steps, successfulStep("repository_init:"+repository.Name, domain.StepRepositoryInit, repository.Name, now))
		}
	}
	project.Steps = append(project.Steps, newStep("tmux", domain.StepTmux, ""))
	if template.Initialize != nil {
		project.Steps = append(project.Steps, newStep("project_init", domain.StepProjectInit, ""))
	}
	return project, nil
}

func (s *Service) completeEnvironmentClaim(environmentID, projectID string) (domain.Project, error) {
	lock, err := store.AcquireNamedLockBlocking(s.options.StateDir, "environment", environmentID)
	if err != nil {
		return domain.Project{}, err
	}
	defer lock.Release()
	environment, err := s.environments.Find(environmentID)
	if err != nil {
		return domain.Project{}, err
	}
	if environment.ClaimReservation == nil || environment.ClaimReservation.Project.ID != projectID {
		return domain.Project{}, fmt.Errorf("Prepared Environment %q does not contain Project claim %q", environmentID, projectID)
	}
	project, err := s.store.Find(projectID)
	if err != nil {
		return domain.Project{}, err
	}
	if err := s.validateEnvironmentForClaim(environment, project); err != nil {
		return project, err
	}
	for index := range project.Repositories {
		repository := &project.Repositories[index]
		prepared, err := preparedRepositoryByName(environment, repository.Name)
		if err != nil {
			return project, err
		}
		err = s.withCacheLock(repository.CachePath, func() error {
			if err := validatePreparedRepositoryForClaim(prepared, *repository); err != nil {
				return err
			}
			branch, err := output(repository.Path, "git", "branch", "--show-current")
			if err != nil {
				return err
			}
			if branch == "" {
				if err := run(repository.Path, "git", "switch", "-c", repository.Branch, prepared.BaseCommit); err != nil {
					return fmt.Errorf("create Project branch for repository %q: %w", repository.Name, err)
				}
			} else if branch != repository.Branch {
				return fmt.Errorf("claimed checkout for repository %q uses branch %q; expected %q", repository.Name, branch, repository.Branch)
			}
			return nil
		})
		if err != nil {
			return project, err
		}
		markProjectStepSucceeded(&project, "checkout:"+repository.Name, s.now())
	}
	marker := map[string]string{"owner": "twt2", "projectId": project.ID, "environmentId": environment.ID}
	if err := writeJSON(filepath.Join(project.Root, ".twt2-owned.json"), marker, 0o600); err != nil {
		return project, err
	}
	if err := os.Remove(filepath.Join(project.Root, environmentMarkerName)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return project, fmt.Errorf("remove Prepared Environment ownership marker: %w", err)
	}
	markProjectStepSucceeded(&project, "project_root", s.now())
	if err := s.store.Save(project); err != nil {
		return project, err
	}
	environment.Status = domain.EnvironmentClaimed
	environment.UpdatedAt = s.now()
	if err := s.environments.Save(environment); err != nil {
		return project, err
	}
	if err := s.runPending(&project); err != nil {
		return project, err
	}
	return project, nil
}

func (s *Service) validateEnvironmentForClaim(environment domain.PreparedEnvironment, project domain.Project) error {
	if environment.Status != domain.EnvironmentClaiming {
		return fmt.Errorf("Prepared Environment %q has status %q; expected %q", environment.ID, environment.Status, domain.EnvironmentClaiming)
	}
	if err := validateEnvironmentClaimMarker(environment, project); err != nil {
		return err
	}
	if len(environment.Repositories) != len(environment.TemplateSnapshot.Repositories) {
		return fmt.Errorf("Prepared Environment %q has an invalid repository count", environment.ID)
	}
	return nil
}

func validatePreparedRepositoryForClaim(repository domain.PreparedRepository, projectRepository domain.ProjectRepository) error {
	if repository.BaseCommit == "" {
		return fmt.Errorf("Prepared Environment repository %q has no base commit", repository.Name)
	}
	commonDir, err := output(repository.Path, "git", "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil || !sameDirectory(commonDir, repository.CachePath) {
		return fmt.Errorf("Prepared Environment repository %q does not use its Repository Cache", repository.Name)
	}
	branch, err := output(repository.Path, "git", "branch", "--show-current")
	if err != nil || (branch != "" && branch != projectRepository.Branch) {
		return fmt.Errorf("Prepared Environment repository %q has an invalid claim branch", repository.Name)
	}
	commit, err := output(repository.Path, "git", "rev-parse", "HEAD")
	if err != nil || commit != repository.BaseCommit {
		return fmt.Errorf("Prepared Environment repository %q is not at its saved base commit", repository.Name)
	}
	return nil
}

func validateEnvironmentClaimMarker(environment domain.PreparedEnvironment, project domain.Project) error {
	if err := validateEnvironmentMarker(environment); err == nil {
		return nil
	}
	data, err := os.ReadFile(filepath.Join(project.Root, ".twt2-owned.json"))
	if err != nil {
		return fmt.Errorf("Prepared Environment %q has no valid claim ownership marker", environment.ID)
	}
	var marker struct {
		Owner         string `json:"owner"`
		ProjectID     string `json:"projectId"`
		EnvironmentID string `json:"environmentId"`
	}
	if json.Unmarshal(data, &marker) != nil || marker.Owner != "twt2" || marker.ProjectID != project.ID || marker.EnvironmentID != environment.ID {
		return fmt.Errorf("Prepared Environment %q has an invalid claim ownership marker", environment.ID)
	}
	return nil
}

func markProjectStepSucceeded(project *domain.Project, id string, now time.Time) {
	for index := range project.Steps {
		if project.Steps[index].ID == id {
			project.Steps[index].Status = domain.StepSucceeded
			project.Steps[index].Attempts++
			project.Steps[index].StartedAt = &now
			project.Steps[index].FinishedAt = &now
			project.Steps[index].Error = ""
			return
		}
	}
}

func preparedRepositoryByName(environment domain.PreparedEnvironment, name string) (domain.PreparedRepository, error) {
	for _, repository := range environment.Repositories {
		if repository.Name == name {
			return repository, nil
		}
	}
	return domain.PreparedRepository{}, fmt.Errorf("repository %q is not in Prepared Environment %q", name, environment.ID)
}

func projectRepositoryByName(project domain.Project, name string) (domain.ProjectRepository, error) {
	for _, repository := range project.Repositories {
		if repository.Name == name {
			return repository, nil
		}
	}
	return domain.ProjectRepository{}, fmt.Errorf("repository %q is not in Project %q", name, project.Name)
}

func repositorySpecHasInitialize(template domain.Template, name string) bool {
	for _, repository := range template.Repositories {
		if repository.Name == name {
			return repository.Initialize != nil
		}
	}
	return false
}

func (s *Service) newPreparedEnvironment(templateName, digest string, template domain.Template) (domain.PreparedEnvironment, error) {
	id, err := newID()
	if err != nil {
		return domain.PreparedEnvironment{}, err
	}
	token, err := newID()
	if err != nil {
		return domain.PreparedEnvironment{}, err
	}
	now := s.now()
	root := filepath.Join(s.options.DataDir, "projects", id)
	environment := domain.PreparedEnvironment{
		Version: domain.PreparedEnvironmentVersion, FormatVersion: domain.PreparationFormatVersion,
		ID: id, TemplateName: templateName, TemplateDigest: digest, TemplateSnapshot: template,
		Status: domain.EnvironmentQueued, Root: root, QueueToken: token, QueuedAt: now,
		CreatedAt: now, UpdatedAt: now,
	}
	environment.Steps = append(environment.Steps, newStep("environment_root", domain.StepProjectRoot, ""))
	for _, spec := range template.Repositories {
		windowName := spec.WindowName
		if windowName == "" {
			windowName = spec.Name
		}
		repository := domain.PreparedRepository{
			Name: spec.Name, CachePath: s.cachePath(spec.Name, spec.Clone.URL),
			Path: filepath.Join(root, spec.Name), WindowName: windowName,
		}
		environment.Repositories = append(environment.Repositories, repository)
		environment.Steps = append(environment.Steps,
			newStep("cache:"+spec.Name, domain.StepCache, spec.Name),
			newStep("checkout:"+spec.Name, domain.StepCheckout, spec.Name),
		)
		if spec.Initialize != nil {
			environment.Steps = append(environment.Steps, newStep("repository_init:"+spec.Name, domain.StepRepositoryInit, spec.Name))
		}
	}
	return environment, nil
}

func (s *Service) prepareEnvironment(environmentID, token string) (domain.PreparedEnvironment, error) {
	lock, err := store.AcquireNamedLockBlocking(s.options.StateDir, "environment", environmentID)
	if err != nil {
		return domain.PreparedEnvironment{}, err
	}
	defer lock.Release()
	environment, err := s.environments.Find(environmentID)
	if err != nil {
		return domain.PreparedEnvironment{}, err
	}
	if environment.Status == domain.EnvironmentReady {
		return environment, nil
	}
	if environment.QueueToken != token || (environment.Status != domain.EnvironmentQueued && environment.Status != domain.EnvironmentPreparing) {
		return environment, fmt.Errorf("Prepared Environment %q does not have the requested queue token", environment.ID)
	}
	environment.Status = domain.EnvironmentPreparing
	environment.UpdatedAt = s.now()
	if err := s.environments.Save(environment); err != nil {
		return environment, err
	}
	for index := range environment.Steps {
		step := &environment.Steps[index]
		if step.Status == domain.StepSucceeded {
			continue
		}
		if step.Kind == domain.StepRepositoryInit && step.Status == domain.StepRunning {
			return s.failEnvironment(&environment, fmt.Errorf("repository initialization was interrupted; this physical environment will not be initialized again"))
		}
		now := s.now()
		step.Status = domain.StepRunning
		step.Attempts++
		step.StartedAt = &now
		step.FinishedAt = nil
		step.Error = ""
		environment.UpdatedAt = now
		if err := s.environments.Save(environment); err != nil {
			return environment, err
		}
		stepErr := s.runEnvironmentStep(&environment, *step)
		finished := s.now()
		step.FinishedAt = &finished
		environment.UpdatedAt = finished
		if stepErr != nil {
			step.Status = domain.StepFailed
			step.Error = stepErr.Error()
			return s.failEnvironment(&environment, stepErr)
		}
		step.Status = domain.StepSucceeded
		if err := s.environments.Save(environment); err != nil {
			return environment, err
		}
	}
	environment.Status = domain.EnvironmentReady
	environment.UpdatedAt = s.now()
	if err := s.environments.Save(environment); err != nil {
		return environment, err
	}
	return environment, nil
}

func (s *Service) failEnvironment(environment *domain.PreparedEnvironment, cause error) (domain.PreparedEnvironment, error) {
	environment.Status = domain.EnvironmentFailed
	environment.Failure = cause.Error()
	environment.UpdatedAt = s.now()
	if err := s.environments.Save(*environment); err != nil {
		return *environment, fmt.Errorf("%v; also could not save Prepared Environment failure: %w", cause, err)
	}
	return *environment, cause
}

func (s *Service) runEnvironmentStep(environment *domain.PreparedEnvironment, step domain.SetupStep) error {
	switch step.Kind {
	case domain.StepProjectRoot:
		return writeEnvironmentMarker(*environment)
	case domain.StepCache:
		project := environmentProject(*environment)
		return s.ensureCache(project, step.Repository)
	case domain.StepCheckout:
		return s.ensurePreparedCheckout(environment, step.Repository)
	case domain.StepRepositoryInit:
		return s.runPreparedInitialize(*environment, step.Repository)
	default:
		return fmt.Errorf("unknown Prepared Environment step %q", step.Kind)
	}
}

func (s *Service) ensurePreparedCheckout(environment *domain.PreparedEnvironment, repositoryName string) error {
	spec, repository, index, err := preparedRepositoryFor(*environment, repositoryName)
	if err != nil {
		return err
	}
	return s.withCacheLock(repository.CachePath, func() error {
		return s.ensurePreparedCheckoutLocked(environment, repositoryName, spec, repository, index)
	})
}

func (s *Service) ensurePreparedCheckoutLocked(environment *domain.PreparedEnvironment, repositoryName string, spec domain.RepositorySpec, repository domain.PreparedRepository, index int) error {
	if info, statErr := os.Stat(repository.Path); statErr == nil && info.IsDir() {
		commonDir, err := output(repository.Path, "git", "rev-parse", "--path-format=absolute", "--git-common-dir")
		if err != nil || !sameDirectory(commonDir, repository.CachePath) {
			return fmt.Errorf("Prepared Environment checkout %q does not use its Repository Cache", repository.Path)
		}
		branch, err := output(repository.Path, "git", "branch", "--show-current")
		if err != nil || branch != "" {
			return fmt.Errorf("Prepared Environment checkout %q is not detached", repository.Path)
		}
	} else if statErr == nil {
		return fmt.Errorf("Prepared Environment checkout path %q is not a directory", repository.Path)
	} else if !errors.Is(statErr, os.ErrNotExist) {
		return fmt.Errorf("inspect Prepared Environment checkout: %w", statErr)
	} else {
		defaultBranch := spec.DefaultBranch
		if defaultBranch == "" {
			var err error
			defaultBranch, err = output(repository.CachePath, "git", "symbolic-ref", "--short", "HEAD")
			if err != nil {
				return fmt.Errorf("find default branch for Repository Cache: %w", err)
			}
		}
		startPoint := "refs/remotes/origin/" + defaultBranch
		if err := run(repository.CachePath, "git", "worktree", "add", "--detach", repository.Path, startPoint); err != nil {
			return fmt.Errorf("create Prepared Environment checkout %q: %w", repositoryName, err)
		}
	}
	commit, err := output(repository.Path, "git", "rev-parse", "HEAD")
	if err != nil {
		return fmt.Errorf("read Prepared Environment base commit: %w", err)
	}
	environment.Repositories[index].BaseCommit = commit
	return nil
}

func (s *Service) runPreparedInitialize(environment domain.PreparedEnvironment, repositoryName string) error {
	spec, repository, _, err := preparedRepositoryFor(environment, repositoryName)
	if err != nil {
		return err
	}
	if spec.Initialize == nil || len(spec.Initialize.Command) == 0 {
		return fmt.Errorf("repository initialization command is empty")
	}
	activityLock, err := store.AcquireActivityLock(s.options.StateDir, environment.ID)
	if err != nil {
		return err
	}
	defer activityLock.Release()
	env := append(os.Environ(),
		"TWT2_ENVIRONMENT_ID="+environment.ID,
		"TWT2_ENVIRONMENT_ROOT="+environment.Root,
		"TWT2_TEMPLATE_NAME="+environment.TemplateName,
		"TWT2_REPOSITORY_NAME="+repository.Name,
		"TWT2_REPOSITORY_PATH="+repository.Path,
	)
	return runInitializationProcess(repository.Path, spec.Initialize.Command, env, activityLock.File())
}

func writeEnvironmentMarker(environment domain.PreparedEnvironment) error {
	if err := os.MkdirAll(environment.Root, 0o755); err != nil {
		return fmt.Errorf("create Prepared Environment root: %w", err)
	}
	marker := map[string]any{
		"owner": "twt2", "environmentId": environment.ID,
		"templateDigest": environment.TemplateDigest, "formatVersion": environment.FormatVersion,
	}
	return writeJSON(filepath.Join(environment.Root, environmentMarkerName), marker, 0o600)
}

func validateEnvironmentMarker(environment domain.PreparedEnvironment) error {
	data, err := os.ReadFile(filepath.Join(environment.Root, environmentMarkerName))
	if err != nil {
		return fmt.Errorf("Prepared Environment %q has no ownership marker", environment.ID)
	}
	var marker struct {
		Owner          string `json:"owner"`
		EnvironmentID  string `json:"environmentId"`
		TemplateDigest string `json:"templateDigest"`
		FormatVersion  int    `json:"formatVersion"`
	}
	if json.Unmarshal(data, &marker) != nil || marker.Owner != "twt2" || marker.EnvironmentID != environment.ID || marker.TemplateDigest != environment.TemplateDigest || marker.FormatVersion != environment.FormatVersion {
		return fmt.Errorf("Prepared Environment %q has an invalid ownership marker", environment.ID)
	}
	return nil
}

func environmentProject(environment domain.PreparedEnvironment) domain.Project {
	project := domain.Project{TemplateSnapshot: environment.TemplateSnapshot}
	for _, repository := range environment.Repositories {
		project.Repositories = append(project.Repositories, domain.ProjectRepository{
			Name: repository.Name, CachePath: repository.CachePath, Path: repository.Path, WindowName: repository.WindowName,
		})
	}
	return project
}

func preparedRepositoryFor(environment domain.PreparedEnvironment, name string) (domain.RepositorySpec, domain.PreparedRepository, int, error) {
	var spec *domain.RepositorySpec
	for index := range environment.TemplateSnapshot.Repositories {
		if environment.TemplateSnapshot.Repositories[index].Name == name {
			copy := environment.TemplateSnapshot.Repositories[index]
			spec = &copy
			break
		}
	}
	for index := range environment.Repositories {
		if environment.Repositories[index].Name == name && spec != nil {
			return *spec, environment.Repositories[index], index, nil
		}
	}
	return domain.RepositorySpec{}, domain.PreparedRepository{}, -1, fmt.Errorf("repository %q is not in Prepared Environment %q", name, environment.ID)
}

func projectRepository(project domain.Project, name string) domain.ProjectRepository {
	for _, repository := range project.Repositories {
		if repository.Name == name {
			return repository
		}
	}
	return domain.ProjectRepository{}
}

func successfulStep(id string, kind domain.StepKind, repository string, now time.Time) domain.SetupStep {
	return domain.SetupStep{ID: id, Kind: kind, Repository: repository, Status: domain.StepSucceeded, Attempts: 1, StartedAt: &now, FinishedAt: &now}
}

func sameDirectory(first, second string) bool {
	firstInfo, firstErr := os.Stat(first)
	secondInfo, secondErr := os.Stat(second)
	return firstErr == nil && secondErr == nil && os.SameFile(firstInfo, secondInfo)
}
