package domain

import (
	"strings"
	"testing"
)

func TestTemplateRejectsCollidingRepositoryEnvironmentNames(t *testing.T) {
	template := Template{
		Version: TemplateVersion,
		Name:    "collision",
		Repositories: []RepositorySpec{
			{Name: "foo-bar", Clone: CloneSpec{URL: "https://example.com/one.git"}},
			{Name: "foo.bar", Clone: CloneSpec{URL: "https://example.com/two.git"}},
		},
	}
	if err := template.Validate(); err == nil || !strings.Contains(err.Error(), "same initialization environment name") {
		t.Fatalf("Template.Validate error = %v", err)
	}
}

func TestTemplateValidatesDeclaredAgentSessions(t *testing.T) {
	tests := []struct {
		name    string
		agents  []TemplateAgent
		message string
	}{
		{
			name:   "valid",
			agents: []TemplateAgent{{Label: "review", Provider: "codex", Start: []string{"codex"}}},
		},
		{
			name:   "two labels",
			agents: []TemplateAgent{{Label: "review", Provider: "codex", Start: []string{"codex"}}, {Label: "build", Provider: "command", Start: []string{"./watch.sh"}}},
		},
		{
			name:    "missing label",
			agents:  []TemplateAgent{{Provider: "codex", Start: []string{"codex"}}},
			message: "must have a label",
		},
		{
			name:    "invalid label",
			agents:  []TemplateAgent{{Label: "../escape", Provider: "codex", Start: []string{"codex"}}},
			message: "is invalid",
		},
		{
			name:    "duplicate labels",
			agents:  []TemplateAgent{{Label: "review", Provider: "codex", Start: []string{"codex"}}, {Label: "review", Provider: "claude", Start: []string{"claude"}}},
			message: "declared more than once",
		},
		{
			name:    "missing provider",
			agents:  []TemplateAgent{{Label: "review", Start: []string{"codex"}}},
			message: "has no provider",
		},
		{
			name:    "unsupported provider",
			agents:  []TemplateAgent{{Label: "review", Provider: "robot", Start: []string{"robot"}}},
			message: "unsupported provider",
		},
		{
			name:    "empty start",
			agents:  []TemplateAgent{{Label: "review", Provider: "codex"}},
			message: "has no start command",
		},
		{
			name:    "blank start",
			agents:  []TemplateAgent{{Label: "review", Provider: "codex", Start: []string{" "}}},
			message: "has no start command",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			template := Template{Version: TemplateVersion, Name: "example", Agents: test.agents}
			err := template.Validate()
			if test.message == "" {
				if err != nil {
					t.Fatalf("Validate() error = %v, want no error", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), test.message) {
				t.Fatalf("Validate() error = %v, want text %q", err, test.message)
			}
		})
	}
}

func TestValidAgentProvider(t *testing.T) {
	for _, provider := range AgentProviders {
		if !ValidAgentProvider(provider) {
			t.Fatalf("ValidAgentProvider(%q) = false", provider)
		}
	}
	if ValidAgentProvider("robot") {
		t.Fatal("ValidAgentProvider(robot) = true")
	}
}

func TestTemplatePoolDepth(t *testing.T) {
	template := Template{Version: TemplateVersion, Name: "example"}
	if depth := template.EffectivePoolDepth(); depth != 1 {
		t.Fatalf("EffectivePoolDepth() with no value = %d, want 1", depth)
	}
	if err := template.Validate(); err != nil {
		t.Fatalf("Validate() with no pool depth error = %v", err)
	}

	template.PoolDepth = 3
	if depth := template.EffectivePoolDepth(); depth != 3 {
		t.Fatalf("EffectivePoolDepth() = %d, want 3", depth)
	}
	if err := template.Validate(); err != nil {
		t.Fatalf("Validate() with pool depth 3 error = %v", err)
	}

	template.PoolDepth = -1
	if err := template.Validate(); err == nil || !strings.Contains(err.Error(), "pool_depth") {
		t.Fatalf("Validate() with a negative pool depth error = %v", err)
	}
}
