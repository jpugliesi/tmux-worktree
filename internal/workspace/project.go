package workspace

import (
	"strings"

	"github.com/jpugliesi/tmux-worktree/internal/clierr"
	"github.com/jpugliesi/tmux-worktree/internal/domain"
	"github.com/jpugliesi/tmux-worktree/internal/store"
)

// ValidateSetProject checks a Workspace Project change without writing.
func (s *Service) ValidateSetProject(reference, project string) error {
	_, err := s.validateSetProject(reference, project)
	return err
}

// SetProject records the Ticket Project on one Workspace. It does not move
// Tickets or checkouts. The caller must confirm that linked Tickets belong
// to that Project.
func (s *Service) SetProject(reference, project string) (domain.Workspace, error) {
	lock, err := store.AcquireMutationLock(s.options.StateDir)
	if err != nil {
		return domain.Workspace{}, err
	}
	defer lock.Release()
	workspace, err := s.validateSetProject(reference, project)
	if err != nil {
		return domain.Workspace{}, err
	}
	if workspace.Project == project {
		return workspace, nil
	}
	workspace.Project = project
	workspace.UpdatedAt = s.now()
	if err := s.store.Save(workspace); err != nil {
		return domain.Workspace{}, err
	}
	return workspace, nil
}

// RetargetProject rewrites Workspace Project links from oldName to newName.
func (s *Service) RetargetProject(oldName, newName string) error {
	oldName = strings.TrimSpace(oldName)
	newName = strings.TrimSpace(newName)
	if oldName == "" || newName == "" || oldName == newName {
		return nil
	}
	lock, err := store.AcquireMutationLock(s.options.StateDir)
	if err != nil {
		return err
	}
	defer lock.Release()
	workspaces, err := s.store.List()
	if err != nil {
		return err
	}
	for _, workspace := range workspaces {
		if workspace.Project != oldName {
			continue
		}
		workspace.Project = newName
		workspace.UpdatedAt = s.now()
		if err := s.store.Save(workspace); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) validateSetProject(reference, project string) (domain.Workspace, error) {
	project = strings.TrimSpace(project)
	if project == "" {
		return domain.Workspace{}, clierr.WithHint(
			clierr.New(clierr.InvalidUsage, "the Project name is empty"),
			"Pass --project PROJECT.")
	}
	if err := store.ValidateResourceName(project); err != nil {
		return domain.Workspace{}, clierr.Wrap(clierr.InvalidUsage, err)
	}
	workspace, err := s.store.Find(reference)
	if err != nil {
		return domain.Workspace{}, err
	}
	return workspace, nil
}
