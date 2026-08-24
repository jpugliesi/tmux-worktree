package workspace

import (
	"errors"
	"fmt"
	"path/filepath"
	"time"

	"github.com/jpugliesi/tmux-worktree/internal/domain"
	"github.com/jpugliesi/tmux-worktree/internal/store"
)

// preparationLaunchLease is the time a queued Prepared Environment stays
// reserved for the worker that holds its queue token. After the lease, a new
// caller can relaunch the preparation with a fresh token.
const preparationLaunchLease = 10 * time.Second

// PrepareLogPath returns the log file of the background preparation worker
// for one Prepared Environment.
func PrepareLogPath(stateDir, environmentID string) string {
	return filepath.Join(stateDir, "logs", "prepare-"+environmentID+".log")
}

// TopUpPool creates queued Prepared Environments until this Workspace Template
// revision has depth environments that are queued, preparing, or ready. It
// returns the queued environments that the caller must prepare or start.
func (s *Service) TopUpPool(templateName string, template domain.Template, depth int) ([]domain.PreparedEnvironment, error) {
	if err := template.Validate(); err != nil {
		return nil, fmt.Errorf("invalid Workspace Template %q: %w", templateName, err)
	}
	digests, err := store.Digests(template)
	if err != nil {
		return nil, err
	}
	lock, err := store.AcquireMutationLockWithTimeout(s.options.StateDir, 5*time.Second)
	if err != nil {
		return nil, err
	}
	defer lock.Release()
	environments, err := s.environments.List()
	if err != nil {
		return nil, err
	}
	pooled := 0
	start := []domain.PreparedEnvironment{}
	for _, environment := range environments {
		if environment.FormatVersion != domain.PreparationFormatVersion || !digests.Matches(environment.TemplateDigest) {
			continue
		}
		switch environment.Status {
		case domain.EnvironmentReady, domain.EnvironmentPreparing:
			pooled++
		case domain.EnvironmentQueued:
			pooled++
			if s.now().Sub(environment.QueuedAt) >= preparationLaunchLease {
				if err := s.relaunchQueued(&environment); err != nil {
					return nil, err
				}
				start = append(start, environment)
			}
		}
	}
	for pooled < depth {
		environment, err := s.saveNewQueuedEnvironment(templateName, digests, template)
		if err != nil {
			return start, err
		}
		start = append(start, environment)
		pooled++
	}
	return start, nil
}

// saveNewQueuedEnvironment creates one queued Prepared Environment record.
// The caller must hold the mutation lock.
func (s *Service) saveNewQueuedEnvironment(templateName string, digests store.DigestSet, template domain.Template) (domain.PreparedEnvironment, error) {
	environment, err := s.newPreparedEnvironment(templateName, digests.Environment, template)
	if err != nil {
		return domain.PreparedEnvironment{}, err
	}
	if err := s.environments.Save(environment); err != nil {
		return domain.PreparedEnvironment{}, err
	}
	return environment, nil
}

// relaunchQueued gives one queued Prepared Environment a new queue token and
// a fresh launch lease, and saves the record. The caller must hold the
// mutation lock.
func (s *Service) relaunchQueued(environment *domain.PreparedEnvironment) error {
	token, err := newID()
	if err != nil {
		return err
	}
	environment.QueueToken = token
	environment.QueuedAt = s.now()
	environment.UpdatedAt = environment.QueuedAt
	return s.environments.Save(*environment)
}

// Prepare returns one ready Prepared Environment for this Workspace Template
// revision. It reuses a ready environment, joins a queued or in-flight
// preparation, or queues a new environment and prepares it in the foreground.
func (s *Service) Prepare(templateName string, template domain.Template) (domain.PreparedEnvironment, error) {
	if err := template.Validate(); err != nil {
		return domain.PreparedEnvironment{}, fmt.Errorf("invalid Workspace Template %q: %w", templateName, err)
	}
	digests, err := store.Digests(template)
	if err != nil {
		return domain.PreparedEnvironment{}, err
	}
	lock, err := store.AcquireMutationLock(s.options.StateDir)
	if err != nil {
		return domain.PreparedEnvironment{}, err
	}
	environments, err := s.environments.List()
	if err != nil {
		lock.Release()
		return domain.PreparedEnvironment{}, err
	}
	for _, environment := range environments {
		if environment.FormatVersion == domain.PreparationFormatVersion && digests.Matches(environment.TemplateDigest) && environment.Status == domain.EnvironmentReady {
			lock.Release()
			return environment, nil
		}
	}
	for _, environment := range environments {
		if environment.FormatVersion != domain.PreparationFormatVersion || !digests.Matches(environment.TemplateDigest) {
			continue
		}
		switch environment.Status {
		case domain.EnvironmentQueued:
			if s.now().Sub(environment.QueuedAt) >= preparationLaunchLease {
				if err := s.relaunchQueued(&environment); err != nil {
					lock.Release()
					return domain.PreparedEnvironment{}, err
				}
			} else {
				s.reportBackgroundWait(environment.ID)
			}
			lock.Release()
			return s.PrepareQueued(environment.ID, environment.QueueToken)
		case domain.EnvironmentPreparing:
			environmentLock, lockErr := store.AcquireEnvironmentLock(s.options.StateDir, environment.ID)
			if lockErr == nil {
				environmentLock.Release()
			} else if errors.Is(lockErr, store.ErrLockHeld) {
				s.reportBackgroundWait(environment.ID)
			} else {
				lock.Release()
				return domain.PreparedEnvironment{}, lockErr
			}
			lock.Release()
			return s.PrepareQueued(environment.ID, environment.QueueToken)
		}
	}
	environment, err := s.saveNewQueuedEnvironment(templateName, digests, template)
	if releaseErr := lock.Release(); err == nil && releaseErr != nil {
		err = releaseErr
	}
	if err != nil {
		return domain.PreparedEnvironment{}, err
	}
	return s.PrepareQueued(environment.ID, environment.QueueToken)
}

// reportBackgroundWait tells the user that a background worker prepares the
// selected Prepared Environment and that this call waits for it.
func (s *Service) reportBackgroundWait(environmentID string) {
	s.report("Waiting for the background preparation of Prepared Environment %s. Log: %s.",
		environmentID, PrepareLogPath(s.options.StateDir, environmentID))
}
