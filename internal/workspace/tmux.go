package workspace

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/jpugliesi/tmux-worktree/internal/domain"
)

const (
	workspaceTmuxOption     = "@twt_workspace_id"
	legacyProjectTmuxOption = "@twt_project_id"
	workspaceTmuxFormat     = "#{@twt_workspace_id}"
	legacyProjectTmuxFormat = "#{@twt_project_id}"
)

type tmuxSessionRow struct {
	id      string
	name    string
	ownerID string
}

// sessionName returns the tmux session name that twt uses for a new Workspace.
// The Workspace Template name comes first, thus the native tmux session picker
// groups the sessions of one codebase together. An adopted Workspace has no
// template, so its session name is the Workspace name alone. The name is
// presentation only: twt finds its sessions through the session ID and the
// @twt_workspace_id option.
func sessionName(templateName, workspaceName string) string {
	if templateName == "" {
		return workspaceName
	}
	return templateName + "-" + workspaceName
}

func (s *Service) ensureTmux(p *domain.Workspace) error {
	if len(p.Repositories) == 0 {
		// An adopted Workspace can have no repositories. Its session is fine
		// while it runs, but twt cannot make it again.
		_, ownerID, exists, err := s.findSession(p.ID, sessionName(p.TemplateName, p.Name))
		if err != nil {
			return err
		}
		if exists && ownerID == p.ID {
			return nil
		}
		return fmt.Errorf("Workspace %q has no repositories and no owned tmux session; twt cannot make the session again", p.Name)
	}
	name := sessionName(p.TemplateName, p.Name)
	sessionID, ownerID, exists, err := s.findSession(p.ID, name)
	if err != nil {
		return err
	}
	if exists && ownerID != "" && ownerID != p.ID {
		fallback := name + "-" + p.ID[:8]
		sessionID, ownerID, exists, err = s.findSession(p.ID, fallback)
		if err != nil {
			return err
		}
		if exists && ownerID != "" && ownerID != p.ID {
			return fmt.Errorf("tmux sessions %q and %q already exist and belong to other Workspaces", name, fallback)
		}
		name = fallback
	}
	if exists && ownerID == "" {
		if err := s.claimSession(sessionID, p.ID); err != nil {
			return err
		}
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
		if err := s.claimSession(sessionID, p.ID); err != nil {
			return err
		}
		if err := s.markWindow(windowID, first); err != nil {
			return err
		}
		createdSession = true
	}
	windowIDs := make(map[string]string, len(p.Repositories))
	for _, repository := range p.Repositories {
		windowID, hasWindow, err := s.ensureManagedWindow(sessionID, repository)
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

// runSessionCommand runs the declared session command of the Workspace Template.
// The caller runs it only after it creates the tmux session, so a setup retry
// against a live session keeps the panes that the user arranged. A failure
// fails the tmux step, and a setup retry runs the step again.
func (s *Service) runSessionCommand(p domain.Workspace, sessionID string, windowIDs map[string]string) error {
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
	environment := append(os.Environ(), workspaceEnvironment(p)...)
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
		return fmt.Errorf("run Workspace Template session command in %q: %w: %s", directory, err, strings.TrimSpace(string(commandOutput)))
	}
	return nil
}

// repositoryEnvironmentKey makes the environment variable suffix of one
// repository name. It matches the suffix of TWT_REPOSITORY_<NAME>.
func repositoryEnvironmentKey(name string) string {
	return strings.ToUpper(strings.NewReplacer("-", "_", ".", "_").Replace(name))
}

func (s *Service) findSession(workspaceID, name string) (sessionID, ownerID string, exists bool, err error) {
	rows, err := s.workspaceSessionRows(true)
	if err != nil {
		if strings.Contains(err.Error(), "no server running") || strings.Contains(err.Error(), "no sessions") || strings.Contains(err.Error(), "error connecting to") {
			return "", "", false, nil
		}
		return "", "", false, err
	}
	var collision *tmuxSessionRow
	for index := range rows {
		row := &rows[index]
		if row.ownerID == workspaceID {
			return row.id, row.ownerID, true, nil
		}
		if row.name == name {
			collision = row
		}
	}
	if collision != nil {
		return collision.id, collision.ownerID, true, nil
	}
	return "", "", false, nil
}

// tmuxWorkspaceID reads the new owner option first, then the old Project
// option. The fallback keeps sessions from version 1 usable after an upgrade.
func (s *Service) tmuxWorkspaceID(sessionID string) (string, error) {
	workspaceID, currentErr := output("", "tmux", s.tmuxArgs("show-options", "-t", sessionID, "-v", workspaceTmuxOption)...)
	if currentErr == nil && workspaceID != "" {
		return workspaceID, nil
	}
	workspaceID, legacyErr := output("", "tmux", s.tmuxArgs("show-options", "-t", sessionID, "-v", legacyProjectTmuxOption)...)
	if legacyErr == nil {
		return workspaceID, nil
	}
	return "", nil
}

// workspaceSessionRows joins the current and version 1 owner options. A
// current option wins when a session has both options.
func (s *Service) workspaceSessionRows(includeName bool) ([]tmuxSessionRow, error) {
	prefix := "#{session_id}\t"
	if includeName {
		prefix = "#{session_id}\t#{session_name}\t"
	}
	current, currentErr := output("", "tmux", s.tmuxArgs("list-sessions", "-F", prefix+workspaceTmuxFormat)...)
	legacy, legacyErr := output("", "tmux", s.tmuxArgs("list-sessions", "-F", prefix+legacyProjectTmuxFormat)...)
	if currentErr != nil && legacyErr != nil {
		return nil, currentErr
	}
	rows := parseWorkspaceSessionRows(current, includeName)
	byID := make(map[string]int, len(rows))
	for index := range rows {
		byID[rows[index].id] = index
	}
	for _, legacyRow := range parseWorkspaceSessionRows(legacy, includeName) {
		if index, ok := byID[legacyRow.id]; ok {
			if rows[index].ownerID == "" {
				rows[index].ownerID = legacyRow.ownerID
			}
			continue
		}
		byID[legacyRow.id] = len(rows)
		rows = append(rows, legacyRow)
	}
	return rows, nil
}

func parseWorkspaceSessionRows(value string, includeName bool) []tmuxSessionRow {
	fieldCount := 2
	if includeName {
		fieldCount = 3
	}
	rows := []tmuxSessionRow{}
	for _, valueRow := range strings.Split(value, "\n") {
		parts := strings.SplitN(valueRow, "\t", fieldCount)
		// output trims surrounding whitespace. Thus, the last empty owner
		// field can be absent for an unowned final session.
		if len(parts) == fieldCount-1 {
			parts = append(parts, "")
		}
		if len(parts) != fieldCount || parts[0] == "" {
			continue
		}
		row := tmuxSessionRow{id: parts[0]}
		if includeName {
			row.name = parts[1]
			row.ownerID = parts[2]
		} else {
			row.ownerID = parts[1]
		}
		rows = append(rows, row)
	}
	return rows
}

// claimSession writes the Workspace ID onto one tmux session.
func (s *Service) claimSession(sessionID, workspaceID string) error {
	if err := run("", "tmux", s.tmuxArgs("set-option", "-t", sessionID, workspaceTmuxOption, workspaceID)...); err != nil {
		return fmt.Errorf("mark tmux session: %w", err)
	}
	return nil
}

// ensureManagedWindow finds the window that twt marked for the repository, or
// an unmarked window with the same window name. A resurrected session often
// keeps the name and loses the owner mark.
func (s *Service) ensureManagedWindow(sessionID string, repository domain.WorkspaceRepository) (string, bool, error) {
	rows, err := output("", "tmux", s.tmuxArgs("list-windows", "-t", sessionID, "-F", "#{window_id}\t#{window_name}\t#{@twt_repository_name}")...)
	if err != nil {
		return "", false, fmt.Errorf("list tmux windows: %w", err)
	}
	var unmarked string
	for _, row := range strings.Split(rows, "\n") {
		parts := strings.SplitN(row, "\t", 3)
		if len(parts) == 2 {
			parts = append(parts, "")
		}
		if len(parts) != 3 || parts[0] == "" {
			continue
		}
		if parts[2] == repository.Name {
			return parts[0], true, nil
		}
		if unmarked == "" && parts[2] == "" && parts[1] == repository.WindowName {
			unmarked = parts[0]
		}
	}
	if unmarked == "" {
		return "", false, nil
	}
	if err := s.markWindow(unmarked, repository); err != nil {
		return "", false, err
	}
	return unmarked, true, nil
}

func (s *Service) markWindow(windowID string, repository domain.WorkspaceRepository) error {
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
