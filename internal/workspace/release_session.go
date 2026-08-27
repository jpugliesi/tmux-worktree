package workspace

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/jpugliesi/tmux-worktree/internal/agent"
	"github.com/jpugliesi/tmux-worktree/internal/clierr"
	"github.com/jpugliesi/tmux-worktree/internal/domain"
	"github.com/jpugliesi/tmux-worktree/internal/store"
)

// PreparedRelease records cleanup that finished inside the source tmux
// session. The Environment stays unavailable until reconciliation confirms
// that SourceSessionID no longer exists.
type PreparedRelease struct {
	ArchiveResult   ArchiveResult
	SourceSessionID string
}

// PrepareReleaseFromPane performs release cleanup in the caller pane. It
// stops every other pane, then leaves the source session as the last process
// that can touch the prepared worktrees.
func (s *Service) PrepareReleaseFromPane(reference, currentPane string, opts ReleaseOptions) (prepared PreparedRelease, err error) {
	workspace, sourceSessionID, callerPaneID, err := s.validateArchiveFromPane(reference, currentPane)
	if err != nil {
		return PreparedRelease{}, err
	}
	stopped, err := agent.NewService(s.options.StateDir, s.options.TmuxSocket).Live(workspace.ID)
	if err != nil {
		return PreparedRelease{ArchiveResult: ArchiveResult{Workspace: workspace}}, err
	}
	result := ArchiveResult{Workspace: workspace, StoppedAgents: stopped}
	if workspace.Adopted || workspace.EnvironmentID == "" {
		if err := s.stopSessionPanesExcept(sourceSessionID, callerPaneID); err != nil {
			return PreparedRelease{ArchiveResult: result}, err
		}
		now := s.now()
		workspace.Status = domain.WorkspaceArchived
		workspace.ArchivedAt = &now
		workspace.UpdatedAt = now
		if err := s.store.Save(workspace); err != nil {
			return PreparedRelease{ArchiveResult: result}, err
		}
		if err := s.clearAgentPanesExcept(workspace.ID, callerPaneID); err != nil {
			return PreparedRelease{ArchiveResult: result}, err
		}
		result.Workspace = workspace
		return PreparedRelease{ArchiveResult: result, SourceSessionID: sourceSessionID}, nil
	}

	if pending, ok, pendingErr := s.pendingRelease(workspace, sourceSessionID); pendingErr != nil {
		return PreparedRelease{ArchiveResult: result}, pendingErr
	} else if ok {
		if err := s.stopSessionPanesExcept(sourceSessionID, callerPaneID); err != nil {
			return PreparedRelease{ArchiveResult: result}, err
		}
		if err := s.clearAgentPanesExcept(workspace.ID, callerPaneID); err != nil {
			return PreparedRelease{ArchiveResult: result}, err
		}
		result.Workspace = pending
		return PreparedRelease{ArchiveResult: result, SourceSessionID: sourceSessionID}, nil
	}

	plan, err := s.releasePlan(workspace, opts)
	if err != nil {
		return PreparedRelease{ArchiveResult: result}, err
	}
	environmentLock, err := s.reserveRelease(&workspace, plan, opts, sourceSessionID)
	if err != nil {
		return PreparedRelease{ArchiveResult: result}, err
	}
	defer environmentLock.Release()
	rollback := true
	defer func() {
		if err == nil || !rollback {
			return
		}
		if rollbackErr := s.rollbackRelease(workspace); rollbackErr != nil {
			err = errors.Join(err, fmt.Errorf("restore Workspace after failed release: %w", rollbackErr))
		}
	}()
	if err = s.stopSessionPanesExcept(sourceSessionID, callerPaneID); err != nil {
		return PreparedRelease{ArchiveResult: result}, err
	}
	if err = s.clearAgentPanesExcept(workspace.ID, callerPaneID); err != nil {
		return PreparedRelease{ArchiveResult: result}, err
	}
	environment, err := s.cleanReleasedEnvironment(workspace, plan, opts)
	if err != nil {
		return PreparedRelease{ArchiveResult: result}, err
	}
	now := s.now()
	workspace.Status = domain.WorkspaceArchived
	workspace.ArchivedAt = &now
	workspace.UpdatedAt = now
	environment.Status = domain.EnvironmentReleasing
	environment.Assignment.Phase = domain.EnvironmentAssignmentSessionStopPending
	environment.Assignment.SourceSessionID = sourceSessionID
	environment.Assignment.Workspace = workspace
	environment.UpdatedAt = now
	if err = s.environments.Save(environment); err != nil {
		return PreparedRelease{ArchiveResult: result}, err
	}
	if err = s.store.Save(workspace); err != nil {
		return PreparedRelease{ArchiveResult: result}, err
	}
	rollback = false
	result.Workspace = workspace
	return PreparedRelease{ArchiveResult: result, SourceSessionID: sourceSessionID}, nil
}

// StopPreparedRelease performs the last tmux operation. It is idempotent when
// the exact source session has already stopped.
func (s *Service) StopPreparedRelease(prepared PreparedRelease) error {
	workspace := prepared.ArchiveResult.Workspace
	if workspace.ID == "" || prepared.SourceSessionID == "" {
		return clierr.New(clierr.InvalidUsage, "Prepared release has no Workspace or source tmux session")
	}
	mutationLock, err := store.AcquireMutationLock(s.options.StateDir)
	if err != nil {
		return err
	}
	defer mutationLock.Release()
	latest, err := s.store.Find(workspace.ID)
	if err != nil {
		return err
	}
	if latest.EnvironmentID != "" {
		environmentLock, err := store.AcquireEnvironmentLockBlocking(s.options.StateDir, latest.EnvironmentID)
		if err != nil {
			return err
		}
		defer environmentLock.Release()
		environment, err := s.environments.Find(latest.EnvironmentID)
		if err != nil {
			return err
		}
		if environment.Status != domain.EnvironmentReleasing || environment.Assignment == nil ||
			environment.Assignment.Kind != domain.EnvironmentAssignmentRelease ||
			environment.Assignment.Phase != domain.EnvironmentAssignmentSessionStopPending ||
			environment.Assignment.Workspace.ID != workspace.ID ||
			environment.Assignment.SourceSessionID != prepared.SourceSessionID {
			return clierr.New(clierr.UnsafeState, "Workspace %q has no matching prepared release", latest.Name)
		}
	} else if latest.Status == domain.WorkspaceArchived && !latest.Materialized {
		return nil
	} else if !latest.Adopted || latest.Status != domain.WorkspaceArchived {
		return clierr.New(clierr.UnsafeState, "Workspace %q has no matching prepared release", latest.Name)
	}
	present, err := s.preparedSessionPresent(prepared.SourceSessionID, workspace.ID)
	if err != nil {
		return err
	}
	if !present {
		return nil
	}
	return s.stopPreparedSession(prepared.SourceSessionID)
}

func (s *Service) pendingRelease(workspace domain.Workspace, sourceSessionID string) (domain.Workspace, bool, error) {
	environment, err := s.environments.Find(workspace.EnvironmentID)
	if err != nil {
		return workspace, false, err
	}
	if environment.Status != domain.EnvironmentReleasing || environment.Assignment == nil ||
		environment.Assignment.Kind != domain.EnvironmentAssignmentRelease ||
		environment.Assignment.Phase != domain.EnvironmentAssignmentSessionStopPending {
		return workspace, false, nil
	}
	if environment.Assignment.Workspace.ID != workspace.ID || environment.Assignment.SourceSessionID != sourceSessionID {
		return workspace, false, clierr.New(clierr.UnsafeState, "Workspace %q has a conflicting pending release", workspace.Name)
	}
	return environment.Assignment.Workspace, true, nil
}

func (s *Service) rollbackRelease(workspace domain.Workspace) error {
	environment, err := s.environments.Find(workspace.EnvironmentID)
	if err != nil {
		return err
	}
	if environment.Status != domain.EnvironmentReleasing || environment.Assignment == nil || environment.Assignment.Workspace.ID != workspace.ID {
		return fmt.Errorf("Prepared Environment %q has no matching release assignment", environment.ID)
	}
	workspace.Status = domain.WorkspaceActive
	workspace.ArchivedAt = nil
	workspace.UpdatedAt = s.now()
	if err := s.restoreReleaseOwnership(workspace, &environment); err != nil {
		return err
	}
	if err := s.store.Save(workspace); err != nil {
		return err
	}
	environment.Generation++
	environment.Status = domain.EnvironmentClaimed
	environment.Assignment = &domain.EnvironmentAssignment{
		Generation: environment.Generation, Kind: domain.EnvironmentAssignmentClaim,
		Phase: domain.EnvironmentAssignmentActive, Workspace: workspace, ReservedAt: s.now(),
	}
	environment.UpdatedAt = s.now()
	if err := s.environments.Save(environment); err != nil {
		return err
	}
	return s.ensureTmux(&workspace, claimUnownedSession)
}

func (s *Service) restoreReleaseOwnership(workspace domain.Workspace, environment *domain.PreparedEnvironment) error {
	for _, repository := range workspace.Repositories {
		if err := s.withCacheLock(repository.CachePath, func() error {
			branch, err := output(repository.Path, "git", "branch", "--show-current")
			if err != nil {
				return err
			}
			if branch == repository.Branch {
				return nil
			}
			if err := run(repository.Path, "git", "switch", repository.Branch); err != nil {
				return fmt.Errorf("restore Workspace branch for repository %q: %w", repository.Name, err)
			}
			return nil
		}); err != nil {
			return err
		}
	}
	marker := map[string]string{"owner": "twt", "workspaceId": workspace.ID, "environmentId": environment.ID}
	if err := writeJSON(filepath.Join(workspace.Root, ".twt-owned.json"), marker, 0o600); err != nil {
		return err
	}
	_ = os.Remove(filepath.Join(workspace.Root, environmentMarkerName))
	return nil
}

func (s *Service) preparedSessionPresent(sourceSessionID, workspaceID string) (bool, error) {
	rows, err := s.workspaceSessionRows(false)
	if err != nil {
		message := err.Error()
		if strings.Contains(message, "no server running") || strings.Contains(message, "no sessions") || strings.Contains(message, "error connecting to") {
			return false, nil
		}
		return false, fmt.Errorf("inspect the source tmux session: %w", err)
	}
	for _, row := range rows {
		if row.id == sourceSessionID && row.ownerID != workspaceID {
			return false, clierr.New(clierr.UnsafeState, "tmux session %q no longer belongs to Workspace %q", sourceSessionID, workspaceID)
		}
		if row.ownerID == workspaceID {
			return true, nil
		}
	}
	return false, nil
}
