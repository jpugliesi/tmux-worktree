package workspace

import (
	"fmt"

	"github.com/jpugliesi/tmux-worktree/internal/clierr"
	"github.com/jpugliesi/tmux-worktree/internal/domain"
	"github.com/jpugliesi/tmux-worktree/internal/store"
)

// ValidateRename checks a Workspace rename without changing state.
func (s *Service) ValidateRename(reference, name string) error {
	_, err := s.validateRename(reference, name)
	return err
}

// Rename changes the display name of a Workspace and the owned tmux session
// name. Its immutable resources keep their existing names and paths.
func (s *Service) Rename(reference, name string) (domain.Workspace, error) {
	lock, err := store.AcquireMutationLock(s.options.StateDir)
	if err != nil {
		return domain.Workspace{}, err
	}
	defer lock.Release()
	workspace, err := s.validateRename(reference, name)
	if err != nil {
		return domain.Workspace{}, err
	}
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
	if workspace.Name == name && workspace.TmuxSession == storedSession {
		return workspace, nil
	}
	workspace.Name = name
	workspace.TmuxSession = storedSession
	workspace.UpdatedAt = s.now()
	if err := s.store.Save(workspace); err != nil {
		return domain.Workspace{}, err
	}
	return workspace, nil
}

func (s *Service) validateRename(reference, name string) (domain.Workspace, error) {
	if err := store.ValidateResourceName(name); err != nil {
		return domain.Workspace{}, fmt.Errorf("invalid Workspace name: %w", err)
	}
	workspace, err := s.store.Find(reference)
	if err != nil {
		return domain.Workspace{}, err
	}
	workspaces, err := s.store.List()
	if err != nil {
		return domain.Workspace{}, err
	}
	for _, existing := range workspaces {
		if existing.ID != workspace.ID && (existing.Name == name || existing.ID == name) {
			return domain.Workspace{}, clierr.New(clierr.AlreadyExists, "Workspace %q already exists", name)
		}
	}
	switch workspace.Status {
	case domain.WorkspaceActive, domain.WorkspaceArchived, domain.WorkspaceSetupFailed:
	case domain.WorkspaceInitializing, domain.WorkspaceRemoving:
		return domain.Workspace{}, clierr.New(clierr.PreconditionFailed, "Workspace %q has status %q and cannot be renamed", workspace.Name, workspace.Status)
	default:
		return domain.Workspace{}, clierr.New(clierr.PreconditionFailed, "Workspace %q has invalid status %q", workspace.Name, workspace.Status)
	}
	if err := s.validateRenameSessionAvailable(workspace, sessionName(workspace.TemplateName, name)); err != nil {
		return domain.Workspace{}, err
	}
	return workspace, nil
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
