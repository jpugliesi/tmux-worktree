package workspace

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/jpugliesi/tmux-worktree/internal/clierr"
	"github.com/jpugliesi/tmux-worktree/internal/domain"
	"github.com/jpugliesi/tmux-worktree/internal/store"
)

// adoptTarget is the tmux session that an adopt selects.
type adoptTarget struct {
	sessionID   string
	sessionName string
	ownerID     string
}

// Adopt turns one existing tmux session into a Workspace. twt records the git
// repositories that the panes of the session sit in, marks the session with
// the Workspace ID, and saves the record. twt did not create the directories of
// an adopted Workspace, and removal never deletes them.
func (s *Service) Adopt(sessionReference, currentPane, name string) (domain.Workspace, error) {
	lock, err := store.AcquireMutationLock(s.options.StateDir)
	if err != nil {
		return domain.Workspace{}, err
	}
	defer lock.Release()
	target, workspace, err := s.validateAdopt(sessionReference, currentPane, name)
	if err != nil {
		return domain.Workspace{}, err
	}
	if err := s.store.Save(workspace); err != nil {
		return domain.Workspace{}, err
	}
	if err := run("", "tmux", s.tmuxArgs("set-option", "-t", target.sessionID, "@twt_workspace_id", workspace.ID)...); err != nil {
		_ = s.store.Delete(workspace.ID)
		return domain.Workspace{}, fmt.Errorf("mark tmux session %q: %w", target.sessionName, err)
	}
	return workspace, nil
}

// ValidateAdopt checks one adopt and returns the Workspace record that a real
// run would save. It writes nothing.
func (s *Service) ValidateAdopt(sessionReference, currentPane, name string) (domain.Workspace, error) {
	_, workspace, err := s.validateAdopt(sessionReference, currentPane, name)
	return workspace, err
}

func (s *Service) validateAdopt(sessionReference, currentPane, name string) (adoptTarget, domain.Workspace, error) {
	target, err := s.findAdoptTarget(sessionReference, currentPane)
	if err != nil {
		return target, domain.Workspace{}, err
	}
	if target.ownerID != "" {
		return target, domain.Workspace{}, clierr.New(clierr.AlreadyExists, "tmux session %q already belongs to Workspace %q", target.sessionName, target.ownerID)
	}
	if name == "" {
		name = target.sessionName
	}
	if err := store.ValidateResourceName(name); err != nil {
		return target, domain.Workspace{}, clierr.WithHint(
			clierr.New(clierr.InvalidUsage, "invalid Workspace name: %v", err),
			"Set a valid name with --name NAME.",
		)
	}
	workspaces, err := s.store.List()
	if err != nil {
		return target, domain.Workspace{}, err
	}
	for _, existing := range workspaces {
		if existing.Name == name {
			return target, domain.Workspace{}, clierr.New(clierr.AlreadyExists, "Workspace %q already exists", name)
		}
	}
	directories, err := s.paneDirectories(target.sessionID)
	if err != nil {
		return target, domain.Workspace{}, err
	}
	repositories := adoptedRepositories(directories)
	root := ""
	if len(repositories) > 0 {
		root = repositories[0].Path
	} else if len(directories) > 0 {
		root = directories[0]
	}
	if root == "" {
		return target, domain.Workspace{}, fmt.Errorf("tmux session %q has no pane directory", target.sessionName)
	}
	id, err := newID()
	if err != nil {
		return target, domain.Workspace{}, err
	}
	now := s.now()
	workspace := domain.Workspace{
		Version: domain.WorkspaceVersion, ID: id, Name: name, Status: domain.WorkspaceActive,
		Adopted: true, Root: root, TmuxSession: target.sessionName,
		Repositories: repositories, CreatedAt: now, UpdatedAt: now,
	}
	return target, workspace, nil
}

// findAdoptTarget selects the tmux session to adopt. An empty reference uses
// the session of the calling tmux pane.
func (s *Service) findAdoptTarget(sessionReference, currentPane string) (adoptTarget, error) {
	if sessionReference == "" {
		if currentPane == "" {
			return adoptTarget{}, clierr.New(clierr.InvalidUsage, "give a tmux session name, or run the command inside tmux")
		}
		row, err := output("", "tmux", s.tmuxArgs("display-message", "-p", "-t", currentPane, "#{session_id}\t#{session_name}")...)
		if err != nil {
			return adoptTarget{}, fmt.Errorf("inspect the current tmux pane: %w", err)
		}
		target, ok := parseAdoptTarget(row)
		if !ok {
			return adoptTarget{}, fmt.Errorf("inspect the current tmux pane: tmux returned no session")
		}
		target.ownerID, err = s.tmuxWorkspaceID(target.sessionID)
		if err != nil {
			return adoptTarget{}, fmt.Errorf("inspect the current tmux pane owner: %w", err)
		}
		return target, nil
	}
	rows, err := s.workspaceSessionRows(true)
	if err != nil {
		return adoptTarget{}, fmt.Errorf("list tmux sessions: %w", err)
	}
	for _, row := range rows {
		if row.name == sessionReference || row.id == sessionReference {
			return adoptTarget{sessionID: row.id, sessionName: row.name, ownerID: row.ownerID}, nil
		}
	}
	return adoptTarget{}, clierr.New(clierr.NotFound, "tmux session %q does not exist", sessionReference)
}

// AdoptableSessions lists the names of the tmux sessions that no Workspace owns.
// Shell completion of the adopt SESSION argument reads it. A tmux that gives
// no answer, such as a machine with no server, gives no names.
func (s *Service) AdoptableSessions() []string {
	rows, err := s.workspaceSessionRows(true)
	if err != nil {
		return nil
	}
	names := []string{}
	for _, row := range rows {
		if row.ownerID == "" {
			names = append(names, row.name)
		}
	}
	return names
}

func parseAdoptTarget(row string) (adoptTarget, bool) {
	parts := strings.SplitN(row, "\t", 3)
	if len(parts) < 2 || parts[0] == "" {
		return adoptTarget{}, false
	}
	target := adoptTarget{sessionID: parts[0], sessionName: parts[1]}
	if len(parts) == 3 {
		target.ownerID = parts[2]
	}
	return target, true
}

// paneDirectories returns the current directory of each pane of the session,
// without repeats, in pane order.
func (s *Service) paneDirectories(sessionID string) ([]string, error) {
	rows, err := output("", "tmux", s.tmuxArgs("list-panes", "-s", "-t", sessionID, "-F", "#{pane_current_path}")...)
	if err != nil {
		return nil, fmt.Errorf("list tmux panes: %w", err)
	}
	seen := map[string]bool{}
	directories := []string{}
	for _, row := range strings.Split(rows, "\n") {
		if row == "" || seen[row] {
			continue
		}
		seen[row] = true
		directories = append(directories, row)
	}
	return directories, nil
}

// adoptedRepositories maps the pane directories to the git repositories they
// sit in. A directory outside a git repository gives no repository. Each
// repository appears one time, in pane order.
func adoptedRepositories(directories []string) []domain.WorkspaceRepository {
	seen := map[string]bool{}
	names := map[string]int{}
	repositories := []domain.WorkspaceRepository{}
	for _, directory := range directories {
		topLevel, err := output(directory, "git", "rev-parse", "--show-toplevel")
		if err != nil || topLevel == "" || seen[topLevel] {
			continue
		}
		seen[topLevel] = true
		name := filepath.Base(topLevel)
		names[name]++
		if names[name] > 1 {
			name = fmt.Sprintf("%s-%d", name, names[name])
		}
		repositories = append(repositories, domain.WorkspaceRepository{Name: name, Path: topLevel, WindowName: name})
	}
	return repositories
}
