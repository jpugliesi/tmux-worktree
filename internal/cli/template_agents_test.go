package cli_test

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jpugliesi/tmux-worktree/internal/cli"
	"github.com/jpugliesi/tmux-worktree/internal/store"
)

func TestWorkspacesCreateRegistersTheDeclaredAgentSessions(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is not installed")
	}

	root := t.TempDir()
	source := filepath.Join(root, "source")
	initGitRepository(t, source)
	configDir := filepath.Join(root, "config")
	if err := os.MkdirAll(filepath.Join(configDir, "templates"), 0o755); err != nil {
		t.Fatal(err)
	}
	template := fmt.Sprintf(`version: 1
name: example
repositories:
  - name: app
    clone:
      url: %s
agents:
  - label: review
    provider: command
    start: [sleep, "60"]
`, source)
	if err := os.WriteFile(filepath.Join(configDir, "templates", "example.yaml"), []byte(template), 0o644); err != nil {
		t.Fatal(err)
	}
	socket := fmt.Sprintf("twt-test-%d", time.Now().UnixNano())
	t.Cleanup(func() { _ = exec.Command("tmux", "-L", socket, "kill-server").Run() })
	options := cli.Options{
		ConfigDir: configDir, StateDir: filepath.Join(root, "state"),
		DataDir: filepath.Join(root, "data"), TmuxSocket: socket,
	}

	validated := executeWithOptions(t, options, nil, "templates", "validate", "example")
	if !strings.Contains(validated, "example") {
		t.Fatalf("templates validate output = %q", validated)
	}
	executeWithOptions(t, options, nil, "workspaces", "create", "fix-auth", "--template", "example", "--no-open")
	workspace, err := store.NewWorkspaceStore(options.StateDir).Find("fix-auth")
	if err != nil {
		t.Fatal(err)
	}

	listJSON := executeWithOptions(t, options, nil, "agents", "list", "--workspace", workspace.ID, "--output", "json")
	var listed struct {
		Agents []struct {
			ID           string `json:"id"`
			Label        string `json:"label"`
			Provider     string `json:"provider"`
			Status       string `json:"status"`
			Capabilities struct {
				CanResume bool `json:"canResume"`
				CanSend   bool `json:"canSend"`
			} `json:"capabilities"`
		} `json:"agents"`
	}
	if err := json.Unmarshal([]byte(listJSON), &listed); err != nil {
		t.Fatalf("decode agents list JSON: %v\n%s", err, listJSON)
	}
	if len(listed.Agents) != 1 {
		t.Fatalf("agents list JSON = %s", listJSON)
	}
	declared := listed.Agents[0]
	if declared.Label != "review" || declared.Provider != "command" || declared.Status != "live" {
		t.Fatalf("declared Agent Session = %+v in %s", declared, listJSON)
	}
	if !declared.Capabilities.CanResume || !declared.Capabilities.CanSend {
		t.Fatalf("declared Agent Session = %+v in %s", declared, listJSON)
	}
	windows := runCommand(t, root, "tmux", "-L", socket, "-f", "/dev/null", "list-windows", "-t", workspace.TmuxSession, "-F", "#{window_name}")
	count := 0
	for _, name := range strings.Fields(windows) {
		if name == "review" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("tmux windows named review = %d in %q, want 1", count, windows)
	}

	// A setup retry replays the agent step without a second registration.
	executeWithOptions(t, options, nil, "workspaces", "setup", "retry", workspace.ID)
	sessions, err := store.NewAgentStore(options.StateDir).List(workspace.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 1 || sessions[0].ID != declared.ID {
		t.Fatalf("Agent Sessions after setup retry = %+v", sessions)
	}
}
