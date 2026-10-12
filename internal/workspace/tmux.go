package workspace

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	"github.com/jpugliesi/tmux-worktree/internal/clierr"
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

// stopPreparedSession lets tmux select another session when possible. One
// tmux call sets the option and stops the session, because each tmux client
// call costs about 100 ms. When tmux refuses the kill, twt unsets the session
// option, so the session uses the global value again.
func (s *Service) stopPreparedSession(sessionID string) error {
	err := run("", "tmux", s.tmuxArgs("set-option", "-t", sessionID, "detach-on-destroy", "off", ";", "kill-session", "-t", sessionID)...)
	if err == nil {
		return nil
	}
	restoreErr := run("", "tmux", s.tmuxArgs("set-option", "-u", "-t", sessionID, "detach-on-destroy")...)
	if restoreErr != nil {
		return errors.Join(fmt.Errorf("stop Workspace tmux session: %w", err), fmt.Errorf("restore tmux detach behavior: %w", restoreErr))
	}
	return fmt.Errorf("stop Workspace tmux session: %w", err)
}

// AdoptUnownedSession claims one live tmux session for the Workspace when
// the session carries no owner and uses the saved Workspace session name. A
// tmux restore (for example tmux-resurrect) recreates sessions without the
// @twt_workspace_id option, and this repairs the mark from the Workspace
// record. It reports whether sessionID now belongs to the Workspace.
func (s *Service) AdoptUnownedSession(workspaceID, sessionID string) (bool, error) {
	workspace, err := s.store.Find(workspaceID)
	if err != nil {
		return false, err
	}
	name := workspace.TmuxSession
	if name == "" {
		name = sessionName(workspace.TemplateName, workspace.Name)
	}
	foundID, ownerID, exists, err := s.findSession(workspace.ID, name)
	if err != nil {
		return false, err
	}
	if !exists || foundID != sessionID {
		// A restore after a rename also loses the saved session name. The
		// session still belongs to the Workspace when every pane works
		// inside it, so a session stop ends no unrelated work.
		return s.adoptSessionInsideWorkspace(workspace, sessionID)
	}
	if ownerID == workspace.ID {
		return true, nil
	}
	if ownerID != "" {
		return false, nil
	}
	if err := s.claimSession(sessionID, workspace.ID); err != nil {
		return false, err
	}
	return true, nil
}

type paneIdentity struct {
	sessionID string
	paneID    string
}

// inspectPane returns the session ID and the pane ID of one tmux pane. It
// asks tmux once for each pane and then uses the cached answer.
func (s *Service) inspectPane(pane string) (paneIdentity, error) {
	if identity, ok := s.panes[pane]; ok {
		return identity, nil
	}
	value, err := output("", "tmux", s.tmuxArgs("display-message", "-p", "-t", pane, "#{session_id}\t#{pane_id}")...)
	if err != nil {
		return paneIdentity{}, err
	}
	parts := strings.SplitN(value, "\t", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return paneIdentity{}, fmt.Errorf("tmux returned invalid identifiers for pane %q", pane)
	}
	identity := paneIdentity{sessionID: parts[0], paneID: parts[1]}
	if s.panes == nil {
		s.panes = map[string]paneIdentity{}
	}
	s.panes[pane] = identity
	return identity, nil
}

// PaneSessionID returns the ID of the tmux session that contains the pane.
func (s *Service) PaneSessionID(pane string) (string, error) {
	identity, err := s.inspectPane(pane)
	return identity.sessionID, err
}

// adoptSessionInsideWorkspace claims an unowned session when the current
// directory of every pane in it is inside the Workspace.
func (s *Service) adoptSessionInsideWorkspace(workspace domain.Workspace, sessionID string) (bool, error) {
	ownerID, err := s.tmuxWorkspaceID(sessionID)
	if err != nil || ownerID != "" {
		return ownerID == workspace.ID, err
	}
	panes, err := s.livePanes()
	if err != nil {
		return false, err
	}
	if !sessionWorksInWorkspace(panes, sessionID, workspace) {
		return false, nil
	}
	if err := s.claimSession(sessionID, workspace.ID); err != nil {
		return false, err
	}
	return true, nil
}

// LivePane is one tmux pane and the directory that it works in.
type LivePane struct {
	SessionID   string `json:"sessionId"`
	SessionName string `json:"sessionName"`
	PaneID      string `json:"paneId"`
	Command     string `json:"command"`
	Path        string `json:"path"`
}

// livePanes lists every pane on the tmux server in one call. A server that
// does not run has no panes.
func (s *Service) livePanes() ([]LivePane, error) {
	value, err := output("", "tmux", s.tmuxArgs("list-panes", "-a", "-F", "#{session_id}\t#{session_name}\t#{pane_id}\t#{pane_current_command}\t#{pane_current_path}")...)
	if err != nil {
		if tmuxUnavailable(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("list tmux panes: %w", err)
	}
	var panes []LivePane
	for _, row := range strings.Split(value, "\n") {
		parts := strings.SplitN(row, "\t", 5)
		if len(parts) != 5 || parts[2] == "" {
			continue
		}
		panes = append(panes, LivePane{SessionID: parts[0], SessionName: parts[1], PaneID: parts[2], Command: parts[3], Path: parts[4]})
	}
	return panes, nil
}

// panesInside returns the panes that work inside root. Tmux reports the
// resolved path of a pane, so root is resolved too.
func panesInside(panes []LivePane, root string) []LivePane {
	if root == "" {
		return nil
	}
	resolved := root
	if path, err := filepath.EvalSymlinks(root); err == nil {
		resolved = path
	}
	var inside []LivePane
	for _, pane := range panes {
		if directoryInside(root, pane.Path) || directoryInside(resolved, pane.Path) {
			inside = append(inside, pane)
		}
	}
	return inside
}

// ForeignPanes returns the tmux panes outside the Workspace sessions that
// still work in the Workspace worktrees. A release must stop them first. The
// caller pane is never foreign: a stop would end the release itself.
func (s *Service) ForeignPanes(reference, currentPane string) ([]LivePane, error) {
	workspace, err := s.store.Find(reference)
	if err != nil {
		return nil, err
	}
	return s.foreignPanes(workspace, currentPane)
}

func (s *Service) foreignPanes(workspace domain.Workspace, currentPane string) ([]LivePane, error) {
	if workspace.Adopted || !workspace.Materialized || workspace.EnvironmentID == "" {
		return nil, nil
	}
	owned, err := s.ownedSessions(workspace.ID)
	if err != nil {
		return nil, err
	}
	panes, err := s.livePanes()
	if err != nil {
		return nil, err
	}
	var foreign []LivePane
	for _, pane := range panesInside(panes, workspace.Root) {
		if pane.PaneID != currentPane && !slices.Contains(owned, pane.SessionID) {
			foreign = append(foreign, pane)
		}
	}
	return foreign, nil
}

func foreignPanesError(workspaceName string, panes []LivePane) error {
	described := make([]string, 0, len(panes))
	for _, pane := range panes {
		described = append(described, fmt.Sprintf("%s (%s in session %q)", pane.PaneID, pane.Command, pane.SessionName))
	}
	return clierr.WithHint(
		clierr.New(clierr.UnsafeState, "%d tmux panes outside the Workspace session still work in Workspace %q: %s", len(panes), workspaceName, strings.Join(described, ", ")),
		"Stop those panes, or run the command with --force to stop them.")
}

// stopPanes stops each pane. A pane that is already gone is not an error.
func (s *Service) stopPanes(panes []LivePane) error {
	for _, pane := range panes {
		if err := run("", "tmux", s.tmuxArgs("kill-pane", "-t", pane.PaneID)...); err != nil && !strings.Contains(err.Error(), "can't find pane") {
			return fmt.Errorf("stop tmux pane %q: %w", pane.PaneID, err)
		}
	}
	return nil
}

// workspaceSessionRow finds the tmux session of the Workspace. A tmux
// restore removes the owner option, so it also adopts an unowned session
// with the saved session name, or an unowned session whose panes all work
// in the Workspace.
func (s *Service) workspaceSessionRow(workspace domain.Workspace) (tmuxSessionRow, bool, error) {
	rows, err := s.workspaceSessionRows(true)
	if err != nil {
		if tmuxUnavailable(err) {
			return tmuxSessionRow{}, false, nil
		}
		return tmuxSessionRow{}, false, err
	}
	for _, row := range rows {
		if row.ownerID == workspace.ID {
			return row, true, nil
		}
	}
	saved := workspace.TmuxSession
	if saved == "" {
		saved = sessionName(workspace.TemplateName, workspace.Name)
	}
	adopt := func(row tmuxSessionRow) (tmuxSessionRow, bool, error) {
		if err := s.claimSession(row.id, workspace.ID); err != nil {
			return tmuxSessionRow{}, false, err
		}
		row.ownerID = workspace.ID
		return row, true, nil
	}
	for _, row := range rows {
		if row.ownerID == "" && row.name == saved {
			return adopt(row)
		}
	}
	var panes []LivePane
	for _, row := range rows {
		if row.ownerID != "" {
			continue
		}
		if panes == nil {
			if panes, err = s.livePanes(); err != nil {
				return tmuxSessionRow{}, false, err
			}
		}
		if sessionWorksInWorkspace(panes, row.id, workspace) {
			return adopt(row)
		}
	}
	return tmuxSessionRow{}, false, nil
}

// sessionWorksInWorkspace reports whether the session has panes and every
// pane works inside the Workspace. Only then can twt adopt the session: a
// session stop ends no unrelated work.
func sessionWorksInWorkspace(panes []LivePane, sessionID string, workspace domain.Workspace) bool {
	roots := []string{workspace.Root}
	for _, repository := range workspace.Repositories {
		roots = append(roots, repository.Path)
	}
	found := false
	for _, pane := range panes {
		if pane.SessionID != sessionID {
			continue
		}
		found = true
		inside := false
		for _, root := range roots {
			if len(panesInside([]LivePane{pane}, root)) > 0 {
				inside = true
				break
			}
		}
		if !inside {
			return false
		}
	}
	return found
}

// MoveClientsOff switches each client attached to the session to the last
// session that the client used, or else to the next session. The caller
// then cleans up without a wait for the user. It returns the moved clients.
// A client stays when no other session exists: the final session stop
// detaches it.
func (s *Service) MoveClientsOff(sessionID string) []string {
	value, err := output("", "tmux", s.tmuxArgs("list-clients", "-t", sessionID, "-F", "#{client_name}")...)
	if err != nil {
		return nil
	}
	var moved []string
	for _, client := range strings.Fields(value) {
		if run("", "tmux", s.tmuxArgs("switch-client", "-c", client, "-l")...) != nil {
			if run("", "tmux", s.tmuxArgs("switch-client", "-c", client, "-n")...) != nil {
				continue
			}
		}
		moved = append(moved, client)
	}
	return moved
}

// ReturnClients switches the moved clients back to the session, so the user
// sees an error that stopped the cleanup.
func (s *Service) ReturnClients(clients []string, sessionID string) {
	for _, client := range clients {
		_ = run("", "tmux", s.tmuxArgs("switch-client", "-c", client, "-t", sessionID)...)
	}
}
