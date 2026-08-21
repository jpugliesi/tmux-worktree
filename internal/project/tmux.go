package project

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/jpugliesi/tmux-worktree/internal/domain"
)

// sessionName returns the tmux session name that twt uses for a new Project.
// The Project Template name comes first, thus the native tmux session picker
// groups the sessions of one codebase together. An adopted Project has no
// template, so its session name is the Project name alone. The name is
// presentation only: twt finds its sessions through the session ID and the
// @twt_project_id option.
func sessionName(templateName, projectName string) string {
	if templateName == "" {
		return projectName
	}
	return templateName + "-" + projectName
}

func (s *Service) ensureTmux(p *domain.Project) error {
	if len(p.Repositories) == 0 {
		// An adopted Project can have no repositories. Its session is fine
		// while it runs, but twt cannot make it again.
		_, ownerID, exists, err := s.findSession(p.ID, sessionName(p.TemplateName, p.Name))
		if err != nil {
			return err
		}
		if exists && ownerID == p.ID {
			return nil
		}
		return fmt.Errorf("Project %q has no repositories and no owned tmux session; twt cannot make the session again", p.Name)
	}
	name := sessionName(p.TemplateName, p.Name)
	sessionID, projectID, exists, err := s.findSession(p.ID, name)
	if err != nil {
		return err
	}
	if exists && projectID != p.ID {
		fallback := name + "-" + p.ID[:8]
		sessionID, projectID, exists, err = s.findSession(p.ID, fallback)
		if err != nil {
			return err
		}
		if exists && projectID != p.ID {
			return fmt.Errorf("tmux sessions %q and %q already exist and belong to other Projects", name, fallback)
		}
		name = fallback
	}
	if !exists && p.TmuxSession != name {
		p.TmuxSession = name
		if err := s.store.Save(*p); err != nil {
			return fmt.Errorf("save tmux session name: %w", err)
		}
	}
	createdSession := false
	if !exists {
		first := p.Repositories[0]
		created, createErr := output("", "tmux", s.tmuxArgs("new-session", "-d", "-P", "-F", "#{session_id}\t#{window_id}", "-s", name, "-n", first.WindowName, "-c", first.Path)...)
		if createErr != nil {
			return fmt.Errorf("create tmux session: %w", createErr)
		}
		parts := strings.SplitN(created, "\t", 2)
		if len(parts) != 2 {
			return fmt.Errorf("create tmux session: tmux returned invalid identifiers")
		}
		sessionID = parts[0]
		windowID := parts[1]
		if err := run("", "tmux", s.tmuxArgs("set-option", "-t", sessionID, "@twt_project_id", p.ID)...); err != nil {
			return fmt.Errorf("mark tmux session: %w", err)
		}
		if err := s.markWindow(windowID, first); err != nil {
			return err
		}
		createdSession = true
	}
	windowIDs := make(map[string]string, len(p.Repositories))
	for _, repository := range p.Repositories {
		windowID, hasWindow, err := s.managedWindowID(sessionID, repository.Name)
		if err != nil {
			return err
		}
		if !hasWindow {
			windowID, err = output("", "tmux", s.tmuxArgs("new-window", "-d", "-P", "-F", "#{window_id}", "-t", sessionID, "-n", repository.WindowName, "-c", repository.Path)...)
			if err != nil {
				return fmt.Errorf("create tmux window for repository %q: %w", repository.Name, err)
			}
			if err := s.markWindow(windowID, repository); err != nil {
				return err
			}
		}
		windowIDs[repository.Name] = windowID
	}
	if !createdSession {
		return nil
	}
	return s.runSessionCommand(*p, sessionID, windowIDs)
}

// runSessionCommand runs the declared session command of the Project Template.
// The caller runs it only after it creates the tmux session, so a setup retry
// against a live session keeps the panes that the user arranged. A failure
// fails the tmux step, and a setup retry runs the step again.
func (s *Service) runSessionCommand(p domain.Project, sessionID string, windowIDs map[string]string) error {
	session := p.TemplateSnapshot.Session
	if session == nil || len(session.Command) == 0 {
		return nil
	}
	directory := p.Root
	if session.CWD != "" {
		directory = filepath.Join(p.Root, filepath.FromSlash(session.CWD))
	}
	command := exec.Command(session.Command[0], session.Command[1:]...)
	command.Dir = directory
	environment := append(os.Environ(), projectEnvironment(p)...)
	environment = append(environment,
		"TWT_TMUX_SESSION="+sessionID,
		"TWT_TMUX_SOCKET="+s.options.TmuxSocket,
	)
	for _, repository := range p.Repositories {
		windowID, found := windowIDs[repository.Name]
		if !found {
			continue
		}
		environment = append(environment, "TWT_TMUX_WINDOW_"+repositoryEnvironmentKey(repository.Name)+"="+windowID)
	}
	command.Env = environment
	commandOutput, err := command.CombinedOutput()
	if err != nil {
		return fmt.Errorf("run Project Template session command in %q: %w: %s", directory, err, strings.TrimSpace(string(commandOutput)))
	}
	return nil
}

// repositoryEnvironmentKey makes the environment variable suffix of one
// repository name. It matches the suffix of TWT_REPOSITORY_<NAME>.
func repositoryEnvironmentKey(name string) string {
	return strings.ToUpper(strings.NewReplacer("-", "_", ".", "_").Replace(name))
}

func (s *Service) findSession(projectID, name string) (sessionID, ownerID string, exists bool, err error) {
	rows, err := output("", "tmux", s.tmuxArgs("list-sessions", "-F", "#{session_id}\t#{session_name}\t#{@twt_project_id}")...)
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

// managedWindowID returns the ID of the window that twt marked for the
// repository in this session.
func (s *Service) managedWindowID(sessionID, repositoryName string) (string, bool, error) {
	rows, err := output("", "tmux", s.tmuxArgs("list-windows", "-t", sessionID, "-F", "#{@twt_repository_name}\t#{window_id}")...)
	if err != nil {
		return "", false, fmt.Errorf("list tmux windows: %w", err)
	}
	for _, row := range strings.Split(rows, "\n") {
		parts := strings.SplitN(row, "\t", 2)
		if len(parts) == 2 && parts[0] == repositoryName {
			return parts[1], true, nil
		}
	}
	return "", false, nil
}

func (s *Service) markWindow(windowID string, repository domain.ProjectRepository) error {
	if err := run("", "tmux", s.tmuxArgs("set-option", "-w", "-t", windowID, "@twt_repository_name", repository.Name)...); err != nil {
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
