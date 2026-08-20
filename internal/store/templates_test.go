package store_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jpugliesi/tmux-worktree/internal/store"
)

func TestTemplateStoreKeepsPoolDepthThroughAYAMLRoundTrip(t *testing.T) {
	t.Parallel()

	configDir := t.TempDir()
	directory := filepath.Join(configDir, "templates")
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	content := `version: 1
name: example
pool_depth: 2
repositories:
  - name: app
    clone: {url: https://example.com/app.git}
`
	if err := os.WriteFile(filepath.Join(directory, "example.yaml"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	templates := store.NewTemplateStore(configDir)
	loaded, err := templates.Load("example")
	if err != nil {
		t.Fatal(err)
	}
	if loaded.PoolDepth != 2 || loaded.EffectivePoolDepth() != 2 {
		t.Fatalf("loaded pool depth = %d", loaded.PoolDepth)
	}

	if err := templates.Save(loaded); err != nil {
		t.Fatal(err)
	}
	encoded, err := os.ReadFile(filepath.Join(directory, "example.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(encoded), "pool_depth: 2") {
		t.Fatalf("saved Project Template does not keep pool_depth:\n%s", encoded)
	}
	again, err := templates.Load("example")
	if err != nil {
		t.Fatal(err)
	}
	if again.PoolDepth != 2 {
		t.Fatalf("reloaded pool depth = %d", again.PoolDepth)
	}
}

func TestTemplateStoreKeepsDeclaredAgentSessionsThroughAYAMLRoundTrip(t *testing.T) {
	t.Parallel()

	configDir := t.TempDir()
	directory := filepath.Join(configDir, "templates")
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	content := `version: 1
name: example
repositories:
  - name: app
    clone: {url: https://example.com/app.git}
agents:
  - label: review
    provider: codex
    start: [codex]
`
	if err := os.WriteFile(filepath.Join(directory, "example.yaml"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	templates := store.NewTemplateStore(configDir)
	loaded, err := templates.Load("example")
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.Agents) != 1 {
		t.Fatalf("loaded Agent Sessions = %+v", loaded.Agents)
	}
	agent := loaded.Agents[0]
	if agent.Label != "review" || agent.Provider != "codex" || len(agent.Start) != 1 || agent.Start[0] != "codex" {
		t.Fatalf("loaded declared Agent Session = %+v", agent)
	}

	if err := templates.Save(loaded); err != nil {
		t.Fatal(err)
	}
	encoded, err := os.ReadFile(filepath.Join(directory, "example.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"agents:", "label: review", "provider: codex", "start:"} {
		if !strings.Contains(string(encoded), want) {
			t.Fatalf("saved Project Template does not keep %q:\n%s", want, encoded)
		}
	}
	again, err := templates.Load("example")
	if err != nil {
		t.Fatal(err)
	}
	if len(again.Agents) != 1 || again.Agents[0].Label != "review" {
		t.Fatalf("reloaded declared Agent Sessions = %+v", again.Agents)
	}
}

func TestTemplateStoreRejectsInvalidYAML(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		yaml    string
		message string
	}{
		{
			name:    "unknown field",
			yaml:    "version: 1\nname: example\nrepositories: []\nunknown: true\n",
			message: "field unknown not found",
		},
		{
			name:    "multiple documents",
			yaml:    "version: 1\nname: example\nrepositories: []\n---\nversion: 1\nname: other\nrepositories: []\n",
			message: "multiple YAML documents",
		},
		{
			name:    "unsupported version",
			yaml:    "version: 2\nname: example\nrepositories: []\n",
			message: "unsupported template version 2",
		},
		{
			name: "duplicate repositories",
			yaml: `version: 1
name: example
repositories:
  - name: app
    clone: {url: https://example.com/app.git}
  - name: app
    clone: {url: https://example.com/app.git}
`,
			message: "declared more than once",
		},
		{
			name: "missing clone URL",
			yaml: `version: 1
name: example
repositories:
  - name: app
    clone: {}
`,
			message: "has no clone URL",
		},
		{
			name: "negative depth",
			yaml: `version: 1
name: example
repositories:
  - name: app
    clone: {url: https://example.com/app.git, depth: -1}
`,
			message: "negative clone depth",
		},
		{
			name: "origin extra remote",
			yaml: `version: 1
name: example
repositories:
  - name: app
    clone: {url: https://example.com/app.git}
    remotes: {origin: https://mirror.example.com/app.git}
`,
			message: "cannot declare origin",
		},
		{
			name: "empty repository initialization",
			yaml: `version: 1
name: example
repositories:
  - name: app
    clone: {url: https://example.com/app.git}
    initialize: {command: []}
`,
			message: "command must not be empty",
		},
		{
			name: "blank repository initialization command",
			yaml: `version: 1
name: example
repositories:
  - name: app
    clone: {url: https://example.com/app.git}
    initialize: {command: [""]}
`,
			message: "command must not be empty",
		},
		{
			name:    "negative pool depth",
			yaml:    "version: 1\nname: example\nrepositories: []\npool_depth: -1\n",
			message: "pool_depth -1 is negative",
		},
		{
			name: "unknown agent field",
			yaml: `version: 1
name: example
repositories: []
agents:
  - label: review
    provider: codex
    start: [codex]
    unknown: true
`,
			message: "field unknown not found",
		},
		{
			name: "declared agent without a start command",
			yaml: `version: 1
name: example
repositories: []
agents:
  - label: review
    provider: codex
`,
			message: "has no start command",
		},
		{
			name: "declared agent with an unsupported provider",
			yaml: `version: 1
name: example
repositories: []
agents:
  - label: review
    provider: robot
    start: [robot]
`,
			message: "unsupported provider",
		},
		{
			name: "template initialization without cwd",
			yaml: `version: 1
name: example
repositories: []
initialize: {command: [./init.sh]}
`,
			message: "working_directory must be set",
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			configDir := t.TempDir()
			directory := filepath.Join(configDir, "templates")
			if err := os.MkdirAll(directory, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(directory, "example.yaml"), []byte(test.yaml), 0o644); err != nil {
				t.Fatal(err)
			}

			_, err := store.NewTemplateStore(configDir).Load("example")
			if err == nil || !strings.Contains(err.Error(), test.message) {
				t.Fatalf("Load error = %v, want text %q", err, test.message)
			}
		})
	}
}
