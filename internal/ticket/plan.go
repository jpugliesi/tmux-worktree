package ticket

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/jpugliesi/tmux-worktree/internal/clierr"
	"github.com/jpugliesi/tmux-worktree/internal/store"
)

// ProjectPlanResult is one read or write of a Project plan document.
type ProjectPlanResult struct {
	Project   string `json:"project"`
	Path      string `json:"path"`
	Content   string `json:"content,omitempty"`
	UpdatedAt string `json:"updatedAt,omitempty"`
	// Created is true when a write created the file.
	Created bool `json:"created,omitempty"`
}

// projectPlanPath resolves the plan.md path of an existing Project.
func (s *Service) projectPlanPath(name string) (string, error) {
	home, err := s.home()
	if err != nil {
		return "", err
	}
	exists, err := projectDirectoryExists(home, name)
	if err != nil {
		return "", err
	}
	if !exists {
		return "", projectMissing(name)
	}
	return filepath.Join(home, name, "plan.md"), nil
}

// ProjectPlan reads the plan document of one Project. A missing plan is
// not_found with the init hint.
func (s *Service) ProjectPlan(name string) (ProjectPlanResult, error) {
	path, err := s.projectPlanPath(name)
	if err != nil {
		return ProjectPlanResult{}, err
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return ProjectPlanResult{}, clierr.WithHint(
			clierr.New(clierr.NotFound, "Project %q has no plan.md", name),
			"Run 'twt projects plan init %s', or pipe content to 'twt projects plan edit %s -'.", name, name)
	}
	if err != nil {
		return ProjectPlanResult{}, fmt.Errorf("read Project %q plan: %w", name, err)
	}
	info, err := os.Stat(path)
	if err != nil {
		return ProjectPlanResult{}, fmt.Errorf("stat Project %q plan: %w", name, err)
	}
	return ProjectPlanResult{
		Project: name, Path: path, Content: string(data),
		UpdatedAt: info.ModTime().UTC().Format(time.RFC3339),
	}, nil
}

// ProjectPlanPath returns the plan path of an existing Project. The file
// itself need not exist: this is the $EDITOR entry point.
func (s *Service) ProjectPlanPath(name string) (ProjectPlanResult, error) {
	path, err := s.projectPlanPath(name)
	if err != nil {
		return ProjectPlanResult{}, err
	}
	return ProjectPlanResult{Project: name, Path: path}, nil
}

// WriteProjectPlan replaces (or creates) the plan document of one Project.
// It is an upsert: agents need exactly one verb.
func (s *Service) WriteProjectPlan(name, content string, dryRun bool) (ProjectPlanResult, error) {
	if strings.TrimSpace(content) == "" {
		return ProjectPlanResult{}, clierr.WithHint(
			clierr.New(clierr.InvalidUsage, "the plan content is empty: refusing to erase plan.md"),
			"Pass the plan content on stdin.")
	}
	return syncWrite(s, syncBestEffort, dryRun, func() string {
		return fmt.Sprintf("twt: plan edit %s", name)
	}, func() (ProjectPlanResult, error) {
		return s.writeProjectPlanOnce(name, []byte(content), false, dryRun)
	})
}

// InitProjectPlan writes the plan scaffold. It refuses an existing plan.
func (s *Service) InitProjectPlan(name string, dryRun bool) (ProjectPlanResult, error) {
	return syncWrite(s, syncBestEffort, dryRun, func() string {
		return fmt.Sprintf("twt: plan init %s", name)
	}, func() (ProjectPlanResult, error) {
		return s.writeProjectPlanOnce(name, projectPlanContent(name, s.today()), true, dryRun)
	})
}

func (s *Service) writeProjectPlanOnce(name string, content []byte, initOnly, dryRun bool) (ProjectPlanResult, error) {
	if _, err := s.activeProject(name); err != nil {
		return ProjectPlanResult{}, err
	}
	path, err := s.projectPlanPath(name)
	if err != nil {
		return ProjectPlanResult{}, err
	}
	lock, err := store.AcquireNamedLock(s.options.StateDir, "project", name)
	if err != nil {
		return ProjectPlanResult{}, err
	}
	defer lock.Release()
	existed := fileExists(path)
	if initOnly && existed {
		return ProjectPlanResult{}, clierr.WithHint(
			clierr.New(clierr.PreconditionFailed, "Project %q already has plan.md", name),
			"Edit it with 'twt projects plan edit %s -'.", name)
	}
	result := ProjectPlanResult{Project: name, Path: path, Created: !existed}
	if dryRun {
		return result, nil
	}
	if err := store.WriteFileAtomic(path, content, 0o644, "Project plan"); err != nil {
		return ProjectPlanResult{}, err
	}
	return result, nil
}

// planTitle scans the first bounded chunk of a plan document for its H1
// heading. Failures return an empty title, never an error.
func planTitle(path string) string {
	file, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer file.Close()
	chunk := make([]byte, 4096)
	read, _ := file.Read(chunk)
	for _, line := range strings.Split(string(chunk[:read]), "\n") {
		if strings.HasPrefix(line, "# ") {
			return strings.TrimSpace(strings.TrimPrefix(line, "# "))
		}
	}
	return ""
}
