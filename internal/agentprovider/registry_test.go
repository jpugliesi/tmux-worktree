package agentprovider

import (
	"path/filepath"
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

func TestRegistryRequiresCursorProofForTheAgentAlias(t *testing.T) {
	root := t.TempDir()
	version := filepath.Join(root, "cursor-agent", "versions", "2026.08.21-test")
	executable := filepath.Join(version, "cursor-agent")
	script := filepath.Join(version, "index.js")

	tests := []struct {
		name    string
		process Process
		want    string
	}{
		{name: "cursor-agent", process: Process{Command: "cursor-agent", Executable: executable, Args: []string{executable}}, want: "cursor"},
		{name: "proved agent alias", process: Process{Command: "node", Executable: executable, Args: []string{filepath.Join(root, "bin", "agent"), "--use-system-ca", script}}, want: "cursor"},
		{name: "plain agent", process: Process{Command: "agent", Executable: filepath.Join(root, "bin", "agent"), Args: []string{filepath.Join(root, "bin", "agent")}}, want: ""},
		{name: "forged script argument", process: Process{Command: "agent", Executable: filepath.Join(root, "bin", "agent"), Args: []string{filepath.Join(root, "bin", "agent"), script}}, want: ""},
		{name: "wrong version script", process: Process{Command: "node", Executable: executable, Args: []string{filepath.Join(root, "bin", "agent"), filepath.Join(root, "cursor-agent", "versions", "other", "index.js")}}, want: ""},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := IdentifyProcess(test.process); got != test.want {
				t.Fatalf("IdentifyProcess(%+v) = %q, want %q", test.process, got, test.want)
			}
		})
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
