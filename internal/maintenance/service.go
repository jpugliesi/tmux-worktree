package maintenance

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/jpugliesi/tmux-worktree/internal/domain"
	projectservice "github.com/jpugliesi/tmux-worktree/internal/project"
	"github.com/jpugliesi/tmux-worktree/internal/store"
	"github.com/jpugliesi/tmux-worktree/skills"
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
	// Warnings holds one message for each directory that twt could not
	// measure. Its size counts as zero.
	Warnings []string `json:"warnings,omitempty"`
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
	// SizeWarning tells why twt could not measure the Prepared Environment
	// root. Bytes is zero in that case.
	SizeWarning string
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
	// ticketsHome is the resolved Tickets home, or empty when no source sets
	// one. Doctor reports its state.
	ticketsHome string
	// skillVersion is the build version that twt stamps into an installed
	// agent skill. It is empty when the caller does not ask for the check.
	skillVersion string
	// skillPaths are the installed agent skill files that doctor compares
	// with skillVersion.
	skillPaths []string
}

func NewService(configDir, stateDir, dataDir, ticketsHome string) *Service {
	return &Service{configDir: configDir, stateDir: stateDir, dataDir: dataDir, ticketsHome: ticketsHome}
}

// WithSkillCheck adds the agent skill drift check to doctor. version is the
// version that 'twt skills install' stamps, and paths are the installed skill
// files. Doctor skips the check without both values.
func (s *Service) WithSkillCheck(version string, paths []string) *Service {
	s.skillVersion = version
	s.skillPaths = paths
	return s
}

func (s *Service) StorageStatus() (StorageStatus, error) {
	var warnings []string
	measure := func(root string) int64 {
		bytes, warning := bestEffortDirectoryBytes(root)
		if warning != "" {
			warnings = append(warnings, warning)
		}
		return bytes
	}
	cacheRoot := filepath.Join(s.dataDir, "caches")
	cacheBytes := measure(cacheRoot)
	snapshotBytes := measure(filepath.Join(s.stateDir, "snapshots"))
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
		bytes := measure(project.Root)
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
		if environment.Status == domain.EnvironmentClaimed || environment.Status == domain.EnvironmentClaiming {
			continue
		}
		preparedBytes += measure(environment.Root)
		preparedCount++
		preparedWorktrees += len(environment.Repositories)
		switch environment.Status {
		case domain.EnvironmentReady:
			readyCount++
		case domain.EnvironmentQueued, domain.EnvironmentPreparing:
			preparingCount++
		case domain.EnvironmentFailed:
			failedCount++
		}
	}
	return StorageStatus{
		TotalBytes: cacheBytes + activeBytes + archivedBytes + preparedBytes + snapshotBytes, CacheBytes: cacheBytes,
		ActiveProjectBytes: activeBytes, ArchivedProjectBytes: archivedBytes, PreparedBytes: preparedBytes, SnapshotBytes: snapshotBytes,
		CacheCount: cacheCount, ProjectCount: len(projects), ActiveProjectCount: activeCount, ArchivedProjectCount: archivedCount, WorktreeCount: worktrees,
		PreparedEnvironmentCount: preparedCount, ReadyEnvironmentCount: readyCount,
		PreparingEnvironmentCount: preparingCount, FailedEnvironmentCount: failedCount, PreparedWorktreeCount: preparedWorktrees,
		Warnings: warnings,
	}, nil
}

// bestEffortDirectoryBytes measures root. When the measurement fails, it
// returns size zero and a warning, so one unreadable directory does not stop
// a report.
func bestEffortDirectoryBytes(root string) (int64, string) {
	bytes, err := store.DirectoryBytes(root)
	if err != nil {
		return 0, err.Error()
	}
	return bytes, ""
}

// EnvironmentReport describes each Prepared Environment record. It joins the
// Prepared Environment store, the Project Template digests, and the Project
// store. A Project Template that twt cannot load keeps the record status,
// because twt cannot know if its Prepared Environments are obsolete.
func (s *Service) EnvironmentReport() ([]EnvironmentInfo, error) {
	environments, err := store.NewEnvironmentStore(s.stateDir).List()
	if err != nil {
		return nil, err
	}
	catalog, _, err := store.LoadTemplateCatalog(s.configDir)
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
		if environment.Status == domain.EnvironmentReady &&
			catalog.Disposition(environment.TemplateName, environment.TemplateDigest) == store.TemplateObsolete {
			info.Status = "obsolete"
		}
		info.Bytes, info.SizeWarning = bestEffortDirectoryBytes(environment.Root)
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

// prepareLogPath returns the twt-owned preparation log of one Prepared
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
			if err := projectservice.ValidateProjectMarker(project.Root, project.ID); err != nil {
				report.addFailure("project:"+project.Name, err.Error())
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
			if environment.Status == domain.EnvironmentQueued {
				continue
			}
			markerName := ".twt-environment.json"
			if environment.Status == domain.EnvironmentClaiming || environment.Status == domain.EnvironmentClaimed {
				markerName = ".twt-owned.json"
			}
			if _, err := os.Stat(filepath.Join(environment.Root, markerName)); err != nil {
				if (environment.Status == domain.EnvironmentPreparing || environment.Status == domain.EnvironmentFailed) && os.IsNotExist(err) {
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
	s.checkTicketsHome(&report)
	s.checkSkill(&report)
	return report
}

// checkSkill compares each installed agent skill file with the running build.
// A copy from another build, or a copy with no version, is a warning: the
// skill still works, but its rules can be old. Doctor reports nothing when no
// skill tree holds the twt skill.
func (s *Service) checkSkill(report *DoctorReport) {
	if strings.TrimSpace(s.skillVersion) == "" || len(s.skillPaths) == 0 {
		return
	}
	installed, stale := 0, []string{}
	for _, path := range s.skillPaths {
		content, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		installed++
		if skills.Version(string(content)) != s.skillVersion {
			stale = append(stale, path)
		}
	}
	if installed == 0 {
		return
	}
	if len(stale) > 0 {
		report.addWarning("skills", fmt.Sprintf("%d of %d installed twt skill files are not version %s (%s). Run 'twt skills install' to update the twt skill.",
			len(stale), installed, s.skillVersion, strings.Join(stale, ", ")))
		return
	}
	report.addPass("skills", fmt.Sprintf("%d installed twt skill files are version %s", installed, s.skillVersion))
}

// checkTicketsHome reports the state of the Tickets home. A missing or
// unusable home is a warning, not a failure: tickets are optional and the
// installation stays healthy.
func (s *Service) checkTicketsHome(report *DoctorReport) {
	if strings.TrimSpace(s.ticketsHome) == "" {
		report.addWarning("tickets-home", "No Tickets home is set. Set ticketsHome in config.yaml or TWT_TICKETS_HOME.")
		return
	}
	info, err := os.Stat(s.ticketsHome)
	if err != nil || !info.IsDir() {
		report.addWarning("tickets-home", fmt.Sprintf("Tickets home %q does not exist. Run 'twt tickets init'.", s.ticketsHome))
		return
	}
	// Check write access without a probe file, so doctor never writes into a
	// vault.
	if err := syscall.Access(s.ticketsHome, 0x2); err != nil {
		report.addWarning("tickets-home", fmt.Sprintf("Tickets home %q is not writable: %v", s.ticketsHome, err))
		return
	}
	report.addPass("tickets-home", s.ticketsHome)
}

// failedEnvironmentMessage tells a person why one Prepared Environment failed
// and how to remove it. A failed Prepared Environment wastes disk space, but it
// does not make the installation unhealthy.
func (s *Service) failedEnvironmentMessage(environment domain.PreparedEnvironment) string {
	failure := environment.Failure
	if failure == "" {
		failure = "twt did not record a reason"
	}
	message := fmt.Sprintf("Prepared Environment failed: %s.", failure)
	if log := s.prepareLogPath(environment.ID); log != "" {
		message += fmt.Sprintf(" See %s.", log)
	}
	return message + " Run 'twt storage clean --apply' to remove it."
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
// report healthy, so twt doctor still ends with success.
func (r *DoctorReport) addWarning(name, message string) {
	r.Checks = append(r.Checks, Check{Name: name, Status: "warn", Message: message})
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
