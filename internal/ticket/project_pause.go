package ticket

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/jpugliesi/tmux-worktree/internal/clierr"
	"github.com/jpugliesi/tmux-worktree/internal/domain"
	"github.com/jpugliesi/tmux-worktree/internal/store"
)

// PauseProject hides one Project from default lists. The Project stays
// writable. Close still ends the Project.
func (s *Service) PauseProject(name string, dryRun bool) (domain.Project, error) {
	return syncWrite(s, syncRequired, dryRun, func() string {
		return fmt.Sprintf("twt: pause project %s", name)
	}, func() (domain.Project, error) {
		return s.setProjectPaused(name, true, dryRun)
	})
}

// ResumeProject returns one paused Project to the default list. Resume does
// not open a closed Project.
func (s *Service) ResumeProject(name string, dryRun bool) (domain.Project, error) {
	return syncWrite(s, syncRequired, dryRun, func() string {
		return fmt.Sprintf("twt: resume project %s", name)
	}, func() (domain.Project, error) {
		return s.setProjectPaused(name, false, dryRun)
	})
}

func (s *Service) setProjectPaused(name string, paused, dryRun bool) (domain.Project, error) {
	home, err := s.home()
	if err != nil {
		return domain.Project{}, err
	}
	lock, err := store.AcquireNamedLock(s.options.StateDir, "project", name)
	if err != nil {
		return domain.Project{}, err
	}
	defer lock.Release()
	project, err := s.Project(name)
	if err != nil {
		return domain.Project{}, err
	}
	if !project.HasIndex {
		return domain.Project{}, clierr.WithHint(
			clierr.New(clierr.PreconditionFailed, "Project %q has no index.md", name),
			"Run 'twt projects create %s' to add the Project index.", name)
	}
	if project.Closed {
		return domain.Project{}, closedProject(name)
	}
	if project.Paused == paused {
		return project, nil
	}
	project.Paused = paused
	if dryRun {
		return project, nil
	}
	indexPath := filepath.Join(home, name, "index.md")
	raw, err := os.ReadFile(indexPath)
	if err != nil {
		return domain.Project{}, fmt.Errorf("read Project %q index: %w", name, err)
	}
	file, err := ParseTicketFile(indexPath, raw)
	if err != nil {
		return domain.Project{}, err
	}
	mapping := file.ensureMapping()
	if paused {
		setMapBool(mapping, "twt_paused", true)
	} else {
		setMapNull(mapping, "twt_paused")
	}
	content, err := file.Render()
	if err != nil {
		return domain.Project{}, err
	}
	if err := store.WriteFileAtomic(indexPath, content, 0o644, "Project index"); err != nil {
		return domain.Project{}, err
	}
	return s.projectInfo(home, name)
}

func (s *Service) pausedProjectNames() (map[string]bool, error) {
	projects, err := s.AllProjects()
	if err != nil {
		return nil, err
	}
	names := make(map[string]bool)
	for _, project := range projects {
		if project.Paused && !project.Closed {
			names[project.Name] = true
		}
	}
	return names, nil
}
