package project

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/jpugliesi/tmux-worktree/internal/domain"
	"github.com/jpugliesi/tmux-worktree/internal/store"
)

type RemovalPlan struct {
	ProjectID    string          `json:"projectId"`
	ProjectName  string          `json:"projectName"`
	Worktrees    []string        `json:"worktrees"`
	TmuxSession  string          `json:"tmuxSession"`
	StateRecords int             `json:"stateRecords"`
	Actions      []RemovalAction `json:"actions"`
}

type RemovalAction struct {
	Kind   string `json:"kind"`
	Target string `json:"target"`
}

func (s *Service) RemovalPlan(reference string) (RemovalPlan, error) {
	p, err := s.store.Find(reference)
	if err != nil {
		return RemovalPlan{}, err
	}
	worktrees := make([]string, 0, len(p.Repositories))
	for _, repository := range p.Repositories {
		worktrees = append(worktrees, repository.Path)
	}
	agents, err := store.NewAgentStore(s.options.StateDir).List(p.ID)
	if err != nil {
		return RemovalPlan{}, err
	}
	actions := []RemovalAction{{Kind: "stop_tmux_session", Target: p.ID}}
	for _, repository := range p.Repositories {
		actions = append(actions,
			RemovalAction{Kind: "remove_worktree", Target: repository.Path},
			RemovalAction{Kind: "delete_branch", Target: repository.Branch},
		)
	}
	actions = append(actions,
		RemovalAction{Kind: "delete_ownership_marker", Target: filepath.Join(p.Root, ".twt2-owned.json")},
		RemovalAction{Kind: "remove_project_root", Target: p.Root},
	)
	snapshotExists, err := s.snapshots.ValidateProject(p.ID, p.Status == domain.ProjectRemoving)
	if err != nil {
		return RemovalPlan{}, err
	}
	if snapshotExists {
		snapshotDirectory, err := s.snapshots.ProjectDir(p.ID)
		if err != nil {
			return RemovalPlan{}, err
		}
		actions = append(actions, RemovalAction{Kind: "delete_transcript_snapshot", Target: snapshotDirectory})
	}
	for _, agent := range agents {
		actions = append(actions, RemovalAction{Kind: "delete_agent_state", Target: agent.ID})
	}
	actions = append(actions, RemovalAction{Kind: "delete_project_state", Target: p.ID})
	return RemovalPlan{ProjectID: p.ID, ProjectName: p.Name, Worktrees: worktrees, TmuxSession: p.TmuxSession, StateRecords: 1 + len(agents), Actions: actions}, nil
}

func (s *Service) Remove(reference, currentPane string) (RemovalPlan, error) {
	lock, err := store.AcquireMutationLock(s.options.StateDir)
	if err != nil {
		return RemovalPlan{}, err
	}
	defer lock.Release()
	p, sessions, err := s.validateRemoval(reference, currentPane)
	if err != nil {
		return RemovalPlan{}, err
	}
	plan, err := s.RemovalPlan(p.ID)
	if err != nil {
		return plan, err
	}
	if p.Status != domain.ProjectRemoving {
		p.Status = domain.ProjectRemoving
		p.UpdatedAt = s.now()
		if err := s.store.Save(p); err != nil {
			return plan, err
		}
	}
	for _, sessionID := range sessions {
		if err := run("", "tmux", s.tmuxArgs("kill-session", "-t", sessionID)...); err != nil {
			return plan, fmt.Errorf("stop Project tmux session: %w", err)
		}
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
	if err := os.Remove(filepath.Join(p.Root, ".twt2-owned.json")); err != nil && !errors.Is(err, os.ErrNotExist) {
		return plan, fmt.Errorf("remove Project ownership marker: %w", err)
	}
	if err := os.Remove(p.Root); err != nil && !errors.Is(err, os.ErrNotExist) {
		return plan, fmt.Errorf("remove Project root %q: %w", p.Root, err)
	}
	if err := s.snapshots.DeleteProject(p.ID, true); err != nil {
		return plan, err
	}
	if err := store.NewAgentStore(s.options.StateDir).DeleteProject(p.ID); err != nil {
		return plan, err
	}
	if p.EnvironmentID != "" {
		if err := s.environments.Delete(p.EnvironmentID); err != nil && !errors.Is(err, os.ErrNotExist) {
			return plan, err
		}
	}
	if err := s.store.Delete(p.ID); err != nil {
		return plan, err
	}
	return plan, nil
}

func (s *Service) ValidateRemoval(reference, currentPane string) error {
	_, _, err := s.validateRemoval(reference, currentPane)
	return err
}

func (s *Service) validateRemoval(reference, currentPane string) (domain.Project, []string, error) {
	p, err := s.store.Find(reference)
	if err != nil {
		return p, nil, err
	}
	if p.Status != domain.ProjectArchived && p.Status != domain.ProjectRemoving {
		return p, nil, fmt.Errorf("Project %q is not archived; run twt2 projects archive %s before removal", p.Name, p.ID)
	}
	if err := s.validateRemovalState(p); err != nil {
		return p, nil, err
	}
	if _, err := s.snapshots.ValidateProject(p.ID, p.Status == domain.ProjectRemoving); err != nil {
		return p, nil, err
	}
	sessions, err := s.ownedSessions(p.ID)
	if err != nil {
		return p, nil, err
	}
	if len(sessions) > 1 {
		return p, nil, fmt.Errorf("Project %q owns more than one tmux session", p.Name)
	}
	if err := s.requireOutsideOwnedSessions(p.Name, "remove", currentPane, sessions); err != nil {
		return p, nil, err
	}
	for _, repository := range p.Repositories {
		if _, err := os.Stat(repository.Path); errors.Is(err, os.ErrNotExist) {
			continue
		} else if err != nil {
			return p, nil, fmt.Errorf("inspect worktree %q: %w", repository.Path, err)
		}
		if err := s.withCacheLock(repository.CachePath, func() error {
			status, err := output(repository.Path, "git", "status", "--porcelain")
			if err != nil {
				return fmt.Errorf("inspect worktree %q: %w", repository.Path, err)
			}
			if status != "" {
				return fmt.Errorf("worktree %q has uncommitted changes; clean or save them before removal", repository.Path)
			}
			published, err := branchIsPublished(repository.CachePath, repository.Branch)
			if err != nil {
				return err
			}
			if !published {
				return fmt.Errorf("branch %q has commits that are not on another declared ref; publish or save them before removal", repository.Branch)
			}
			return nil
		}); err != nil {
			return p, nil, err
		}
	}
	return p, sessions, nil
}

func (s *Service) validateRemovalState(p domain.Project) error {
	if len(p.ID) < 8 {
		return fmt.Errorf("Project %q has an invalid ID", p.Name)
	}
	expectedRoot := filepath.Join(s.options.DataDir, "projects", p.Name+"-"+p.ID[:8])
	if p.EnvironmentID != "" {
		expectedRoot = filepath.Join(s.options.DataDir, "projects", p.EnvironmentID)
		environment, err := s.environments.Find(p.EnvironmentID)
		if errors.Is(err, os.ErrNotExist) && p.Status == domain.ProjectRemoving {
			// A retry can continue after the Prepared Environment record was deleted.
		} else if err != nil {
			return err
		} else if environment.Status != domain.EnvironmentClaimed || environment.ClaimReservation == nil || environment.ClaimReservation.Project.ID != p.ID {
			return fmt.Errorf("Project %q does not own its Prepared Environment", p.Name)
		}
	}
	if filepath.Clean(p.Root) != filepath.Clean(expectedRoot) {
		return fmt.Errorf("Project %q has an invalid root path", p.Name)
	}
	expectedEntries := map[string]bool{".twt2-owned.json": true}
	for _, repository := range p.Repositories {
		spec, _, err := repositoryFor(p, repository.Name)
		if err != nil {
			return err
		}
		if repository.Path != filepath.Join(p.Root, repository.Name) {
			return fmt.Errorf("repository %q has a checkout path outside its Project root", repository.Name)
		}
		if repository.CachePath != s.cachePath(repository.Name, spec.Clone.URL) {
			return fmt.Errorf("repository %q has an invalid cache path", repository.Name)
		}
		if repository.Branch != "twt2/"+p.Name+"-"+p.ID[:8] {
			return fmt.Errorf("repository %q has an invalid Project branch", repository.Name)
		}
		if _, err := os.Stat(repository.CachePath); errors.Is(err, os.ErrNotExist) {
			if _, checkoutErr := os.Stat(repository.Path); !errors.Is(checkoutErr, os.ErrNotExist) {
				return fmt.Errorf("repository %q has a checkout but no repository cache", repository.Name)
			}
		} else if err != nil {
			return fmt.Errorf("inspect repository cache: %w", err)
		} else if err := validateCacheMarker(repository.CachePath, spec.Clone.URL); err != nil {
			return err
		}
		expectedEntries[repository.Name] = true
	}
	entries, err := os.ReadDir(p.Root)
	if errors.Is(err, os.ErrNotExist) && p.Status == domain.ProjectRemoving {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect Project root: %w", err)
	}
	markerPresent := false
	for _, entry := range entries {
		if !expectedEntries[entry.Name()] {
			return fmt.Errorf("Project root %q contains unexpected item %q; move it before removal", p.Root, entry.Name())
		}
		if entry.Name() == ".twt2-owned.json" {
			markerPresent = true
		}
	}
	if markerPresent {
		return validateProjectMarker(p.Root, p.ID)
	}
	if p.Status != domain.ProjectRemoving || len(entries) != 0 {
		return fmt.Errorf("Project root %q has no twt2 ownership marker", p.Root)
	}
	return nil
}
