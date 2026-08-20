package tmux

import (
	"io"
	"strings"
	"testing"
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
