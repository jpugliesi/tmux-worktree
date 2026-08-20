package maintenance

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/jpugliesi/tmux-worktree/internal/domain"
	"github.com/jpugliesi/tmux-worktree/internal/store"
)

type StorageStatus struct {
	TotalBytes                int64 `json:"totalBytes"`
	CacheBytes                int64 `json:"cacheBytes"`
	ActiveProjectBytes        int64 `json:"activeProjectBytes"`
	ArchivedProjectBytes      int64 `json:"archivedProjectBytes"`
	PreparedBytes             int64 `json:"preparedBytes"`
	SnapshotBytes             int64 `json:"snapshotBytes"`
	CacheCount                int   `json:"cacheCount"`
	ProjectCount              int   `json:"projectCount"`
	ActiveProjectCount        int   `json:"activeProjectCount"`
	ArchivedProjectCount      int   `json:"archivedProjectCount"`
	WorktreeCount             int   `json:"worktreeCount"`
	PreparedEnvironmentCount  int   `json:"preparedEnvironmentCount"`
	ReadyEnvironmentCount     int   `json:"readyEnvironmentCount"`
	PreparingEnvironmentCount int   `json:"preparingEnvironmentCount"`
	FailedEnvironmentCount    int   `json:"failedEnvironmentCount"`
	PreparedWorktreeCount     int   `json:"preparedWorktreeCount"`
}

// EnvironmentInfo describes one Prepared Environment. Status is the record
// status, or "obsolete" for a ready Prepared Environment that no longer
// matches its Project Template.
type EnvironmentInfo struct {
	ID           string
	TemplateName string
	Status       string
	ReadyAt      *time.Time
	CreatedAt    time.Time
	Bytes        int64
	BaseCommits  map[string]string
	Failure      string
	LogPath      string
	Steps        []domain.SetupStep
	Project      *EnvironmentProject
}

// EnvironmentProject describes the Project that claims a Prepared Environment.
type EnvironmentProject struct {
	ID     string
	Name   string
	Status string
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
	cacheBytes, err := DirectoryBytes(cacheRoot)
	if err != nil {
		return StorageStatus{}, err
	}
	snapshotBytes, err := DirectoryBytes(filepath.Join(s.stateDir, "snapshots"))
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
	var activeBytes, archivedBytes int64
	activeCount, archivedCount := 0, 0
	for _, project := range projects {
		worktrees += len(project.Repositories)
		bytes, err := DirectoryBytes(project.Root)
		if err != nil {
			return StorageStatus{}, err
		}
		if project.Status == domain.ProjectArchived {
			archivedBytes += bytes
			archivedCount++
			continue
		}
		activeBytes += bytes
		activeCount++
	}
	environments, err := store.NewEnvironmentStore(s.stateDir).List()
	if err != nil {
		return StorageStatus{}, err
	}
	var preparedBytes int64
	preparedCount, readyCount, preparingCount, failedCount, preparedWorktrees := 0, 0, 0, 0, 0
	for _, environment := range environments {
		if environment.Status == "claimed" || environment.Status == "claiming" {
			continue
		}
		bytes, err := DirectoryBytes(environment.Root)
		if err != nil {
			return StorageStatus{}, err
		}
		preparedBytes += bytes
		preparedCount++
		preparedWorktrees += len(environment.Repositories)
		switch environment.Status {
		case "ready":
			readyCount++
		case "queued", "preparing":
			preparingCount++
		case "failed":
			failedCount++
		}
	}
	return StorageStatus{
		TotalBytes: cacheBytes + activeBytes + archivedBytes + preparedBytes + snapshotBytes, CacheBytes: cacheBytes,
		ActiveProjectBytes: activeBytes, ArchivedProjectBytes: archivedBytes, PreparedBytes: preparedBytes, SnapshotBytes: snapshotBytes,
		CacheCount: cacheCount, ProjectCount: len(projects), ActiveProjectCount: activeCount, ArchivedProjectCount: archivedCount, WorktreeCount: worktrees,
		PreparedEnvironmentCount: preparedCount, ReadyEnvironmentCount: readyCount,
		PreparingEnvironmentCount: preparingCount, FailedEnvironmentCount: failedCount, PreparedWorktreeCount: preparedWorktrees,
	}, nil
}

// EnvironmentReport describes each Prepared Environment record. It joins the
// Prepared Environment store, the Project Template digests, and the Project
// store. A Project Template that twt2 cannot load keeps the record status,
// because twt2 cannot know if its Prepared Environments are obsolete.
func (s *Service) EnvironmentReport() ([]EnvironmentInfo, error) {
	environments, err := store.NewEnvironmentStore(s.stateDir).List()
	if err != nil {
		return nil, err
	}
	digests, unreadable, err := s.templateDigests()
	if err != nil {
		return nil, err
	}
	projects, err := store.NewProjectStore(s.stateDir).List()
	if err != nil {
		return nil, err
	}
	live := make(map[string]domain.Project, len(projects))
	for _, project := range projects {
		live[project.ID] = project
	}
	report := make([]EnvironmentInfo, 0, len(environments))
	for _, environment := range environments {
		info := EnvironmentInfo{
			ID: environment.ID, TemplateName: environment.TemplateName, Status: string(environment.Status),
			ReadyAt: environment.ReadyAt, CreatedAt: environment.CreatedAt, Failure: environment.Failure,
			BaseCommits: map[string]string{}, Steps: environment.Steps,
		}
		if environment.Status == domain.EnvironmentReady && !unreadable[environment.TemplateName] &&
			!digests[environment.TemplateName].Matches(environment.TemplateDigest) {
			info.Status = "obsolete"
		}
		if bytes, err := DirectoryBytes(environment.Root); err == nil {
			info.Bytes = bytes
		}
		for _, repository := range environment.Repositories {
			info.BaseCommits[repository.Name] = shortCommit(repository.BaseCommit)
		}
		info.LogPath = s.prepareLogPath(environment.ID)
		if environment.ClaimReservation != nil {
			reserved := environment.ClaimReservation.Project
			claim := EnvironmentProject{ID: reserved.ID, Name: reserved.Name, Status: string(reserved.Status)}
			if current, found := live[reserved.ID]; found {
				claim.Name = current.Name
				claim.Status = string(current.Status)
			}
			info.Project = &claim
		}
		report = append(report, info)
	}
	return report, nil
}

// templateDigests returns the digests of each Project Template that twt2 can
// load, and the names of the Project Templates that it cannot load.
func (s *Service) templateDigests() (map[string]store.DigestSet, map[string]bool, error) {
	templates := store.NewTemplateStore(s.configDir)
	names, err := templates.List()
	if err != nil {
		return nil, nil, err
	}
	digests := make(map[string]store.DigestSet, len(names))
	unreadable := map[string]bool{}
	for _, name := range names {
		template, err := templates.Load(name)
		if err != nil {
			unreadable[name] = true
			continue
		}
		digestSet, err := store.Digests(template)
		if err != nil {
			unreadable[name] = true
			continue
		}
		digests[name] = digestSet
	}
	return digests, unreadable, nil
}

// prepareLogPath returns the twt2-owned preparation log of one Prepared
// Environment, or an empty value when the log does not exist.
func (s *Service) prepareLogPath(environmentID string) string {
	path := filepath.Join(s.stateDir, "logs", "prepare-"+environmentID+".log")
	if _, err := os.Stat(path); err != nil {
		return ""
	}
	return path
}

func shortCommit(commit string) string {
	if len(commit) > 7 {
		return commit[:7]
	}
	return commit
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
	environments, err := store.NewEnvironmentStore(s.stateDir).List()
	if err != nil {
		report.addFailure("prepared-environments", err.Error())
	} else {
		valid := true
		for _, environment := range environments {
			if environment.Status == "queued" {
				continue
			}
			markerName := ".twt2-environment.json"
			if environment.Status == "claiming" || environment.Status == "claimed" {
				markerName = ".twt2-owned.json"
			}
			if _, err := os.Stat(filepath.Join(environment.Root, markerName)); err != nil {
				if (environment.Status == "preparing" || environment.Status == "failed") && os.IsNotExist(err) {
					continue
				}
				report.addFailure("environment:"+environment.ID, "Prepared Environment ownership marker is missing")
				valid = false
			}
		}
		if valid {
			report.addPass("prepared-environments", fmt.Sprintf("%d Prepared Environment records are valid", len(environments)))
		}
		for _, environment := range environments {
			if environment.Status != domain.EnvironmentFailed {
				continue
			}
			report.addWarning("environment:"+environment.ID, s.failedEnvironmentMessage(environment))
		}
	}
	return report
}

// failedEnvironmentMessage tells a person why one Prepared Environment failed
// and how to remove it. A failed Prepared Environment wastes disk space, but it
// does not make the installation unhealthy.
func (s *Service) failedEnvironmentMessage(environment domain.PreparedEnvironment) string {
	failure := environment.Failure
	if failure == "" {
		failure = "twt2 did not record a reason"
	}
	message := fmt.Sprintf("Prepared Environment failed: %s.", failure)
	if log := s.prepareLogPath(environment.ID); log != "" {
		message += fmt.Sprintf(" See %s.", log)
	}
	return message + " Run 'twt2 storage clean --apply' to remove it."
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

// addWarning reports a problem that a person can repair later. It keeps the
// report healthy, so twt2 doctor still ends with success.
func (r *DoctorReport) addWarning(name, message string) {
	r.Checks = append(r.Checks, Check{Name: name, Status: "warn", Message: message})
}

// DirectoryBytes returns the total size of the regular files under root. A
// directory that does not exist has size zero.
func DirectoryBytes(root string) (int64, error) {
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
