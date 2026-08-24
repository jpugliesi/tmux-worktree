package cli_test

import (
	"encoding/json"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/jpugliesi/tmux-worktree/internal/cli"
	"github.com/spf13/cobra"
)

// completableArguments are the positional argument names that name a twt
// resource that already exists. A command with one of them must offer shell
// completion, so a TAB press never comes back empty.
var completableArguments = map[string]bool{
	"agent_id":       true,
	"workspace":      true,
	"template":       true,
	"name":           true,
	"repo":           true,
	"ticket":         true,
	"environment_id": true,
	"session":        true,
}

// freeTextArguments are the positional arguments that name a resource that twt
// creates, or free text. They have nothing to complete, and this list records
// that decision. The key is the command path and the argument name.
var freeTextArguments = map[string]bool{
	// The Workspace, Workspace Template, and Project names of a create are new.
	"twt create name":            true,
	"twt workspaces create name": true,
	"twt templates create name":  true,
	"twt projects create name":   true,
	// The repository name and URL of a repos add are new.
	"twt templates repos add repo": true,
	"twt templates repos add url":  true,
}

// positionalArgument is the part of the twt.arguments annotation that the
// coverage test reads. Each command declares its own positional arguments next
// to its Use value.
type positionalArgument struct {
	Name string `json:"name"`
}

// declaredArguments reads the positional argument schema of one command.
func declaredArguments(t *testing.T, command *cobra.Command) []positionalArgument {
	t.Helper()
	value := command.Annotations["twt.arguments"]
	if value == "" {
		return nil
	}
	var arguments []positionalArgument
	if err := json.Unmarshal([]byte(value), &arguments); err != nil {
		t.Fatalf("%s declares an unreadable twt.arguments annotation: %v", command.CommandPath(), err)
	}
	return arguments
}

// TestEveryResourceArgumentCompletes walks the command tree and checks that
// each command with a resource reference positional has an argument completion
// function. The user reported `twt agents focus <TAB>` as silent; this test
// keeps every command of the same shape wired.
func TestEveryResourceArgumentCompletes(t *testing.T) {
	root := t.TempDir()
	command := cli.New(cli.Options{
		ConfigDir:   filepath.Join(root, "config"),
		StateDir:    filepath.Join(root, "state"),
		DataDir:     filepath.Join(root, "data"),
		TicketsHome: filepath.Join(root, "tickets"),
	})
	missing := []string{}
	unused := map[string]bool{}
	for key := range freeTextArguments {
		unused[key] = true
	}
	var walk func(*cobra.Command)
	walk = func(current *cobra.Command) {
		for _, child := range current.Commands() {
			walk(child)
		}
		switch current.Name() {
		case "help", "completion", cobra.ShellCompRequestCmd, cobra.ShellCompNoDescRequestCmd:
			return
		}
		if !current.Runnable() {
			return
		}
		wanted := []string{}
		for _, argument := range declaredArguments(t, current) {
			key := current.CommandPath() + " " + argument.Name
			if freeTextArguments[key] {
				delete(unused, key)
				continue
			}
			if completableArguments[argument.Name] {
				wanted = append(wanted, argument.Name)
			}
		}
		if len(wanted) > 0 && current.ValidArgsFunction == nil {
			missing = append(missing, current.CommandPath()+" ("+strings.Join(wanted, ", ")+")")
		}
	}
	walk(command)
	sort.Strings(missing)
	if len(missing) > 0 {
		t.Fatalf("these commands complete no argument:\n%s", strings.Join(missing, "\n"))
	}
	if len(unused) > 0 {
		keys := make([]string, 0, len(unused))
		for key := range unused {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		t.Fatalf("freeTextArguments names arguments that no command declares:\n%s", strings.Join(keys, "\n"))
	}
}
