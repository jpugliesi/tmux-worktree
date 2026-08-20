package project

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/jpugliesi/tmux-worktree/internal/clierr"
	"github.com/jpugliesi/tmux-worktree/internal/domain"
	"github.com/jpugliesi/tmux-worktree/internal/store"
)

const environmentMarkerName = ".twt2-environment.json"

const preparationLaunchLease = 10 * time.Second

// claimFreshnessWindow is the age after which a claim refreshes the default
// branch of each repository before it creates the Project branch.
const claimFreshnessWindow = 15 * time.Minute

// ErrEnvironmentFailed marks a Prepared Environment that has the failed
// status. Callers can branch on it with errors.Is.
var ErrEnvironmentFailed = errors.New("the Prepared Environment failed")

// ErrClaimLostRace marks a claim attempt that found the selected Prepared
// Environment already taken or changed. Create retries on this error.
var ErrClaimLostRace = errors.New("the Prepared Environment claim lost a race")

// EnvironmentFailedError describes one failed Prepared Environment.
type EnvironmentFailedError struct {
	EnvironmentID string
	Failure       string
	LogPath       string
}

func (e *EnvironmentFailedError) Error() string {
	return fmt.Sprintf("Prepared Environment %q failed: %s. See the log: %s", e.EnvironmentID, e.Failure, e.LogPath)
}

func (e *EnvironmentFailedError) Is(target error) bool { return target == ErrEnvironmentFailed }

// PrepareLogPath returns the log file of the background preparation worker
// for one Prepared Environment.
func PrepareLogPath(stateDir, environmentID string) string {
	return filepath.Join(stateDir, "logs", "prepare-"+environmentID+".log")
}

func (s *Service) failedEnvironmentError(environment domain.PreparedEnvironment) error {
	failure := environment.Failure
	if failure == "" {
		failure = "twt2 did not save a failure cause"
	}
	return clierr.Wrap(clierr.Internal, &EnvironmentFailedError{
		EnvironmentID: environment.ID,
		Failure:       failure,
		LogPath:       PrepareLogPath(s.options.StateDir, environment.ID),
	})
}

// CreateOptions changes how Create claims a Prepared Environment.
type CreateOptions struct {
	// Branch is an optional custom Project branch name. An empty value uses
	// the default twt2/<name>-<id> branch name.
	Branch string
	// NoFetch turns the default-branch refresh before the claim off.
	NoFetch bool
}

type PreparationQueue struct {
	Environment domain.PreparedEnvironment
	ShouldStart bool
}

func (s *Service) CreateWithOptions(name, templateName string, template domain.Template, opts CreateOptions) (domain.Project, error) {
	if reserved, found, err := s.restoreReservedProject(name); err != nil {
		return domain.Project{}, err
	} else if found {
		return s.completeEnvironmentClaim(reserved.EnvironmentID, reserved.ID, opts)
	}
	if err := s.ValidateCreate(name, templateName, template); err != nil {
		return domain.Project{}, err
	}
	healed := false
	for attempt := 1; ; attempt++ {
		environment, err := s.Prepare(templateName, template)
		if err != nil {
			if s.replaceFailedEnvironment(templateName, template, err, &healed) {
				continue
			}
			return domain.Project{}, err
		}
		project, err := s.claimPreparedEnvironment(name, templateName, template, environment.ID, opts)
		if err == nil {
			return project, nil
		}
		if errors.Is(err, ErrClaimLostRace) && attempt < 3 {
			continue
		}
		if s.replaceFailedEnvironment(templateName, template, err, &healed) {
			continue
		}
		return project, err
	}
}

// replaceFailedEnvironment reports whether Create can retry after cause. It
// removes failed Prepared Environments of this Project Template revision one
// time, so the retry prepares a fresh replacement in the foreground.
func (s *Service) replaceFailedEnvironment(templateName string, template domain.Template, cause error, healed *bool) bool {
	var failed *EnvironmentFailedError
	if *healed || !errors.As(cause, &failed) {
		return false
	}
	*healed = true
	s.report("Prepared Environment %s failed. twt2 prepares a replacement.", failed.EnvironmentID)
	s.cleanFailedEnvironments(templateName, template)
	return true
}

// cleanFailedEnvironments removes failed Prepared Environments that match
// this Project Template revision. The cleanup is best-effort.
func (s *Service) cleanFailedEnvironments(templateName string, template domain.Template) {
	digests, err := store.Digests(template)
	if err != nil {
		return
	}
	environments, err := s.environments.List()
	if err != nil {
		return
	}
	current := TemplateDigests{templateName: store.TemplateStatus{Digests: digests}}
	for _, environment := range environments {
		if environment.Status == domain.EnvironmentFailed && digests.Matches(environment.TemplateDigest) {
			_ = s.cleanPreparedEnvironment(environment.ID, current)
		}
	}
}

func (s *Service) Prepare(templateName string, template domain.Template) (domain.PreparedEnvironment, error) {
	queued, err := s.QueuePreparation(templateName, template)
	if err != nil {
		return domain.PreparedEnvironment{}, err
	}
	if queued.Environment.Status == domain.EnvironmentReady {
		return queued.Environment, nil
	}
	if !queued.ShouldStart {
		s.report("Waiting for the background preparation of Prepared Environment %s. Log: %s.",
			queued.Environment.ID, PrepareLogPath(s.options.StateDir, queued.Environment.ID))
	}
	return s.prepareEnvironment(queued.Environment.ID, queued.Environment.QueueToken)
}

// TopUpPool creates queued Prepared Environments until this Project Template
// revision has depth environments that are queued, preparing, or ready. It
// returns the queued environments that the caller must prepare or start.
func (s *Service) TopUpPool(templateName string, template domain.Template, depth int) ([]domain.PreparedEnvironment, error) {
	if err := template.Validate(); err != nil {
		return nil, fmt.Errorf("invalid Project Template %q: %w", templateName, err)
	}
	digests, err := store.Digests(template)
	if err != nil {
		return nil, err
	}
	lock, err := store.AcquireMutationLockWithTimeout(s.options.StateDir, 5*time.Second)
	if err != nil {
		return nil, err
	}
	defer lock.Release()
	environments, err := s.environments.List()
	if err != nil {
		return nil, err
	}
	pooled := 0
	start := []domain.PreparedEnvironment{}
	for _, environment := range environments {
		if environment.FormatVersion != domain.PreparationFormatVersion || !digests.Matches(environment.TemplateDigest) {
			continue
		}
		switch environment.Status {
		case domain.EnvironmentReady, domain.EnvironmentPreparing:
			pooled++
		case domain.EnvironmentQueued:
			pooled++
			if s.now().Sub(environment.QueuedAt) >= preparationLaunchLease {
				token, tokenErr := newID()
				if tokenErr != nil {
					return nil, tokenErr
				}
				environment.QueueToken = token
				environment.QueuedAt = s.now()
				environment.UpdatedAt = environment.QueuedAt
				if err := s.environments.Save(environment); err != nil {
					return nil, err
				}
				start = append(start, environment)
			}
		}
	}
	for pooled < depth {
		environment, err := s.saveNewQueuedEnvironment(templateName, digests, template)
		if err != nil {
			return start, err
		}
		start = append(start, environment)
		pooled++
	}
	return start, nil
}

// saveNewQueuedEnvironment creates one queued Prepared Environment record.
// The caller must hold the mutation lock.
func (s *Service) saveNewQueuedEnvironment(templateName string, digests store.DigestSet, template domain.Template) (domain.PreparedEnvironment, error) {
	environment, err := s.newPreparedEnvironment(templateName, digests.Environment, template)
	if err != nil {
		return domain.PreparedEnvironment{}, err
	}
	if err := s.environments.Save(environment); err != nil {
		return domain.PreparedEnvironment{}, err
	}
	return environment, nil
}

func (s *Service) QueuePreparation(templateName string, template domain.Template) (PreparationQueue, error) {
	if err := template.Validate(); err != nil {
		return PreparationQueue{}, fmt.Errorf("invalid Project Template %q: %w", templateName, err)
	}
	digests, err := store.Digests(template)
	if err != nil {
		return PreparationQueue{}, err
	}
	lock, err := store.AcquireMutationLock(s.options.StateDir)
	if err != nil {
		return PreparationQueue{}, err
	}
	environments, err := s.environments.List()
	if err != nil {
		lock.Release()
		return PreparationQueue{}, err
	}
	for _, environment := range environments {
		if environment.FormatVersion == domain.PreparationFormatVersion && digests.Matches(environment.TemplateDigest) && environment.Status == domain.EnvironmentReady {
			lock.Release()
			return PreparationQueue{Environment: environment}, nil
		}
	}
	for _, environment := range environments {
		if environment.FormatVersion != domain.PreparationFormatVersion || !digests.Matches(environment.TemplateDigest) {
			continue
		}
		if environment.Status == domain.EnvironmentQueued {
			shouldStart := s.now().Sub(environment.QueuedAt) >= preparationLaunchLease
			if shouldStart {
				token, tokenErr := newID()
				if tokenErr != nil {
					lock.Release()
					return PreparationQueue{}, tokenErr
				}
				environment.QueueToken = token
				environment.QueuedAt = s.now()
				environment.UpdatedAt = environment.QueuedAt
				if saveErr := s.environments.Save(environment); saveErr != nil {
					lock.Release()
					return PreparationQueue{}, saveErr
				}
			}
			lock.Release()
			return PreparationQueue{Environment: environment, ShouldStart: shouldStart}, nil
		}
		if environment.Status == domain.EnvironmentPreparing {
			environmentLock, lockErr := store.AcquireEnvironmentLock(s.options.StateDir, environment.ID)
			if lockErr == nil {
				environmentLock.Release()
				lock.Release()
				return PreparationQueue{Environment: environment, ShouldStart: true}, nil
			}
			if errors.Is(lockErr, store.ErrLockHeld) {
				lock.Release()
				return PreparationQueue{Environment: environment}, nil
			}
			lock.Release()
			return PreparationQueue{}, lockErr
		}
	}
	environment, err := s.saveNewQueuedEnvironment(templateName, digests, template)
	if releaseErr := lock.Release(); err == nil && releaseErr != nil {
		err = releaseErr
	}
	if err != nil {
		return PreparationQueue{}, err
	}
	return PreparationQueue{Environment: environment, ShouldStart: true}, nil
}

func (s *Service) PrepareQueued(environmentID, token string) (domain.PreparedEnvironment, error) {
	return s.prepareEnvironment(environmentID, token)
}

func (s *Service) FailQueuedPreparation(environmentID, token string, cause error) error {
	lock, err := store.AcquireEnvironmentLock(s.options.StateDir, environmentID)
	if err != nil {
		return err
	}
	defer lock.Release()
	environment, err := s.environments.Find(environmentID)
	if err != nil {
		return err
	}
	if environment.Status != domain.EnvironmentQueued || environment.QueueToken != token {
		return nil
	}
	_, err = s.failEnvironment(&environment, cause)
	return err
}

func (s *Service) claimPreparedEnvironment(name, templateName string, template domain.Template, environmentID string, opts CreateOptions) (domain.Project, error) {
	digests, err := store.Digests(template)
	if err != nil {
		return domain.Project{}, err
	}
	projectID, err := newID()
	if err != nil {
		return domain.Project{}, err
	}
	branch, err := s.resolveProjectBranch(name, projectID, template, opts)
	if err != nil {
		return domain.Project{}, err
	}
	lock, err := store.AcquireMutationLock(s.options.StateDir)
	if err != nil {
		return domain.Project{}, err
	}
	environment, err := s.environments.Find(environmentID)
	if err == nil {
		if environment.Status == domain.EnvironmentFailed {
			err = s.failedEnvironmentError(environment)
		} else if environment.Status != domain.EnvironmentReady || !digests.Matches(environment.TemplateDigest) || environment.FormatVersion != domain.PreparationFormatVersion {
			err = fmt.Errorf("%w: Prepared Environment %q is not ready for this Project Template revision", ErrClaimLostRace, environment.ID)
		}
	}
	if err == nil {
		err = s.requireProjectNameAvailable(name)
	}
	var project domain.Project
	if err == nil {
		project = s.projectForEnvironment(name, templateName, template, environment, projectID, branch)
		environment.Status = domain.EnvironmentClaiming
		environment.ClaimReservation = &domain.EnvironmentClaim{Project: project, ReservedAt: s.now()}
		environment.UpdatedAt = s.now()
		err = s.environments.Save(environment)
	}
	if err == nil {
		err = s.store.Save(project)
	}
	if releaseErr := lock.Release(); err == nil && releaseErr != nil {
		err = releaseErr
	}
	if err != nil {
		return project, err
	}
	if s.options.AfterClaimReserved != nil {
		s.options.AfterClaimReserved()
	}
	return s.completeEnvironmentClaim(environment.ID, project.ID, opts)
}

// resolveProjectBranch selects the Project branch name before the claim
// reservation is saved. A custom branch must not be a repository default
// branch. On a name collision twt2 falls back to the default branch name.
func (s *Service) resolveProjectBranch(name, projectID string, template domain.Template, opts CreateOptions) (string, error) {
	defaultName := "twt2/" + name + "-" + projectID[:8]
	candidate := strings.TrimSpace(opts.Branch)
	if candidate == "" {
		return defaultName, nil
	}
	for _, spec := range template.Repositories {
		repositoryDefault := spec.DefaultBranch
		cachePath := s.cachePath(spec.Name, spec.Clone.URL)
		cacheInfo, statErr := os.Stat(cachePath)
		cacheExists := statErr == nil && cacheInfo.IsDir()
		if repositoryDefault == "" && cacheExists {
			repositoryDefault, _ = output(cachePath, "git", "symbolic-ref", "--short", "HEAD")
		}
		if repositoryDefault != "" && candidate == repositoryDefault {
			return "", clierr.New(clierr.InvalidUsage, "branch %q is the default branch of repository %q; use a different branch name", candidate, spec.Name)
		}
		if !cacheExists {
			continue
		}
		exists, err := refExists(cachePath, "refs/heads/"+candidate)
		if err != nil {
			return "", err
		}
		if exists {
			s.report("Branch %q exists. twt2 uses %q.", candidate, defaultName)
			return defaultName, nil
		}
	}
	return candidate, nil
}

func (s *Service) requireProjectNameAvailable(name string) error {
	projects, err := s.store.List()
	if err != nil {
		return err
	}
	for _, project := range projects {
		if project.Name == name {
			return clierr.New(clierr.AlreadyExists, "Project %q already exists", name)
		}
	}
	environments, err := s.environments.List()
	if err != nil {
		return err
	}
	for _, environment := range environments {
		if environment.ClaimReservation != nil && environment.ClaimReservation.Project.Name == name {
			return clierr.New(clierr.AlreadyExists, "Project %q is already reserved by a Prepared Environment claim", name)
		}
	}
	return nil
}

func (s *Service) projectForEnvironment(name, templateName string, template domain.Template, environment domain.PreparedEnvironment, id, branch string) domain.Project {
	now := s.now()
	project := domain.Project{
		Version: domain.ProjectVersion, ID: id, Name: name, TemplateName: templateName,
		TemplateSnapshot: template, EnvironmentID: environment.ID, Status: domain.ProjectInitializing,
		Root: environment.Root, TmuxSession: name, CreatedAt: now, UpdatedAt: now,
	}
	project.Steps = append(project.Steps, newStep("project_root", domain.StepProjectRoot, ""))
	for _, repository := range environment.Repositories {
		project.Repositories = append(project.Repositories, domain.ProjectRepository{
			Name: repository.Name, CachePath: repository.CachePath, Path: repository.Path,
			Branch: branch, WindowName: repository.WindowName,
		})
		project.Steps = append(project.Steps,
			successfulStep("cache:"+repository.Name, domain.StepCache, repository.Name, now),
			newStep("checkout:"+repository.Name, domain.StepCheckout, repository.Name),
		)
		if repositorySpecHasInitialize(template, repository.Name) {
			project.Steps = append(project.Steps, successfulStep("repository_init:"+repository.Name, domain.StepRepositoryInit, repository.Name, now))
		}
	}
	project.Steps = append(project.Steps, newStep("tmux", domain.StepTmux, ""))
	if template.Initialize != nil {
		project.Steps = append(project.Steps, newStep("project_init", domain.StepProjectInit, ""))
	}
	project.Steps = append(project.Steps, agentSteps(template)...)
	return project
}

func (s *Service) completeEnvironmentClaim(environmentID, projectID string, opts CreateOptions) (domain.Project, error) {
	lock, err := store.AcquireEnvironmentLockBlocking(s.options.StateDir, environmentID)
	if err != nil {
		return domain.Project{}, err
	}
	defer lock.Release()
	environment, err := s.environments.Find(environmentID)
	if err != nil {
		return domain.Project{}, err
	}
	if environment.ClaimReservation == nil || environment.ClaimReservation.Project.ID != projectID {
		return domain.Project{}, fmt.Errorf("Prepared Environment %q does not contain Project claim %q", environmentID, projectID)
	}
	project, err := s.store.Find(projectID)
	if err != nil {
		return domain.Project{}, err
	}
	if err := s.validateEnvironmentForClaim(environment, project); err != nil {
		return project, err
	}
	for index := range project.Repositories {
		repository := &project.Repositories[index]
		spec, prepared, preparedIndex, err := preparedRepositoryFor(environment, repository.Name)
		if err != nil {
			return project, err
		}
		err = s.withCacheLock(repository.CachePath, func() error {
			branch, err := validatePreparedRepositoryForClaim(prepared, *repository)
			if err != nil {
				return err
			}
			if branch == "" {
				base, err := s.claimBaseCommit(&environment, preparedIndex, spec, opts)
				if err != nil {
					return err
				}
				if err := run(repository.Path, "git", "switch", "-c", repository.Branch, base); err != nil {
					return fmt.Errorf("create Project branch for repository %q: %w", repository.Name, err)
				}
			} else if branch != repository.Branch {
				return fmt.Errorf("claimed checkout for repository %q uses branch %q; expected %q", repository.Name, branch, repository.Branch)
			}
			return nil
		})
		if err != nil {
			return project, err
		}
		markProjectStepSucceeded(&project, "checkout:"+repository.Name, s.now())
	}
	marker := map[string]string{"owner": "twt2", "projectId": project.ID, "environmentId": environment.ID}
	if err := writeJSON(filepath.Join(project.Root, ".twt2-owned.json"), marker, 0o600); err != nil {
		return project, err
	}
	if err := os.Remove(filepath.Join(project.Root, environmentMarkerName)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return project, fmt.Errorf("remove Prepared Environment ownership marker: %w", err)
	}
	markProjectStepSucceeded(&project, "project_root", s.now())
	if err := s.store.Save(project); err != nil {
		return project, err
	}
	environment.Status = domain.EnvironmentClaimed
	environment.UpdatedAt = s.now()
	if err := s.environments.Save(environment); err != nil {
		return project, err
	}
	if err := s.runPending(&project); err != nil {
		return project, err
	}
	return project, nil
}

func (s *Service) validateEnvironmentForClaim(environment domain.PreparedEnvironment, project domain.Project) error {
	if environment.Status != domain.EnvironmentClaiming {
		return fmt.Errorf("Prepared Environment %q has status %q; expected %q", environment.ID, environment.Status, domain.EnvironmentClaiming)
	}
	if err := validateEnvironmentClaimMarker(environment, project); err != nil {
		return err
	}
	if len(environment.Repositories) != len(environment.TemplateSnapshot.Repositories) {
		return fmt.Errorf("Prepared Environment %q has an invalid repository count", environment.ID)
	}
	return nil
}

// validatePreparedRepositoryForClaim checks one prepared checkout and returns
// the branch that the checkout already uses. An empty branch means that the
// checkout is still detached at the saved base commit.
func validatePreparedRepositoryForClaim(repository domain.PreparedRepository, projectRepository domain.ProjectRepository) (string, error) {
	if repository.BaseCommit == "" {
		return "", fmt.Errorf("Prepared Environment repository %q has no base commit", repository.Name)
	}
	commonDir, err := output(repository.Path, "git", "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil || !sameDirectory(commonDir, repository.CachePath) {
		return "", fmt.Errorf("Prepared Environment repository %q does not use its Repository Cache", repository.Name)
	}
	branch, err := output(repository.Path, "git", "branch", "--show-current")
	if err != nil || (branch != "" && branch != projectRepository.Branch) {
		return "", fmt.Errorf("Prepared Environment repository %q has an invalid claim branch", repository.Name)
	}
	commit, err := output(repository.Path, "git", "rev-parse", "HEAD")
	if err != nil || commit != repository.BaseCommit {
		return "", fmt.Errorf("Prepared Environment repository %q is not at its saved base commit", repository.Name)
	}
	return branch, nil
}

// claimBaseCommit returns the base commit for the new Project branch of one
// repository. It runs inside the repository cache lock. When the Prepared
// Environment is stale, it refreshes the default branch and moves the
// detached checkout to the new tip when the saved base is its ancestor.
func (s *Service) claimBaseCommit(environment *domain.PreparedEnvironment, index int, spec domain.RepositorySpec, opts CreateOptions) (string, error) {
	repository := environment.Repositories[index]
	base := repository.BaseCommit
	defaultBranch := spec.DefaultBranch
	if defaultBranch == "" {
		resolved, err := output(repository.CachePath, "git", "symbolic-ref", "--short", "HEAD")
		if err != nil {
			return "", fmt.Errorf("find default branch for Repository Cache: %w", err)
		}
		defaultBranch = resolved
	}
	fetchedAt := environment.ReadyAt
	if !opts.NoFetch {
		if environment.ReadyAt == nil || s.now().Sub(*environment.ReadyAt) > claimFreshnessWindow {
			if err := fetchOrigin(repository.CachePath, spec.Clone.Depth, defaultBranch); err != nil {
				s.report("Warning: twt2 could not fetch origin for repository %q: %v. twt2 uses the saved base commit.", repository.Name, err)
			} else {
				now := s.now()
				fetchedAt = &now
			}
		}
		tip, err := output(repository.CachePath, "git", "rev-parse", "refs/remotes/origin/"+defaultBranch)
		if err == nil && tip != base {
			ancestor, ancestorErr := isAncestor(repository.CachePath, base, tip)
			if ancestorErr != nil {
				return "", ancestorErr
			}
			if ancestor {
				if err := run(repository.Path, "git", "reset", "--hard", tip); err != nil {
					return "", fmt.Errorf("move repository %q to the new origin/%s tip: %w", repository.Name, defaultBranch, err)
				}
				environment.Repositories[index].BaseCommit = tip
				environment.UpdatedAt = s.now()
				if err := s.environments.Save(*environment); err != nil {
					return "", err
				}
				base = tip
			} else {
				s.report("Warning: origin/%s does not contain the saved base commit for repository %q. twt2 keeps the saved base commit.", defaultBranch, repository.Name)
			}
		}
	}
	age := "an unknown time"
	if fetchedAt != nil {
		age = s.now().Sub(*fetchedAt).Truncate(time.Second).String()
	}
	s.report("Base: origin/%s @ %s (fetched %s ago)", defaultBranch, shortCommit(base), age)
	return base, nil
}

// isAncestor reports whether ancestor is reachable from descendant.
func isAncestor(cachePath, ancestor, descendant string) (bool, error) {
	command := exec.Command("git", "merge-base", "--is-ancestor", ancestor, descendant)
	command.Dir = cachePath
	if err := command.Run(); err == nil {
		return true, nil
	} else if exitError, ok := err.(*exec.ExitError); ok && exitError.ExitCode() == 1 {
		return false, nil
	} else {
		return false, fmt.Errorf("compare commits in Repository Cache %q: %w", cachePath, err)
	}
}

func shortCommit(commit string) string {
	if len(commit) > 12 {
		return commit[:12]
	}
	return commit
}

func validateEnvironmentClaimMarker(environment domain.PreparedEnvironment, project domain.Project) error {
	if err := validateEnvironmentMarker(environment); err == nil {
		return nil
	}
	data, err := os.ReadFile(filepath.Join(project.Root, ".twt2-owned.json"))
	if err != nil {
		return fmt.Errorf("Prepared Environment %q has no valid claim ownership marker", environment.ID)
	}
	var marker struct {
		Owner         string `json:"owner"`
		ProjectID     string `json:"projectId"`
		EnvironmentID string `json:"environmentId"`
	}
	if json.Unmarshal(data, &marker) != nil || marker.Owner != "twt2" || marker.ProjectID != project.ID || marker.EnvironmentID != environment.ID {
		return fmt.Errorf("Prepared Environment %q has an invalid claim ownership marker", environment.ID)
	}
	return nil
}

func markProjectStepSucceeded(project *domain.Project, id string, now time.Time) {
	for index := range project.Steps {
		if project.Steps[index].ID == id {
			project.Steps[index].Status = domain.StepSucceeded
			project.Steps[index].Attempts++
			project.Steps[index].StartedAt = &now
			project.Steps[index].FinishedAt = &now
			project.Steps[index].Error = ""
			return
		}
	}
}

func projectRepositoryByName(project domain.Project, name string) (domain.ProjectRepository, error) {
	for _, repository := range project.Repositories {
		if repository.Name == name {
			return repository, nil
		}
	}
	return domain.ProjectRepository{}, fmt.Errorf("repository %q is not in Project %q", name, project.Name)
}

func repositorySpecHasInitialize(template domain.Template, name string) bool {
	for _, repository := range template.Repositories {
		if repository.Name == name {
			return repository.Initialize != nil
		}
	}
	return false
}

func (s *Service) newPreparedEnvironment(templateName, digest string, template domain.Template) (domain.PreparedEnvironment, error) {
	id, err := newID()
	if err != nil {
		return domain.PreparedEnvironment{}, err
	}
	token, err := newID()
	if err != nil {
		return domain.PreparedEnvironment{}, err
	}
	now := s.now()
	root := filepath.Join(s.options.DataDir, "projects", id)
	environment := domain.PreparedEnvironment{
		Version: domain.PreparedEnvironmentVersion, FormatVersion: domain.PreparationFormatVersion,
		ID: id, TemplateName: templateName, TemplateDigest: digest, TemplateSnapshot: template,
		Status: domain.EnvironmentQueued, Root: root, QueueToken: token, QueuedAt: now,
		CreatedAt: now, UpdatedAt: now,
	}
	environment.Steps = append(environment.Steps, newStep("environment_root", domain.StepProjectRoot, ""))
	for _, spec := range template.Repositories {
		windowName := spec.WindowName
		if windowName == "" {
			windowName = spec.Name
		}
		repository := domain.PreparedRepository{
			Name: spec.Name, CachePath: s.cachePath(spec.Name, spec.Clone.URL),
			Path: filepath.Join(root, spec.Name), WindowName: windowName,
		}
		environment.Repositories = append(environment.Repositories, repository)
		environment.Steps = append(environment.Steps,
			newStep("cache:"+spec.Name, domain.StepCache, spec.Name),
			newStep("checkout:"+spec.Name, domain.StepCheckout, spec.Name),
		)
		if spec.Initialize != nil {
			environment.Steps = append(environment.Steps, newStep("repository_init:"+spec.Name, domain.StepRepositoryInit, spec.Name))
		}
	}
	return environment, nil
}

func (s *Service) prepareEnvironment(environmentID, token string) (domain.PreparedEnvironment, error) {
	lock, err := store.AcquireEnvironmentLockBlocking(s.options.StateDir, environmentID)
	if err != nil {
		return domain.PreparedEnvironment{}, err
	}
	defer lock.Release()
	environment, err := s.environments.Find(environmentID)
	if err != nil {
		return domain.PreparedEnvironment{}, err
	}
	if environment.Status == domain.EnvironmentReady {
		return environment, nil
	}
	if environment.Status == domain.EnvironmentFailed {
		return environment, s.failedEnvironmentError(environment)
	}
	if environment.QueueToken != token || (environment.Status != domain.EnvironmentQueued && environment.Status != domain.EnvironmentPreparing) {
		return environment, fmt.Errorf("Prepared Environment %q does not have the requested queue token", environment.ID)
	}
	environment.Status = domain.EnvironmentPreparing
	environment.UpdatedAt = s.now()
	if err := s.environments.Save(environment); err != nil {
		return environment, err
	}
	for index := range environment.Steps {
		step := &environment.Steps[index]
		if step.Status == domain.StepSucceeded {
			continue
		}
		s.report("Step %d of %d: %s", index+1, len(environment.Steps), step.ID)
		if step.Kind == domain.StepRepositoryInit && step.Status == domain.StepRunning {
			return s.failEnvironment(&environment, fmt.Errorf("repository initialization was interrupted; twt2 removes this environment and prepares a new one on the next create"))
		}
		now := s.now()
		step.Status = domain.StepRunning
		step.Attempts++
		step.StartedAt = &now
		step.FinishedAt = nil
		step.Error = ""
		environment.UpdatedAt = now
		if err := s.environments.Save(environment); err != nil {
			return environment, err
		}
		stepErr := s.runEnvironmentStep(&environment, *step)
		finished := s.now()
		step.FinishedAt = &finished
		environment.UpdatedAt = finished
		if stepErr != nil {
			step.Status = domain.StepFailed
			step.Error = stepErr.Error()
			return s.failEnvironment(&environment, stepErr)
		}
		step.Status = domain.StepSucceeded
		if err := s.environments.Save(environment); err != nil {
			return environment, err
		}
	}
	readyAt := s.now()
	environment.Status = domain.EnvironmentReady
	environment.UpdatedAt = readyAt
	environment.ReadyAt = &readyAt
	if err := s.environments.Save(environment); err != nil {
		return environment, err
	}
	return environment, nil
}

func (s *Service) failEnvironment(environment *domain.PreparedEnvironment, cause error) (domain.PreparedEnvironment, error) {
	environment.Status = domain.EnvironmentFailed
	environment.Failure = cause.Error()
	environment.UpdatedAt = s.now()
	if err := s.environments.Save(*environment); err != nil {
		return *environment, fmt.Errorf("%v; also could not save Prepared Environment failure: %w", cause, err)
	}
	return *environment, cause
}

func (s *Service) runEnvironmentStep(environment *domain.PreparedEnvironment, step domain.SetupStep) error {
	switch step.Kind {
	case domain.StepProjectRoot:
		return writeEnvironmentMarker(*environment)
	case domain.StepCache:
		project := environmentProject(*environment)
		return s.ensureCache(project, step.Repository)
	case domain.StepCheckout:
		return s.ensurePreparedCheckout(environment, step.Repository)
	case domain.StepRepositoryInit:
		return s.runPreparedInitialize(*environment, step.Repository)
	default:
		return fmt.Errorf("unknown Prepared Environment step %q", step.Kind)
	}
}

func (s *Service) ensurePreparedCheckout(environment *domain.PreparedEnvironment, repositoryName string) error {
	spec, repository, index, err := preparedRepositoryFor(*environment, repositoryName)
	if err != nil {
		return err
	}
	return s.withCacheLock(repository.CachePath, func() error {
		return s.ensurePreparedCheckoutLocked(environment, repositoryName, spec, repository, index)
	})
}

func (s *Service) ensurePreparedCheckoutLocked(environment *domain.PreparedEnvironment, repositoryName string, spec domain.RepositorySpec, repository domain.PreparedRepository, index int) error {
	if info, statErr := os.Stat(repository.Path); statErr == nil && info.IsDir() {
		commonDir, err := output(repository.Path, "git", "rev-parse", "--path-format=absolute", "--git-common-dir")
		if err != nil || !sameDirectory(commonDir, repository.CachePath) {
			return fmt.Errorf("Prepared Environment checkout %q does not use its Repository Cache", repository.Path)
		}
		branch, err := output(repository.Path, "git", "branch", "--show-current")
		if err != nil || branch != "" {
			return fmt.Errorf("Prepared Environment checkout %q is not detached", repository.Path)
		}
	} else if statErr == nil {
		return fmt.Errorf("Prepared Environment checkout path %q is not a directory", repository.Path)
	} else if !errors.Is(statErr, os.ErrNotExist) {
		return fmt.Errorf("inspect Prepared Environment checkout: %w", statErr)
	} else {
		defaultBranch := spec.DefaultBranch
		if defaultBranch == "" {
			var err error
			defaultBranch, err = output(repository.CachePath, "git", "symbolic-ref", "--short", "HEAD")
			if err != nil {
				return fmt.Errorf("find default branch for Repository Cache: %w", err)
			}
		}
		startPoint := "refs/remotes/origin/" + defaultBranch
		if err := run(repository.CachePath, "git", "worktree", "add", "--detach", repository.Path, startPoint); err != nil {
			return fmt.Errorf("create Prepared Environment checkout %q: %w", repositoryName, err)
		}
	}
	commit, err := output(repository.Path, "git", "rev-parse", "HEAD")
	if err != nil {
		return fmt.Errorf("read Prepared Environment base commit: %w", err)
	}
	environment.Repositories[index].BaseCommit = commit
	return nil
}

func (s *Service) runPreparedInitialize(environment domain.PreparedEnvironment, repositoryName string) error {
	spec, repository, _, err := preparedRepositoryFor(environment, repositoryName)
	if err != nil {
		return err
	}
	if spec.Initialize == nil || len(spec.Initialize.Command) == 0 {
		return fmt.Errorf("repository initialization command is empty")
	}
	activityLock, err := store.AcquireActivityLock(s.options.StateDir, environment.ID)
	if err != nil {
		return err
	}
	defer activityLock.Release()
	env := append(os.Environ(),
		"TWT2_ENVIRONMENT_ID="+environment.ID,
		"TWT2_ENVIRONMENT_ROOT="+environment.Root,
		"TWT2_TEMPLATE_NAME="+environment.TemplateName,
		"TWT2_REPOSITORY_NAME="+repository.Name,
		"TWT2_REPOSITORY_PATH="+repository.Path,
	)
	return runInitializationProcess(repository.Path, spec.Initialize.Command, env, activityLock.File())
}

func writeEnvironmentMarker(environment domain.PreparedEnvironment) error {
	if err := os.MkdirAll(environment.Root, 0o755); err != nil {
		return fmt.Errorf("create Prepared Environment root: %w", err)
	}
	marker := map[string]any{
		"owner": "twt2", "environmentId": environment.ID,
		"templateDigest": environment.TemplateDigest, "formatVersion": environment.FormatVersion,
	}
	return writeJSON(filepath.Join(environment.Root, environmentMarkerName), marker, 0o600)
}

func validateEnvironmentMarker(environment domain.PreparedEnvironment) error {
	data, err := os.ReadFile(filepath.Join(environment.Root, environmentMarkerName))
	if err != nil {
		return fmt.Errorf("Prepared Environment %q has no ownership marker", environment.ID)
	}
	var marker struct {
		Owner          string `json:"owner"`
		EnvironmentID  string `json:"environmentId"`
		TemplateDigest string `json:"templateDigest"`
		FormatVersion  int    `json:"formatVersion"`
	}
	if json.Unmarshal(data, &marker) != nil || marker.Owner != "twt2" || marker.EnvironmentID != environment.ID || marker.TemplateDigest != environment.TemplateDigest || marker.FormatVersion != environment.FormatVersion {
		return fmt.Errorf("Prepared Environment %q has an invalid ownership marker", environment.ID)
	}
	return nil
}

func environmentProject(environment domain.PreparedEnvironment) domain.Project {
	project := domain.Project{TemplateSnapshot: environment.TemplateSnapshot}
	for _, repository := range environment.Repositories {
		project.Repositories = append(project.Repositories, domain.ProjectRepository{
			Name: repository.Name, CachePath: repository.CachePath, Path: repository.Path, WindowName: repository.WindowName,
		})
	}
	return project
}

func preparedRepositoryFor(environment domain.PreparedEnvironment, name string) (domain.RepositorySpec, domain.PreparedRepository, int, error) {
	var spec *domain.RepositorySpec
	for index := range environment.TemplateSnapshot.Repositories {
		if environment.TemplateSnapshot.Repositories[index].Name == name {
			copy := environment.TemplateSnapshot.Repositories[index]
			spec = &copy
			break
		}
	}
	for index := range environment.Repositories {
		if environment.Repositories[index].Name == name && spec != nil {
			return *spec, environment.Repositories[index], index, nil
		}
	}
	return domain.RepositorySpec{}, domain.PreparedRepository{}, -1, fmt.Errorf("repository %q is not in Prepared Environment %q", name, environment.ID)
}

func projectRepository(project domain.Project, name string) domain.ProjectRepository {
	for _, repository := range project.Repositories {
		if repository.Name == name {
			return repository
		}
	}
	return domain.ProjectRepository{}
}

func successfulStep(id string, kind domain.StepKind, repository string, now time.Time) domain.SetupStep {
	return domain.SetupStep{ID: id, Kind: kind, Repository: repository, Status: domain.StepSucceeded, Attempts: 1, StartedAt: &now, FinishedAt: &now}
}

func sameDirectory(first, second string) bool {
	firstInfo, firstErr := os.Stat(first)
	secondInfo, secondErr := os.Stat(second)
	return firstErr == nil && secondErr == nil && os.SameFile(firstInfo, secondInfo)
}
