package tmux

import (
	"bytes"
	"fmt"
	"io"
	"os/exec"
	"strings"

	"github.com/jpugliesi/tmux-worktree/internal/clierr"
	"github.com/jpugliesi/tmux-worktree/internal/domain"
)

type Client struct {
	Socket string

	// run overrides the tmux exec for tests; nil means real tmux.
	run func(stdin io.Reader, args ...string) (string, error)
}

func (c Client) PaneBelongsToWorkspace(pane, workspaceID string) bool {
	if pane == "" {
		return false
	}
	sessionID, err := c.output(nil, "display-message", "-p", "-t", pane, "#{session_id}")
	if err != nil {
		return false
	}
	owner, err := c.workspaceOwner(sessionID)
	return err == nil && owner == workspaceID
}

func (c Client) workspaceOwner(sessionID string) (string, error) {
	owner, err := c.output(nil, "show-options", "-t", sessionID, "-v", "@twt_workspace_id")
	if err == nil && owner != "" {
		return owner, nil
	}
	return c.output(nil, "show-options", "-t", sessionID, "-v", "@twt_project_id")
}

func (c Client) PaneProcess(pane, workspaceID string) (string, string, error) {
	if !c.PaneBelongsToWorkspace(pane, workspaceID) {
		return "", "", fmt.Errorf("the pane is not owned by this Workspace")
	}
	dead, current, start, err := c.paneState(pane)
	if err != nil {
		return "", "", fmt.Errorf("read pane process: %w", err)
	}
	if dead || current == "" || start == "" {
		return "", "", fmt.Errorf("the pane does not have a live direct process")
	}
	return current, start, nil
}

// paneState reads the dead flag, the current command, and the start command
// of one pane in one tmux call. A pane state that twt cannot parse counts as
// dead.
func (c Client) paneState(pane string) (dead bool, current, start string, err error) {
	value, err := c.output(nil, "display-message", "-p", "-t", pane, "#{pane_dead}\t#{pane_current_command}\t#{pane_start_command}")
	if err != nil {
		return true, "", "", err
	}
	parts := strings.SplitN(value, "\t", 3)
	if len(parts) != 3 {
		return true, "", "", nil
	}
	return parts[0] != "0", parts[1], parts[2], nil
}

func (c Client) ClaimAgentPane(pane, workspaceID, agentID string) error {
	if !c.PaneBelongsToWorkspace(pane, workspaceID) {
		return fmt.Errorf("the pane is not owned by this Workspace")
	}
	owner, err := c.output(nil, "show-options", "-p", "-t", pane, "-v", "@twt_agent_id")
	if err == nil && owner != "" && owner != agentID {
		return fmt.Errorf("the pane is already owned by Agent Session %q", owner)
	}
	if _, err := c.output(nil, "set-option", "-p", "-t", pane, "@twt_agent_id", agentID); err != nil {
		return fmt.Errorf("mark Agent Session pane: %w", err)
	}
	return nil
}

// PaneCheck is the result of one Agent Session pane predicate. An advisory
// check does not change liveness. The current command of a pane changes when
// the Agent starts a pager or an editor, so twt shows it but does not use it.
type PaneCheck struct {
	Name     string
	OK       bool
	Advisory bool
}

// ExplainPane returns one check for each Agent Session pane predicate, in a
// stable order. Callers can show the result to a user.
func (c Client) ExplainPane(pane, workspaceID, agentID, paneCommand, paneStart string) []PaneCheck {
	workspacePane := pane != "" && c.PaneBelongsToWorkspace(pane, workspaceID)
	owned := false
	if pane != "" && agentID != "" {
		owner, err := c.output(nil, "show-options", "-p", "-t", pane, "-v", "@twt_agent_id")
		owned = err == nil && owner == agentID
	}
	dead, current, start := true, "", ""
	if workspacePane {
		if paneDead, paneCurrent, paneStart, err := c.paneState(pane); err == nil {
			dead, current, start = paneDead, paneCurrent, paneStart
		}
	}
	return []PaneCheck{
		{Name: "workspace pane", OK: workspacePane},
		{Name: "agent marker", OK: owned},
		{Name: "live process", OK: !dead},
		{Name: "start command", OK: paneStart != "" && start == paneStart},
		{Name: "current command", OK: paneCommand != "" && current == paneCommand, Advisory: true},
	}
}

func (c Client) PaneBelongsToAgent(pane, workspaceID, agentID, paneCommand, paneStart string) bool {
	for _, check := range c.ExplainPane(pane, workspaceID, agentID, paneCommand, paneStart) {
		if !check.Advisory && !check.OK {
			return false
		}
	}
	return true
}

func (c Client) StartAgent(workspace domain.Workspace, label string, command []string) (string, error) {
	if len(command) == 0 {
		return "", fmt.Errorf("Agent Session has no resume command")
	}
	sessionID, err := c.workspaceSession(workspace)
	if err != nil {
		return "", err
	}
	windowName := safeWindowName(label)
	args := []string{"new-window", "-d", "-P", "-F", "#{pane_id}", "-t", sessionID, "-n", windowName, "-c", workspace.Repositories[0].Path, "--"}
	args = append(args, command...)
	pane, err := c.output(nil, args...)
	if err != nil {
		return "", fmt.Errorf("start Agent Session: %w", err)
	}
	if pane == "" {
		return "", fmt.Errorf("tmux did not return an Agent Session pane")
	}
	return pane, nil
}

func (c Client) Focus(pane, workspaceID, agentID, paneCommand, paneStart string) error {
	if !c.PaneBelongsToAgent(pane, workspaceID, agentID, paneCommand, paneStart) {
		return NotLiveError(agentID)
	}
	if _, err := c.output(nil, "select-window", "-t", pane); err != nil {
		return fmt.Errorf("select Agent Session window: %w", err)
	}
	if _, err := c.output(nil, "select-pane", "-t", pane); err != nil {
		return fmt.Errorf("focus Agent Session: %w", err)
	}
	return nil
}

// NotLiveError reports that the Agent Session process is not live in its
// owned pane, and tells the caller how to start the Agent Session again.
func NotLiveError(agentID string) error {
	return clierr.WithHint(
		clierr.New(clierr.PreconditionFailed, "the Agent Session process is not live in its owned pane"),
		"Run 'twt agents resume %s' to start the Agent Session again.", agentID,
	)
}

func (c Client) Send(pane, workspaceID, agentID, paneCommand, paneStart, text string) error {
	if !c.PaneBelongsToAgent(pane, workspaceID, agentID, paneCommand, paneStart) {
		return NotLiveError(agentID)
	}
	buffer := "twt-feedback-" + strings.TrimPrefix(pane, "%")
	if _, err := c.output(strings.NewReader(text), "load-buffer", "-b", buffer, "-"); err != nil {
		return fmt.Errorf("load Agent Session feedback: %w", err)
	}
	if _, err := c.output(nil, "paste-buffer", "-d", "-p", "-b", buffer, "-t", pane); err != nil {
		return fmt.Errorf("paste Agent Session feedback: %w", err)
	}
	if _, err := c.output(nil, "send-keys", "-t", pane, "Enter"); err != nil {
		return fmt.Errorf("submit Agent Session feedback: %w", err)
	}
	return nil
}

func (c Client) workspaceSession(workspace domain.Workspace) (string, error) {
	rows, err := c.output(nil, "list-sessions", "-F", "#{session_id}\t#{@twt_workspace_id}\t#{@twt_project_id}")
	if err != nil {
		return "", fmt.Errorf("list tmux sessions: %w", err)
	}
	for _, row := range strings.Split(rows, "\n") {
		parts := strings.SplitN(row, "\t", 3)
		if len(parts) < 2 {
			continue
		}
		owner := parts[1]
		if owner == "" && len(parts) == 3 {
			owner = parts[2]
		}
		if owner != workspace.ID {
			continue
		}
		return parts[0], nil
	}
	return "", fmt.Errorf("Workspace %q does not have a live owned tmux session", workspace.Name)
}

func (c Client) output(stdin io.Reader, args ...string) (string, error) {
	if c.run != nil {
		return c.run(stdin, args...)
	}
	allArgs := args
	if c.Socket != "" {
		allArgs = append([]string{"-L", c.Socket, "-f", "/dev/null"}, args...)
	}
	command := exec.Command("tmux", allArgs...)
	if stdin != nil {
		command.Stdin = stdin
	}
	var stdout, stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	if err := command.Run(); err != nil {
		return "", fmt.Errorf("tmux %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return strings.TrimSpace(stdout.String()), nil
}

func safeWindowName(label string) string {
	var result strings.Builder
	for _, character := range label {
		if (character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z') || (character >= '0' && character <= '9') || character == '-' || character == '_' {
			result.WriteRune(character)
		}
	}
	if result.Len() == 0 {
		return "agent"
	}
	return result.String()
}
