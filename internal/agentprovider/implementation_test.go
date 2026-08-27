package agentprovider

import (
	"reflect"
	"strings"
	"testing"
)

func TestBuildTicketImplementationLaunches(t *testing.T) {
	tests := []struct {
		provider   string
		wantStart  []string
		wantResume []string
	}{
		{
			provider:   "codex",
			wantStart:  []string{"codex", "--dangerously-bypass-approvals-and-sandbox", "-c", `model_reasoning_effort="high"`, "PROMPT"},
			wantResume: []string{"codex", "--dangerously-bypass-approvals-and-sandbox", "-c", `model_reasoning_effort="high"`},
		},
		{
			provider:   "claude",
			wantStart:  []string{"claude", "--permission-mode", "bypassPermissions", "--effort", "high", "PROMPT"},
			wantResume: []string{"claude", "--permission-mode", "bypassPermissions", "--effort", "high"},
		},
		{
			provider:   "cursor",
			wantStart:  []string{"cursor-agent", "--force", "--trust", "PROMPT"},
			wantResume: []string{"cursor-agent", "--force", "--trust"},
		},
		{
			provider:   "grok",
			wantStart:  []string{"grok", "--permission-mode", "bypassPermissions", "--reasoning-effort", "high", "PROMPT"},
			wantResume: []string{"grok", "--permission-mode", "bypassPermissions", "--reasoning-effort", "high"},
		},
	}
	for _, test := range tests {
		t.Run(test.provider, func(t *testing.T) {
			launch, err := BuildTicketImplementationLaunch(TicketImplementationRequest{
				Provider: test.provider,
				Effort:   TicketPlanningEffortLarge,
				Ticket:   "fix-auth",
				Claimant: "twt-local-abc12345",
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
			for _, argument := range launch.Resume {
				if argument == "plan" || argument == "--plan" {
					t.Fatalf("implementation launch runs in plan mode: %v", launch.Resume)
				}
			}
			for _, want := range []string{
				"Implement twt Ticket `fix-auth`.",
				"twt tickets show fix-auth --output json",
				"Follow the Ticket's ## Plan section",
				"twt projects plan show PROJECT --output json",
				"AGENTS.md or CLAUDE.md",
				"Definition of done",
				"every acceptance criterion in the Ticket holds",
				"Do not end your turn until the Ticket is complete",
				"three genuinely different attempts",
				"re-claim it if you released it",
				"twt tickets complete fix-auth --as twt-local-abc12345 --pr URL",
				"twt tickets unclaim fix-auth --as twt-local-abc12345",
				"twt tickets comment fix-auth -",
			} {
				if !strings.Contains(prompt, want) {
					t.Fatalf("prompt lacks %q:\n%s", want, prompt)
				}
			}
		})
	}
}

func TestBuildTicketImplementationLaunchPrependsInstructions(t *testing.T) {
	launch, err := BuildTicketImplementationLaunch(TicketImplementationRequest{
		Provider:     "grok",
		Effort:       TicketPlanningEffortMedium,
		Instructions: "Use the repo skills.",
		Ticket:       "fix-auth",
		Claimant:     "twt-local-abc12345",
	}, func(name string) (string, error) { return "/bin/" + name, nil })
	if err != nil {
		t.Fatal(err)
	}
	prompt := launch.Start[len(launch.Start)-1]
	if !strings.HasPrefix(prompt, "Use the repo skills.\n\n") {
		t.Fatalf("instructions are not first:\n%s", prompt)
	}
}

func TestBuildTicketImplementationLaunchValidates(t *testing.T) {
	lookPath := func(name string) (string, error) { return "/bin/" + name, nil }
	if _, err := BuildTicketImplementationLaunch(TicketImplementationRequest{
		Provider: "vim", Effort: TicketPlanningEffortLarge, Ticket: "x", Claimant: "c",
	}, lookPath); err == nil {
		t.Fatal("unsupported provider accepted")
	}
	if _, err := BuildTicketImplementationLaunch(TicketImplementationRequest{
		Provider: "grok", Effort: TicketPlanningEffortLarge, Claimant: "c",
	}, lookPath); err == nil {
		t.Fatal("missing ticket accepted")
	}
	if _, err := BuildTicketImplementationLaunch(TicketImplementationRequest{
		Provider: "grok", Effort: TicketPlanningEffortLarge, Ticket: "x",
	}, lookPath); err == nil {
		t.Fatal("missing claimant accepted")
	}
}
