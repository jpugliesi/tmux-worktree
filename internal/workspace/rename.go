package workspace

import (
	"errors"
	"fmt"
	"os"

	"github.com/jpugliesi/tmux-worktree/internal/clierr"
	"github.com/jpugliesi/tmux-worktree/internal/domain"
	"github.com/jpugliesi/tmux-worktree/internal/store"
)

// RenameOptions changes how Rename treats a new name that an archived
// Workspace still holds.
type RenameOptions struct {
	// RemoveArchived removes the archived Workspace that holds the new name
	// before the rename. The removal follows the normal removal plan and
	// stops on its blockers.
	RemoveArchived bool
	// CurrentPane is the tmux pane of the caller, for the removal plan.
	CurrentPane string
}

// RenamePlan describes one rename before it runs.
type RenamePlan struct {
	Workspace domain.Workspace
	// ArchivedHolder is the archived Workspace that holds the new name, when
	// one exists.
	ArchivedHolder *domain.Workspace
	// Removal is the removal plan for ArchivedHolder when the options ask
	// for the removal.
	Removal *RemovalPlan
}

// ValidateRename checks a Workspace rename without changing state.
func (s *Service) ValidateRename(reference, name string) error {
	_, err := s.validateRename(reference, name, RenameOptions{})
	return err
}

// PlanRename checks a Workspace rename with options and returns the plan.
func (s *Service) PlanRename(reference, name string, opts RenameOptions) (RenamePlan, error) {
	return s.validateRename(reference, name, opts)
}

// Rename changes the display name of a Workspace and the owned tmux session
// name. Its immutable resources keep their existing names and paths.
func (s *Service) Rename(reference, name string) (domain.Workspace, error) {
	return s.RenameWithOptions(reference, name, RenameOptions{})
}

// RenameWithOptions renames a Workspace. With RemoveArchived, it first
// removes the archived Workspace that holds the new name. The removal is its
// own mutation with its own lock, so it runs before the rename lock.
func (s *Service) RenameWithOptions(reference, name string, opts RenameOptions) (domain.Workspace, error) {
	plan, err := s.validateRename(reference, name, opts)
	if err != nil {
		return domain.Workspace{}, err
	}
	if plan.Removal != nil {
		s.report("Removing archived Workspace %q to free its name.", plan.ArchivedHolder.Name)
		if _, err := s.Remove(plan.ArchivedHolder.ID, opts.CurrentPane, RemovalOptions{AllowUnpublished: true}); err != nil {
			return domain.Workspace{}, err
		}
	}
	lock, err := store.AcquireMutationLock(s.options.StateDir)
	if err != nil {
		return domain.Workspace{}, err
	}
	defer lock.Release()
	plan, err = s.validateRename(reference, name, opts)
	if err != nil {
		return domain.Workspace{}, err
	}
	workspace := plan.Workspace
	desiredSession := sessionName(workspace.TemplateName, name)
	owned, hasSession, err := s.ownedSessionRow(workspace.ID)
	if err != nil {
		return domain.Workspace{}, err
	}
	if hasSession && owned.name != desiredSession {
		if err := s.renameSession(owned.id, desiredSession); err != nil {
			return domain.Workspace{}, err
		}
	}
	storedSession := workspace.TmuxSession
	if hasSession || storedSession != "" {
		storedSession = desiredSession
	}
	if workspace.Name != name || workspace.TmuxSession != storedSession {
		workspace.Name = name
		workspace.TmuxSession = storedSession
		workspace.UpdatedAt = s.now()
		if err := s.store.Save(workspace); err != nil {
			return domain.Workspace{}, err
		}
	}
	if err := s.syncEnvironmentAssignmentWorkspace(workspace); err != nil {
		return domain.Workspace{}, err
	}
	return workspace, nil
}

func (s *Service) syncEnvironmentAssignmentWorkspace(workspace domain.Workspace) error {
	if workspace.EnvironmentID == "" {
		return nil
	}
	environment, err := s.environments.Find(workspace.EnvironmentID)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	if environment.Assignment == nil || environment.Assignment.Workspace.ID != workspace.ID {
		return nil
	}
	if environment.Assignment.Workspace.Name == workspace.Name &&
		environment.Assignment.Workspace.TmuxSession == workspace.TmuxSession &&
		environment.Assignment.Workspace.Status == workspace.Status &&
		environment.Assignment.Workspace.UpdatedAt.Equal(workspace.UpdatedAt) {
		return nil
	}
	environment.Assignment.Workspace = workspace
	environment.UpdatedAt = workspace.UpdatedAt
	return s.environments.Save(environment)
}

func (s *Service) validateRename(reference, name string, opts RenameOptions) (RenamePlan, error) {
	if err := store.ValidateResourceName(name); err != nil {
		return RenamePlan{}, fmt.Errorf("invalid Workspace name: %w", err)
	}
	workspace, err := s.store.Find(reference)
	if err != nil {
		return RenamePlan{}, err
	}
	plan := RenamePlan{Workspace: workspace}
	workspaces, err := s.store.List()
	if err != nil {
		return plan, err
	}
	for _, existing := range workspaces {
		if existing.ID == workspace.ID || (existing.Name != name && existing.ID != name) {
			continue
		}
		if existing.Status == domain.WorkspaceArchived && existing.Name == name {
			holder := existing
			plan.ArchivedHolder = &holder
			continue
		}
		return plan, clierr.New(clierr.AlreadyExists, "Workspace %q already exists", name)
	}
	if plan.ArchivedHolder != nil {
		if !opts.RemoveArchived {
			return plan, clierr.WithHint(
				clierr.New(clierr.AlreadyExists, "archived Workspace %q already exists", name),
				"Use --remove-archived to remove archived Workspace %q and reuse its name.", name)
		}
		removal, err := s.PlanRemoval(plan.ArchivedHolder.ID, opts.CurrentPane, RemovalOptions{AllowUnpublished: true})
		if err != nil {
			return plan, err
		}
		if len(removal.Blockers) > 0 {
			return plan, removalRefusal(plan.ArchivedHolder.Name, removal.Blockers)
		}
		plan.Removal = &removal
	}
	switch workspace.Status {
	case domain.WorkspaceActive, domain.WorkspaceArchived, domain.WorkspaceSetupFailed:
	case domain.WorkspaceInitializing, domain.WorkspaceRemoving:
		return plan, clierr.New(clierr.PreconditionFailed, "Workspace %q has status %q and cannot be renamed", workspace.Name, workspace.Status)
	default:
		return plan, clierr.New(clierr.PreconditionFailed, "Workspace %q has invalid status %q", workspace.Name, workspace.Status)
	}
	if err := s.validateRenameSessionAvailable(workspace, sessionName(workspace.TemplateName, name)); err != nil {
		return plan, err
	}
	return plan, nil
}

func (s *Service) validateRenameSessionAvailable(workspace domain.Workspace, desired string) error {
	rows, err := s.workspaceSessionRows(true)
	if err != nil {
		if tmuxUnavailable(err) {
			return nil
		}
		return err
	}
	for _, row := range rows {
		if row.ownerID == workspace.ID {
			continue
		}
		if row.name == desired {
			return clierr.New(clierr.AlreadyExists, "tmux session %q already exists", desired)
		}
	}
	return nil
}

func (s *Service) ownedSessionRow(workspaceID string) (tmuxSessionRow, bool, error) {
	rows, err := s.workspaceSessionRows(true)
	if err != nil {
		if tmuxUnavailable(err) {
			return tmuxSessionRow{}, false, nil
		}
		return tmuxSessionRow{}, false, err
	}
	for _, row := range rows {
		if row.ownerID == workspaceID {
			return row, true, nil
		}
	}
	return tmuxSessionRow{}, false, nil
}

func (s *Service) renameSession(sessionID, name string) error {
	if err := run("", "tmux", s.tmuxArgs("rename-session", "-t", sessionID, name)...); err != nil {
		return fmt.Errorf("rename tmux session: %w", err)
	}
	return nil
}
