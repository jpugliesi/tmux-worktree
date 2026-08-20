package tmux

import (
	"io"
	"strings"
	"testing"

	"github.com/jpugliesi/tmux-worktree/internal/clierr"
)

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
			if args[len(args)-1] == "@twt2_project_id" {
				return "project-1", nil
			}
			return "agent-1", nil
		}
		return "", nil
	}}

	err := client.Send("%5", "project-1", "agent-1", "claude", "claude --resume abc", "line one\nline two")
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
				if args[len(args)-1] == "@twt2_project_id" {
					return "project-1", nil
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
			if live := test.client.PaneBelongsToAgent("%5", "project-1", "agent-1", "claude", "claude --resume abc"); live != test.wantLive {
				t.Fatalf("PaneBelongsToAgent() = %v, want %v", live, test.wantLive)
			}
			checks := test.client.ExplainPane("%5", "project-1", "agent-1", "claude", "claude --resume abc")
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
	if hint := clierr.HintOf(err); !strings.Contains(hint, "twt2 agents resume agent-1") {
		t.Fatalf("NotLiveError() hint = %q", hint)
	}
}
