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

	workspaceservice "github.com/jpugliesi/tmux-worktree/internal/workspace"
)

const quickCreateWorkerArgument = "__twt_quick_create_worker"

// workerSpec identifies one private relocation worker argv mode together
// with its signal channel prefix and its tmux window names.
type workerSpec struct {
	// argument is the hidden argv mode that starts the worker.
	argument string
	// channelPrefix guards the tmux wait-for signal channel.
	channelPrefix string
	// windowName names the worker window in the destination session.
	windowName string
	// failedWindow names the worker window after a failure.
	failedWindow string
}

var quickCreateWorker = workerSpec{
	argument:      quickCreateWorkerArgument,
	channelPrefix: "twt-create-",
	windowName:    "twt-archive",
	failedWindow:  "archive-failed",
}

var doneWorker = workerSpec{
	argument:      doneWorkerArgument,
	channelPrefix: "twt-done-",
	windowName:    "twt-done",
	failedWindow:  "done-failed",
}

// openTmux moves the user to a tmux session: it switches the current client
// inside tmux and attaches outside tmux.
func openTmux(options Options, session string) error {
	args := make([]string, 0, 8)
	if options.TmuxSocket != "" {
		args = append(args, "-L", options.TmuxSocket, "-f", "/dev/null")
	}
	if os.Getenv("TMUX") != "" {
		args = append(args, "switch-client", "-t", "="+session)
	} else {
		args = append(args, "attach-session", "-t", "="+session)
	}
	process := exec.Command("tmux", args...)
	process.Stdin = os.Stdin
	process.Stdout = options.Stdout
	process.Stderr = options.Stderr
	if err := process.Run(); err != nil {
		return fmt.Errorf("open tmux session %q: %w", session, err)
	}
	return nil
}

// realQuickCreateSwitch returns the tmux implementation of the
// QuickCreateSwitch hook. With a client name it switches that client; without
// one it attaches or switches through openTmux.
func realQuickCreateSwitch(options Options) func(clientName, session string) error {
	return func(clientName, session string) error {
		if clientName == "" {
			return openTmux(options, session)
		}
		return switchTmuxClient(options, clientName, "="+session)
	}
}

// realQuickCreateArchive returns the tmux implementation of the
// QuickCreateArchive hook. It starts the archive worker in the new Workspace
// session and signals it. The worker archives the old Workspace and keeps its
// window visible on failure.
func realQuickCreateArchive(options Options) func(clientName, oldWorkspaceID, newWorkspaceID string) error {
	return func(clientName, oldWorkspaceID, newWorkspaceID string) error {
		hostSessionID, err := options.workspaceService().OwnedSessionID(newWorkspaceID)
		if err != nil {
			return err
		}
		helper, err := startRelocationHelper(options, quickCreateWorker, hostSessionID, clientName, []string{oldWorkspaceID, newWorkspaceID})
		if err != nil {
			return err
		}
		return helper.commit()
	}
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

// startRelocationHelper starts the worker window for one workerSpec in the
// given host session. workerArgs does not contain the argv mode, the signal
// channel, or the client name; this function adds them.
func startRelocationHelper(options Options, spec workerSpec, hostSessionID, clientName string, workerArgs []string) (*relocationHelper, error) {
	random := make([]byte, 12)
	if _, err := rand.Read(random); err != nil {
		return nil, fmt.Errorf("create relocation signal: %w", err)
	}
	channel := spec.channelPrefix + hex.EncodeToString(random)
	executable := options.QuickCreateExecutable
	if executable == "" {
		var err error
		executable, err = os.Executable()
		if err != nil {
			return nil, fmt.Errorf("find twt executable: %w", err)
		}
	}
	args := tmuxCommandArgs(options,
		"new-window", "-d", "-P", "-F", "#{window_id}", "-t", hostSessionID,
		"-n", spec.windowName, "-e", "TWT_CONFIG_DIR="+options.ConfigDir,
		"-e", "TWT_STATE_DIR="+options.StateDir, "-e", "TWT_DATA_DIR="+options.DataDir,
		"-e", "TWT_TMUX_SOCKET="+options.TmuxSocket, "--", executable, spec.argument,
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

// runRelocationWorker waits for the relocation signal, archives the Workspace,
// and runs the completion step of one worker. It shows the result message on
// the moved client. On failure it renames its window and pulls the client
// back to it.
func runRelocationWorker(options Options, spec workerSpec, workspaceID, channel, clientName, retry string, complete func(*workspaceservice.Service, workspaceservice.ArchiveResult) (string, error)) error {
	if !strings.HasPrefix(channel, spec.channelPrefix) {
		return fmt.Errorf("invalid relocation signal")
	}
	if err := waitForRelocationSignal(options, channel); err != nil {
		showRelocationFailureWindow(options, clientName, spec.failedWindow)
		return fmt.Errorf("the relocation signal timed out: %w; the Workspace did not change; run '%s' to retry", err, retry)
	}
	service := options.workspaceService()
	result := workspaceservice.ArchiveResult{}
	_, err := service.Find(workspaceID)
	if err == nil {
		result, err = service.Archive(workspaceID, os.Getenv("TMUX_PANE"))
	}
	if err != nil {
		showRelocationFailureWindow(options, clientName, spec.failedWindow)
		return fmt.Errorf("archive Workspace: %w; run '%s' to retry", err, retry)
	}
	message, err := complete(service, result)
	if err != nil {
		showRelocationFailureWindow(options, clientName, spec.failedWindow)
		return err
	}
	_ = runCommand("tmux", tmuxCommandArgs(options, "display-message", "-c", clientName, message)...)
	return nil
}

// RunQuickCreateWorker runs the private __twt_quick_create_worker argv
// mode. It waits for the relocation signal and archives the old Workspace.
func RunQuickCreateWorker(options Options, args []string) error {
	if len(args) != 4 {
		return fmt.Errorf("invalid quick create worker request")
	}
	oldWorkspaceID, newWorkspaceID, channel, clientName := args[0], args[1], args[2], args[3]
	retry := "twt archive " + oldWorkspaceID
	err := runRelocationWorker(options, quickCreateWorker, oldWorkspaceID, channel, clientName, retry,
		func(service *workspaceservice.Service, result workspaceservice.ArchiveResult) (string, error) {
			newWorkspace, _ := service.Find(newWorkspaceID)
			return fmt.Sprintf("Created Workspace %s; archived Workspace %s", newWorkspace.Name, result.Workspace.Name), nil
		})
	if err != nil {
		return err
	}
	return clearRelocationWindow(options)
}

// clearRelocationWindow lets a successful worker window close.
func clearRelocationWindow(options Options) error {
	return runCommand("tmux", tmuxCommandArgs(options, "set-option", "-w", "-t", os.Getenv("TMUX_PANE"), "remain-on-exit", "off")...)
}

func callingTmuxClient(options Options, pane string) (string, error) {
	sourceSession, err := commandOutput("tmux", tmuxCommandArgs(options, "display-message", "-p", "-t", pane, "#{session_id}")...)
	if err != nil {
		return "", fmt.Errorf("find the source Workspace session: %w", err)
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
		return "", fmt.Errorf("tmux pane %q is visible in %d clients; next requires exactly 1", pane, len(paneMatches))
	}
	if len(sessionMatches) == 1 {
		return sessionMatches[0], nil
	}
	return "", fmt.Errorf("tmux pane %q is not active in a client, and %d clients are attached to its Workspace session; next requires exactly 1", pane, len(sessionMatches))
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
