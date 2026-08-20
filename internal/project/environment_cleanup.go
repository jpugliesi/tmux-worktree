package project

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/jpugliesi/tmux-worktree/internal/domain"
	"github.com/jpugliesi/tmux-worktree/internal/store"
)

type EnvironmentCleanupPlan struct {
	Environments []EnvironmentCleanupItem `json:"environments"`
}

type EnvironmentCleanupItem struct {
	ID           string `json:"id"`
	TemplateName string `json:"template"`
	Reason       string `json:"reason"`
	Root         string `json:"root"`
}

func (s *Service) PreparedCleanupPlan(currentTemplateDigests map[string]store.DigestSet) (EnvironmentCleanupPlan, error) {
	environments, err := s.environments.List()
	if err != nil {
		return EnvironmentCleanupPlan{}, err
	}
	plan := EnvironmentCleanupPlan{}
	for _, environment := range environments {
		reason := ""
		switch environment.Status {
		case domain.EnvironmentFailed:
			reason = "failed Prepared Environment"
		case domain.EnvironmentReady:
			if !currentTemplateDigests[environment.TemplateName].Matches(environment.TemplateDigest) {
				reason = "obsolete Prepared Environment"
			}
		}
		if reason != "" {
			plan.Environments = append(plan.Environments, EnvironmentCleanupItem{
				ID: environment.ID, TemplateName: environment.TemplateName, Reason: reason, Root: environment.Root,
			})
		}
	}
	return plan, nil
}

func (s *Service) CleanPrepared(currentTemplateDigests map[string]store.DigestSet) (EnvironmentCleanupPlan, error) {
	plan, err := s.PreparedCleanupPlan(currentTemplateDigests)
	if err != nil {
		return plan, err
	}
	for _, item := range plan.Environments {
		if err := s.cleanPreparedEnvironment(item.ID, currentTemplateDigests); err != nil {
			return plan, err
		}
	}
	return plan, nil
}

func (s *Service) cleanPreparedEnvironment(environmentID string, currentTemplateDigests map[string]store.DigestSet) error {
	global, err := store.AcquireMutationLock(s.options.StateDir)
	if err != nil {
		return err
	}
	environment, err := s.environments.Find(environmentID)
	if err == nil {
		candidate := environment.Status == domain.EnvironmentFailed || (environment.Status == domain.EnvironmentReady && !currentTemplateDigests[environment.TemplateName].Matches(environment.TemplateDigest))
		if !candidate {
			err = fmt.Errorf("Prepared Environment %q is not safe to clean", environment.ID)
		}
	}
	if err == nil && environment.Status == domain.EnvironmentReady {
		environment.Status = domain.EnvironmentFailed
		environment.Failure = "cleanup reserved for an obsolete Prepared Environment"
		environment.UpdatedAt = s.now()
		err = s.environments.Save(environment)
	}
	if releaseErr := global.Release(); err == nil && releaseErr != nil {
		err = releaseErr
	}
	if err != nil {
		return err
	}
	environmentLock, err := store.AcquireEnvironmentLock(s.options.StateDir, environment.ID)
	if err != nil {
		return err
	}
	defer environmentLock.Release()
	active, err := store.ActivityLockHeld(s.options.StateDir, environment.ID)
	if err != nil {
		return err
	}
	if active {
		return fmt.Errorf("Prepared Environment %q still has active initialization", environment.ID)
	}
	if environment.Root != filepath.Join(s.options.DataDir, "projects", environment.ID) {
		return fmt.Errorf("Prepared Environment %q has an invalid root path", environment.ID)
	}
	if _, err := os.Stat(environment.Root); errors.Is(err, os.ErrNotExist) {
		return s.environments.Delete(environment.ID)
	} else if err != nil {
		return fmt.Errorf("inspect Prepared Environment root: %w", err)
	}
	if err := validateEnvironmentMarker(environment); err != nil {
		return err
	}
	for _, repository := range environment.Repositories {
		if repository.Path != filepath.Join(environment.Root, repository.Name) {
			return fmt.Errorf("Prepared Environment repository %q has an invalid checkout path", repository.Name)
		}
		if _, err := os.Stat(repository.Path); errors.Is(err, os.ErrNotExist) {
			continue
		} else if err != nil {
			return fmt.Errorf("inspect Prepared Environment checkout: %w", err)
		}
		commonDir, err := output(repository.Path, "git", "rev-parse", "--path-format=absolute", "--git-common-dir")
		if err != nil || !sameDirectory(commonDir, repository.CachePath) {
			return fmt.Errorf("Prepared Environment repository %q does not use its Repository Cache", repository.Name)
		}
		cacheLock, err := store.AcquireNamedLockBlocking(s.options.StateDir, "cache", repository.CachePath)
		if err != nil {
			return err
		}
		removeErr := run(repository.CachePath, "git", "worktree", "remove", "--force", repository.Path)
		releaseErr := cacheLock.Release()
		if removeErr != nil {
			return fmt.Errorf("remove Prepared Environment worktree %q: %w", repository.Name, removeErr)
		}
		if releaseErr != nil {
			return releaseErr
		}
	}
	if err := os.Remove(filepath.Join(environment.Root, environmentMarkerName)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove Prepared Environment marker: %w", err)
	}
	if err := os.Remove(environment.Root); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove Prepared Environment root: %w", err)
	}
	return s.environments.Delete(environment.ID)
}
