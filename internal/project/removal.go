package project

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/jpugliesi/tmux-worktree/internal/clierr"
	"github.com/jpugliesi/tmux-worktree/internal/domain"
	"github.com/jpugliesi/tmux-worktree/internal/store"
)

type RemovalPlan struct {
	ProjectID    string           `json:"projectId"`
	ProjectName  string           `json:"projectName"`
	ArchivedAt   *time.Time       `json:"archivedAt,omitempty"`
	Worktrees    []string         `json:"worktrees"`
	TmuxSession  string           `json:"tmuxSession"`
	StateRecords int              `json:"stateRecords"`
	Bytes        int64            `json:"bytes,omitempty"`
	Actions      []RemovalAction  `json:"actions"`
	Blockers     []RemovalBlocker `json:"blockers"`
}

type RemovalAction struct {
	Kind   string `json:"kind"`
	Target string `json:"target"`
}

// BlockerCode identifies one kind of removal blocker.
type BlockerCode string

const (
	BlockerNotArchived        BlockerCode = "not_archived"
	BlockerInsideSession      BlockerCode = "inside_session"
	BlockerUnsafeSessions     BlockerCode = "unsafe_sessions"
	BlockerUnsafeSnapshot     BlockerCode = "unsafe_snapshot"
	BlockerUncommittedChanges BlockerCode = "uncommitted_changes"
	BlockerUnpublishedBranch  BlockerCode = "unpublished_branch"
	BlockerUnpublishedUnknown BlockerCode = "unpublished_unknown"
	BlockerInvalidState       BlockerCode = "invalid_state"
	BlockerUnsafeState        BlockerCode = "unsafe_state"
	BlockerProtectedBranch    BlockerCode = "protected_branch"
	BlockerUnexpectedItem     BlockerCode = "unexpected_item"
)

// refusalCode returns the error code that a removal refusal with this
// blocker uses.
func (code BlockerCode) refusalCode() clierr.Code {
	if code == BlockerNotArchived || code == BlockerInsideSession {
		return clierr.PreconditionFailed
	}
	return clierr.UnsafeState
}

// RemovalBlocker is one condition that prevents removal. The plan always
// renders; blockers tell the operator what to correct first.
type RemovalBlocker struct {
	Code    BlockerCode `json:"code"`
	Message string      `json:"message"`
	Hint    string      `json:"hint,omitempty"`
	Paths   []string    `json:"paths,omitempty"`
}

type RemovalOptions struct {
	AllowUnpublished bool
}

// PlanRemoval is the single removal entry point. It always returns a plan;
// it returns a hard error only for infrastructure failures. Safety refusals
// become blockers on the plan.
func (s *Service) PlanRemoval(reference, currentPane string, opts RemovalOptions) (RemovalPlan, error) {
	plan, _, _, err := s.planRemoval(reference, currentPane, opts)
	return plan, err
}

func (s *Service) planRemoval(reference, currentPane string, opts RemovalOptions) (RemovalPlan, domain.Project, []string, error) {
	p, err := s.store.Find(reference)
	if err != nil {
		return RemovalPlan{}, p, nil, err
	}
	plan := RemovalPlan{ProjectID: p.ID, ProjectName: p.Name, TmuxSession: p.TmuxSession, Blockers: []RemovalBlocker{}}
	if p.ArchivedAt != nil {
		archivedAt := *p.ArchivedAt
		plan.ArchivedAt = &archivedAt
	}
	plan.Worktrees = make([]string, 0, len(p.Repositories))
	var actions []RemovalAction
	if p.Adopted {
		// twt did not create the directories of an adopted Project. Removal
		// deletes the twt state only, and releases the session marker.
		actions = []RemovalAction{{Kind: "release_tmux_session", Target: p.ID}}
		for _, repository := range p.Repositories {
			actions = append(actions, RemovalAction{Kind: "keep_directory", Target: repository.Path})
		}
	} else {
		for _, repository := range p.Repositories {
			plan.Worktrees = append(plan.Worktrees, repository.Path)
		}
		actions = []RemovalAction{{Kind: "stop_tmux_session", Target: p.ID}}
		for _, repository := range p.Repositories {
			actions = append(actions,
				RemovalAction{Kind: "remove_worktree", Target: repository.Path},
				RemovalAction{Kind: "delete_branch", Target: repository.Branch},
				RemovalAction{Kind: "keep_repository_cache", Target: repository.CachePath},
			)
		}
		actions = append(actions,
			RemovalAction{Kind: "delete_ownership_marker", Target: filepath.Join(p.Root, ".twt-owned.json")},
			RemovalAction{Kind: "remove_project_root", Target: p.Root},
		)
	}

	if p.Status != domain.ProjectArchived && p.Status != domain.ProjectRemoving && p.Status != domain.ProjectSetupFailed {
		plan.Blockers = append(plan.Blockers, RemovalBlocker{
			Code:    BlockerNotArchived,
			Message: fmt.Sprintf("Project %q is not archived", p.Name),
			Hint:    fmt.Sprintf("Run 'twt projects archive %s' before removal.", p.ID),
		})
	}

	// An adopted Project has no twt-owned layout to validate: its state is
	// only the record itself.
	var stateBlockers []RemovalBlocker
	if !p.Adopted {
		stateBlockers, err = s.validateRemovalState(p)
		if err != nil {
			return plan, p, nil, err
		}
		plan.Blockers = append(plan.Blockers, stateBlockers...)
	}

	allowEmptySnapshot := p.Status == domain.ProjectRemoving || p.Status == domain.ProjectSetupFailed
	snapshotExists, snapshotErr := s.snapshots.ValidateProject(p.ID, allowEmptySnapshot)
	if snapshotErr != nil {
		if clierr.CodeOf(snapshotErr) != clierr.UnsafeState {
			return plan, p, nil, snapshotErr
		}
		plan.Blockers = append(plan.Blockers, RemovalBlocker{Code: BlockerUnsafeSnapshot, Message: snapshotErr.Error()})
	}
	if snapshotExists {
		snapshotDirectory, err := s.snapshots.ProjectDir(p.ID)
		if err != nil {
			return plan, p, nil, err
		}
		actions = append(actions, RemovalAction{Kind: "delete_transcript_snapshot", Target: snapshotDirectory})
	}

	agents, err := store.NewAgentStore(s.options.StateDir).List(p.ID)
	if err != nil {
		return plan, p, nil, err
	}
	for _, agent := range agents {
		actions = append(actions, RemovalAction{Kind: "delete_agent_state", Target: agent.ID})
	}
	if p.EnvironmentID != "" {
		actions = append(actions, RemovalAction{Kind: "delete_environment_record", Target: p.EnvironmentID})
	}
	actions = append(actions, RemovalAction{Kind: "delete_project_state", Target: p.ID})
	plan.Actions = actions
	plan.StateRecords = 1 + len(agents)

	sessions, err := s.ownedSessions(p.ID)
	if err != nil {
		return plan, p, nil, err
	}
	if len(sessions) > 1 {
		plan.Blockers = append(plan.Blockers, RemovalBlocker{
			Code:    BlockerUnsafeSessions,
			Message: fmt.Sprintf("Project %q owns more than one tmux session", p.Name),
		})
	}
	if err := s.requireOutsideOwnedSessions(p.Name, "remove", currentPane, sessions); err != nil {
		if clierr.CodeOf(err) != clierr.PreconditionFailed {
			return plan, p, nil, err
		}
		plan.Blockers = append(plan.Blockers, RemovalBlocker{Code: BlockerInsideSession, Message: err.Error(), Hint: clierr.HintOf(err)})
	}

	if p.Adopted {
		// Removal never inspects or deletes the directories of an adopted
		// Project.
		return plan, p, sessions, nil
	}
	if len(stateBlockers) > 0 {
		// The recorded repository paths are not safe to inspect.
		return plan, p, sessions, nil
	}
	baseCommits := s.recordedBaseCommits(p)
	for _, repository := range p.Repositories {
		if _, err := os.Stat(repository.Path); errors.Is(err, os.ErrNotExist) {
			continue
		} else if err != nil {
			return plan, p, nil, fmt.Errorf("inspect worktree %q: %w", repository.Path, err)
		}
		if _, err := os.Stat(repository.CachePath); errors.Is(err, os.ErrNotExist) {
			// validateRemovalState reports a checkout without a cache.
			continue
		} else if err != nil {
			return plan, p, nil, fmt.Errorf("inspect repository cache: %w", err)
		}
		var repositoryBlockers []RemovalBlocker
		if err := s.withCacheLock(repository.CachePath, func() error {
			status, err := output(repository.Path, "git", "status", "--porcelain")
			if err != nil {
				return fmt.Errorf("inspect worktree %q: %w", repository.Path, err)
			}
			if status != "" {
				repositoryBlockers = append(repositoryBlockers, RemovalBlocker{
					Code:    BlockerUncommittedChanges,
					Message: fmt.Sprintf("worktree %q has uncommitted changes; clean or save them before removal", repository.Path),
					Paths:   dirtyPaths(status, 5),
				})
			}
			if err := ensureOriginFetchRefspec(repository.CachePath); err != nil {
				return err
			}
			exists, err := refExists(repository.CachePath, "refs/heads/"+repository.Branch)
			if err != nil {
				return err
			}
			if !exists {
				return nil
			}
			if base := baseCommits[repository.Name]; base != "" {
				tip, err := output(repository.CachePath, "git", "rev-parse", "refs/heads/"+repository.Branch)
				if err == nil && strings.TrimSpace(tip) == base {
					// The branch has no commits after its recorded base;
					// removal loses no work and needs no remote check.
					return nil
				}
			}
			if opts.AllowUnpublished {
				// The operator accepts unpublished commits; do not read the
				// remote.
				return nil
			}
			published, unknown, err := branchPublished(repository.CachePath, repository.Branch)
			if err != nil {
				return err
			}
			if published {
				return nil
			}
			if unknown {
				origin, _ := output(repository.CachePath, "git", "remote", "get-url", "origin")
				repositoryBlockers = append(repositoryBlockers, RemovalBlocker{
					Code:    BlockerUnpublishedUnknown,
					Message: fmt.Sprintf("twt could not read the remote \"origin\" (%s) to make sure branch %q is published", origin, repository.Branch),
					Hint:    fmt.Sprintf("Connect to the remote and run the command again, or run 'twt projects remove %s --force --apply'.", reference),
				})
				return nil
			}
			repositoryBlockers = append(repositoryBlockers, RemovalBlocker{
				Code:    BlockerUnpublishedBranch,
				Message: fmt.Sprintf("branch %q has commits that are not on the remote \"origin\" and not on another remote-tracking ref", repository.Branch),
				Hint:    fmt.Sprintf("Run 'git -C %s push origin %s' to publish the branch, or run 'twt projects remove %s --force --apply' to remove it without publication.", repository.Path, repository.Branch, reference),
			})
			return nil
		}); err != nil {
			return plan, p, nil, err
		}
		plan.Blockers = append(plan.Blockers, repositoryBlockers...)
	}
	return plan, p, sessions, nil
}

func (s *Service) Remove(reference, currentPane string, opts RemovalOptions) (RemovalPlan, error) {
	lock, err := store.AcquireMutationLock(s.options.StateDir)
	if err != nil {
		return RemovalPlan{}, err
	}
	defer lock.Release()
	plan, p, sessions, err := s.planRemoval(reference, currentPane, opts)
	if err != nil {
		return plan, err
	}
	if len(plan.Blockers) > 0 {
		return plan, removalRefusal(p.Name, plan.Blockers)
	}
	if !p.Adopted {
		// Measure the Project root just before removal. The size feeds the
		// reclaimed-space summary; a plan without removal does not pay for
		// the walk. An adopted Project reclaims no space.
		plan.Bytes, _ = store.DirectoryBytes(p.Root)
	}
	if p.Status != domain.ProjectRemoving {
		p.Status = domain.ProjectRemoving
		p.UpdatedAt = s.now()
		if err := s.store.Save(p); err != nil {
			return plan, err
		}
	}
	for _, sessionID := range sessions {
		if p.Adopted {
			// The user made this session; removal releases it and keeps it
			// running.
			if err := run("", "tmux", s.tmuxArgs("set-option", "-t", sessionID, "-u", "@twt_project_id")...); err != nil {
				return plan, fmt.Errorf("release Project tmux session: %w", err)
			}
			continue
		}
		if err := run("", "tmux", s.tmuxArgs("kill-session", "-t", sessionID)...); err != nil {
			return plan, fmt.Errorf("stop Project tmux session: %w", err)
		}
	}
	if p.Adopted {
		return plan, s.removeState(p)
	}
	for _, repository := range p.Repositories {
		if err := s.withCacheLock(repository.CachePath, func() error {
			if _, err := os.Stat(repository.Path); err == nil {
				if err := run(repository.CachePath, "git", "worktree", "remove", repository.Path); err != nil {
					return fmt.Errorf("remove worktree %q: %w", repository.Path, err)
				}
			}
			if _, err := os.Stat(repository.CachePath); errors.Is(err, os.ErrNotExist) {
				return nil
			} else if err != nil {
				return fmt.Errorf("inspect repository cache: %w", err)
			}
			exists, err := refExists(repository.CachePath, "refs/heads/"+repository.Branch)
			if err != nil {
				return err
			}
			if exists {
				if err := run(repository.CachePath, "git", "branch", "-D", repository.Branch); err != nil {
					return fmt.Errorf("remove Project branch %q: %w", repository.Branch, err)
				}
			}
			return nil
		}); err != nil {
			return plan, err
		}
	}
	if err := os.Remove(filepath.Join(p.Root, ".twt-owned.json")); err != nil && !errors.Is(err, os.ErrNotExist) {
		return plan, fmt.Errorf("remove Project ownership marker: %w", err)
	}
	if err := os.Remove(p.Root); err != nil && !errors.Is(err, os.ErrNotExist) {
		return plan, fmt.Errorf("remove Project root %q: %w", p.Root, err)
	}
	return plan, s.removeState(p)
}

// removeState deletes the twt state records of one Project: its Transcript
// Snapshots, Agent Sessions, Prepared Environment record, and the Project
// record itself. It touches no worktree or user directory.
func (s *Service) removeState(p domain.Project) error {
	if err := s.snapshots.DeleteProject(p.ID, true); err != nil {
		return err
	}
	if err := store.NewAgentStore(s.options.StateDir).DeleteProject(p.ID); err != nil {
		return err
	}
	if p.EnvironmentID != "" {
		if err := s.environments.Delete(p.EnvironmentID); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return s.store.Delete(p.ID)
}

// BulkRemovalPlans returns one removal plan for each archived Project whose
// archive time is at least olderThan in the past. An olderThan of zero
// selects all archived Projects. The oldest archive comes first.
func (s *Service) BulkRemovalPlans(olderThan time.Duration, opts RemovalOptions) ([]RemovalPlan, error) {
	projects, err := s.store.List()
	if err != nil {
		return nil, err
	}
	now := s.now()
	plans := []RemovalPlan{}
	for _, p := range projects {
		if p.Status != domain.ProjectArchived || p.ArchivedAt == nil {
			continue
		}
		if olderThan > 0 && now.Sub(*p.ArchivedAt) < olderThan {
			continue
		}
		plan, err := s.PlanRemoval(p.ID, "", opts)
		if err != nil {
			return nil, err
		}
		// The bulk plan shows the size of each selected Project. An adopted
		// Project reclaims no space.
		if !p.Adopted {
			plan.Bytes, _ = store.DirectoryBytes(p.Root)
		}
		plans = append(plans, plan)
	}
	sort.SliceStable(plans, func(i, j int) bool {
		iTime, jTime := plans[i].ArchivedAt, plans[j].ArchivedAt
		if iTime != nil && jTime != nil && !iTime.Equal(*jTime) {
			return iTime.Before(*jTime)
		}
		return plans[i].ProjectName < plans[j].ProjectName
	})
	return plans, nil
}

// CancelRemoval returns a Project from status "removing" to "archived".
func (s *Service) CancelRemoval(reference string) (domain.Project, error) {
	lock, err := store.AcquireMutationLock(s.options.StateDir)
	if err != nil {
		return domain.Project{}, err
	}
	defer lock.Release()
	p, err := s.store.Find(reference)
	if err != nil {
		return p, err
	}
	if p.Status != domain.ProjectRemoving {
		return p, clierr.New(clierr.PreconditionFailed, "Project %q has status %q; cancel requires status %q", p.Name, p.Status, domain.ProjectRemoving)
	}
	now := s.now()
	p.Status = domain.ProjectArchived
	if p.ArchivedAt == nil {
		p.ArchivedAt = &now
	}
	p.UpdatedAt = now
	if err := s.store.Save(p); err != nil {
		return p, err
	}
	return p, nil
}

func removalRefusal(projectName string, blockers []RemovalBlocker) error {
	code := clierr.UnsafeState
	messages := make([]string, 0, len(blockers))
	hint := ""
	for _, blocker := range blockers {
		if blocker.Code.refusalCode() == clierr.PreconditionFailed {
			code = clierr.PreconditionFailed
		}
		messages = append(messages, blocker.Message)
		if hint == "" && blocker.Hint != "" {
			hint = blocker.Hint
		}
	}
	refusal := clierr.New(code, "Project %q removal is blocked: %s", projectName, strings.Join(messages, "; "))
	if hint != "" {
		refusal = clierr.WithHint(refusal, "%s", hint)
	}
	return refusal
}

// validateRemovalState checks the recorded Project state against the layout
// twt owns. Policy refusals return as blockers; the error return is only
// for infrastructure failures.
func (s *Service) validateRemovalState(p domain.Project) ([]RemovalBlocker, error) {
	blocked := func(code BlockerCode, format string, values ...any) []RemovalBlocker {
		return []RemovalBlocker{{Code: code, Message: fmt.Sprintf(format, values...)}}
	}
	if len(p.ID) < 8 {
		return blocked(BlockerInvalidState, "Project %q has an invalid ID", p.Name), nil
	}
	tolerantStatus := p.Status == domain.ProjectRemoving || p.Status == domain.ProjectSetupFailed
	expectedRoot := filepath.Join(s.options.DataDir, "projects", p.Name+"-"+p.ID[:8])
	if p.EnvironmentID != "" {
		expectedRoot = filepath.Join(s.options.DataDir, "projects", p.EnvironmentID)
		environment, err := s.environments.Find(p.EnvironmentID)
		if errors.Is(err, os.ErrNotExist) && p.Status == domain.ProjectRemoving {
			// A retry can continue after the Prepared Environment record was deleted.
		} else if err != nil {
			return nil, err
		} else if environment.Status != domain.EnvironmentClaimed || environment.ClaimReservation == nil || environment.ClaimReservation.Project.ID != p.ID {
			return blocked(BlockerInvalidState, "Project %q does not own its Prepared Environment", p.Name), nil
		}
	}
	if filepath.Clean(p.Root) != filepath.Clean(expectedRoot) {
		return blocked(BlockerInvalidState, "Project %q has an invalid root path", p.Name), nil
	}
	expectedEntries := map[string]bool{".twt-owned.json": true}
	for _, repository := range p.Repositories {
		spec, _, err := repositoryFor(p, repository.Name)
		if err != nil {
			return blocked(BlockerInvalidState, "%s", err.Error()), nil
		}
		if repository.Path != filepath.Join(p.Root, repository.Name) {
			return blocked(BlockerInvalidState, "repository %q has a checkout path outside its Project root", repository.Name), nil
		}
		if repository.CachePath != s.cachePath(repository.Name, spec.Clone.URL) {
			return blocked(BlockerInvalidState, "repository %q has an invalid cache path", repository.Name), nil
		}
		cacheExists := true
		if _, err := os.Stat(repository.CachePath); errors.Is(err, os.ErrNotExist) {
			cacheExists = false
			if _, checkoutErr := os.Stat(repository.Path); !errors.Is(checkoutErr, os.ErrNotExist) {
				return blocked(BlockerInvalidState, "repository %q has a checkout but no repository cache", repository.Name), nil
			}
		} else if err != nil {
			return nil, fmt.Errorf("inspect repository cache: %w", err)
		} else if err := validateCacheMarker(repository.CachePath, spec.Clone.URL); err != nil {
			return blocked(BlockerUnsafeState, "%s", err.Error()), nil
		}
		if repository.Branch == "" {
			return blocked(BlockerProtectedBranch, "repository %q has no recorded Project branch", repository.Name), nil
		}
		protected := spec.DefaultBranch
		if cacheExists {
			// Deliberate: a cache without a readable HEAD must not block removal.
			protected, _ = defaultBranch(repository.CachePath, spec)
		}
		if protected != "" && repository.Branch == protected {
			return blocked(BlockerProtectedBranch, "repository %q records the default branch %q as its Project branch; removal does not delete the default branch", repository.Name, protected), nil
		}
		expectedEntries[repository.Name] = true
	}
	entries, err := os.ReadDir(p.Root)
	if errors.Is(err, os.ErrNotExist) && tolerantStatus {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("inspect Project root: %w", err)
	}
	markerPresent := false
	for _, entry := range entries {
		if !expectedEntries[entry.Name()] {
			return []RemovalBlocker{{
				Code:    BlockerUnexpectedItem,
				Message: fmt.Sprintf("Project root %q contains unexpected item %q; move it before removal", p.Root, entry.Name()),
				Paths:   []string{entry.Name()},
			}}, nil
		}
		if entry.Name() == ".twt-owned.json" {
			markerPresent = true
		}
	}
	if markerPresent {
		if err := ValidateProjectMarker(p.Root, p.ID); err != nil {
			return blocked(BlockerUnsafeState, "%s", err.Error()), nil
		}
		return nil, nil
	}
	if !tolerantStatus || len(entries) != 0 {
		return blocked(BlockerUnsafeState, "Project root %q has no twt ownership marker", p.Root), nil
	}
	return nil, nil
}

// dirtyPaths returns the first limit paths from a "git status --porcelain"
// output.
func dirtyPaths(status string, limit int) []string {
	var paths []string
	for _, line := range strings.Split(status, "\n") {
		if len(paths) == limit {
			break
		}
		if len(line) < 4 {
			continue
		}
		path := strings.TrimSpace(line[2:])
		if path != "" {
			paths = append(paths, path)
		}
	}
	return paths
}

// recordedBaseCommits maps each repository to the base commit its checkout
// started from, when the Prepared Environment record still exists.
func (s *Service) recordedBaseCommits(p domain.Project) map[string]string {
	if p.EnvironmentID == "" {
		return nil
	}
	environment, err := s.environments.Find(p.EnvironmentID)
	if err != nil {
		return nil
	}
	commits := make(map[string]string, len(environment.Repositories))
	for _, repository := range environment.Repositories {
		commits[repository.Name] = repository.BaseCommit
	}
	return commits
}
