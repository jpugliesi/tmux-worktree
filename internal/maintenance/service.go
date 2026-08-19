package maintenance

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/jpugliesi/tmux-worktree/internal/store"
)

type StorageStatus struct {
	TotalBytes    int64 `json:"totalBytes"`
	CacheBytes    int64 `json:"cacheBytes"`
	ProjectBytes  int64 `json:"projectBytes"`
	CacheCount    int   `json:"cacheCount"`
	ProjectCount  int   `json:"projectCount"`
	WorktreeCount int   `json:"worktreeCount"`
}

type Check struct {
	Name    string `json:"name"`
	Status  string `json:"status"`
	Message string `json:"message"`
}

type DoctorReport struct {
	Healthy bool    `json:"healthy"`
	Checks  []Check `json:"checks"`
}

type Service struct {
	configDir string
	stateDir  string
	dataDir   string
}

func NewService(configDir, stateDir, dataDir string) *Service {
	return &Service{configDir: configDir, stateDir: stateDir, dataDir: dataDir}
}

func (s *Service) StorageStatus() (StorageStatus, error) {
	cacheRoot := filepath.Join(s.dataDir, "caches")
	projectRoot := filepath.Join(s.dataDir, "projects")
	cacheBytes, err := directoryBytes(cacheRoot)
	if err != nil {
		return StorageStatus{}, err
	}
	projectBytes, err := directoryBytes(projectRoot)
	if err != nil {
		return StorageStatus{}, err
	}
	cacheCount, err := directoryCount(cacheRoot)
	if err != nil {
		return StorageStatus{}, err
	}
	projects, err := store.NewProjectStore(s.stateDir).List()
	if err != nil {
		return StorageStatus{}, err
	}
	worktrees := 0
	for _, project := range projects {
		worktrees += len(project.Repositories)
	}
	return StorageStatus{
		TotalBytes: cacheBytes + projectBytes, CacheBytes: cacheBytes, ProjectBytes: projectBytes,
		CacheCount: cacheCount, ProjectCount: len(projects), WorktreeCount: worktrees,
	}, nil
}

func (s *Service) Doctor() DoctorReport {
	report := DoctorReport{Healthy: true, Checks: []Check{}}
	report.addCommand("git")
	report.addCommand("tmux")
	templates := store.NewTemplateStore(s.configDir)
	names, err := templates.List()
	if err != nil {
		report.addFailure("templates", err.Error())
	} else {
		valid := true
		for _, name := range names {
			if _, err := templates.Load(name); err != nil {
				report.addFailure("template:"+name, err.Error())
				valid = false
			}
		}
		if valid {
			report.addPass("templates", fmt.Sprintf("%d Project Templates are valid", len(names)))
		}
	}
	projects, err := store.NewProjectStore(s.stateDir).List()
	if err != nil {
		report.addFailure("projects", err.Error())
	} else {
		valid := true
		for _, project := range projects {
			data, err := os.ReadFile(filepath.Join(project.Root, ".twt2-owned.json"))
			if err != nil {
				report.addFailure("project:"+project.Name, "Project ownership marker is missing")
				valid = false
				continue
			}
			var marker struct {
				Owner     string `json:"owner"`
				ProjectID string `json:"projectId"`
			}
			if json.Unmarshal(data, &marker) != nil || marker.Owner != "twt2" || marker.ProjectID != project.ID {
				report.addFailure("project:"+project.Name, "Project ownership marker is invalid")
				valid = false
			}
		}
		if valid {
			report.addPass("projects", fmt.Sprintf("%d Project records are valid", len(projects)))
		}
	}
	return report
}

func (r *DoctorReport) addCommand(name string) {
	path, err := exec.LookPath(name)
	if err != nil {
		r.addFailure(name, name+" is not installed")
		return
	}
	r.addPass(name, path)
}

func (r *DoctorReport) addPass(name, message string) {
	r.Checks = append(r.Checks, Check{Name: name, Status: "pass", Message: message})
}

func (r *DoctorReport) addFailure(name, message string) {
	r.Healthy = false
	r.Checks = append(r.Checks, Check{Name: name, Status: "fail", Message: message})
}

func directoryBytes(root string) (int64, error) {
	var total int64
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if errors.Is(walkErr, os.ErrNotExist) {
			return nil
		}
		if walkErr != nil {
			return walkErr
		}
		if entry.Type().IsRegular() {
			info, err := entry.Info()
			if err != nil {
				return err
			}
			total += info.Size()
		}
		return nil
	})
	if errors.Is(err, os.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("measure %q: %w", root, err)
	}
	return total, nil
}

func directoryCount(root string) (int, error) {
	entries, err := os.ReadDir(root)
	if errors.Is(err, os.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("list %q: %w", root, err)
	}
	count := 0
	for _, entry := range entries {
		if entry.IsDir() {
			count++
		}
	}
	return count, nil
}
