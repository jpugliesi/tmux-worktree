package ticket

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/jpugliesi/tmux-worktree/internal/clierr"
	"github.com/jpugliesi/tmux-worktree/internal/domain"
	"github.com/jpugliesi/tmux-worktree/internal/store"
)

// RenameProject moves one Project directory and its closed Ticket tree to a
// new name. Ticket Project comes from the path. Rename heals the project
// frontmatter so files match.
func (s *Service) RenameProject(name, newName string, dryRun bool) (domain.Project, error) {
	return syncWrite(s, syncRequired, dryRun, func() string {
		return fmt.Sprintf("twt: rename project %s to %s", name, newName)
	}, func() (domain.Project, error) {
		return s.renameProjectOnce(name, newName, dryRun)
	})
}

func (s *Service) renameProjectOnce(name, newName string, dryRun bool) (domain.Project, error) {
	name = strings.TrimSpace(name)
	newName = strings.TrimSpace(newName)
	if err := store.ValidateResourceName(newName); err != nil {
		return domain.Project{}, clierr.Wrap(clierr.InvalidUsage, err)
	}
	if reservedProjectName(newName) {
		return domain.Project{}, clierr.New(clierr.InvalidUsage, "the Project name %q is reserved", newName)
	}
	home, err := s.home()
	if err != nil {
		return domain.Project{}, err
	}
	oldLock, err := store.AcquireNamedLock(s.options.StateDir, "project", name)
	if err != nil {
		return domain.Project{}, err
	}
	defer oldLock.Release()
	project, err := s.Project(name)
	if err != nil {
		return domain.Project{}, err
	}
	if name == newName {
		return project, nil
	}
	newLock, err := store.AcquireNamedLock(s.options.StateDir, "project", newName)
	if err != nil {
		return domain.Project{}, err
	}
	defer newLock.Release()
	exists, err := projectDirectoryExists(home, newName)
	if err != nil {
		return domain.Project{}, err
	}
	if exists {
		return domain.Project{}, clierr.New(clierr.AlreadyExists, "Project %q already exists", newName)
	}
	if _, err := s.Project(newName); err == nil {
		return domain.Project{}, clierr.New(clierr.AlreadyExists, "Project %q already exists", newName)
	} else if clierr.CodeOf(err) != clierr.NotFound {
		return domain.Project{}, err
	}
	if dryRun {
		project.Name = newName
		project.Path = filepath.Join(home, newName)
		return project, nil
	}
	oldDir := filepath.Join(home, name)
	newDir := filepath.Join(home, newName)
	if oldExists, existsErr := projectDirectoryExists(home, name); existsErr != nil {
		return domain.Project{}, existsErr
	} else if oldExists {
		if err := os.Rename(oldDir, newDir); err != nil {
			return domain.Project{}, fmt.Errorf("rename Project %q to %q: %w", name, newName, err)
		}
	}
	oldClosed := filepath.Join(home, closedDirectoryName, name)
	newClosed := filepath.Join(home, closedDirectoryName, newName)
	if closedExists, closedErr := regularTicketDirectory(oldClosed, "closed Project"); closedErr != nil {
		return domain.Project{}, closedErr
	} else if closedExists {
		if err := os.MkdirAll(filepath.Dir(newClosed), 0o755); err != nil {
			return domain.Project{}, fmt.Errorf("create closed Project parent: %w", err)
		}
		if err := os.Rename(oldClosed, newClosed); err != nil {
			return domain.Project{}, fmt.Errorf("rename closed Tickets for Project %q: %w", name, err)
		}
	}
	if err := s.healRenamedProjectTickets(home, newName); err != nil {
		return domain.Project{}, err
	}
	return s.Project(newName)
}

func (s *Service) healRenamedProjectTickets(home, projectName string) error {
	idx, err := buildIndex(home)
	if err != nil {
		return err
	}
	for path, ticket := range idx.tickets {
		if ticket.Project != projectName {
			continue
		}
		if parseErr := idx.skipped[ticket.Slug]; parseErr != nil {
			return parseErr
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("read ticket %q: %w", path, err)
		}
		file, err := ParseTicketFile(path, raw)
		if err != nil {
			return err
		}
		healProject(file.ensureMapping(), home, path)
		content, err := file.Render()
		if err != nil {
			return err
		}
		if err := store.WriteFileAtomic(path, content, 0o644, "Ticket"); err != nil {
			return err
		}
	}
	return nil
}
