package workspace

import (
	"fmt"

	"github.com/jpugliesi/tmux-worktree/internal/domain"
	"github.com/jpugliesi/tmux-worktree/internal/store"
)

// RefreshReadyEnvironments refreshes every ready environment for one exact
// Workspace Template revision.
func (s *Service) RefreshReadyEnvironments(template domain.Template) ([]domain.PreparedEnvironment, error) {
	digests, err := store.Digests(template)
	if err != nil {
		return nil, err
	}
	environments, err := s.environments.List()
	if err != nil {
		return nil, err
	}
	refreshed := []domain.PreparedEnvironment{}
	for _, environment := range environments {
		if environment.Status != domain.EnvironmentReady || !digests.Matches(environment.TemplateDigest) {
			continue
		}
		updated, err := s.RefreshPreparedEnvironment(environment.ID)
		if err != nil {
			return refreshed, err
		}
		refreshed = append(refreshed, updated)
	}
	return refreshed, nil
}

// RefreshPreparedEnvironment fetches each default branch and updates one
// ready physical worktree before a claim.
//
// The refresh holds the mutation lock only for the two status flips, never
// across the fetch and the initialization: a long hold blocks every
// interactive command while the daemon refreshes. Between the flips the
// environment is preparing under the held environment lock, so a claim
// skips it, and an interrupted refresh becomes an abandoned preparation
// that the next pool top-up requeues.
func (s *Service) RefreshPreparedEnvironment(environmentID string) (domain.PreparedEnvironment, error) {
	lock, err := store.AcquireEnvironmentLockBlocking(s.options.StateDir, environmentID)
	if err != nil {
		return domain.PreparedEnvironment{}, err
	}
	defer lock.Release()
	environment, err := s.markEnvironmentRefreshing(environmentID)
	if err != nil {
		return environment, err
	}
	changed := map[string]bool{}
	for index := range environment.Repositories {
		repository := environment.Repositories[index]
		spec, _, _, err := preparedRepositoryFor(environment, repository.Name)
		if err != nil {
			return environment, err
		}
		err = s.withCacheLock(repository.CachePath, func() error {
			branch, err := defaultBranch(repository.CachePath, spec)
			if err != nil {
				return err
			}
			if err := fetchOrigin(repository.CachePath, branch); err != nil {
				return err
			}
			tip, err := output(repository.CachePath, "git", "rev-parse", "refs/remotes/origin/"+branch)
			if err != nil {
				return err
			}
			if tip == repository.BaseCommit {
				return nil
			}
			if err := run(repository.Path, "git", "reset", "--hard", tip); err != nil {
				return fmt.Errorf("refresh prepared repository %q: %w", repository.Name, err)
			}
			environment.Repositories[index].BaseCommit = tip
			changed[repository.Name] = true
			return nil
		})
		if err != nil {
			return environment, err
		}
	}
	for repositoryName := range changed {
		if repositorySpecHasInitialize(environment.TemplateSnapshot, repositoryName) {
			if err := s.runPreparedInitialize(environment, repositoryName); err != nil {
				return environment, err
			}
		}
	}
	now := s.now()
	environment.Status = domain.EnvironmentReady
	environment.ReadyAt = &now
	environment.UpdatedAt = now
	if err := s.environments.Save(environment); err != nil {
		return environment, err
	}
	return environment, nil
}

// markEnvironmentRefreshing flips one ready Prepared Environment to
// preparing under the mutation lock, so no claim reserves it while the
// refresh changes its worktrees. The caller holds the environment lock.
func (s *Service) markEnvironmentRefreshing(environmentID string) (domain.PreparedEnvironment, error) {
	mutationLock, err := store.AcquireMutationLock(s.options.StateDir)
	if err != nil {
		return domain.PreparedEnvironment{}, err
	}
	defer mutationLock.Release()
	environment, err := s.environments.Find(environmentID)
	if err != nil {
		return environment, err
	}
	if environment.Status != domain.EnvironmentReady {
		return environment, fmt.Errorf("Prepared Environment %q has status %q; refresh requires %q", environment.ID, environment.Status, domain.EnvironmentReady)
	}
	environment.Status = domain.EnvironmentPreparing
	environment.UpdatedAt = s.now()
	if err := s.environments.Save(environment); err != nil {
		return environment, err
	}
	return environment, nil
}
