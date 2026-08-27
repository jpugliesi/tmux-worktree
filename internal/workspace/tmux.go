package workspace

import (
	"errors"
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

// unownedSessionPolicy controls whether an existing Workspace can adopt a
// matching tmux session that has no Workspace owner marker.
type unownedSessionPolicy uint8

const (
	preserveUnownedSession unownedSessionPolicy = iota
	claimUnownedSession
)

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

func (s *Service) ensureTmux(p *domain.Workspace, unownedPolicy unownedSessionPolicy) error {
	name := p.TmuxSession
	if name == "" {
		name = sessionName(p.TemplateName, p.Name)
	}
	if len(p.Repositories) == 0 {
		// An adopted Workspace can have no repositories. Its session is fine
		// while it runs, but twt cannot make it again.
		_, ownerID, exists, err := s.findSession(p.ID, name)
		if err != nil {
			return err
		}
		if exists && ownerID == p.ID {
			return nil
		}
		return fmt.Errorf("Workspace %q has no repositories and no owned tmux session; twt cannot make the session again", p.Name)
	}
	sessionID, ownerID, exists, err := s.findSession(p.ID, name)
	if err != nil {
		return err
	}
	nameUnavailable := func(exists bool, ownerID string) bool {
		return exists && ownerID != p.ID && (ownerID != "" || unownedPolicy == preserveUnownedSession)
	}
	if nameUnavailable(exists, ownerID) {
		fallback := name + "-" + p.ID[:8]
		sessionID, ownerID, exists, err = s.findSession(p.ID, fallback)
		if err != nil {
			return err
		}
		if nameUnavailable(exists, ownerID) {
			return fmt.Errorf("tmux session names %q and %q are already in use", name, fallback)
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
	if !exists {
		var windowIDs map[string]string
		sessionID, windowIDs, err = s.createWorkspaceSession(name, *p)
		if err != nil {
			return err
		}
		return s.runSessionCommand(*p, sessionID, windowIDs)
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
	return nil
}

// createWorkspaceSession creates the base session and all managed windows in
// one tmux process. A second process writes all owner options.
func (s *Service) createWorkspaceSession(name string, workspace domain.Workspace) (string, map[string]string, error) {
	first := workspace.Repositories[0]
	args := s.tmuxArgs("new-session", "-d", "-P", "-F", "#{session_id}\t#{window_id}", "-s", name, "-n", first.WindowName, "-c", first.Path)
	for _, repository := range workspace.Repositories[1:] {
		args = append(args, ";", "new-window", "-d", "-P", "-F", "#{window_id}", "-t", "="+name, "-n", repository.WindowName, "-c", repository.Path)
	}
	created, err := output("", "tmux", args...)
	if err != nil {
		return "", nil, fmt.Errorf("create tmux session: %w", err)
	}
	lines := strings.Split(created, "\n")
	parts := strings.SplitN(lines[0], "\t", 2)
	if len(parts) != 2 || len(lines) != len(workspace.Repositories) {
		return "", nil, fmt.Errorf("create tmux session: tmux returned invalid identifiers")
	}
	sessionID := parts[0]
	windowIDs := map[string]string{first.Name: parts[1]}
	for index, repository := range workspace.Repositories[1:] {
		windowIDs[repository.Name] = lines[index+1]
	}
	markArgs := s.tmuxArgs("set-option", "-t", sessionID, workspaceTmuxOption, workspace.ID)
	for _, repository := range workspace.Repositories {
		markArgs = append(markArgs, ";", "set-option", "-w", "-t", windowIDs[repository.Name], "@twt_repository_name", repository.Name)
	}
	if err := run("", "tmux", markArgs...); err != nil {
		return "", nil, fmt.Errorf("mark Workspace tmux session: %w", err)
	}
	return sessionID, windowIDs, nil
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
		if tmuxUnavailable(err) {
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
	value, err := output("", "tmux", s.tmuxArgs("list-sessions", "-F", prefix+workspaceTmuxFormat+"\t"+legacyProjectTmuxFormat)...)
	if err != nil {
		return nil, err
	}
	return parseCombinedWorkspaceSessionRows(value, includeName), nil
}

func parseCombinedWorkspaceSessionRows(value string, includeName bool) []tmuxSessionRow {
	fieldCount := 3
	if includeName {
		fieldCount = 4
	}
	rows := []tmuxSessionRow{}
	for _, valueRow := range strings.Split(value, "\n") {
		parts := strings.Split(valueRow, "\t")
		for len(parts) < fieldCount {
			parts = append(parts, "")
		}
		if len(parts) != fieldCount || parts[0] == "" {
			continue
		}
		row := tmuxSessionRow{id: parts[0]}
		ownerIndex := 1
		if includeName {
			row.name = parts[1]
			ownerIndex = 2
		}
		row.ownerID = parts[ownerIndex]
		if row.ownerID == "" {
			row.ownerID = parts[ownerIndex+1]
		}
		rows = append(rows, row)
	}
	return rows
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

func tmuxUnavailable(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "no server running") ||
		strings.Contains(msg, "no sessions") ||
		strings.Contains(msg, "error connecting to") ||
		strings.Contains(msg, "executable file not found")
}

// stopSessionPanesExcept stops every process in a Workspace session except
// the process that runs release cleanup.
func (s *Service) stopSessionPanesExcept(sessionID, keepPane string) error {
	value, err := output("", "tmux", s.tmuxArgs("list-panes", "-s", "-t", sessionID, "-F", "#{pane_id}")...)
	if err != nil {
		return fmt.Errorf("list Workspace tmux panes: %w", err)
	}
	keepFound := false
	var targets []string
	for _, paneID := range strings.Fields(value) {
		if paneID == keepPane {
			keepFound = true
			continue
		}
		targets = append(targets, paneID)
	}
	if !keepFound {
		return fmt.Errorf("current tmux pane %q is not in session %q", keepPane, sessionID)
	}
	for _, paneID := range targets {
		if err := run("", "tmux", s.tmuxArgs("kill-pane", "-t", paneID)...); err != nil {
			return fmt.Errorf("stop Workspace tmux pane %q: %w", paneID, err)
		}
	}
	return nil
}

// stopPreparedSession lets tmux select another session when possible. The
// caller restores the option only when tmux refuses the final kill.
func (s *Service) stopPreparedSession(sessionID string) error {
	previous, err := output("", "tmux", s.tmuxArgs("show-options", "-v", "-t", sessionID, "detach-on-destroy")...)
	if err != nil {
		return fmt.Errorf("read tmux detach behavior: %w", err)
	}
	if err := run("", "tmux", s.tmuxArgs("set-option", "-t", sessionID, "detach-on-destroy", "off")...); err != nil {
		return fmt.Errorf("set tmux detach behavior: %w", err)
	}
	if err := run("", "tmux", s.tmuxArgs("kill-session", "-t", sessionID)...); err != nil {
		restoreErr := run("", "tmux", s.tmuxArgs("set-option", "-t", sessionID, "detach-on-destroy", previous)...)
		if restoreErr != nil {
			return errors.Join(fmt.Errorf("stop Workspace tmux session: %w", err), fmt.Errorf("restore tmux detach behavior: %w", restoreErr))
		}
		return fmt.Errorf("stop Workspace tmux session: %w", err)
	}
	return nil
}
