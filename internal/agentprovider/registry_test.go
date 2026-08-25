package agentprovider

import (
	"reflect"
	"strings"
	"testing"
)

func TestRegistryIsTheOrderedProviderAuthority(t *testing.T) {
	want := []string{"codex", "claude", "cursor", "grok", "command"}
	if got := Names(); !reflect.DeepEqual(got, want) {
		t.Fatalf("Names() = %v, want %v", got, want)
	}
	if err := Validate(); err != nil {
		t.Fatal(err)
	}
	for _, name := range want {
		if _, ok := Lookup(name); !ok {
			t.Fatalf("Lookup(%q) failed", name)
		}
	}
	if _, ok := Lookup("robot"); ok {
		t.Fatal("Lookup(robot) succeeded")
	}
}

func TestRegistryIdentifiesCommandsAndBuildsResumeCommands(t *testing.T) {
	tests := []struct {
		command []string
		want    string
	}{
		{[]string{"/opt/bin/codex", "resume", "one"}, "codex"},
		{[]string{"claude", "--resume", "one"}, "claude"},
		{[]string{"cursor-agent", "--resume", "one"}, "cursor"},
		{[]string{"grok", "--resume", "one"}, "grok"},
		{[]string{"./review-bot"}, "command"},
		{[]string{"bash", "-lc", "codex"}, ""},
	}
	for _, test := range tests {
		if got := IdentifyCommand(test.command); got != test.want {
			t.Errorf("IdentifyCommand(%v) = %q, want %q", test.command, got, test.want)
		}
	}

	resume := map[string]string{
		"codex":  "codex resume session-one",
		"claude": "claude --resume session-one",
		"grok":   "grok --resume session-one",
	}
	for provider, want := range resume {
		descriptor, _ := Lookup(provider)
		if got := strings.Join(descriptor.ResumeCommand("session-one"), " "); got != want {
			t.Errorf("%s ResumeCommand() = %q, want %q", provider, got, want)
		}
	}
	for _, provider := range []string{"cursor", "command"} {
		descriptor, _ := Lookup(provider)
		if got := descriptor.ResumeCommand("session-one"); got != nil {
			t.Errorf("%s ResumeCommand() = %v, want nil", provider, got)
		}
	}
}

func TestRegistryIdentifiesCursorAgentProcess(t *testing.T) {
	process := Process{Command: "cursor-agent", Args: []string{"/opt/bin/cursor-agent"}}
	if got := IdentifyProcess(process); got != "cursor" {
		t.Fatalf("IdentifyProcess(%+v) = %q, want cursor", process, got)
	}
}

func TestRegistryTranscriptCapabilitiesMatchTheImplementedProviders(t *testing.T) {
	for _, provider := range []string{"codex", "claude", "grok"} {
		descriptor, _ := Lookup(provider)
		if !descriptor.SupportsTranscript() {
			t.Errorf("%s has no transcript capability", provider)
		}
	}
	for _, provider := range []string{"cursor", "command"} {
		descriptor, _ := Lookup(provider)
		if descriptor.SupportsTranscript() {
			t.Errorf("%s unexpectedly has a transcript capability", provider)
		}
	}
}
