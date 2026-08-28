package workspace

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/jpugliesi/tmux-worktree/internal/agent"
	"github.com/jpugliesi/tmux-worktree/internal/clierr"
	"github.com/jpugliesi/tmux-worktree/internal/domain"
	"github.com/jpugliesi/tmux-worktree/internal/store"
)

// ReleaseOptions records the user authorization for one release.
type ReleaseOptions struct {
	Force               bool
	ExpectedFingerprint string
	// Prevalidated means the CLI already inspected this fingerprint and
	// approved the dirty-state policy. Service callers leave it false.
	Prevalidated bool
}

// ReleasePlan describes the state that a release can discard.
type ReleasePlan struct {
	WorkspaceID   string   `json:"workspaceId"`
	Name          string   `json:"name"`
	Dirty         bool     `json:"dirty"`
	Paths         []string `json:"paths,omitempty"`
	GitOperation  string   `json:"gitOperation,omitempty"`
	GitRepository string   `json:"gitRepository,omitempty"`
	Fingerprint   string   `json:"fingerprint"`
}

type repositoryReleaseState struct {
	name         string
	path         string
	dirty        bool
	paths        []string
	gitOperation string
	gitState     string
	fingerprint  string
}

// ValidateRelease checks release policy without changing state.
func (s *Service) ValidateRelease(reference, currentPane string, opts ReleaseOptions) error {
	plan, err := s.InspectRelease(reference, currentPane)
	if err != nil {
		return err
	}
	if plan.GitOperation != "" {
		return clierr.New(clierr.UnsafeState, "Workspace %q has active Git operation %q in repository %q", plan.Name, plan.GitOperation, plan.GitRepository)
	}
	if plan.Dirty && !opts.Force {
		return clierr.WithHint(
			clierr.New(clierr.UnsafeState, "Workspace %q has uncommitted changes", plan.Name),
			"Save the changes or run the command with --force.")
	}
	return nil
}

// InspectRelease validates a release and returns an opaque state fingerprint.
func (s *Service) InspectRelease(reference, currentPane string) (ReleasePlan, error) {
	workspace, _, err := s.validateArchive(reference, currentPane)
	if err != nil {
		return ReleasePlan{}, err
	}
	return s.inspectRelease(workspace)
}

func (s *Service) inspectRelease(workspace domain.Workspace) (ReleasePlan, error) {
	plan, _, err := s.inspectReleaseState(workspace)
	return plan, err
}

func (s *Service) inspectReleaseState(workspace domain.Workspace) (ReleasePlan, []repositoryReleaseState, error) {
	plan := ReleasePlan{WorkspaceID: workspace.ID, Name: workspace.Name}
	if workspace.Adopted || !workspace.Materialized || workspace.Root == "" {
		plan.Fingerprint = releaseFingerprint(nil)
		return plan, nil, nil
	}
	states := make([]repositoryReleaseState, 0, len(workspace.Repositories))
	for _, repository := range workspace.Repositories {
		state, err := inspectRepositoryRelease(repository)
		if err != nil {
			return plan, nil, err
		}
		states = append(states, state)
		if state.dirty {
			plan.Dirty = true
			plan.Paths = append(plan.Paths, state.paths...)
		}
		if plan.GitOperation == "" && state.gitOperation != "" {
			plan.GitOperation = state.gitOperation
			plan.GitRepository = state.name
		}
	}
	plan.Fingerprint = releaseFingerprint(states)
	return plan, states, nil
}

// Release archives a Workspace and returns its physical worktrees to its
// Prepared Environment. It keeps the Workspace branches and logical state.
func (s *Service) Release(reference, currentPane string, opts ReleaseOptions) (ArchiveResult, error) {
	workspace, _, err := s.validateArchive(reference, currentPane)
	if err != nil {
		return ArchiveResult{}, err
	}
	if workspace.Adopted || workspace.EnvironmentID == "" {
		return s.Archive(workspace.ID, currentPane)
	}
	plan, err := s.releasePlan(workspace, opts)
	if err != nil {
		return ArchiveResult{Workspace: workspace}, err
	}

	stopped, err := agent.NewService(s.options.StateDir, s.options.TmuxSocket).Live(workspace.ID)
	if err != nil {
		return ArchiveResult{Workspace: workspace}, err
	}
	environmentLock, err := s.reserveRelease(&workspace, plan, opts, "")
	if err != nil {
		return ArchiveResult{Workspace: workspace}, err
	}
	defer environmentLock.Release()
	if err := s.stopWorkspaceSessions(workspace); err != nil {
		return ArchiveResult{Workspace: workspace, StoppedAgents: stopped}, err
	}
	if err := s.clearAgentPanes(workspace.ID); err != nil {
		return ArchiveResult{Workspace: workspace, StoppedAgents: stopped}, err
	}

	environment, err := s.cleanReleasedEnvironment(workspace, plan, opts)
	if err != nil {
		return ArchiveResult{Workspace: workspace, StoppedAgents: stopped}, err
	}
	released, err := s.finalizeReleasedEnvironment(workspace, &environment)
	if err != nil {
		return ArchiveResult{Workspace: released, StoppedAgents: stopped}, err
	}
	return ArchiveResult{Workspace: released, StoppedAgents: stopped}, nil
}

func (s *Service) releasePlan(workspace domain.Workspace, opts ReleaseOptions) (ReleasePlan, error) {
	plan := ReleasePlan{WorkspaceID: workspace.ID, Name: workspace.Name, Fingerprint: opts.ExpectedFingerprint}
	if opts.Prevalidated && plan.Fingerprint != "" {
		return plan, nil
	}
	inspected, err := s.inspectRelease(workspace)
	if err != nil {
		return plan, err
	}
	if inspected.GitOperation != "" {
		return inspected, clierr.New(clierr.UnsafeState, "Workspace %q has active Git operation %q in repository %q", workspace.Name, inspected.GitOperation, inspected.GitRepository)
	}
	if inspected.Dirty && !opts.Force {
		return inspected, clierr.WithHint(clierr.New(clierr.UnsafeState, "Workspace %q has uncommitted changes", workspace.Name), "Save the changes or run the command with --force.")
	}
	if opts.ExpectedFingerprint != "" && opts.ExpectedFingerprint != inspected.Fingerprint {
		return inspected, clierr.New(clierr.UnsafeState, "Workspace %q changed after release approval", workspace.Name)
	}
	return inspected, nil
}

func (s *Service) cleanReleasedEnvironment(workspace domain.Workspace, plan ReleasePlan, opts ReleaseOptions) (domain.PreparedEnvironment, error) {
	environment, err := s.environments.Find(workspace.EnvironmentID)
	if err != nil {
		return environment, err
	}
	if environment.Status != domain.EnvironmentReleasing || environment.Assignment == nil || environment.Assignment.Workspace.ID != workspace.ID {
		return environment, fmt.Errorf("Prepared Environment %q does not contain the release assignment", environment.ID)
	}
	if environment.Assignment.Generation != environment.Generation {
		return environment, fmt.Errorf("Prepared Environment %q release generation changed", environment.ID)
	}
	current, states, err := s.inspectReleaseState(workspace)
	if err != nil {
		return environment, err
	}
	if current.Fingerprint != plan.Fingerprint {
		return environment, clierr.New(clierr.UnsafeState, "Workspace %q changed after release approval. Its worktrees stay assigned", workspace.Name)
	}
	if current.GitOperation != "" {
		return environment, clierr.New(clierr.UnsafeState, "Workspace %q has active Git operation %q in repository %q", workspace.Name, current.GitOperation, current.GitRepository)
	}
	if current.Dirty {
		if !opts.Force {
			return environment, clierr.New(clierr.UnsafeState, "Workspace %q has uncommitted changes", workspace.Name)
		}
		for _, repository := range workspace.Repositories {
			if err := cleanRepositoryForRelease(repository.Path); err != nil {
				return environment, err
			}
		}
	}
	baselines := make(map[string]repositoryReleaseState, len(states))
	for _, state := range states {
		baselines[state.name] = state
	}
	if current.Dirty {
		baselines = nil
	}
	if err := s.runRecycleHooks(workspace, baselines); err != nil {
		return environment, err
	}
	if err := s.detachReleasedRepositories(workspace, &environment); err != nil {
		return environment, err
	}
	if err := os.Remove(filepath.Join(workspace.Root, ".twt-owned.json")); err != nil && !errors.Is(err, os.ErrNotExist) {
		return environment, fmt.Errorf("remove Workspace ownership marker: %w", err)
	}
	if err := writeEnvironmentMarker(environment); err != nil {
		return environment, err
	}
	return environment, nil
}

func (s *Service) finalizeReleasedEnvironment(workspace domain.Workspace, environment *domain.PreparedEnvironment) (domain.Workspace, error) {
	released := workspace
	released.EnvironmentID = ""
	released.Root = ""
	released.TmuxSession = ""
	released.Materialized = false
	for index := range released.Repositories {
		released.Repositories[index].Path = ""
	}
	now := s.now()
	released.Status = domain.WorkspaceArchived
	released.ArchivedAt = &now
	released.UpdatedAt = now
	if err := s.store.Save(released); err != nil {
		return workspace, err
	}
	environment.Status = domain.EnvironmentReady
	environment.Assignment = nil
	environment.ReadyAt = &now
	environment.UpdatedAt = now
	environment.Failure = ""
	if err := s.environments.Save(*environment); err != nil {
		return released, err
	}
	if s.options.AfterReleaseFinalized != nil {
		s.options.AfterReleaseFinalized(workspace.TemplateName)
	}
	return released, nil
}

func (s *Service) runRecycleHooks(workspace domain.Workspace, baselines map[string]repositoryReleaseState) error {
	for _, repository := range workspace.Repositories {
		spec, _, err := repositoryFor(workspace, repository.Name)
		if err != nil {
			return err
		}
		if spec.Recycle == nil || len(spec.Recycle.Command) == 0 {
			continue
		}
		before, found := baselines[repository.Name]
		if !found {
			before, err = inspectRepositoryRelease(repository)
			if err != nil {
				return err
			}
		}
		command := exec.Command(spec.Recycle.Command[0], spec.Recycle.Command[1:]...)
		command.Dir = repository.Path
		command.Env = append(os.Environ(), workspaceEnvironment(workspace)...)
		command.Env = append(command.Env,
			"TWT_ENVIRONMENT_ID="+workspace.EnvironmentID,
			"TWT_TEMPLATE_NAME="+workspace.TemplateName,
			"TWT_REPOSITORY_NAME="+repository.Name,
			"TWT_REPOSITORY_PATH="+repository.Path,
		)
		output, err := command.CombinedOutput()
		if err != nil {
			return fmt.Errorf("run recycle command for repository %q: %w: %s", repository.Name, err, strings.TrimSpace(string(output)))
		}
		state, err := inspectRepositoryRelease(repository)
		if err != nil {
			return err
		}
		if state.gitOperation != "" || state.dirty || state.fingerprint != before.fingerprint {
			return clierr.New(clierr.UnsafeState, "recycle command left repository %q with tracked or nonignored changes", repository.Name)
		}
	}
	return nil
}

func (s *Service) reserveRelease(workspace *domain.Workspace, plan ReleasePlan, opts ReleaseOptions, sourceSessionID string) (*store.NamedLock, error) {
	lock, err := store.AcquireMutationLock(s.options.StateDir)
	if err != nil {
		return nil, err
	}
	defer lock.Release()
	environmentLock, err := store.AcquireEnvironmentLockBlocking(s.options.StateDir, workspace.EnvironmentID)
	if err != nil {
		return nil, err
	}
	releaseEnvironmentLock := true
	defer func() {
		if releaseEnvironmentLock {
			_ = environmentLock.Release()
		}
	}()
	latest, err := s.store.Find(workspace.ID)
	if err != nil {
		return nil, err
	}
	if latest.EnvironmentID != workspace.EnvironmentID || latest.Root != workspace.Root {
		return nil, clierr.New(clierr.UnsafeState, "Workspace %q physical ownership changed", workspace.Name)
	}
	environment, err := s.environments.Find(workspace.EnvironmentID)
	if err != nil {
		return nil, err
	}
	if environment.Status != domain.EnvironmentClaimed || environment.Assignment == nil || environment.Assignment.Workspace.ID != workspace.ID {
		return nil, clierr.New(clierr.UnsafeState, "Workspace %q does not own Prepared Environment %q", workspace.Name, environment.ID)
	}
	environment.Generation++
	environment.Status = domain.EnvironmentReleasing
	environment.Assignment = &domain.EnvironmentAssignment{
		Generation: environment.Generation, Kind: domain.EnvironmentAssignmentRelease,
		Phase: domain.EnvironmentAssignmentReserved, Workspace: latest, Fingerprint: plan.Fingerprint,
		Force: opts.Force, SourceSessionID: sourceSessionID, ReservedAt: s.now(),
	}
	environment.UpdatedAt = s.now()
	if err := s.environments.Save(environment); err != nil {
		return nil, err
	}
	*workspace = latest
	releaseEnvironmentLock = false
	return environmentLock, nil
}

func (s *Service) stopWorkspaceSessions(workspace domain.Workspace) error {
	sessions, err := s.ownedSessions(workspace.ID)
	if err != nil {
		return err
	}
	for _, sessionID := range sessions {
		if err := run("", "tmux", s.tmuxArgs("kill-session", "-t", sessionID)...); err != nil {
			return fmt.Errorf("stop Workspace tmux session: %w", err)
		}
	}
	return nil
}

func (s *Service) detachReleasedRepositories(workspace domain.Workspace, environment *domain.PreparedEnvironment) error {
	for _, repository := range workspace.Repositories {
		_, prepared, _, err := preparedRepositoryFor(*environment, repository.Name)
		if err != nil {
			return err
		}
		base := repository.BaseCommit
		if base == "" {
			base = prepared.BaseCommit
		}
		if err := s.withCacheLock(repository.CachePath, func() error {
			if err := run(repository.Path, "git", "switch", "--detach", base); err != nil {
				return fmt.Errorf("detach repository %q for reuse: %w", repository.Name, err)
			}
			return nil
		}); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) openReleasedWorkspace(workspace domain.Workspace) (domain.Workspace, error) {
	environment, err := s.Prepare(workspace.TemplateName, workspace.TemplateSnapshot)
	if err != nil {
		return workspace, err
	}
	lock, err := store.AcquireMutationLock(s.options.StateDir)
	if err != nil {
		return workspace, err
	}
	latest, err := s.store.Find(workspace.ID)
	if err == nil && (latest.Status != domain.WorkspaceArchived || latest.Materialized || latest.EnvironmentID != "") {
		err = clierr.New(clierr.UnsafeState, "Workspace %q changed before it could acquire a Prepared Environment", latest.Name)
	}
	if err == nil {
		environment, err = s.environments.Find(environment.ID)
	}
	if err == nil && environment.Status != domain.EnvironmentReady {
		err = fmt.Errorf("%w: Prepared Environment %q is not ready", ErrClaimLostRace, environment.ID)
	}
	if err == nil {
		latest = workspaceForReopen(latest, environment, s.now())
		environment.Generation++
		environment.Status = domain.EnvironmentClaiming
		environment.Assignment = &domain.EnvironmentAssignment{
			Generation: environment.Generation, Kind: domain.EnvironmentAssignmentClaim,
			Phase: domain.EnvironmentAssignmentReserved, Workspace: latest, ReservedAt: s.now(),
		}
		environment.UpdatedAt = s.now()
		err = s.environments.Save(environment)
	}
	if err == nil {
		err = s.store.Save(latest)
	}
	if releaseErr := lock.Release(); err == nil && releaseErr != nil {
		err = releaseErr
	}
	if err != nil {
		return latest, err
	}
	return s.completeEnvironmentClaim(environment.ID, latest.ID, CreateOptions{})
}

// Reconcile completes durable release transitions after the source tmux
// session stops. It never makes an Environment ready while an owned session
// can still change its worktrees.
func (s *Service) Reconcile() error {
	lock, err := store.AcquireMutationLock(s.options.StateDir)
	if err != nil {
		return err
	}
	defer lock.Release()
	environments, err := s.environments.List()
	if err != nil {
		return err
	}
	for _, environment := range environments {
		if environment.Status != domain.EnvironmentReleasing || environment.Assignment == nil {
			continue
		}
		if err := s.reconcileReleasedEnvironment(environment); errors.Is(err, store.ErrLockHeld) {
			continue
		} else if err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) reconcileReleasedEnvironment(environment domain.PreparedEnvironment) error {
	environmentLock, err := store.AcquireEnvironmentLock(s.options.StateDir, environment.ID)
	if err != nil {
		return err
	}
	defer environmentLock.Release()
	latest, err := s.environments.Find(environment.ID)
	if err != nil {
		return err
	}
	if latest.Status != domain.EnvironmentReleasing || latest.Assignment == nil {
		return nil
	}
	workspace, err := s.store.Find(latest.Assignment.Workspace.ID)
	if err != nil {
		return nil
	}
	assignment := latest.Assignment
	if workspace.Materialized || workspace.EnvironmentID != "" || workspace.Root != "" {
		if assignment.Phase != domain.EnvironmentAssignmentSessionStopPending {
			return nil
		}
		present, err := s.preparedSessionPresent(assignment.SourceSessionID, workspace.ID)
		if err != nil {
			return err
		}
		if present {
			return nil
		}
		if err := validateReleasedEnvironment(workspace, latest); err != nil {
			return err
		}
		if err := s.clearAgentPanes(workspace.ID); err != nil {
			return err
		}
		_, err = s.finalizeReleasedEnvironment(workspace, &latest)
		return err
	}
	now := s.now()
	latest.Status = domain.EnvironmentReady
	latest.Assignment = nil
	latest.ReadyAt = &now
	latest.UpdatedAt = now
	return s.environments.Save(latest)
}

// validateReleasedEnvironment confirms that no process changed a worktree
// after cleanup and before the source tmux session stopped.
func validateReleasedEnvironment(workspace domain.Workspace, environment domain.PreparedEnvironment) error {
	if err := validateEnvironmentMarker(environment); err != nil {
		return err
	}
	for _, repository := range workspace.Repositories {
		_, prepared, _, err := preparedRepositoryFor(environment, repository.Name)
		if err != nil {
			return err
		}
		operation, _, err := activeGitOperation(repository.Path)
		if err != nil {
			return err
		}
		status, err := gitBytes(repository.Path, "status", "--porcelain=v1", "-z", "--ignore-submodules=none")
		if err != nil {
			return fmt.Errorf("inspect released repository %q: %w", repository.Name, err)
		}
		head, err := output(repository.Path, "git", "rev-parse", "HEAD")
		if err != nil {
			return fmt.Errorf("inspect released repository %q HEAD: %w", repository.Name, err)
		}
		base := repository.BaseCommit
		if base == "" {
			base = prepared.BaseCommit
		}
		if operation != "" || len(status) != 0 || head != base {
			return clierr.WithHint(
				clierr.New(clierr.UnsafeState, "Workspace %q changed after release cleanup in repository %q. Prepared Environment %q stays unavailable", workspace.Name, repository.Name, environment.ID),
				fmt.Sprintf("Run 'twt workspaces open %s' to inspect the Workspace.", workspace.ID),
			)
		}
	}
	return nil
}

// restoreBoundWorkspace cancels an incomplete release when the user opens the
// archived Workspace that still owns the physical worktrees.
func (s *Service) restoreBoundWorkspace(workspace *domain.Workspace) error {
	environmentLock, err := store.AcquireEnvironmentLock(s.options.StateDir, workspace.EnvironmentID)
	if errors.Is(err, store.ErrLockHeld) {
		return clierr.New(clierr.Locked, "Workspace %q release is in progress", workspace.Name)
	}
	if err != nil {
		return err
	}
	defer environmentLock.Release()
	environment, err := s.environments.Find(workspace.EnvironmentID)
	if err != nil {
		return err
	}
	if environment.Status == domain.EnvironmentClaimed && environment.Assignment != nil && environment.Assignment.Workspace.ID == workspace.ID {
		return nil
	}
	if environment.Status != domain.EnvironmentReleasing {
		return clierr.New(clierr.UnsafeState, "Workspace %q has inconsistent Prepared Environment status %q", workspace.Name, environment.Status)
	}
	if environment.Assignment == nil || environment.Assignment.Workspace.ID != workspace.ID {
		return clierr.New(clierr.UnsafeState, "Prepared Environment %q has no matching release assignment", environment.ID)
	}
	if err := s.restoreReleaseOwnership(*workspace, &environment); err != nil {
		return err
	}
	environment.Generation++
	environment.Status = domain.EnvironmentClaimed
	environment.Assignment = &domain.EnvironmentAssignment{
		Generation: environment.Generation, Kind: domain.EnvironmentAssignmentClaim,
		Phase: domain.EnvironmentAssignmentActive, Workspace: *workspace, ReservedAt: s.now(),
	}
	environment.UpdatedAt = s.now()
	return s.environments.Save(environment)
}

func workspaceForReopen(workspace domain.Workspace, environment domain.PreparedEnvironment, now time.Time) domain.Workspace {
	workspace.EnvironmentID = environment.ID
	workspace.EnvironmentDigest = environment.TemplateDigest
	workspace.Materialized = true
	workspace.Root = environment.Root
	workspace.TmuxSession = sessionName(workspace.TemplateName, workspace.Name)
	workspace.Status = domain.WorkspaceInitializing
	workspace.ArchivedAt = nil
	workspace.UpdatedAt = now

	preparedByName := make(map[string]domain.PreparedRepository, len(environment.Repositories))
	for _, repository := range environment.Repositories {
		preparedByName[repository.Name] = repository
	}
	workspace.Steps = []domain.SetupStep{newStep("workspace_root", domain.StepWorkspaceRoot, "")}
	for index := range workspace.Repositories {
		repository := &workspace.Repositories[index]
		prepared := preparedByName[repository.Name]
		repository.CachePath = prepared.CachePath
		repository.Path = prepared.Path
		if repository.BaseCommit == "" {
			repository.BaseCommit = prepared.BaseCommit
		}
		workspace.Steps = append(workspace.Steps,
			successfulStep("cache:"+repository.Name, domain.StepCache, repository.Name, now),
			newStep("checkout:"+repository.Name, domain.StepCheckout, repository.Name),
		)
		if repositorySpecHasInitialize(workspace.TemplateSnapshot, repository.Name) {
			workspace.Steps = append(workspace.Steps, successfulStep("repository_init:"+repository.Name, domain.StepRepositoryInit, repository.Name, now))
		}
	}
	workspace.Steps = append(workspace.Steps, newStep("tmux", domain.StepTmux, ""))
	if workspace.TemplateSnapshot.Initialize != nil {
		workspace.Steps = append(workspace.Steps, newStep("workspace_init", domain.StepWorkspaceInit, ""))
	}
	for _, step := range agentSteps(workspace.TemplateSnapshot) {
		step.Status = domain.StepSucceeded
		step.Attempts = 1
		step.StartedAt = &now
		step.FinishedAt = &now
		workspace.Steps = append(workspace.Steps, step)
	}
	return workspace
}

func inspectRepositoryRelease(repository domain.WorkspaceRepository) (repositoryReleaseState, error) {
	state := repositoryReleaseState{name: repository.Name, path: repository.Path}
	status, err := gitBytes(repository.Path, "status", "--porcelain=v1", "-z", "--ignore-submodules=none")
	if err != nil {
		return state, fmt.Errorf("inspect repository %q: %w", repository.Name, err)
	}
	state.dirty = len(status) > 0
	state.paths = dirtyPaths(strings.ReplaceAll(strings.TrimRight(string(status), "\x00"), "\x00", "\n"), 20)
	operation, gitState, err := activeGitOperation(repository.Path)
	if err != nil {
		return state, err
	}
	state.gitOperation = operation
	state.gitState = gitState

	hash := sha256.New()
	hash.Write(status)
	hash.Write([]byte{0})
	// An empty status proves that the diffs are empty and that no untracked
	// file exists, so a clean worktree needs only the HEAD commit. This keeps
	// the fingerprint of a large clean worktree at one Git scan.
	commands := [][]string{{"rev-parse", "HEAD"}}
	if state.dirty {
		commands = append(commands,
			[]string{"diff", "--binary"},
			[]string{"diff", "--cached", "--binary"},
			[]string{"submodule", "status", "--recursive"},
		)
	}
	for _, args := range commands {
		data, err := gitBytes(repository.Path, args...)
		if err != nil {
			return state, fmt.Errorf("fingerprint repository %q: %w", repository.Name, err)
		}
		hash.Write(data)
		hash.Write([]byte{0})
	}
	var untracked []byte
	if state.dirty {
		untracked, err = gitBytes(repository.Path, "ls-files", "--others", "--exclude-standard", "-z")
		if err != nil {
			return state, fmt.Errorf("list untracked files in repository %q: %w", repository.Name, err)
		}
	}
	paths := strings.Split(strings.TrimRight(string(untracked), "\x00"), "\x00")
	sort.Strings(paths)
	for _, relative := range paths {
		if relative == "" {
			continue
		}
		path := filepath.Join(repository.Path, filepath.FromSlash(relative))
		info, err := os.Lstat(path)
		if err != nil {
			return state, fmt.Errorf("fingerprint untracked path %q: %w", relative, err)
		}
		hash.Write([]byte(relative))
		hash.Write([]byte{0})
		if info.Mode()&os.ModeSymlink != 0 {
			target, err := os.Readlink(path)
			if err != nil {
				return state, err
			}
			hash.Write([]byte(target))
		} else if info.Mode().IsRegular() {
			data, err := os.ReadFile(path)
			if err != nil {
				return state, err
			}
			hash.Write(data)
		}
		hash.Write([]byte{0})
	}
	state.fingerprint = hex.EncodeToString(hash.Sum(nil))
	return state, nil
}

func releaseFingerprint(states []repositoryReleaseState) string {
	hash := sha256.New()
	for _, state := range states {
		hash.Write([]byte(state.name))
		hash.Write([]byte{0})
		hash.Write([]byte(state.fingerprint))
		hash.Write([]byte{0})
		hash.Write([]byte(state.gitOperation))
		hash.Write([]byte{0})
		hash.Write([]byte(state.gitState))
		hash.Write([]byte{0})
	}
	return hex.EncodeToString(hash.Sum(nil))
}

func activeGitOperation(repositoryPath string) (string, string, error) {
	gitDirectory, err := repositoryGitDirectory(repositoryPath)
	if err != nil {
		return "", "", err
	}
	operations := []struct {
		name string
		path string
	}{
		{"merge", "MERGE_HEAD"}, {"rebase", "rebase-merge"}, {"rebase", "rebase-apply"},
		{"cherry-pick", "CHERRY_PICK_HEAD"}, {"revert", "REVERT_HEAD"}, {"bisect", "BISECT_LOG"},
	}
	for _, operation := range operations {
		path := filepath.Join(gitDirectory, operation.path)
		if _, err := os.Stat(path); err == nil {
			state, err := hashGitOperationPath(path)
			return operation.name, state, err
		} else if !errors.Is(err, os.ErrNotExist) {
			return "", "", err
		}
	}
	return "", "", nil
}

func repositoryGitDirectory(repositoryPath string) (string, error) {
	dotGit := filepath.Join(repositoryPath, ".git")
	info, err := os.Stat(dotGit)
	if err != nil {
		return "", fmt.Errorf("find Git directory for repository %q: %w", repositoryPath, err)
	}
	if info.IsDir() {
		return dotGit, nil
	}
	data, err := os.ReadFile(dotGit)
	if err != nil {
		return "", fmt.Errorf("read Git directory for repository %q: %w", repositoryPath, err)
	}
	value := strings.TrimSpace(string(data))
	const prefix = "gitdir: "
	if !strings.HasPrefix(value, prefix) {
		return "", fmt.Errorf("repository %q has an invalid .git file", repositoryPath)
	}
	gitDirectory := strings.TrimSpace(strings.TrimPrefix(value, prefix))
	if gitDirectory == "" {
		return "", fmt.Errorf("repository %q has an empty .git file", repositoryPath)
	}
	if !filepath.IsAbs(gitDirectory) {
		gitDirectory = filepath.Join(repositoryPath, gitDirectory)
	}
	return filepath.Clean(gitDirectory), nil
}

func hashGitOperationPath(root string) (string, error) {
	hash := sha256.New()
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		hash.Write([]byte(relative))
		hash.Write([]byte{0})
		if entry.Type()&os.ModeSymlink != 0 {
			target, err := os.Readlink(path)
			if err != nil {
				return err
			}
			hash.Write([]byte(target))
		} else if entry.Type().IsRegular() {
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			hash.Write(data)
		}
		hash.Write([]byte{0})
		return nil
	})
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func cleanRepositoryForRelease(path string) error {
	commands := [][]string{
		{"reset", "--hard"},
		{"submodule", "foreach", "--recursive", "git reset --hard"},
		{"clean", "-ffd"},
		{"submodule", "foreach", "--recursive", "git clean -ffd"},
	}
	for _, args := range commands {
		if err := run(path, "git", args...); err != nil {
			return fmt.Errorf("clean repository %q for release: %w", path, err)
		}
	}
	return nil
}

func gitBytes(directory string, args ...string) ([]byte, error) {
	command := exec.Command("git", args...)
	command.Dir = directory
	data, err := command.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(data)))
	}
	return data, nil
}
