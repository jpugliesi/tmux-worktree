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
	command.SetArgs(forceTextOutput([]string{"templates", "create"}))
	executed, err := command.ExecuteC()
	if err == nil {
		t.Fatal("templates create without NAME did not fail")
	}
	if writeErr := cli.WriteError(executed, &stderr, err); writeErr != nil {
		t.Fatal(writeErr)
	}
	want := "twt: missing required argument NAME\nRun 'twt templates create --help' for usage and examples.\n"
	if stderr.String() != want {
		t.Fatalf("stderr:\n%s\nwant:\n%s", stderr.String(), want)
	}
}

func TestJSONUsageErrorHasAStableCodeAndHelpCommand(t *testing.T) {
	var stdout, stderr bytes.Buffer
	command := cli.New(cli.Options{ConfigDir: t.TempDir(), StateDir: t.TempDir(), DataDir: t.TempDir(), Stdout: &stdout, Stderr: &stderr})
	command.SetArgs(forceTextOutput([]string{"templates", "create", "--output", "json"}))
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
	if result.Error.Code != "invalid_usage" || result.Error.Message != "missing required argument NAME" || result.Error.HelpCommand != "twt templates create --help" {
		t.Fatalf("JSON usage error = %+v", result.Error)
	}
}

func TestGroupCommandsRejectUnknownSubcommands(t *testing.T) {
	root := cli.New(cli.Options{ConfigDir: t.TempDir(), StateDir: t.TempDir(), DataDir: t.TempDir()})
	var groups [][]string
	var walk func(command *cobra.Command, path []string)
	walk = func(command *cobra.Command, path []string) {
		for _, child := range command.Commands() {
			childPath := append(append([]string(nil), path...), child.Name())
			if child.HasSubCommands() {
				groups = append(groups, childPath)
				walk(child, childPath)
			}
		}
	}
	walk(root, nil)
	if len(groups) < 9 {
		t.Fatalf("expected at least 9 group commands, found %d: %v", len(groups), groups)
	}
	newCommand := func(stdout, stderr *bytes.Buffer) *cobra.Command {
		return cli.New(cli.Options{ConfigDir: t.TempDir(), StateDir: t.TempDir(), DataDir: t.TempDir(), Stdout: stdout, Stderr: stderr})
	}
	for _, group := range groups {
		var helpStdout, helpStderr bytes.Buffer
		bare := newCommand(&helpStdout, &helpStderr)
		bare.SetArgs(forceTextOutput(group))
		if err := bare.Execute(); err != nil {
			t.Fatalf("%v without a subcommand failed: %v", group, err)
		}
		if !strings.Contains(helpStdout.String(), "Available Commands") {
			t.Fatalf("%v without a subcommand did not show help:\n%s", group, helpStdout.String())
		}

		args := append(append([]string(nil), group...), "definitely-not-a-command")
		var stdout, stderr bytes.Buffer
		command := newCommand(&stdout, &stderr)
		command.SetArgs(forceTextOutput(args))
		if _, err := command.ExecuteC(); err == nil {
			t.Fatalf("%v did not fail", args)
		}

		jsonArgs := append(append([]string(nil), args...), "--output", "json")
		var jsonStdout, jsonStderr bytes.Buffer
		jsonCommand := newCommand(&jsonStdout, &jsonStderr)
		jsonCommand.SetArgs(forceTextOutput(jsonArgs))
		executed, err := jsonCommand.ExecuteC()
		if err == nil {
			t.Fatalf("%v did not fail", jsonArgs)
		}
		if writeErr := cli.WriteError(executed, &jsonStderr, err); writeErr != nil {
			t.Fatal(writeErr)
		}
		var result struct {
			Error struct {
				Code        string `json:"code"`
				Message     string `json:"message"`
				HelpCommand string `json:"helpCommand"`
			} `json:"error"`
		}
		if decodeErr := json.Unmarshal(jsonStderr.Bytes(), &result); decodeErr != nil {
			t.Fatalf("decode JSON error for %v: %v\n%s", jsonArgs, decodeErr, jsonStderr.String())
		}
		wantHelp := "twt " + strings.Join(group, " ") + " --help"
		if result.Error.Code != "invalid_usage" || result.Error.HelpCommand != wantHelp {
			t.Fatalf("JSON error for %v = %+v, want code invalid_usage and helpCommand %q", jsonArgs, result.Error, wantHelp)
		}
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
		"twt tickets ls --ready",
		"twt tickets claim fix-auth-tokens",
		"twt next fix-auth-tokens",
		"twt tickets close fix-auth-tokens",
		"twt done",
		"Restore active Workspace sessions after tmux restarts.",
		"twt workspaces open --all-active --dry-run",
		"\n  twt workspaces open --all-active\n",
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
		"twt templates create everysphere",
		"twt templates repos add everysphere",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("templates create help does not contain %q:\n%s", want, output)
		}
	}
}

func TestTicketsStartHelpExplainsThePicker(t *testing.T) {
	output, err := execute(t, t.TempDir(), "tickets", "start", "--help")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"interactive Ticket picker",
		"twt tickets show",
		"twt tickets start",
		"keeps the current Workspace active",
		"twt next TICKET",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("tickets start help does not contain %q:\n%s", want, output)
		}
	}
}

func TestOpenHelpExplainsAllActive(t *testing.T) {
	output, err := execute(t, t.TempDir(), "workspaces", "open", "--help")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"--all-active",
		"unowned tmux session",
		"twt workspaces open --all-active",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("workspaces open help does not contain %q:\n%s", want, output)
		}
	}
}

func TestCreateHelpExplainsTheNamePrompt(t *testing.T) {
	for _, args := range [][]string{{"create", "--help"}, {"workspaces", "create", "--help"}} {
		output, err := execute(t, t.TempDir(), args...)
		if err != nil {
			t.Fatal(err)
		}
		for _, want := range []string{
			"asks for a Workspace name",
			"--output json still require NAME",
		} {
			if !strings.Contains(output, want) {
				t.Fatalf("%v help does not contain %q:\n%s", args, want, output)
			}
		}
	}
}

func TestNextHelpExplainsTheWorkspaceChange(t *testing.T) {
	output, err := execute(t, t.TempDir(), "next", "--help")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"latest saved version of the current Workspace Template",
		"switches the calling client",
		"archives the old Workspace",
		"interactive Ticket picker",
		"twt next fix-auth",
		"twt create",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("next help does not contain %q:\n%s", want, output)
		}
	}
}
