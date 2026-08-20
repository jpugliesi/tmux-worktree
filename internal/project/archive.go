package project

import (
	"fmt"
	"strings"

	"github.com/jpugliesi/tmux-worktree/internal/agent"
	"github.com/jpugliesi/tmux-worktree/internal/clierr"
	"github.com/jpugliesi/tmux-worktree/internal/domain"
	"github.com/jpugliesi/tmux-worktree/internal/store"
)

// ArchiveResult reports the archived Project and the Agent Sessions that had
// a live pane before the archive stopped the Project tmux session.
type ArchiveResult struct {
	Project       domain.Project
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
		return ArchiveResult{Project: p}, err
	}
	stopped, err := agent.NewService(s.options.StateDir, s.options.TmuxSocket).Live(p.ID)
	if err != nil {
		return ArchiveResult{Project: p}, err
	}
	// Stop the sessions before the status save. A crash between the two
	// steps leaves an active Project without a session, and 'projects
	// open' repairs that state.
	for _, sessionID := range sessions {
		if err := run("", "tmux", s.tmuxArgs("kill-session", "-t", sessionID)...); err != nil {
			return ArchiveResult{Project: p}, fmt.Errorf("stop Project tmux session: %w", err)
		}
	}
	if p.Status != domain.ProjectArchived {
		now := s.now()
		p.Status = domain.ProjectArchived
		p.ArchivedAt = &now
		p.UpdatedAt = now
		if err := s.store.Save(p); err != nil {
			return ArchiveResult{Project: p}, err
		}
	}
	if err := s.clearAgentPanes(p.ID); err != nil {
		return ArchiveResult{Project: p, StoppedAgents: stopped}, err
	}
	remaining, err := s.ownedSessions(p.ID)
	if err != nil {
		return ArchiveResult{Project: p, StoppedAgents: stopped}, err
	}
	if len(remaining) != 0 {
		return ArchiveResult{Project: p, StoppedAgents: stopped}, fmt.Errorf("Project %q still has an owned tmux session", p.Name)
	}
	return ArchiveResult{Project: p, StoppedAgents: stopped}, nil
}

// clearAgentPanes blanks the recorded pane identity on the Project's Agent
// Session records after their panes stopped.
func (s *Service) clearAgentPanes(projectID string) error {
	agentStore := store.NewAgentStore(s.options.StateDir)
	agents, err := agentStore.List(projectID)
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

func (s *Service) validateArchive(reference, currentPane string) (domain.Project, []string, error) {
	p, err := s.store.Find(reference)
	if err != nil {
		return p, nil, err
	}
	if p.Status != domain.ProjectActive && p.Status != domain.ProjectArchived && p.Status != domain.ProjectSetupFailed {
		return p, nil, clierr.New(clierr.PreconditionFailed, "Project %q has status %q; archive requires status %q, %q, or %q", p.Name, p.Status, domain.ProjectActive, domain.ProjectSetupFailed, domain.ProjectArchived)
	}
	sessions, err := s.ownedSessions(p.ID)
	if err != nil {
		return p, nil, err
	}
	if len(sessions) > 1 {
		return p, nil, fmt.Errorf("Project %q owns more than one tmux session", p.Name)
	}
	if err := s.requireOutsideOwnedSessions(p.Name, "archive", currentPane, sessions); err != nil {
		return p, nil, err
	}
	return p, sessions, nil
}

func (s *Service) requireOutsideOwnedSessions(projectName, action, currentPane string, sessions []string) error {
	if currentPane == "" || len(sessions) == 0 {
		return nil
	}
	currentSession, err := output("", "tmux", s.tmuxArgs("display-message", "-p", "-t", currentPane, "#{session_id}")...)
	if err != nil {
		return fmt.Errorf("inspect the current tmux pane before you %s Project %q: %w", action, projectName, err)
	}
	if currentSession == "" {
		return fmt.Errorf("inspect the current tmux pane before you %s Project %q: tmux returned no session", action, projectName)
	}
	for _, sessionID := range sessions {
		if currentSession == sessionID {
			return clierr.WithHint(
				clierr.New(clierr.PreconditionFailed, "cannot %s Project %q from inside its tmux session; switch to another session first", action, projectName),
				"Switch to a different tmux session first.")
		}
	}
	return nil
}

func (s *Service) ownedSessions(projectID string) ([]string, error) {
	rows, err := output("", "tmux", s.tmuxArgs("list-sessions", "-F", "#{session_id}\t#{@twt_project_id}")...)
	if err != nil {
		if strings.Contains(err.Error(), "no server running") || strings.Contains(err.Error(), "no sessions") || strings.Contains(err.Error(), "error connecting to") {
			return []string{}, nil
		}
		return nil, err
	}
	var sessions []string
	for _, row := range strings.Split(rows, "\n") {
		parts := strings.SplitN(row, "\t", 2)
		if len(parts) == 2 && parts[1] == projectID {
			sessions = append(sessions, parts[0])
		}
	}
	return sessions, nil
}

func (s *Service) OwnedSessionID(projectID string) (string, error) {
	sessions, err := s.ownedSessions(projectID)
	if err != nil {
		return "", err
	}
	if len(sessions) != 1 {
		return "", fmt.Errorf("Project %q owns %d tmux sessions; expected 1", projectID, len(sessions))
	}
	return sessions[0], nil
}
