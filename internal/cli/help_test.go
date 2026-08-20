package cli_test

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jpugliesi/tmux-worktree/internal/cli"
	"github.com/spf13/cobra"
)

func TestMissingTemplateNameGivesActionableHelp(t *testing.T) {
	var stdout, stderr bytes.Buffer
	command := cli.New(cli.Options{
		ConfigDir: filepath.Join(t.TempDir(), "config"),
		StateDir:  filepath.Join(t.TempDir(), "state"),
		DataDir:   filepath.Join(t.TempDir(), "data"),
		Stdout:    &stdout,
		Stderr:    &stderr,
	})
	command.SetArgs([]string{"templates", "create"})
	executed, err := command.ExecuteC()
	if err == nil {
		t.Fatal("templates create without NAME did not fail")
	}
	if writeErr := cli.WriteError(executed, &stderr, err); writeErr != nil {
		t.Fatal(writeErr)
	}
	want := "twt2: missing required argument NAME\nRun 'twt2 templates create --help' for usage and examples.\n"
	if stderr.String() != want {
		t.Fatalf("stderr:\n%s\nwant:\n%s", stderr.String(), want)
	}
}

func TestJSONUsageErrorHasAStableCodeAndHelpCommand(t *testing.T) {
	var stdout, stderr bytes.Buffer
	command := cli.New(cli.Options{ConfigDir: t.TempDir(), StateDir: t.TempDir(), DataDir: t.TempDir(), Stdout: &stdout, Stderr: &stderr})
	command.SetArgs([]string{"templates", "create", "--output", "json"})
	executed, err := command.ExecuteC()
	if err == nil {
		t.Fatal("templates create without NAME did not fail")
	}
	if writeErr := cli.WriteError(executed, &stderr, err); writeErr != nil {
		t.Fatal(writeErr)
	}
	var result struct {
		Error struct {
			Code        string `json:"code"`
			Message     string `json:"message"`
			HelpCommand string `json:"helpCommand"`
		} `json:"error"`
	}
	if err := json.Unmarshal(stderr.Bytes(), &result); err != nil {
		t.Fatalf("decode JSON error: %v\n%s", err, stderr.String())
	}
	if result.Error.Code != "invalid_usage" || result.Error.Message != "missing required argument NAME" || result.Error.HelpCommand != "twt2 templates create --help" {
		t.Fatalf("JSON usage error = %+v", result.Error)
	}
}

func TestEveryRunnableCommandHasAnExample(t *testing.T) {
	root := cli.New(cli.Options{ConfigDir: t.TempDir(), StateDir: t.TempDir(), DataDir: t.TempDir()})
	var missing []string
	var walk func(*cobra.Command)
	walk = func(command *cobra.Command) {
		if command.Runnable() && command.Example == "" {
			missing = append(missing, command.CommandPath())
		}
		for _, child := range command.Commands() {
			walk(child)
		}
	}
	walk(root)
	if len(missing) > 0 {
		t.Fatalf("runnable commands without examples: %v", missing)
	}
}

func TestRootHelpGroupsCommandsAndShowsAWorkingExample(t *testing.T) {
	output, err := execute(t, t.TempDir(), "--help")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"Workflows:",
		"Inspect and maintain:",
		"Automation:",
		"twt2 projects create fix-auth --template everysphere",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("root help does not contain %q:\n%s", want, output)
		}
	}
}

func TestTemplateCreateHelpExplainsTheNameAndShowsNextStep(t *testing.T) {
	output, err := execute(t, t.TempDir(), "templates", "create", "--help")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"NAME is the reusable template name",
		"twt2 templates create everysphere",
		"twt2 templates repos add everysphere",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("templates create help does not contain %q:\n%s", want, output)
		}
	}
}

func TestQuickCreateHelpExplainsTheProjectChange(t *testing.T) {
	output, err := execute(t, t.TempDir(), "create", "--help")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"latest saved version of the current Project Template",
		"switches the calling client",
		"archives the old Project",
		"twt2 create fix-auth",
		"twt2 projects create",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("quick create help does not contain %q:\n%s", want, output)
		}
	}
}
