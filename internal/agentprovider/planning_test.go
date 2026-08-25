package agentprovider

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestBuildTicketPlanningLaunches(t *testing.T) {
	tests := []struct {
		provider   string
		wantStart  []string
		wantResume []string
	}{
		{
			provider:   "codex",
			wantStart:  []string{"codex", "-c", `model_reasoning_effort="xhigh"`, "PROMPT"},
			wantResume: []string{"codex", "-c", `model_reasoning_effort="xhigh"`},
		},
		{
			provider:   "claude",
			wantStart:  []string{"claude", "--permission-mode", "plan", "--effort", "xhigh", "PROMPT"},
			wantResume: []string{"claude", "--permission-mode", "plan", "--effort", "xhigh"},
		},
		{
			provider:   "cursor",
			wantStart:  []string{"agent", "--plan", "PROMPT"},
			wantResume: []string{"agent", "--plan"},
		},
		{
			provider:   "grok",
			wantStart:  []string{"grok", "--permission-mode", "plan", "--reasoning-effort", "xhigh", "PROMPT"},
			wantResume: []string{"grok", "--permission-mode", "plan", "--reasoning-effort", "xhigh"},
		},
	}
	for _, test := range tests {
		t.Run(test.provider, func(t *testing.T) {
			launch, err := BuildTicketPlanningLaunch(TicketPlanningRequest{
				Provider: test.provider,
				Effort:   TicketPlanningEffortXLarge,
				Tickets:  []string{"ticket-one"},
			}, func(name string) (string, error) { return "/bin/" + name, nil })
			if err != nil {
				t.Fatal(err)
			}
			prompt := launch.Start[len(launch.Start)-1]
			gotStart := append([]string(nil), launch.Start...)
			gotStart[len(gotStart)-1] = "PROMPT"
			if !reflect.DeepEqual(gotStart, test.wantStart) {
				t.Fatalf("Start = %#v, want %#v", gotStart, test.wantStart)
			}
			if !reflect.DeepEqual(launch.Resume, test.wantResume) {
				t.Fatalf("Resume = %#v, want %#v", launch.Resume, test.wantResume)
			}
			if test.provider == "codex" && (containsArgument(launch.Start[:len(launch.Start)-1], "plan") || containsArgument(launch.Start[:len(launch.Start)-1], "read-only")) {
				t.Fatalf("Codex launch claims unsupported plan or read-only mode: %v", launch.Start)
			}
			if !strings.Contains(prompt, "twt tickets show ticket-one --output json") {
				t.Fatalf("prompt does not tell the Agent to read the Ticket:\n%s", prompt)
			}
		})
	}
}

func TestTicketPlanningEffortMapping(t *testing.T) {
	want := map[TicketPlanningEffort]string{
		TicketPlanningEffortSmall:  "low",
		TicketPlanningEffortMedium: "medium",
		TicketPlanningEffortLarge:  "high",
		TicketPlanningEffortXLarge: "xhigh",
	}
	for effort, level := range want {
		if got, err := effort.ProviderLevel(); err != nil || got != level {
			t.Errorf("%s ProviderLevel() = %q, %v; want %q", effort, got, err, level)
		}
	}
	if _, err := TicketPlanningEffort("huge").ProviderLevel(); err == nil {
		t.Fatal("unknown effort was accepted")
	}
}

func TestTicketPlanningPromptStartsWithInstructionsAndIncludesAllTickets(t *testing.T) {
	launch, err := BuildTicketPlanningLaunch(TicketPlanningRequest{
		Provider:     "cursor",
		Effort:       TicketPlanningEffortLarge,
		Instructions: "Read CONTEXT.md first.\nCheck the public CLI.",
		Tickets:      []string{"ticket-one", "ticket-two"},
	}, func(name string) (string, error) { return "/bin/" + name, nil })
	if err != nil {
		t.Fatal(err)
	}
	prompt := launch.Start[len(launch.Start)-1]
	if !strings.HasPrefix(prompt, "Read CONTEXT.md first.\nCheck the public CLI.\n\nUse a thorough planning effort.") {
		t.Fatalf("custom instructions are not first:\n%s", prompt)
	}
	for _, ticket := range []string{"ticket-one", "ticket-two"} {
		if strings.Count(prompt, "twt tickets show "+ticket+" --output json") != 1 {
			t.Fatalf("prompt does not contain one read command for %s:\n%s", ticket, prompt)
		}
	}
	if !strings.Contains(prompt, "Create one plan to implement these twt Tickets: `ticket-one`, `ticket-two`.") {
		t.Fatalf("multi-Ticket request is missing:\n%s", prompt)
	}
}

func TestBuildTicketPlanningLaunchValidatesBeforeUse(t *testing.T) {
	lookPath := func(name string) (string, error) { return "", errors.New("missing " + name) }
	for _, request := range []TicketPlanningRequest{
		{Provider: "command", Effort: TicketPlanningEffortLarge, Tickets: []string{"one"}},
		{Provider: "codex", Effort: "huge", Tickets: []string{"one"}},
		{Provider: "codex", Effort: TicketPlanningEffortLarge},
		{Provider: "codex", Effort: TicketPlanningEffortLarge, Tickets: []string{"one"}},
	} {
		if _, err := BuildTicketPlanningLaunch(request, lookPath); err == nil {
			t.Fatalf("BuildTicketPlanningLaunch(%+v) succeeded", request)
		}
	}
}

func TestTicketPlanningCursorFallsBackToCursorAgent(t *testing.T) {
	launch, err := BuildTicketPlanningLaunch(TicketPlanningRequest{
		Provider: "cursor", Effort: TicketPlanningEffortLarge, Tickets: []string{"one"},
	}, func(name string) (string, error) {
		if name == "agent" {
			return "", errors.New("missing")
		}
		return "/bin/cursor-agent", nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if launch.Start[0] != "cursor-agent" || launch.Resume[0] != "cursor-agent" {
		t.Fatalf("Cursor commands = %v, %v", launch.Start, launch.Resume)
	}
}

func containsArgument(arguments []string, value string) bool {
	for _, argument := range arguments {
		if argument == value || strings.Contains(argument, value) {
			return true
		}
	}
	return false
}
