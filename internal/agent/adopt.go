package agent

import (
	"fmt"
	"strings"

	"github.com/jpugliesi/tmux-worktree/internal/clierr"
	"github.com/jpugliesi/tmux-worktree/internal/domain"
	"github.com/jpugliesi/tmux-worktree/internal/store"
	tmuxclient "github.com/jpugliesi/tmux-worktree/internal/tmux"
)

// AdoptLivePane registers a strongly identified provider process from the
// read-only Agent Catalog. The boolean is false when the reference does not
// name a live-pane candidate. A dry run validates and returns an unsaved
// record without changing the pane marker.
func (s *Service) AdoptLivePane(workspace domain.Workspace, reference string, dryRun bool) (domain.AgentSession, bool, error) {
	if existing, found, err := s.findRuntimeReference(workspace.ID, reference); found || err != nil {
		return existing, found, err
	}
	entry, found, err := s.findLivePaneCandidate(workspace, reference)
	if err != nil || !found {
		return domain.AgentSession{}, found, err
	}
	if dryRun {
		existing, listErr := s.store.List(workspace.ID)
		if listErr != nil {
			return domain.AgentSession{}, true, listErr
		}
		agent, buildErr := s.buildLivePaneSession(workspace, entry, existing)
		return agent, true, buildErr
	}

	lock, err := store.AcquireMutationLock(s.stateDir)
	if err != nil {
		return domain.AgentSession{}, true, err
	}
	defer lock.Release()
	workspace, err = store.NewWorkspaceStore(s.stateDir).Find(workspace.ID)
	if err != nil {
		return domain.AgentSession{}, true, err
	}
	if existing, found, findErr := s.findRuntimeReference(workspace.ID, reference); found || findErr != nil {
		return existing, found, findErr
	}
	entry, found, err = s.findLivePaneCandidate(workspace, reference)
	if err != nil || !found {
		if err == nil {
			err = clierr.New(clierr.PreconditionFailed, "the discovered Agent Session is no longer live")
		}
		return domain.AgentSession{}, true, err
	}
	existing, err := s.store.List(workspace.ID)
	if err != nil {
		return domain.AgentSession{}, true, err
	}
	agent, err := s.buildLivePaneSession(workspace, entry, existing)
	if err != nil {
		return domain.AgentSession{}, true, err
	}
	if err := s.tmux.ClaimAgentPane(agent.TmuxPane, workspace.ID, agent.ID); err != nil {
		return domain.AgentSession{}, true, err
	}
	binding, _ := processBinding(agent)
	if !s.tmux.ProcessPaneBelongs(workspace, agent.TmuxPane, agent.ID, binding, false) {
		if releaseErr := s.tmux.ReleaseAgentPane(agent.TmuxPane, workspace.ID, agent.ID); releaseErr != nil {
			return domain.AgentSession{}, true, fmt.Errorf("the discovered Agent Session changed during adoption; release pane marker: %w", releaseErr)
		}
		return domain.AgentSession{}, true, clierr.New(clierr.PreconditionFailed, "the discovered Agent Session changed during adoption")
	}
	if err := s.store.Save(agent); err != nil {
		releaseErr := s.tmux.ReleaseAgentPane(agent.TmuxPane, workspace.ID, agent.ID)
		if releaseErr != nil {
			return domain.AgentSession{}, true, fmt.Errorf("save Agent Session: %w; release pane marker: %v", err, releaseErr)
		}
		return domain.AgentSession{}, true, err
	}
	return agent, true, nil
}

func (s *Service) findLivePaneCandidate(workspace domain.Workspace, reference string) (CatalogEntry, bool, error) {
	panes, err := s.tmux.ObserveWorkspace(workspace)
	if err != nil {
		return CatalogEntry{}, false, err
	}
	entries := s.livePaneEntries(workspace, panes)
	entry, err := findCatalogEntry(entries, reference)
	if clierr.CodeOf(err) == clierr.NotFound {
		return CatalogEntry{}, false, nil
	}
	return entry, err == nil, err
}

func (s *Service) buildLivePaneSession(workspace domain.Workspace, entry CatalogEntry, existing []domain.AgentSession) (domain.AgentSession, error) {
	if workspace.Status != domain.WorkspaceActive {
		return domain.AgentSession{}, workspaceNotActiveError(workspace)
	}
	if entry.pane == nil || entry.process == nil {
		return domain.AgentSession{}, clierr.New(clierr.PreconditionFailed, "the discovered Agent Session has no live provider process")
	}
	agent, err := newSession(workspace, entry.Provider, "", entry.pane.ID, "", nil, existing, s.now())
	if err != nil {
		return domain.AgentSession{}, err
	}
	agent.RuntimeReference = entry.Reference
	agent.PaneCommand = entry.pane.CurrentCommand
	agent.PaneRootProcessID = entry.pane.RootProcessID
	agent.PaneRootStarted = entry.pane.RootStarted
	agent.ProcessID = entry.process.ID
	agent.ProcessStarted = entry.process.Started
	agent.ProcessCommand = entry.process.Command
	agent.ProcessEvidence = tmuxclient.ProcessEvidence(*entry.process)
	return agent, nil
}

func (s *Service) findRuntimeReference(workspaceID, reference string) (domain.AgentSession, bool, error) {
	agents, err := s.store.List(workspaceID)
	if err != nil {
		return domain.AgentSession{}, false, err
	}
	matches := []domain.AgentSession{}
	for _, agent := range agents {
		if agent.RuntimeReference == "" {
			continue
		}
		if agent.RuntimeReference == reference {
			return agent, true, nil
		}
		if strings.HasPrefix(agent.RuntimeReference, reference) {
			matches = append(matches, agent)
		}
	}
	if len(matches) == 1 {
		return matches[0], true, nil
	}
	if len(matches) > 1 {
		return domain.AgentSession{}, false, clierr.New(clierr.InvalidUsage, "Agent Session reference %q is ambiguous", reference)
	}
	return domain.AgentSession{}, false, nil
}
