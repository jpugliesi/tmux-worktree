package cli

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	projectservice "github.com/jpugliesi/tmux-worktree/internal/project"
)

const quickCreateWorkerArgument = "__twt2_quick_create_worker"

type quickCreateHelper struct {
	options      Options
	channel      string
	windowID     string
	newSessionID string
}

func startQuickCreateHelper(options Options, service *projectservice.Service, oldProjectID, newProjectID, clientName string) (quickCreateHelper, error) {
	newSessionID, err := service.OwnedSessionID(newProjectID)
	if err != nil {
		return quickCreateHelper{}, err
	}
	random := make([]byte, 12)
	if _, err := rand.Read(random); err != nil {
		return quickCreateHelper{}, fmt.Errorf("create archive signal: %w", err)
	}
	channel := "twt2-create-" + hex.EncodeToString(random)
	executable := options.QuickCreateExecutable
	if executable == "" {
		executable, err = os.Executable()
		if err != nil {
			return quickCreateHelper{}, fmt.Errorf("find twt2 executable: %w", err)
		}
	}
	args := tmuxCommandArgs(options,
		"new-window", "-d", "-P", "-F", "#{window_id}", "-t", newSessionID,
		"-n", "twt2-archive", "-e", "TWT2_CONFIG_DIR="+options.ConfigDir,
		"-e", "TWT2_STATE_DIR="+options.StateDir, "-e", "TWT2_DATA_DIR="+options.DataDir,
		"-e", "TWT2_TMUX_SOCKET="+options.TmuxSocket, "--", executable,
		quickCreateWorkerArgument, oldProjectID, newProjectID, channel, clientName,
	)
	windowID, err := commandOutput("tmux", args...)
	if err != nil {
		return quickCreateHelper{}, fmt.Errorf("start Project archive helper: %w", err)
	}
	if err := runCommand("tmux", tmuxCommandArgs(options, "set-option", "-w", "-t", windowID, "remain-on-exit", "on")...); err != nil {
		_ = runCommand("tmux", tmuxCommandArgs(options, "kill-window", "-t", windowID)...)
		return quickCreateHelper{}, fmt.Errorf("protect Project archive helper output: %w", err)
	}
	return quickCreateHelper{options: options, channel: channel, windowID: windowID, newSessionID: newSessionID}, nil
}

func (h quickCreateHelper) commit() error {
	return runCommand("tmux", tmuxCommandArgs(h.options, "wait-for", "-S", h.channel)...)
}

func (h quickCreateHelper) cancel() {
	_ = runCommand("tmux", tmuxCommandArgs(h.options, "kill-window", "-t", h.windowID)...)
}

func callingTmuxClient(options Options, pane string) (string, error) {
	sourceSession, err := commandOutput("tmux", tmuxCommandArgs(options, "display-message", "-p", "-t", pane, "#{session_id}")...)
	if err != nil {
		return "", fmt.Errorf("find the source Project session: %w", err)
	}
	rows, err := commandOutput("tmux", tmuxCommandArgs(options, "list-clients", "-F", "#{client_name}\t#{pane_id}\t#{session_id}")...)
	if err != nil {
		return "", fmt.Errorf("find the calling tmux client: %w", err)
	}
	var paneMatches, sessionMatches []string
	for _, row := range strings.Split(rows, "\n") {
		parts := strings.SplitN(row, "\t", 3)
		if len(parts) != 3 {
			continue
		}
		if parts[1] == pane {
			paneMatches = append(paneMatches, parts[0])
		}
		if parts[2] == sourceSession {
			sessionMatches = append(sessionMatches, parts[0])
		}
	}
	if len(paneMatches) == 1 {
		return paneMatches[0], nil
	}
	if len(paneMatches) > 1 {
		return "", fmt.Errorf("tmux pane %q is visible in %d clients; quick create requires exactly 1", pane, len(paneMatches))
	}
	if len(sessionMatches) == 1 {
		return sessionMatches[0], nil
	}
	return "", fmt.Errorf("tmux pane %q is not active in a client, and %d clients are attached to its Project session; quick create requires exactly 1", pane, len(sessionMatches))
}

func switchTmuxClient(options Options, clientName, sessionID string) error {
	if err := runCommand("tmux", tmuxCommandArgs(options, "switch-client", "-c", clientName, "-t", sessionID)...); err != nil {
		return fmt.Errorf("switch tmux client: %w", err)
	}
	return nil
}

func tmuxCommandArgs(options Options, args ...string) []string {
	if options.TmuxSocket == "" {
		return args
	}
	return append([]string{"-L", options.TmuxSocket, "-f", "/dev/null"}, args...)
}

func commandOutput(name string, args ...string) (string, error) {
	command := exec.Command(name, args...)
	data, err := command.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("%s: %w: %s", name, err, strings.TrimSpace(string(data)))
	}
	return strings.TrimSpace(string(data)), nil
}

func runCommand(name string, args ...string) error {
	_, err := commandOutput(name, args...)
	return err
}

func RunQuickCreateWorker(options Options, args []string) error {
	if len(args) != 4 {
		return fmt.Errorf("invalid quick create worker request")
	}
	oldProjectID, newProjectID, channel, clientName := args[0], args[1], args[2], args[3]
	if !strings.HasPrefix(channel, "twt2-create-") {
		return fmt.Errorf("invalid quick create archive signal")
	}
	if err := waitForQuickCreateSignal(options, channel); err != nil {
		showQuickCreateWorkerFailure(options, clientName)
		return fmt.Errorf("archive signal timed out: %w; old Project is still active; retry with 'twt2 archive %s'", err, oldProjectID)
	}
	service := projectservice.NewService(projectservice.Options{StateDir: options.StateDir, DataDir: options.DataDir, TmuxSocket: options.TmuxSocket})
	oldProject, err := service.Find(oldProjectID)
	if err == nil {
		_, err = service.Archive(oldProjectID, os.Getenv("TMUX_PANE"))
	}
	if err != nil {
		showQuickCreateWorkerFailure(options, clientName)
		return fmt.Errorf("archive old Project: %w; retry with 'twt2 archive %s'", err, oldProjectID)
	}
	newProject, _ := service.Find(newProjectID)
	message := fmt.Sprintf("Created Project %s; archived Project %s", newProject.Name, oldProject.Name)
	_ = runCommand("tmux", tmuxCommandArgs(options, "display-message", "-c", clientName, message)...)
	return runCommand("tmux", tmuxCommandArgs(options, "set-option", "-w", "-t", os.Getenv("TMUX_PANE"), "remain-on-exit", "off")...)
}

func waitForQuickCreateSignal(options Options, channel string) error {
	timeout := options.QuickCreateWaitTimeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	command := exec.CommandContext(ctx, "tmux", tmuxCommandArgs(options, "wait-for", channel)...)
	data, err := command.CombinedOutput()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err != nil {
		return fmt.Errorf("tmux: %w: %s", err, strings.TrimSpace(string(data)))
	}
	return nil
}

func showQuickCreateWorkerFailure(options Options, clientName string) {
	pane := os.Getenv("TMUX_PANE")
	_ = runCommand("tmux", tmuxCommandArgs(options, "rename-window", "-t", pane, "archive-failed")...)
	_ = runCommand("tmux", tmuxCommandArgs(options, "switch-client", "-c", clientName, "-t", pane)...)
}
