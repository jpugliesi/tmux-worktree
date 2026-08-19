package tmux

import (
	"bytes"
	"fmt"
	"os/exec"
	"strings"

	"github.com/jpugliesi/tmux-worktree/internal/domain"
)

type Client struct {
	Socket string
}

func (c Client) PaneBelongsToProject(pane, projectID string) bool {
	if pane == "" {
		return false
	}
	sessionID, err := c.output(nil, "display-message", "-p", "-t", pane, "#{session_id}")
	if err != nil {
		return false
	}
	owner, err := c.output(nil, "show-options", "-t", sessionID, "-v", "@twt2_project_id")
	return err == nil && owner == projectID
}

func (c Client) PaneProcess(pane, projectID string) (string, string, error) {
	if !c.PaneBelongsToProject(pane, projectID) {
		return "", "", fmt.Errorf("the pane is not owned by this Project")
	}
	process, err := c.output(nil, "display-message", "-p", "-t", pane, "#{pane_dead}\t#{pane_current_command}\t#{pane_start_command}")
	if err != nil {
		return "", "", fmt.Errorf("read pane process: %w", err)
	}
	parts := strings.SplitN(process, "\t", 3)
	if len(parts) != 3 || parts[0] != "0" || parts[1] == "" || parts[2] == "" {
		return "", "", fmt.Errorf("the pane does not have a live direct process")
	}
	return parts[1], parts[2], nil
}

func (c Client) ClaimAgentPane(pane, projectID, agentID string) error {
	if !c.PaneBelongsToProject(pane, projectID) {
		return fmt.Errorf("the pane is not owned by this Project")
	}
	owner, err := c.output(nil, "show-options", "-p", "-t", pane, "-v", "@twt2_agent_id")
	if err == nil && owner != "" && owner != agentID {
		return fmt.Errorf("the pane is already owned by Agent Session %q", owner)
	}
	if _, err := c.output(nil, "set-option", "-p", "-t", pane, "@twt2_agent_id", agentID); err != nil {
		return fmt.Errorf("mark Agent Session pane: %w", err)
	}
	return nil
}

func (c Client) PaneBelongsToAgent(pane, projectID, agentID, paneCommand, paneStart string) bool {
	if paneCommand == "" || paneStart == "" {
		return false
	}
	owner, err := c.output(nil, "show-options", "-p", "-t", pane, "-v", "@twt2_agent_id")
	if err != nil || owner != agentID {
		return false
	}
	current, start, err := c.PaneProcess(pane, projectID)
	return err == nil && current == paneCommand && start == paneStart
}

func (c Client) StartAgent(project domain.Project, label string, command []string) (string, error) {
	if len(command) == 0 {
		return "", fmt.Errorf("Agent Session has no resume command")
	}
	sessionID, err := c.projectSession(project)
	if err != nil {
		return "", err
	}
	windowName := safeWindowName(label)
	args := []string{"new-window", "-d", "-P", "-F", "#{pane_id}", "-t", sessionID, "-n", windowName, "-c", project.Repositories[0].Path, "--"}
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

func (c Client) Focus(pane, projectID, agentID, paneCommand, paneStart string) error {
	if !c.PaneBelongsToAgent(pane, projectID, agentID, paneCommand, paneStart) {
		return fmt.Errorf("the Agent Session process is not live in its owned pane")
	}
	if _, err := c.output(nil, "select-window", "-t", pane); err != nil {
		return fmt.Errorf("select Agent Session window: %w", err)
	}
	if _, err := c.output(nil, "select-pane", "-t", pane); err != nil {
		return fmt.Errorf("focus Agent Session: %w", err)
	}
	return nil
}

func (c Client) Send(pane, projectID, agentID, paneCommand, paneStart, text string) error {
	if !c.PaneBelongsToAgent(pane, projectID, agentID, paneCommand, paneStart) {
		return fmt.Errorf("the Agent Session process is not live in its owned pane")
	}
	buffer := "twt2-feedback-" + strings.TrimPrefix(pane, "%")
	if _, err := c.output(strings.NewReader(text), "load-buffer", "-b", buffer, "-"); err != nil {
		return fmt.Errorf("load Agent Session feedback: %w", err)
	}
	if _, err := c.output(nil, "paste-buffer", "-d", "-b", buffer, "-t", pane); err != nil {
		return fmt.Errorf("paste Agent Session feedback: %w", err)
	}
	if _, err := c.output(nil, "send-keys", "-t", pane, "Enter"); err != nil {
		return fmt.Errorf("submit Agent Session feedback: %w", err)
	}
	return nil
}

func (c Client) projectSession(project domain.Project) (string, error) {
	rows, err := c.output(nil, "list-sessions", "-F", "#{session_id}\t#{@twt2_project_id}")
	if err != nil {
		return "", fmt.Errorf("list tmux sessions: %w", err)
	}
	for _, row := range strings.Split(rows, "\n") {
		parts := strings.SplitN(row, "\t", 2)
		if len(parts) != 2 || parts[1] != project.ID {
			continue
		}
		return parts[0], nil
	}
	return "", fmt.Errorf("Project %q does not have a live owned tmux session", project.Name)
}

func (c Client) output(stdin *strings.Reader, args ...string) (string, error) {
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
