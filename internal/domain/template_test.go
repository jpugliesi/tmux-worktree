package domain

import (
	"fmt"
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

func TestTemplateValidatesCursorCloudDefaults(t *testing.T) {
	template := Template{
		Version: TemplateVersion,
		Name:    "product",
		Repositories: []RepositorySpec{
			{Name: "api", Clone: CloneSpec{URL: "https://github.com/acme/api.git"}, DefaultBranch: "main"},
			{Name: "web", Clone: CloneSpec{URL: "https://github.com/acme/web.git"}, DefaultBranch: "trunk"},
		},
		CursorCloud: &CursorCloudSpec{
			Model:          "composer-2.5",
			Effort:         CursorCloudEffortLarge,
			MaxConcurrency: 4,
			Repositories: []CursorCloudRepositorySpec{
				{Name: "api"},
				{Name: "web", URL: "https://github.com/acme/web-cloud.git", StartingRef: "release"},
			},
		},
	}
	if err := template.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	if got := template.CursorCloud.EffectiveEffort(); got != CursorCloudEffortLarge {
		t.Fatalf("EffectiveEffort() = %q, want %q", got, CursorCloudEffortLarge)
	}
	if got := template.CursorCloud.EffectiveMaxConcurrency(); got != 4 {
		t.Fatalf("EffectiveMaxConcurrency() = %d, want 4", got)
	}
}

func TestTemplateValidatesLocalDispatchDefaults(t *testing.T) {
	template := Template{
		Version: TemplateVersion,
		Name:    "product",
		Repositories: []RepositorySpec{
			{Name: "api", Clone: CloneSpec{URL: "https://github.com/acme/api.git"}, DefaultBranch: "main"},
		},
		LocalDispatch: &LocalDispatchSpec{Provider: "grok", Effort: CursorCloudEffortLarge, MaxConcurrency: 3},
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

func TestTemplateRejectsInvalidCursorCloudConfiguration(t *testing.T) {
	base := Template{
		Version: TemplateVersion,
		Name:    "product",
		Repositories: []RepositorySpec{
			{Name: "api", Clone: CloneSpec{URL: "https://github.com/acme/api.git"}},
		},
	}
	tests := []struct {
		name   string
		config CursorCloudSpec
	}{
		{name: "effort", config: CursorCloudSpec{Effort: "huge"}},
		{name: "negative concurrency", config: CursorCloudSpec{MaxConcurrency: -1}},
		{name: "missing repository", config: CursorCloudSpec{Repositories: []CursorCloudRepositorySpec{{Name: "web"}}}},
		{name: "duplicate repository", config: CursorCloudSpec{Repositories: []CursorCloudRepositorySpec{{Name: "api"}, {Name: "api"}}}},
		{name: "empty repository name", config: CursorCloudSpec{Repositories: []CursorCloudRepositorySpec{{Name: ""}}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			template := base
			template.CursorCloud = &test.config
			if err := template.Validate(); err == nil {
				t.Fatal("Validate() succeeded")
			}
		})
	}
}

func TestTemplateRejectsAnInvalidDefaultCursorCloudRepositorySelection(t *testing.T) {
	empty := Template{Version: TemplateVersion, Name: "empty", CursorCloud: &CursorCloudSpec{}}
	if err := empty.Validate(); err == nil || !strings.Contains(err.Error(), "at least one repository") {
		t.Fatalf("empty Cursor Cloud repository selection error = %v", err)
	}

	many := Template{Version: TemplateVersion, Name: "many", CursorCloud: &CursorCloudSpec{}}
	for index := 0; index < CursorCloudRepositoryLimit+1; index++ {
		many.Repositories = append(many.Repositories, RepositorySpec{
			Name: fmt.Sprintf("repo-%d", index), Clone: CloneSpec{URL: fmt.Sprintf("https://example.com/repo-%d.git", index)},
		})
	}
	if err := many.Validate(); err == nil || !strings.Contains(err.Error(), "at most") {
		t.Fatalf("large Cursor Cloud repository selection error = %v", err)
	}
}
