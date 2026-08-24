package workspace

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
	// Branch is an optional custom Workspace branch name. It wins over the
	// branch pattern and it ignores BranchPrefix. An empty value renders the
	// branch pattern of the Workspace Template, or the default pattern
	// {prefix}{name}.
	Branch string
	// BranchPrefix is the user branch prefix for the {prefix} token. The CLI
	// resolves it from TWT_BRANCH_PREFIX and then the branchPrefix value of
	// config.yaml.
	BranchPrefix string
	// NoFetch turns the default-branch refresh before the claim off.
	NoFetch bool
	// Tickets are the Ticket slugs that the new Workspace works on. The
	// Workspace record and its claim reservation snapshot carry them.
	Tickets []string
	// Project is the durable Ticket Project of Tickets.
	Project string
}

func (s *Service) CreateWithOptions(name, templateName string, template domain.Template, opts CreateOptions) (domain.Workspace, error) {
	if reserved, found, err := s.restoreReservedWorkspace(name); err != nil {
		return domain.Workspace{}, err
	} else if found {
		return s.completeEnvironmentClaim(reserved.EnvironmentID, reserved.ID, opts)
	}
	if err := s.ValidateCreate(name, templateName, template); err != nil {
		return domain.Workspace{}, err
	}
	races, healed := 0, false
	for {
		workspace, err := s.prepareAndClaim(name, templateName, template, opts)
		var failed *EnvironmentFailedError
		switch {
		case err == nil:
			return workspace, nil
		case errors.Is(err, ErrClaimLostRace) && races < 2:
			races++
		case !healed && errors.As(err, &failed):
			healed = true
			s.report("Prepared Environment %s failed. twt prepares a replacement.", failed.EnvironmentID)
			s.cleanFailedEnvironments(templateName, template)
		default:
			return workspace, err
		}
	}
}

// prepareAndClaim runs one Create attempt: it gets a ready Prepared
// Environment and claims it for the new Workspace.
func (s *Service) prepareAndClaim(name, templateName string, template domain.Template, opts CreateOptions) (domain.Workspace, error) {
	environment, err := s.Prepare(templateName, template)
	if err != nil {
		return domain.Workspace{}, err
	}
	return s.claimPreparedEnvironment(name, templateName, template, environment.ID, opts)
}

// cleanFailedEnvironments removes failed Prepared Environments that match
// this Workspace Template revision. The cleanup is best-effort.
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

func (s *Service) claimPreparedEnvironment(name, templateName string, template domain.Template, environmentID string, opts CreateOptions) (domain.Workspace, error) {
	digests, err := store.Digests(template)
	if err != nil {
		return domain.Workspace{}, err
	}
	workspaceID, err := newID()
	if err != nil {
		return domain.Workspace{}, err
	}
	branch, err := s.resolveWorkspaceBranch(name, workspaceID, template, opts)
	if err != nil {
		return domain.Workspace{}, err
	}
	lock, err := store.AcquireMutationLock(s.options.StateDir)
	if err != nil {
		return domain.Workspace{}, err
	}
	environment, err := s.environments.Find(environmentID)
	if err == nil {
		if environment.Status == domain.EnvironmentFailed {
			err = s.failedEnvironmentError(environment)
		} else if environment.Status != domain.EnvironmentReady || !digests.Matches(environment.TemplateDigest) || environment.FormatVersion != domain.PreparationFormatVersion {
			err = fmt.Errorf("%w: Prepared Environment %q is not ready for this Workspace Template revision", ErrClaimLostRace, environment.ID)
		}
	}
	if err == nil {
		err = s.requireWorkspaceNameAvailable(name)
	}
	if err == nil {
		err = s.validateTicketLinks(opts)
	}
	var workspace domain.Workspace
	if err == nil {
		workspace = s.workspaceForEnvironment(name, templateName, template, environment, workspaceID, branch, opts)
		environment.Status = domain.EnvironmentClaiming
		environment.ClaimReservation = &domain.EnvironmentClaim{Workspace: workspace, ReservedAt: s.now()}
		environment.UpdatedAt = s.now()
		err = s.environments.Save(environment)
	}
	if err == nil {
		err = s.store.Save(workspace)
	}
	if releaseErr := lock.Release(); err == nil && releaseErr != nil {
		err = releaseErr
	}
	if err != nil {
		return workspace, err
	}
	if s.options.AfterClaimReserved != nil {
		s.options.AfterClaimReserved()
	}
	return s.completeEnvironmentClaim(environment.ID, workspace.ID, opts)
}

// resolveWorkspaceBranch selects the Workspace branch name before the claim
// reservation is saved. The order is: the --branch flag, then the rendered
// branch_pattern of the Workspace Template, then the default pattern
// {prefix}{name}. The resolved name must be a valid Git branch name and must
// not be a repository default branch. When the resolved name already exists
// in a Repository Cache, twt falls back to twt/<name>-<id8>.
func (s *Service) resolveWorkspaceBranch(name, workspaceID string, template domain.Template, opts CreateOptions) (string, error) {
	fallback := "twt/" + name + "-" + workspaceID[:8]
	candidate, err := s.validateBranchSelection(name, workspaceID, template, opts)
	if err != nil {
		return "", err
	}
	for _, spec := range template.Repositories {
		cachePath := s.cachePath(spec.Name, spec.Clone.URL)
		cacheInfo, statErr := os.Stat(cachePath)
		if statErr != nil || !cacheInfo.IsDir() {
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

// validateBranchSelection renders the branch candidate and refuses a name
// that equals a repository default branch. Dry-run validation shares it with
// the claim path, so both agree before any change.
func (s *Service) validateBranchSelection(name, workspaceID string, template domain.Template, opts CreateOptions) (string, error) {
	candidate, err := s.workspaceBranchCandidate(name, workspaceID, template, opts)
	if err != nil {
		return "", err
	}
	for _, spec := range template.Repositories {
		repositoryDefault := spec.DefaultBranch
		cachePath := s.cachePath(spec.Name, spec.Clone.URL)
		cacheInfo, statErr := os.Stat(cachePath)
		if statErr == nil && cacheInfo.IsDir() {
			// Deliberate: a cache without a readable HEAD must not block
			// branch selection.
			repositoryDefault, _ = defaultBranch(cachePath, spec)
		}
		if repositoryDefault != "" && candidate == repositoryDefault {
			return "", clierr.New(clierr.InvalidUsage, "branch %q is the default branch of repository %q; use --branch to set a different branch name", candidate, spec.Name)
		}
	}
	return candidate, nil
}

// workspaceBranchCandidate renders and validates the Workspace branch name before
// the repository checks. The --branch flag wins and ignores the branch
// prefix; otherwise twt renders the branch pattern.
func (s *Service) workspaceBranchCandidate(name, workspaceID string, template domain.Template, opts CreateOptions) (string, error) {
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
	candidate := domain.RenderBranchPattern(pattern, opts.BranchPrefix, name, workspaceID[:8])
	if err := domain.ValidateBranchName(candidate); err != nil {
		if opts.BranchPrefix != "" {
			withoutPrefix := domain.RenderBranchPattern(pattern, "", name, workspaceID[:8])
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

func (s *Service) requireWorkspaceNameAvailable(name string) error {
	workspaces, err := s.store.List()
	if err != nil {
		return err
	}
	for _, workspace := range workspaces {
		if workspace.Name == name {
			return clierr.New(clierr.AlreadyExists, "Workspace %q already exists", name)
		}
	}
	environments, err := s.environments.List()
	if err != nil {
		return err
	}
	for _, environment := range environments {
		if environment.ClaimReservation != nil && environment.ClaimReservation.Workspace.Name == name {
			return clierr.New(clierr.AlreadyExists, "Workspace %q is already reserved by a Prepared Environment claim", name)
		}
	}
	return nil
}

func (s *Service) workspaceForEnvironment(name, templateName string, template domain.Template, environment domain.PreparedEnvironment, id, branch string, opts CreateOptions) domain.Workspace {
	now := s.now()
	workspace := domain.Workspace{
		Version: domain.WorkspaceVersion, ID: id, Name: name, TemplateName: templateName,
		TemplateSnapshot: template, EnvironmentID: environment.ID, Status: domain.WorkspaceInitializing,
		Project: opts.Project, Tickets: append([]string(nil), opts.Tickets...),
		Root: environment.Root, TmuxSession: sessionName(templateName, name), CreatedAt: now, UpdatedAt: now,
	}
	workspace.Steps = append(workspace.Steps, newStep("workspace_root", domain.StepWorkspaceRoot, ""))
	for _, repository := range environment.Repositories {
		workspace.Repositories = append(workspace.Repositories, domain.WorkspaceRepository{
			Name: repository.Name, CachePath: repository.CachePath, Path: repository.Path,
			Branch: branch, WindowName: repository.WindowName,
		})
		workspace.Steps = append(workspace.Steps,
			successfulStep("cache:"+repository.Name, domain.StepCache, repository.Name, now),
			newStep("checkout:"+repository.Name, domain.StepCheckout, repository.Name),
		)
		if repositorySpecHasInitialize(template, repository.Name) {
			workspace.Steps = append(workspace.Steps, successfulStep("repository_init:"+repository.Name, domain.StepRepositoryInit, repository.Name, now))
		}
	}
	workspace.Steps = append(workspace.Steps, newStep("tmux", domain.StepTmux, ""))
	if template.Initialize != nil {
		workspace.Steps = append(workspace.Steps, newStep("workspace_init", domain.StepWorkspaceInit, ""))
	}
	workspace.Steps = append(workspace.Steps, agentSteps(template)...)
	return workspace
}

func (s *Service) completeEnvironmentClaim(environmentID, workspaceID string, opts CreateOptions) (domain.Workspace, error) {
	lock, err := store.AcquireEnvironmentLockBlocking(s.options.StateDir, environmentID)
	if err != nil {
		return domain.Workspace{}, err
	}
	defer lock.Release()
	environment, err := s.environments.Find(environmentID)
	if err != nil {
		return domain.Workspace{}, err
	}
	if environment.ClaimReservation == nil || environment.ClaimReservation.Workspace.ID != workspaceID {
		return domain.Workspace{}, fmt.Errorf("Prepared Environment %q does not contain Workspace claim %q", environmentID, workspaceID)
	}
	workspace, err := s.store.Find(workspaceID)
	if err != nil {
		return domain.Workspace{}, err
	}
	if err := s.validateEnvironmentForClaim(environment, workspace); err != nil {
		return workspace, err
	}
	for index := range workspace.Repositories {
		repository := &workspace.Repositories[index]
		spec, prepared, preparedIndex, err := preparedRepositoryFor(environment, repository.Name)
		if err != nil {
			return workspace, err
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
					return fmt.Errorf("create Workspace branch for repository %q: %w", repository.Name, err)
				}
			} else if branch != repository.Branch {
				return fmt.Errorf("claimed checkout for repository %q uses branch %q; expected %q", repository.Name, branch, repository.Branch)
			}
			return nil
		})
		if err != nil {
			return workspace, err
		}
		markWorkspaceStepSucceeded(&workspace, "checkout:"+repository.Name, s.now())
	}
	marker := map[string]string{"owner": "twt", "workspaceId": workspace.ID, "environmentId": environment.ID}
	if err := writeJSON(filepath.Join(workspace.Root, ".twt-owned.json"), marker, 0o600); err != nil {
		return workspace, err
	}
	if err := os.Remove(filepath.Join(workspace.Root, environmentMarkerName)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return workspace, fmt.Errorf("remove Prepared Environment ownership marker: %w", err)
	}
	markWorkspaceStepSucceeded(&workspace, "workspace_root", s.now())
	if err := s.store.Save(workspace); err != nil {
		return workspace, err
	}
	environment.Status = domain.EnvironmentClaimed
	environment.UpdatedAt = s.now()
	if err := s.environments.Save(environment); err != nil {
		return workspace, err
	}
	if err := s.runPending(&workspace); err != nil {
		return workspace, err
	}
	return workspace, nil
}

func (s *Service) validateEnvironmentForClaim(environment domain.PreparedEnvironment, workspace domain.Workspace) error {
	if environment.Status != domain.EnvironmentClaiming {
		return fmt.Errorf("Prepared Environment %q has status %q; expected %q", environment.ID, environment.Status, domain.EnvironmentClaiming)
	}
	if err := validateEnvironmentClaimMarker(environment, workspace); err != nil {
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
func validatePreparedRepositoryForClaim(repository domain.PreparedRepository, workspaceRepository domain.WorkspaceRepository) (string, error) {
	if repository.BaseCommit == "" {
		return "", fmt.Errorf("Prepared Environment repository %q has no base commit", repository.Name)
	}
	if err := worktreeUsesCache(repository.Path, repository.CachePath); err != nil {
		return "", fmt.Errorf("Prepared Environment repository %q does not use its Repository Cache", repository.Name)
	}
	branch, err := output(repository.Path, "git", "branch", "--show-current")
	if err != nil || (branch != "" && branch != workspaceRepository.Branch) {
		return "", fmt.Errorf("Prepared Environment repository %q has an invalid claim branch", repository.Name)
	}
	commit, err := output(repository.Path, "git", "rev-parse", "HEAD")
	if err != nil || commit != repository.BaseCommit {
		return "", fmt.Errorf("Prepared Environment repository %q is not at its saved base commit", repository.Name)
	}
	return branch, nil
}

// claimBaseCommit returns the base commit for the new Workspace branch of one
// repository. It runs inside the repository cache lock. It fetches
// origin/<default-branch> unless NoFetch is set, then owns the one record
// update and save that a moved base commit needs.
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

// refreshStaleBase fetches origin/<branch> in the Repository Cache and moves
// the detached checkout to the new tip when the saved base commit is its
// ancestor. It does Git work only and returns the new base commit with the
// effective fetch time. The caller owns the Prepared Environment record
// update and save.
func (s *Service) refreshStaleBase(repository domain.PreparedRepository, spec domain.RepositorySpec, branch string, readyAt *time.Time) (string, *time.Time, error) {
	base := repository.BaseCommit
	fetchedAt := readyAt
	if err := fetchOrigin(repository.CachePath, spec.Clone.Depth, branch); err != nil {
		s.report("Warning: twt could not fetch origin for repository %q: %v. twt uses the saved base commit.", repository.Name, err)
	} else {
		now := s.now()
		fetchedAt = &now
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

func validateEnvironmentClaimMarker(environment domain.PreparedEnvironment, workspace domain.Workspace) error {
	if err := validateEnvironmentMarker(environment); err == nil {
		return nil
	}
	data, err := os.ReadFile(filepath.Join(workspace.Root, ".twt-owned.json"))
	if err != nil {
		return fmt.Errorf("Prepared Environment %q has no valid claim ownership marker", environment.ID)
	}
	var marker struct {
		Owner         string `json:"owner"`
		WorkspaceID   string `json:"workspaceId"`
		ProjectID     string `json:"projectId"`
		EnvironmentID string `json:"environmentId"`
	}
	if json.Unmarshal(data, &marker) != nil {
		return fmt.Errorf("Prepared Environment %q has an invalid claim ownership marker", environment.ID)
	}
	if marker.WorkspaceID == "" {
		marker.WorkspaceID = marker.ProjectID
	}
	if marker.Owner != "twt" || marker.WorkspaceID != workspace.ID || marker.EnvironmentID != environment.ID {
		return fmt.Errorf("Prepared Environment %q has an invalid claim ownership marker", environment.ID)
	}
	return nil
}

func markWorkspaceStepSucceeded(workspace *domain.Workspace, id string, now time.Time) {
	for index := range workspace.Steps {
		if workspace.Steps[index].ID == id {
			workspace.Steps[index].Status = domain.StepSucceeded
			workspace.Steps[index].Attempts++
			workspace.Steps[index].StartedAt = &now
			workspace.Steps[index].FinishedAt = &now
			workspace.Steps[index].Error = ""
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
