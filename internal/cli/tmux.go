package cli

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

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
