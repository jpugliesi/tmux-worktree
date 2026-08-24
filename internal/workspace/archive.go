package workspace

import (
	"fmt"
	"strings"

	"github.com/jpugliesi/tmux-worktree/internal/agent"
	"github.com/jpugliesi/tmux-worktree/internal/clierr"
	"github.com/jpugliesi/tmux-worktree/internal/domain"
	"github.com/jpugliesi/tmux-worktree/internal/store"
)

// ArchiveResult reports the archived Workspace and the Agent Sessions that had
// a live pane before the archive stopped the Workspace tmux session.
type ArchiveResult struct {
	Workspace     domain.Workspace
	StoppedAgents []domain.AgentSession
}

func (s *Service) Archive(reference, currentPane string) (ArchiveResult, error) {
	lock, err := store.AcquireMutationLock(s.options.StateDir)
	if err != nil {
		return ArchiveResult{}, err
	}
	defer lock.Release()

	p, sessions, err := s.validateArchive(reference, currentPane)
	if err != nil {
		return ArchiveResult{Workspace: p}, err
	}
	stopped, err := agent.NewService(s.options.StateDir, s.options.TmuxSocket).Live(p.ID)
	if err != nil {
		return ArchiveResult{Workspace: p}, err
	}
	// Stop the sessions before the status save. A crash between the two
	// steps leaves an active Workspace without a session, and 'workspaces
	// open' repairs that state.
	for _, sessionID := range sessions {
		if err := run("", "tmux", s.tmuxArgs("kill-session", "-t", sessionID)...); err != nil {
			return ArchiveResult{Workspace: p}, fmt.Errorf("stop Workspace tmux session: %w", err)
		}
	}
	if p.Status != domain.WorkspaceArchived {
		now := s.now()
		p.Status = domain.WorkspaceArchived
		p.ArchivedAt = &now
		p.UpdatedAt = now
		if err := s.store.Save(p); err != nil {
			return ArchiveResult{Workspace: p}, err
		}
	}
	if err := s.clearAgentPanes(p.ID); err != nil {
		return ArchiveResult{Workspace: p, StoppedAgents: stopped}, err
	}
	remaining, err := s.ownedSessions(p.ID)
	if err != nil {
		return ArchiveResult{Workspace: p, StoppedAgents: stopped}, err
	}
	if len(remaining) != 0 {
		return ArchiveResult{Workspace: p, StoppedAgents: stopped}, fmt.Errorf("Workspace %q still has an owned tmux session", p.Name)
	}
	return ArchiveResult{Workspace: p, StoppedAgents: stopped}, nil
}

// clearAgentPanes blanks the recorded pane identity on the Workspace's Agent
// Session records after their panes stopped.
func (s *Service) clearAgentPanes(workspaceID string) error {
	agentStore := store.NewAgentStore(s.options.StateDir)
	agents, err := agentStore.List(workspaceID)
	if err != nil {
		return err
	}
	for _, agent := range agents {
		if agent.TmuxPane == "" && agent.PaneCommand == "" && agent.PaneStart == "" {
			continue
		}
		agent.TmuxPane = ""
		agent.PaneCommand = ""
		agent.PaneStart = ""
		agent.UpdatedAt = s.now()
		if err := agentStore.Save(agent); err != nil {
			return fmt.Errorf("clear the stopped pane on Agent Session %q: %w", agent.ID, err)
		}
	}
	return nil
}

func (s *Service) ValidateArchive(reference, currentPane string) error {
	_, _, err := s.validateArchive(reference, currentPane)
	return err
}

func (s *Service) validateArchive(reference, currentPane string) (domain.Workspace, []string, error) {
	p, err := s.store.Find(reference)
	if err != nil {
		return p, nil, err
	}
	if p.Status != domain.WorkspaceActive && p.Status != domain.WorkspaceArchived && p.Status != domain.WorkspaceSetupFailed {
		return p, nil, clierr.New(clierr.PreconditionFailed, "Workspace %q has status %q; archive requires status %q, %q, or %q", p.Name, p.Status, domain.WorkspaceActive, domain.WorkspaceSetupFailed, domain.WorkspaceArchived)
	}
	sessions, err := s.ownedSessions(p.ID)
	if err != nil {
		return p, nil, err
	}
	if len(sessions) > 1 {
		return p, nil, fmt.Errorf("Workspace %q owns more than one tmux session", p.Name)
	}
	if err := s.requireOutsideOwnedSessions(p.Name, "archive", currentPane, sessions); err != nil {
		return p, nil, err
	}
	return p, sessions, nil
}

func (s *Service) requireOutsideOwnedSessions(workspaceName, action, currentPane string, sessions []string) error {
	if currentPane == "" || len(sessions) == 0 {
		return nil
	}
	currentSession, err := output("", "tmux", s.tmuxArgs("display-message", "-p", "-t", currentPane, "#{session_id}")...)
	if err != nil {
		return fmt.Errorf("inspect the current tmux pane before you %s Workspace %q: %w", action, workspaceName, err)
	}
	if currentSession == "" {
		return fmt.Errorf("inspect the current tmux pane before you %s Workspace %q: tmux returned no session", action, workspaceName)
	}
	for _, sessionID := range sessions {
		if currentSession == sessionID {
			return clierr.WithHint(
				clierr.New(clierr.PreconditionFailed, "cannot %s Workspace %q from inside its tmux session; switch to another session first", action, workspaceName),
				"Switch to a different tmux session first.")
		}
	}
	return nil
}

func (s *Service) ownedSessions(workspaceID string) ([]string, error) {
	rows, err := s.workspaceSessionRows(false)
	if err != nil {
		if strings.Contains(err.Error(), "no server running") || strings.Contains(err.Error(), "no sessions") || strings.Contains(err.Error(), "error connecting to") {
			return []string{}, nil
		}
		return nil, err
	}
	var sessions []string
	for _, row := range rows {
		if row.ownerID == workspaceID {
			sessions = append(sessions, row.id)
		}
	}
	return sessions, nil
}

func (s *Service) OwnedSessionID(workspaceID string) (string, error) {
	sessions, err := s.ownedSessions(workspaceID)
	if err != nil {
		return "", err
	}
	if len(sessions) != 1 {
		return "", fmt.Errorf("Workspace %q owns %d tmux sessions; expected 1", workspaceID, len(sessions))
	}
	return sessions[0], nil
}
