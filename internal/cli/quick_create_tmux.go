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
	*relocationHelper
	newSessionID string
}

func startQuickCreateHelper(options Options, service *projectservice.Service, oldProjectID, newProjectID, clientName string) (quickCreateHelper, error) {
	newSessionID, err := service.OwnedSessionID(newProjectID)
	if err != nil {
		return quickCreateHelper{}, err
	}
	helper, err := startRelocationHelper(options, newSessionID, clientName, []string{quickCreateWorkerArgument, oldProjectID, newProjectID})
	if err != nil {
		return quickCreateHelper{}, err
	}
	return quickCreateHelper{relocationHelper: helper, newSessionID: newSessionID}, nil
}

// relocationHelper is a worker window inside a destination tmux session. The
// caller switches or detaches the tmux client, then signals the worker with
// commit. The worker window keeps its output on failure through
// remain-on-exit.
type relocationHelper struct {
	options  Options
	channel  string
	windowID string
}

// startRelocationHelper starts the worker window for workerArgs in the given
// host session. workerArgs starts with the private worker argv mode and does
// not contain the signal channel or the client name; this function appends
// both.
func startRelocationHelper(options Options, hostSessionID, clientName string, workerArgs []string) (*relocationHelper, error) {
	if len(workerArgs) == 0 {
		return nil, fmt.Errorf("missing relocation worker arguments")
	}
	random := make([]byte, 12)
	if _, err := rand.Read(random); err != nil {
		return nil, fmt.Errorf("create relocation signal: %w", err)
	}
	channel := relocationChannelPrefix(workerArgs[0]) + hex.EncodeToString(random)
	executable := options.QuickCreateExecutable
	if executable == "" {
		var err error
		executable, err = os.Executable()
		if err != nil {
			return nil, fmt.Errorf("find twt2 executable: %w", err)
		}
	}
	args := tmuxCommandArgs(options,
		"new-window", "-d", "-P", "-F", "#{window_id}", "-t", hostSessionID,
		"-n", relocationWindowName(workerArgs[0]), "-e", "TWT2_CONFIG_DIR="+options.ConfigDir,
		"-e", "TWT2_STATE_DIR="+options.StateDir, "-e", "TWT2_DATA_DIR="+options.DataDir,
		"-e", "TWT2_TMUX_SOCKET="+options.TmuxSocket, "--", executable,
	)
	args = append(args, workerArgs...)
	args = append(args, channel, clientName)
	windowID, err := commandOutput("tmux", args...)
	if err != nil {
		return nil, fmt.Errorf("start the relocation helper: %w", err)
	}
	if err := runCommand("tmux", tmuxCommandArgs(options, "set-option", "-w", "-t", windowID, "remain-on-exit", "on")...); err != nil {
		_ = runCommand("tmux", tmuxCommandArgs(options, "kill-window", "-t", windowID)...)
		return nil, fmt.Errorf("protect the relocation helper output: %w", err)
	}
	return &relocationHelper{options: options, channel: channel, windowID: windowID}, nil
}

func (h *relocationHelper) commit() error {
	return runCommand("tmux", tmuxCommandArgs(h.options, "wait-for", "-S", h.channel)...)
}

func (h *relocationHelper) cancel() {
	_ = runCommand("tmux", tmuxCommandArgs(h.options, "kill-window", "-t", h.windowID)...)
}

func relocationChannelPrefix(worker string) string {
	if worker == finishWorkerArgument {
		return "twt2-finish-"
	}
	return "twt2-create-"
}

func relocationWindowName(worker string) string {
	if worker == finishWorkerArgument {
		return "twt2-finish"
	}
	return "twt2-archive"
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
	if err := waitForRelocationSignal(options, channel); err != nil {
		showRelocationFailureWindow(options, clientName, "archive-failed")
		return fmt.Errorf("archive signal timed out: %w; old Project is still active; retry with 'twt2 archive %s'", err, oldProjectID)
	}
	service := projectservice.NewService(projectservice.Options{StateDir: options.StateDir, DataDir: options.DataDir, TmuxSocket: options.TmuxSocket})
	oldProject, err := service.Find(oldProjectID)
	if err == nil {
		_, err = service.Archive(oldProjectID, os.Getenv("TMUX_PANE"))
	}
	if err != nil {
		showRelocationFailureWindow(options, clientName, "archive-failed")
		return fmt.Errorf("archive old Project: %w; retry with 'twt2 archive %s'", err, oldProjectID)
	}
	newProject, _ := service.Find(newProjectID)
	message := fmt.Sprintf("Created Project %s; archived Project %s", newProject.Name, oldProject.Name)
	_ = runCommand("tmux", tmuxCommandArgs(options, "display-message", "-c", clientName, message)...)
	return runCommand("tmux", tmuxCommandArgs(options, "set-option", "-w", "-t", os.Getenv("TMUX_PANE"), "remain-on-exit", "off")...)
}

func waitForRelocationSignal(options Options, channel string) error {
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

func showRelocationFailureWindow(options Options, clientName, windowName string) {
	pane := os.Getenv("TMUX_PANE")
	_ = runCommand("tmux", tmuxCommandArgs(options, "rename-window", "-t", pane, windowName)...)
	_ = runCommand("tmux", tmuxCommandArgs(options, "switch-client", "-c", clientName, "-t", pane)...)
}
