package project

import (
	"fmt"
	"strings"

	"github.com/jpugliesi/tmux-worktree/internal/domain"
)

func (s *Service) ensureTmux(p *domain.Project) error {
	if len(p.Repositories) == 0 {
		return fmt.Errorf("Project Template has no repositories")
	}
	sessionID, projectID, exists, err := s.findSession(p.ID, p.TmuxSession)
	if err != nil {
		return err
	}
	if exists && projectID != p.ID {
		fallback := p.Name + "-" + p.ID[:8]
		sessionID, projectID, exists, err = s.findSession(p.ID, fallback)
		if err != nil {
			return err
		}
		if exists && projectID != p.ID {
			return fmt.Errorf("tmux sessions %q and %q already exist and belong to other Projects", p.TmuxSession, fallback)
		}
		p.TmuxSession = fallback
		if err := s.store.Save(*p); err != nil {
			return fmt.Errorf("save fallback tmux session name: %w", err)
		}
	}
	if !exists {
		first := p.Repositories[0]
		created, createErr := output("", "tmux", s.tmuxArgs("new-session", "-d", "-P", "-F", "#{session_id}\t#{window_id}", "-s", p.TmuxSession, "-n", first.WindowName, "-c", first.Path)...)
		if createErr != nil {
			return fmt.Errorf("create tmux session: %w", createErr)
		}
		parts := strings.SplitN(created, "\t", 2)
		if len(parts) != 2 {
			return fmt.Errorf("create tmux session: tmux returned invalid identifiers")
		}
		sessionID = parts[0]
		windowID := parts[1]
		if err := run("", "tmux", s.tmuxArgs("set-option", "-t", sessionID, "@twt2_project_id", p.ID)...); err != nil {
			return fmt.Errorf("mark tmux session: %w", err)
		}
		if err := s.markWindow(windowID, first); err != nil {
			return err
		}
	}
	for _, repository := range p.Repositories {
		hasWindow, err := s.hasManagedWindow(sessionID, repository.Name)
		if err != nil {
			return err
		}
		if hasWindow {
			continue
		}
		windowID, err := output("", "tmux", s.tmuxArgs("new-window", "-d", "-P", "-F", "#{window_id}", "-t", sessionID, "-n", repository.WindowName, "-c", repository.Path)...)
		if err != nil {
			return fmt.Errorf("create tmux window for repository %q: %w", repository.Name, err)
		}
		if err := s.markWindow(windowID, repository); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) findSession(projectID, name string) (sessionID, ownerID string, exists bool, err error) {
	rows, err := output("", "tmux", s.tmuxArgs("list-sessions", "-F", "#{session_id}\t#{session_name}\t#{@twt2_project_id}")...)
	if err != nil {
		if strings.Contains(err.Error(), "no server running") || strings.Contains(err.Error(), "no sessions") || strings.Contains(err.Error(), "error connecting to") {
			return "", "", false, nil
		}
		return "", "", false, err
	}
	var collision []string
	for _, row := range strings.Split(rows, "\n") {
		parts := strings.SplitN(row, "\t", 3)
		if len(parts) == 2 {
			parts = append(parts, "")
		}
		if len(parts) != 3 {
			continue
		}
		if parts[2] == projectID {
			return parts[0], parts[2], true, nil
		}
		if parts[1] == name {
			collision = parts
		}
	}
	if len(collision) == 3 {
		return collision[0], collision[2], true, nil
	}
	return "", "", false, nil
}

func (s *Service) hasManagedWindow(sessionID, repositoryName string) (bool, error) {
	rows, err := output("", "tmux", s.tmuxArgs("list-windows", "-t", sessionID, "-F", "#{@twt2_repository_name}")...)
	if err != nil {
		return false, fmt.Errorf("list tmux windows: %w", err)
	}
	for _, value := range strings.Split(rows, "\n") {
		if value == repositoryName {
			return true, nil
		}
	}
	return false, nil
}

func (s *Service) markWindow(windowID string, repository domain.ProjectRepository) error {
	if err := run("", "tmux", s.tmuxArgs("set-option", "-w", "-t", windowID, "@twt2_repository_name", repository.Name)...); err != nil {
		return fmt.Errorf("mark tmux window for repository %q: %w", repository.Name, err)
	}
	return nil
}

func (s *Service) tmuxArgs(args ...string) []string {
	if s.options.TmuxSocket == "" {
		return args
	}
	return append([]string{"-L", s.options.TmuxSocket, "-f", "/dev/null"}, args...)
}
