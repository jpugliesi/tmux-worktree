package cli_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jpugliesi/tmux-worktree/internal/cli"
	"github.com/jpugliesi/tmux-worktree/internal/clierr"
	"github.com/jpugliesi/tmux-worktree/internal/store"
	"github.com/spf13/cobra"
)

func TestCreateSchemaMarksNameOptional(t *testing.T) {
	output, err := execute(t, t.TempDir(), "schema")
	if err != nil {
		t.Fatal(err)
	}
	var schema struct {
		Commands []struct {
			Path      string `json:"path"`
			Arguments []struct {
				Name      string `json:"name"`
				Required  bool   `json:"required"`
				Condition string `json:"condition"`
			} `json:"arguments"`
		} `json:"commands"`
	}
	if err := json.Unmarshal([]byte(output), &schema); err != nil {
		t.Fatalf("decode schema: %v", err)
	}
	found := map[string]bool{}
	for _, command := range schema.Commands {
		if command.Path != "twt create" && command.Path != "twt workspaces create" && command.Path != "twt projects create" {
			continue
		}
		found[command.Path] = true
		if len(command.Arguments) != 1 || command.Arguments[0].Name != "name" || command.Arguments[0].Required {
			t.Fatalf("%s schema arguments = %+v", command.Path, command.Arguments)
		}
		if !strings.Contains(command.Arguments[0].Condition, "prompt") {
			t.Fatalf("%s schema name condition = %q", command.Path, command.Arguments[0].Condition)
		}
	}
	if !found["twt create"] || !found["twt workspaces create"] || !found["twt projects create"] {
		t.Fatalf("create schema coverage = %v", found)
	}
}

func TestCreateWithoutNameNeedsATerminal(t *testing.T) {
	options := createNameTestOptions(t)
	for _, args := range [][]string{{"create"}, {"workspaces", "create"}} {
		_, _, err := executeCollectingInput(t, options, nil, args...)
		if err == nil || clierr.CodeOf(err) != clierr.InvalidUsage || clierr.ExitCode(err) != 2 {
			t.Fatalf("%v without NAME = %v (code %q, exit %d)", args, err, clierr.CodeOf(err), clierr.ExitCode(err))
		}
		if !strings.Contains(err.Error(), "missing Workspace name") {
			t.Fatalf("%v without NAME = %v", args, err)
		}
	}
}

func TestCreateWithoutNameRefusesJSON(t *testing.T) {
	options := createNameTestOptions(t)
	writeCreateNameTemplate(t, options.ConfigDir)
	for _, args := range [][]string{
		{"create", "--output", "json"},
		{"workspaces", "create", "--output", "json"},
	} {
		_, stderr, err := executeCollectingInput(t, options, strings.NewReader("ignored-name\n"), args...)
		if err == nil || clierr.CodeOf(err) != clierr.InvalidUsage {
			t.Fatalf("%v without NAME = %v", args, err)
		}
		if !strings.Contains(err.Error(), "missing Workspace name") {
			t.Fatalf("%v without NAME = %v", args, err)
		}
		if strings.Contains(stderr, "Workspace name:") {
			t.Fatalf("%v prompted on JSON output: %q", args, stderr)
		}
		if _, findErr := store.NewWorkspaceStore(options.StateDir).Find("ignored-name"); findErr == nil {
			t.Fatalf("%v created a Workspace from JSON stdin", args)
		}
	}
}

func TestCreateWithoutNameUsesTheJSONDefault(t *testing.T) {
	options := createNameTestOptions(t)
	writeCreateNameTemplate(t, options.ConfigDir)
	_, stderr, err := executeRaw(t, options, "create")
	if err == nil || clierr.CodeOf(err) != clierr.InvalidUsage {
		t.Fatalf("create without NAME and without a terminal = %v", err)
	}
	if !strings.Contains(err.Error(), "missing Workspace name") {
		t.Fatalf("create without NAME = %v", err)
	}
	if strings.Contains(stderr, "Workspace name:") {
		t.Fatalf("create prompted when output defaulted to json: %q", stderr)
	}
}

func TestCreatePromptsForANameInATerminal(t *testing.T) {
	options := createNameTestOptions(t)
	writeCreateNameTemplate(t, options.ConfigDir)
	stdout, stderr, err := executeCollectingInput(t, options, strings.NewReader("prompted-workspace\n"),
		"create", "--dry-run", "--no-open")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(stderr, "Template: example (only template)\nWorkspace name: ") {
		t.Fatalf("create prompt = %q", stderr)
	}
	if !strings.Contains(stdout, "workspaces.create: valid") {
		t.Fatalf("create dry-run output = %q", stdout)
	}
	if _, findErr := store.NewWorkspaceStore(options.StateDir).Find("prompted-workspace"); findErr == nil {
		t.Fatal("create dry-run created a Workspace")
	}

	_, _, err = executeCollectingInput(t, options, strings.NewReader("\n"), "workspaces", "create", "--dry-run")
	if err == nil || !strings.Contains(err.Error(), "no Workspace name was given") {
		t.Fatalf("empty create prompt = %v", err)
	}

	_, _, err = executeCollectingInput(t, options, strings.NewReader("../evil\n"), "create", "--dry-run")
	if err == nil || !strings.Contains(err.Error(), "invalid Workspace name") {
		t.Fatalf("prompted invalid name = %v", err)
	}
}

func createNameTestOptions(t *testing.T) cli.Options {
	t.Helper()
	root := t.TempDir()
	return cli.Options{
		ConfigDir: filepath.Join(root, "config"),
		StateDir:  filepath.Join(root, "state"),
		DataDir:   filepath.Join(root, "data"),
	}
}

func writeCreateNameTemplate(t *testing.T, configDir string) {
	t.Helper()
	writeNamedTemplate(t, configDir, "example")
}

func writeNamedTemplate(t *testing.T, configDir, name string) {
	t.Helper()
	dir := filepath.Join(configDir, "templates")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	content := "version: 1\nname: " + name + "\nrepositories:\n  - name: app\n    clone:\n      url: https://example.com/app.git\n"
	if err := os.WriteFile(filepath.Join(dir, name+".yaml"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestCreatePicksATemplateInATerminalWhenSeveralExist(t *testing.T) {
	options := createNameTestOptions(t)
	writeCreateNameTemplate(t, options.ConfigDir)
	writeNamedTemplate(t, options.ConfigDir, "zeta")
	if err := store.SaveLastTemplate(options.StateDir, "example"); err != nil {
		t.Fatal(err)
	}
	options.PickTemplate = func(_ *cobra.Command, lines []string) (int, error) {
		for index, line := range lines {
			if line == "zeta" {
				return index, nil
			}
		}
		t.Fatalf("picker lines = %v", lines)
		return 0, nil
	}
	stdout, stderr, err := executeCollectingInput(t, options, strings.NewReader("prompted-workspace\n"),
		"create", "--dry-run", "--no-open")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stderr, "Template: zeta (selected)") {
		t.Fatalf("create stderr = %q", stderr)
	}
	if strings.Contains(stderr, "last used") {
		t.Fatalf("interactive create used last-used: %q", stderr)
	}
	if !strings.Contains(stdout, "workspaces.create: valid") {
		t.Fatalf("create dry-run output = %q", stdout)
	}
}
