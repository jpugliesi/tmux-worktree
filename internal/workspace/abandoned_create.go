package workspace

import (
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/jpugliesi/tmux-worktree/internal/clierr"
	"github.com/jpugliesi/tmux-worktree/internal/domain"
	"github.com/jpugliesi/tmux-worktree/internal/store"
)

// abandonedClaimGrace is the age at which a claim reservation without the
// environment lock counts as abandoned. A create saves the reservation and
// takes the environment lock only after it releases the mutation lock, so a
// new reservation can be live for a moment without the lock.
const abandonedClaimGrace = time.Minute

// SettleAbandonedCreate gives done a Workspace that it can finish when the
// create of that Workspace stopped before it finished. A live create holds
// the lock of its Prepared Environment, or the mutation lock on a retry, so
// an initializing Workspace with both locks free has no live create.
//
//   - A claim that never completed: the Prepared Environment fails, and the
//     Workspace record goes away, as abandonClaim does. removed is true. The
//     pool removes the failed environment and prepares a replacement.
//   - A claim that completed, but whose setup stopped: the Workspace gets
//     status setup_failed, the status that a failed setup step saves. The
//     normal release then returns its worktrees to the pool.
//
// A Workspace in another status comes back unchanged.
func (s *Service) SettleAbandonedCreate(reference string) (workspace domain.Workspace, removed bool, err error) {
	lock, err := store.AcquireMutationLock(s.options.StateDir)
	if err != nil {
		return domain.Workspace{}, false, err
	}
	defer lock.Release()
	workspace, err = s.store.Find(reference)
	if err != nil || workspace.Status != domain.WorkspaceInitializing {
		return workspace, false, err
	}
	running := clierr.New(clierr.Locked, "Workspace %q create is still running; wait for it to finish, or stop it and run the command again", workspace.Name)
	if workspace.EnvironmentID != "" {
		environmentLock, err := store.AcquireEnvironmentLock(s.options.StateDir, workspace.EnvironmentID)
		if errors.Is(err, store.ErrLockHeld) {
			return workspace, false, running
		}
		if err != nil {
			return workspace, false, err
		}
		defer environmentLock.Release()
		environment, err := s.environments.Find(workspace.EnvironmentID)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return workspace, false, err
		}
		if err == nil && environment.Status == domain.EnvironmentClaiming && environment.Assignment != nil && environment.Assignment.Workspace.ID == workspace.ID {
			if s.now().Sub(environment.Assignment.ReservedAt) < abandonedClaimGrace {
				return workspace, false, running
			}
			cause := fmt.Errorf("the create of Workspace %q stopped before it claimed this environment", workspace.Name)
			if _, err := s.abandonClaim(environment, workspace, cause); !errors.Is(err, ErrEnvironmentFailed) {
				return workspace, false, err
			}
			return workspace, true, nil
		}
	}
	for index := range workspace.Steps {
		if workspace.Steps[index].Status == domain.StepRunning {
			workspace.Steps[index].Status = domain.StepUnknown
			workspace.Steps[index].Error = "the create process stopped while this step was running"
		}
	}
	workspace.Status = domain.WorkspaceSetupFailed
	workspace.UpdatedAt = s.now()
	if err := s.store.Save(workspace); err != nil {
		return workspace, false, err
	}
	return workspace, false, nil
}
