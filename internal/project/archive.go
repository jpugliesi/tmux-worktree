package project

import (
	"fmt"
	"strings"

	"github.com/jpugliesi/tmux-worktree/internal/domain"
	"github.com/jpugliesi/tmux-worktree/internal/store"
)

func (s *Service) Archive(reference, currentPane string) (domain.Project, error) {
	lock, err := store.AcquireMutationLock(s.options.StateDir)
	if err != nil {
		return domain.Project{}, err
	}
	defer lock.Release()

	p, sessions, err := s.validateArchive(reference, currentPane)
	if err != nil {
		return p, err
	}
	if p.Status != domain.ProjectArchived {
		now := s.now()
		p.Status = domain.ProjectArchived
		p.ArchivedAt = &now
		p.UpdatedAt = now
		if err := s.store.Save(p); err != nil {
			return p, err
		}
	}
	for _, sessionID := range sessions {
		if err := run("", "tmux", s.tmuxArgs("kill-session", "-t", sessionID)...); err != nil {
			return p, fmt.Errorf("stop Project tmux session: %w", err)
		}
	}
	remaining, err := s.ownedSessions(p.ID)
	if err != nil {
		return p, err
	}
	if len(remaining) != 0 {
		return p, fmt.Errorf("Project %q still has an owned tmux session", p.Name)
	}
	return p, nil
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
	if p.Status != domain.ProjectActive && p.Status != domain.ProjectArchived {
		return p, nil, fmt.Errorf("Project %q has status %q; archive requires status %q or %q", p.Name, p.Status, domain.ProjectActive, domain.ProjectArchived)
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
			return fmt.Errorf("cannot %s Project %q from inside its tmux session; switch to another session first", action, projectName)
		}
	}
	return nil
}

func (s *Service) ownedSessions(projectID string) ([]string, error) {
	rows, err := output("", "tmux", s.tmuxArgs("list-sessions", "-F", "#{session_id}\t#{@twt2_project_id}")...)
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
