package tmux

import (
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/jpugliesi/tmux-worktree/internal/clierr"
	"github.com/jpugliesi/tmux-worktree/internal/domain"
)

func TestWorkspaceSessionAcceptsATrimmedCurrentOwnerRow(t *testing.T) {
	client := Client{run: func(_ io.Reader, args ...string) (string, error) {
		if args[0] == "list-sessions" {
			return "$1\tworkspace-one", nil
		}
		return "", nil
	}}
	sessionID, err := client.workspaceSession(domain.Workspace{ID: "workspace-one", Name: "one"})
	if err != nil || sessionID != "$1" {
		t.Fatalf("workspaceSession() = %q, %v", sessionID, err)
	}
}

func TestPaneBelongsToWorkspaceAcceptsTheLegacyTmuxOption(t *testing.T) {
	client := Client{run: func(_ io.Reader, args ...string) (string, error) {
		switch args[0] {
		case "display-message":
			return "$1", nil
		case "show-options":
			if args[len(args)-1] == "@twt_workspace_id" {
				return "", errors.New("missing option")
			}
			if args[len(args)-1] == "@twt_project_id" {
				return "workspace-one", nil
			}
		}
		return "", nil
	}}

	if !client.PaneBelongsToWorkspace("%1", "workspace-one") {
		t.Fatal("a pane with the legacy tmux option does not belong to its Workspace")
	}
}

func TestClaimAgentPaneDoesNotWriteWhenTheMarkerReadFails(t *testing.T) {
	wrote := false
	client := Client{run: func(_ io.Reader, args ...string) (string, error) {
		switch args[0] {
		case "display-message":
			return "$1", nil
		case "show-options":
			if args[len(args)-1] == "@twt_workspace_id" {
				return "workspace-1", nil
			}
			return "", errors.New("injected marker read failure")
		case "set-option":
			wrote = true
		}
		return "", nil
	}}
	err := client.ClaimAgentPane("%1", "workspace-1", "agent-1")
	if err == nil || !strings.Contains(err.Error(), "marker") {
		t.Fatalf("ClaimAgentPane() error = %v", err)
	}
	if wrote {
		t.Fatal("ClaimAgentPane() replaced the marker after a read failure")
	}
}

func TestSendPastesFeedbackBracketed(t *testing.T) {
	var calls [][]string
	client := Client{run: func(stdin io.Reader, args ...string) (string, error) {
		calls = append(calls, args)
		switch args[0] {
		case "display-message":
			if strings.Contains(args[len(args)-1], "session_id") {
				return "$1", nil
			}
			return "0\tclaude\tclaude --resume abc", nil
		case "show-options":
			if args[len(args)-1] == "@twt_workspace_id" {
				return "workspace-1", nil
			}
			return "agent-1", nil
		}
		return "", nil
	}}

	err := client.Send("%5", "workspace-1", "agent-1", "claude", "claude --resume abc", "line one\nline two")
	if err != nil {
		t.Fatalf("Send: %v", err)
	}

	var sequence [][]string
	for _, call := range calls {
		switch call[0] {
		case "load-buffer", "paste-buffer", "send-keys":
			sequence = append(sequence, call)
		}
	}
	if len(sequence) != 3 {
		t.Fatalf("got %d feedback commands, want 3: %v", len(sequence), sequence)
	}
	if sequence[0][0] != "load-buffer" || sequence[1][0] != "paste-buffer" || sequence[2][0] != "send-keys" {
		t.Fatalf("wrong command sequence: %v", sequence)
	}

	paste := sequence[1]
	bracketed := false
	for _, arg := range paste[1:] {
		if arg == "-p" {
			bracketed = true
		}
	}
	if !bracketed {
		t.Fatalf("paste-buffer is not bracketed (-p): %v", paste)
	}

	enter := sequence[2]
	if enter[len(enter)-1] != "Enter" {
		t.Fatalf("send-keys does not submit with Enter: %v", enter)
	}
}

func TestPaneLivenessIgnoresTheCurrentCommandOfThePane(t *testing.T) {
	client := func(paneDead, current, start, agentOwner string) Client {
		return Client{run: func(_ io.Reader, args ...string) (string, error) {
			switch args[0] {
			case "display-message":
				if strings.Contains(args[len(args)-1], "session_id") {
					return "$1", nil
				}
				return paneDead + "\t" + current + "\t" + start, nil
			case "show-options":
				if args[len(args)-1] == "@twt_workspace_id" {
					return "workspace-1", nil
				}
				return agentOwner, nil
			}
			return "", nil
		}}
	}

	tests := []struct {
		name     string
		client   Client
		wantLive bool
	}{
		{name: "pager in the pane", client: client("0", "less", "claude --resume abc", "agent-1"), wantLive: true},
		{name: "dead pane", client: client("1", "claude", "claude --resume abc", "agent-1"), wantLive: false},
		{name: "other start command", client: client("0", "claude", "zsh", "agent-1"), wantLive: false},
		{name: "other agent marker", client: client("0", "claude", "claude --resume abc", "agent-2"), wantLive: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if live := test.client.PaneBelongsToAgent("%5", "workspace-1", "agent-1", "claude", "claude --resume abc"); live != test.wantLive {
				t.Fatalf("PaneBelongsToAgent() = %v, want %v", live, test.wantLive)
			}
			checks := test.client.ExplainPane("%5", "workspace-1", "agent-1", "claude", "claude --resume abc")
			if len(checks) != 5 {
				t.Fatalf("ExplainPane() checks = %+v", checks)
			}
			if !checks[len(checks)-1].Advisory {
				t.Fatalf("the current command check is not advisory: %+v", checks)
			}
		})
	}
}

func TestNotLiveErrorTellsTheUserToResume(t *testing.T) {
	err := NotLiveError("agent-1")
	if !strings.Contains(err.Error(), "not live in its owned pane") {
		t.Fatalf("NotLiveError() = %v", err)
	}
	if hint := clierr.HintOf(err); !strings.Contains(hint, "twt agents resume agent-1") {
		t.Fatalf("NotLiveError() hint = %q", hint)
	}
}

func TestCaptureVisibleDoesNotReadScrollback(t *testing.T) {
	var capture []string
	client := Client{run: func(_ io.Reader, args ...string) (string, error) {
		switch args[0] {
		case "display-message":
			return "$1", nil
		case "show-options":
			return "workspace-1", nil
		case "capture-pane":
			capture = append([]string(nil), args...)
			return "visible", nil
		}
		return "", nil
	}}
	got, err := client.CaptureVisible("%1", "workspace-1")
	if err != nil || got != "visible" {
		t.Fatalf("CaptureVisible() = %q, %v", got, err)
	}
	for _, argument := range capture {
		if argument == "-S" {
			t.Fatalf("CaptureVisible() read scrollback: %v", capture)
		}
	}
}

func TestProcessPaneUsesStableProcessIdentityAndSendReadiness(t *testing.T) {
	processRows := "100 1 100 200 ttys001 Mon Aug 24 10:00:00 2026 zsh zsh\n" +
		"200 100 200 200 ttys001 Mon Aug 24 10:01:00 2026 cursor-agent /opt/cursor-agent\n"
	client := Client{
		run: func(_ io.Reader, args ...string) (string, error) {
			switch args[0] {
			case "list-sessions":
				return "$1\tworkspace-1\t", nil
			case "list-panes":
				return "%1\x1f100\x1f/dev/ttys001\x1f0\x1fcursor-agent\x1fzsh\x1f/tmp\x1fagent-1\x1f", nil
			}
			return "", nil
		},
		runProcesses: func() (string, error) { return processRows, nil },
	}
	workspace := domain.Workspace{ID: "workspace-1", Name: "one"}
	binding := ProcessBinding{
		PaneRootID: 100, PaneRootStarted: "Mon Aug 24 10:00:00 2026",
		ID: 200, Started: "Mon Aug 24 10:01:00 2026", Command: "cursor-agent",
		Evidence: ProcessEvidence(ProcessObservation{Command: "cursor-agent", Args: []string{"/opt/cursor-agent"}}),
	}
	if !client.ProcessPaneBelongs(workspace, "%1", "agent-1", binding, true) {
		t.Fatal("matching foreground provider process is not ready")
	}

	processRows = strings.Replace(processRows, "10:01:00", "10:02:00", 1)
	if client.ProcessPaneBelongs(workspace, "%1", "agent-1", binding, false) {
		t.Fatal("a reused PID with a different start time is live")
	}
	processRows = strings.Replace(processRows, "10:02:00", "10:01:00", 1)
	processRows = strings.Replace(processRows, "/opt/cursor-agent", "/opt/other-agent", 1)
	if client.ProcessPaneBelongs(workspace, "%1", "agent-1", binding, false) {
		t.Fatal("a process with changed provider arguments is live")
	}
	processRows = strings.Replace(processRows, "/opt/other-agent", "/opt/cursor-agent", 1)
	client.run = func(_ io.Reader, args ...string) (string, error) {
		switch args[0] {
		case "list-sessions":
			return "$1\tworkspace-1\t", nil
		case "list-panes":
			return "%1\x1f100\x1f/dev/ttys001\x1f0\x1fless\x1fzsh\x1f/tmp\x1fagent-1\x1f", nil
		}
		return "", nil
	}
	if !client.ProcessPaneBelongs(workspace, "%1", "agent-1", binding, false) {
		t.Fatal("a live provider with a foreground child cannot be focused")
	}
	if client.ProcessPaneBelongs(workspace, "%1", "agent-1", binding, true) {
		t.Fatal("input is ready while another command is in the foreground")
	}
}

func TestSendProcessChecksTheProviderAgainAfterItLoadsTheBuffer(t *testing.T) {
	initialRows := "100 1 100 200 ttys001 Mon Aug 24 10:00:00 2026 zsh zsh\n" +
		"200 100 200 200 ttys001 Mon Aug 24 10:01:00 2026 cursor-agent cursor-agent\n"
	changedRows := strings.Replace(initialRows, "10:01:00", "10:02:00", 1)
	loaded, pasted := false, false
	client := Client{
		run: func(_ io.Reader, args ...string) (string, error) {
			switch args[0] {
			case "list-sessions":
				return "$1\tworkspace-1\t", nil
			case "list-panes":
				return "%1\x1f100\x1f/dev/ttys001\x1f0\x1fcursor-agent\x1fzsh\x1f/tmp\x1fagent-1\x1f", nil
			case "load-buffer":
				loaded = true
			case "paste-buffer":
				pasted = true
			}
			return "", nil
		},
		runProcesses: func() (string, error) {
			if loaded {
				return changedRows, nil
			}
			return initialRows, nil
		},
	}
	process := parseProcesses(initialRows)[1]
	binding := ProcessBinding{
		PaneRootID: 100, PaneRootStarted: "Mon Aug 24 10:00:00 2026",
		ID: process.ID, Started: process.Started, Command: process.Command,
		Evidence: ProcessEvidence(process),
	}
	err := client.SendProcess(domain.Workspace{ID: "workspace-1"}, "%1", "agent-1", binding, "review")
	if err == nil || !strings.Contains(err.Error(), "not live") {
		t.Fatalf("SendProcess() error = %v", err)
	}
	if !loaded || pasted {
		t.Fatalf("loaded = %v, pasted = %v", loaded, pasted)
	}
}

func TestSendUsesAUniqueBufferAndCleansUpAPasteFailure(t *testing.T) {
	loaded, deleted := "", ""
	client := Client{run: func(_ io.Reader, args ...string) (string, error) {
		switch args[0] {
		case "display-message":
			if strings.Contains(args[len(args)-1], "session_id") {
				return "$1", nil
			}
			return "0\tclaude\tclaude --resume abc", nil
		case "show-options":
			if args[len(args)-1] == "@twt_workspace_id" {
				return "workspace-1", nil
			}
			return "agent-1", nil
		case "load-buffer":
			loaded = args[2]
		case "paste-buffer":
			return "", errors.New("injected paste failure")
		case "delete-buffer":
			deleted = args[2]
		}
		return "", nil
	}}
	err := client.Send("%5", "workspace-1", "agent-1", "claude", "claude --resume abc", "text")
	if err == nil || !strings.Contains(err.Error(), "paste") {
		t.Fatalf("Send() error = %v", err)
	}
	if loaded == "" || deleted != loaded || !strings.HasPrefix(loaded, "twt-feedback-5-") {
		t.Fatalf("feedback buffers: loaded=%q deleted=%q", loaded, deleted)
	}
}

func TestReleaseAgentPaneReportsAMarkerReadFailure(t *testing.T) {
	client := Client{run: func(_ io.Reader, args ...string) (string, error) {
		switch args[0] {
		case "display-message":
			return "$1", nil
		case "show-options":
			if args[len(args)-1] == "@twt_workspace_id" {
				return "workspace-1", nil
			}
			return "", errors.New("injected marker read failure")
		}
		return "", nil
	}}
	if err := client.ReleaseAgentPane("%1", "workspace-1", "agent-1"); err == nil || !strings.Contains(err.Error(), "marker") {
		t.Fatalf("ReleaseAgentPane() error = %v", err)
	}
}
