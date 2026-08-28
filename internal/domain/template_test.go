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

func TestTemplateValidatesTheSessionCommand(t *testing.T) {
	tests := []struct {
		name    string
		session *SessionSpec
		message string
	}{
		{
			name: "no session command",
		},
		{
			name:    "command only",
			session: &SessionSpec{Command: []string{"./scripts/layout.sh"}},
		},
		{
			name:    "command with arguments and a directory",
			session: &SessionSpec{Command: []string{"./scripts/layout.sh", "--wide"}, CWD: "app/scripts"},
		},
		{
			name:    "empty command",
			session: &SessionSpec{},
			message: "command must not be empty",
		},
		{
			name:    "blank command",
			session: &SessionSpec{Command: []string{" "}},
			message: "command must not be empty",
		},
		{
			name:    "absolute directory",
			session: &SessionSpec{Command: []string{"./layout.sh"}, CWD: "/etc"},
			message: "cwd must stay inside the Workspace root",
		},
		{
			name:    "directory above the Workspace root",
			session: &SessionSpec{Command: []string{"./layout.sh"}, CWD: "../elsewhere"},
			message: "cwd must stay inside the Workspace root",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			template := Template{Version: TemplateVersion, Name: "example", Session: test.session}
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

func TestTemplateValidatesTheCloneFilter(t *testing.T) {
	template := Template{
		Version: TemplateVersion, Name: "example",
		Repositories: []RepositorySpec{{Name: "app", Clone: CloneSpec{URL: "https://example.com/app.git"}}},
	}
	for _, filter := range []string{"", "blob:none", "tree:0", "blob:limit=1m"} {
		template.Repositories[0].Clone.Filter = filter
		if err := template.Validate(); err != nil {
			t.Fatalf("Validate() with clone filter %q error = %v", filter, err)
		}
	}
	for _, filter := range []string{"blob none", "--upload-pack=evil", "a\nb"} {
		template.Repositories[0].Clone.Filter = filter
		if err := template.Validate(); err == nil || !strings.Contains(err.Error(), "clone filter") {
			t.Fatalf("Validate() with clone filter %q error = %v", filter, err)
		}
	}
}

func TestTemplateValidatesLocalDispatchDefaults(t *testing.T) {
	template := Template{
		Version: TemplateVersion,
		Name:    "product",
		Repositories: []RepositorySpec{
			{Name: "api", Clone: CloneSpec{URL: "https://github.com/acme/api.git"}, DefaultBranch: "main"},
		},
		LocalDispatch: &LocalDispatchSpec{Provider: "grok", Effort: DispatchEffortLarge, MaxConcurrency: 3},
	}
	if err := template.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	if got := template.LocalDispatch.EffectiveProvider("codex"); got != "grok" {
		t.Fatalf("EffectiveProvider() = %q, want grok", got)
	}
	if got := template.LocalDispatch.EffectiveMaxConcurrency(); got != 3 {
		t.Fatalf("EffectiveMaxConcurrency() = %d, want 3", got)
	}
	var nilSpec *LocalDispatchSpec
	if got := nilSpec.EffectiveProvider("codex"); got != "codex" {
		t.Fatalf("nil EffectiveProvider() = %q, want codex", got)
	}
	if got := nilSpec.EffectiveMaxConcurrency(); got != DefaultLocalDispatchMaxConcurrency {
		t.Fatalf("nil EffectiveMaxConcurrency() = %d, want %d", got, DefaultLocalDispatchMaxConcurrency)
	}
	template.LocalDispatch = &LocalDispatchSpec{Provider: "vim"}
	if err := template.Validate(); err == nil {
		t.Fatal("Validate() accepted an invalid local_dispatch provider")
	}
	template.LocalDispatch = &LocalDispatchSpec{Effort: "huge"}
	if err := template.Validate(); err == nil {
		t.Fatal("Validate() accepted an invalid local_dispatch effort")
	}
	template.LocalDispatch = &LocalDispatchSpec{MaxConcurrency: -1}
	if err := template.Validate(); err == nil {
		t.Fatal("Validate() accepted a negative local_dispatch max_concurrency")
	}
}
