package workspace

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

const environmentMarkerName = ".twt-environment.json"

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
	environment.Steps = append(environment.Steps, newStep("environment_root", domain.StepWorkspaceRoot, ""))
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

// PrepareQueued is the preparation worker entry point. It runs the pending
// setup steps of one queued Prepared Environment and returns the ready
// environment.
func (s *Service) PrepareQueued(environmentID, token string) (domain.PreparedEnvironment, error) {
	lock, err := store.AcquireEnvironmentLockBlocking(s.options.StateDir, environmentID)
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
	if environment.Status == domain.EnvironmentFailed {
		return environment, s.failedEnvironmentError(environment)
	}
	if environment.QueueToken != token || (environment.Status != domain.EnvironmentQueued && environment.Status != domain.EnvironmentPreparing) {
		return environment, fmt.Errorf("Prepared Environment %q does not have the requested queue token", environment.ID)
	}
	environment.Status = domain.EnvironmentPreparing
	environment.UpdatedAt = s.now()
	if err := s.environments.Save(environment); err != nil {
		return environment, err
	}
	err = s.runSteps(environment.Steps,
		func(now time.Time) error {
			environment.UpdatedAt = now
			return s.environments.Save(environment)
		},
		func(step domain.SetupStep) error {
			return s.runEnvironmentStep(&environment, step)
		},
		func(now time.Time, cause error) error {
			_, failErr := s.failEnvironment(&environment, cause)
			return failErr
		},
	)
	if err != nil {
		return environment, err
	}
	readyAt := s.now()
	environment.Status = domain.EnvironmentReady
	environment.UpdatedAt = readyAt
	environment.ReadyAt = &readyAt
	if err := s.environments.Save(environment); err != nil {
		return environment, err
	}
	return environment, nil
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

func (s *Service) failEnvironment(environment *domain.PreparedEnvironment, cause error) (domain.PreparedEnvironment, error) {
	environment.Status = domain.EnvironmentFailed
	environment.Failure = cause.Error()
	environment.UpdatedAt = s.now()
	if err := s.environments.Save(*environment); err != nil {
		return *environment, fmt.Errorf("%v; also could not save Prepared Environment failure: %w", cause, err)
	}
	return *environment, cause
}

// runEnvironmentStep runs one preparation step. The step argument carries the
// status that the step engine found: a repository initialization step that
// was already running belonged to an interrupted worker, and this environment
// must fail.
func (s *Service) runEnvironmentStep(environment *domain.PreparedEnvironment, step domain.SetupStep) error {
	if step.Kind == domain.StepRepositoryInit && step.Status == domain.StepRunning {
		return fmt.Errorf("repository initialization was interrupted; twt removes this environment and prepares a new one on the next create")
	}
	switch step.Kind {
	case domain.StepWorkspaceRoot:
		return writeEnvironmentMarker(*environment)
	case domain.StepCache:
		spec, repository, _, err := preparedRepositoryFor(*environment, step.Repository)
		if err != nil {
			return err
		}
		return s.ensureCache(spec, repository.CachePath)
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
		if err := worktreeUsesCache(repository.Path, repository.CachePath); err != nil {
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
		branch, err := defaultBranch(repository.CachePath, spec)
		if err != nil {
			return err
		}
		startPoint := "refs/remotes/origin/" + branch
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
		"TWT_ENVIRONMENT_ID="+environment.ID,
		"TWT_ENVIRONMENT_ROOT="+environment.Root,
		"TWT_TEMPLATE_NAME="+environment.TemplateName,
		"TWT_REPOSITORY_NAME="+repository.Name,
		"TWT_REPOSITORY_PATH="+repository.Path,
	)
	return runInitializationProcess(repository.Path, spec.Initialize.Command, env, activityLock.File())
}

func writeEnvironmentMarker(environment domain.PreparedEnvironment) error {
	if err := os.MkdirAll(environment.Root, 0o755); err != nil {
		return fmt.Errorf("create Prepared Environment root: %w", err)
	}
	marker := map[string]any{
		"owner": "twt", "environmentId": environment.ID,
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
	if json.Unmarshal(data, &marker) != nil || marker.Owner != "twt" || marker.EnvironmentID != environment.ID || marker.TemplateDigest != environment.TemplateDigest || marker.FormatVersion != environment.FormatVersion {
		return fmt.Errorf("Prepared Environment %q has an invalid ownership marker", environment.ID)
	}
	return nil
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
