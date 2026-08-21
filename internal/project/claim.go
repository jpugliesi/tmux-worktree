package project

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/jpugliesi/tmux-worktree/internal/clierr"
	"github.com/jpugliesi/tmux-worktree/internal/domain"
	"github.com/jpugliesi/tmux-worktree/internal/store"
)

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

func (s *Service) failedEnvironmentError(environment domain.PreparedEnvironment) error {
	failure := environment.Failure
	if failure == "" {
		failure = "twt did not save a failure cause"
	}
	return clierr.Wrap(clierr.Internal, &EnvironmentFailedError{
		EnvironmentID: environment.ID,
		Failure:       failure,
		LogPath:       PrepareLogPath(s.options.StateDir, environment.ID),
	})
}

// CreateOptions changes how Create claims a Prepared Environment.
type CreateOptions struct {
	// Branch is an optional custom Project branch name. It wins over the
	// branch pattern and it ignores BranchPrefix. An empty value renders the
	// branch pattern of the Project Template, or the default pattern
	// {prefix}{name}.
	Branch string
	// BranchPrefix is the user branch prefix for the {prefix} token. The CLI
	// resolves it from TWT_BRANCH_PREFIX and then the branchPrefix value of
	// config.yaml.
	BranchPrefix string
	// NoFetch turns the default-branch refresh before the claim off.
	NoFetch bool
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
	races, healed := 0, false
	for {
		project, err := s.prepareAndClaim(name, templateName, template, opts)
		var failed *EnvironmentFailedError
		switch {
		case err == nil:
			return project, nil
		case errors.Is(err, ErrClaimLostRace) && races < 2:
			races++
		case !healed && errors.As(err, &failed):
			healed = true
			s.report("Prepared Environment %s failed. twt prepares a replacement.", failed.EnvironmentID)
			s.cleanFailedEnvironments(templateName, template)
		default:
			return project, err
		}
	}
}

// prepareAndClaim runs one Create attempt: it gets a ready Prepared
// Environment and claims it for the new Project.
func (s *Service) prepareAndClaim(name, templateName string, template domain.Template, opts CreateOptions) (domain.Project, error) {
	environment, err := s.Prepare(templateName, template)
	if err != nil {
		return domain.Project{}, err
	}
	return s.claimPreparedEnvironment(name, templateName, template, environment.ID, opts)
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
	current := store.TemplateCatalog{templateName: store.TemplateStatus{Digests: digests}}
	for _, environment := range environments {
		if environment.Status == domain.EnvironmentFailed && digests.Matches(environment.TemplateDigest) {
			_ = s.cleanPreparedEnvironment(environment.ID, current)
		}
	}
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
// reservation is saved. The order is: the --branch flag, then the rendered
// branch_pattern of the Project Template, then the default pattern
// {prefix}{name}. The resolved name must be a valid Git branch name and must
// not be a repository default branch. When the resolved name already exists
// in a Repository Cache, twt falls back to twt/<name>-<id8>.
func (s *Service) resolveProjectBranch(name, projectID string, template domain.Template, opts CreateOptions) (string, error) {
	fallback := "twt/" + name + "-" + projectID[:8]
	candidate, err := s.projectBranchCandidate(name, projectID, template, opts)
	if err != nil {
		return "", err
	}
	for _, spec := range template.Repositories {
		repositoryDefault := spec.DefaultBranch
		cachePath := s.cachePath(spec.Name, spec.Clone.URL)
		cacheInfo, statErr := os.Stat(cachePath)
		cacheExists := statErr == nil && cacheInfo.IsDir()
		if cacheExists {
			// Deliberate: a cache without a readable HEAD must not block
			// branch selection.
			repositoryDefault, _ = defaultBranch(cachePath, spec)
		}
		if repositoryDefault != "" && candidate == repositoryDefault {
			return "", clierr.New(clierr.InvalidUsage, "branch %q is the default branch of repository %q; use --branch to set a different branch name", candidate, spec.Name)
		}
		if !cacheExists {
			continue
		}
		exists, err := refExists(cachePath, "refs/heads/"+candidate)
		if err != nil {
			return "", err
		}
		if exists {
			s.report("Branch %q exists. twt uses %q.", candidate, fallback)
			return fallback, nil
		}
	}
	return candidate, nil
}

// projectBranchCandidate renders and validates the Project branch name before
// the repository checks. The --branch flag wins and ignores the branch
// prefix; otherwise twt renders the branch pattern.
func (s *Service) projectBranchCandidate(name, projectID string, template domain.Template, opts CreateOptions) (string, error) {
	if candidate := strings.TrimSpace(opts.Branch); candidate != "" {
		if err := domain.ValidateBranchName(candidate); err != nil {
			return "", clierr.New(clierr.InvalidUsage, "branch name %q is not valid: %v", candidate, err)
		}
		return candidate, nil
	}
	pattern := template.BranchPattern
	if pattern == "" {
		pattern = domain.DefaultBranchPattern
	}
	candidate := domain.RenderBranchPattern(pattern, opts.BranchPrefix, name, projectID[:8])
	if err := domain.ValidateBranchName(candidate); err != nil {
		if opts.BranchPrefix != "" {
			withoutPrefix := domain.RenderBranchPattern(pattern, "", name, projectID[:8])
			if domain.ValidateBranchName(withoutPrefix) == nil {
				return "", clierr.WithHint(
					clierr.New(clierr.InvalidUsage, "branch prefix %q makes the invalid branch name %q: %v", opts.BranchPrefix, candidate, err),
					"Correct TWT_BRANCH_PREFIX or the branchPrefix value of config.yaml.")
			}
		}
		return "", clierr.New(clierr.InvalidUsage, "branch name %q is not valid: %v", candidate, err)
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
	marker := map[string]string{"owner": "twt", "projectId": project.ID, "environmentId": environment.ID}
	if err := writeJSON(filepath.Join(project.Root, ".twt-owned.json"), marker, 0o600); err != nil {
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
	if err := worktreeUsesCache(repository.Path, repository.CachePath); err != nil {
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
// repository. It runs inside the repository cache lock. It refreshes a stale
// base with refreshStaleBase and owns the one record update and save that a
// moved base commit needs.
func (s *Service) claimBaseCommit(environment *domain.PreparedEnvironment, index int, spec domain.RepositorySpec, opts CreateOptions) (string, error) {
	repository := environment.Repositories[index]
	base := repository.BaseCommit
	branch, err := defaultBranch(repository.CachePath, spec)
	if err != nil {
		return "", err
	}
	fetchedAt := environment.ReadyAt
	if !opts.NoFetch {
		newBase, refreshedAt, err := s.refreshStaleBase(repository, spec, branch, environment.ReadyAt)
		if err != nil {
			return "", err
		}
		fetchedAt = refreshedAt
		if newBase != base {
			environment.Repositories[index].BaseCommit = newBase
			environment.UpdatedAt = s.now()
			if err := s.environments.Save(*environment); err != nil {
				return "", err
			}
			base = newBase
		}
	}
	age := "an unknown time"
	if fetchedAt != nil {
		age = s.now().Sub(*fetchedAt).Truncate(time.Second).String()
	}
	s.report("Base: origin/%s @ %s (fetched %s ago)", branch, shortCommit(base), age)
	return base, nil
}

// refreshStaleBase refreshes origin/<branch> in the Repository Cache when the
// Prepared Environment is stale, and moves the detached checkout to the new
// tip when the saved base commit is its ancestor. It does Git work only and
// returns the new base commit with the effective fetch time. The caller owns
// the Prepared Environment record update and save.
func (s *Service) refreshStaleBase(repository domain.PreparedRepository, spec domain.RepositorySpec, branch string, readyAt *time.Time) (string, *time.Time, error) {
	base := repository.BaseCommit
	fetchedAt := readyAt
	if readyAt == nil || s.now().Sub(*readyAt) > claimFreshnessWindow {
		if err := fetchOrigin(repository.CachePath, spec.Clone.Depth, branch); err != nil {
			s.report("Warning: twt could not fetch origin for repository %q: %v. twt uses the saved base commit.", repository.Name, err)
		} else {
			now := s.now()
			fetchedAt = &now
		}
	}
	tip, err := output(repository.CachePath, "git", "rev-parse", "refs/remotes/origin/"+branch)
	if err != nil || tip == base {
		return base, fetchedAt, nil
	}
	ancestor, err := isAncestor(repository.CachePath, base, tip)
	if err != nil {
		return "", nil, err
	}
	if !ancestor {
		s.report("Warning: origin/%s does not contain the saved base commit for repository %q. twt keeps the saved base commit.", branch, repository.Name)
		return base, fetchedAt, nil
	}
	if err := run(repository.Path, "git", "reset", "--hard", tip); err != nil {
		return "", nil, fmt.Errorf("move repository %q to the new origin/%s tip: %w", repository.Name, branch, err)
	}
	return tip, fetchedAt, nil
}

func validateEnvironmentClaimMarker(environment domain.PreparedEnvironment, project domain.Project) error {
	if err := validateEnvironmentMarker(environment); err == nil {
		return nil
	}
	data, err := os.ReadFile(filepath.Join(project.Root, ".twt-owned.json"))
	if err != nil {
		return fmt.Errorf("Prepared Environment %q has no valid claim ownership marker", environment.ID)
	}
	var marker struct {
		Owner         string `json:"owner"`
		ProjectID     string `json:"projectId"`
		EnvironmentID string `json:"environmentId"`
	}
	if json.Unmarshal(data, &marker) != nil || marker.Owner != "twt" || marker.ProjectID != project.ID || marker.EnvironmentID != environment.ID {
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

func repositorySpecHasInitialize(template domain.Template, name string) bool {
	for _, repository := range template.Repositories {
		if repository.Name == name {
			return repository.Initialize != nil
		}
	}
	return false
}

func successfulStep(id string, kind domain.StepKind, repository string, now time.Time) domain.SetupStep {
	return domain.SetupStep{ID: id, Kind: kind, Repository: repository, Status: domain.StepSucceeded, Attempts: 1, StartedAt: &now, FinishedAt: &now}
}
