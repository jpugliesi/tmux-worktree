package cli

import (
	"testing"

	"github.com/spf13/cobra"
)

// TestHelpContentCoversEveryCommand checks the two help invariants: every
// help entry names a command that exists, and every command below the root
// has a help entry. A rename that forgets the help map fails here.
func TestHelpContentCoversEveryCommand(t *testing.T) {
	root := New(Options{ConfigDir: t.TempDir(), StateDir: t.TempDir(), DataDir: t.TempDir()})
	paths := map[string]bool{}
	var walk func(*cobra.Command)
	walk = func(command *cobra.Command) {
		if skipSchemaCommand(command) {
			return
		}
		paths[command.CommandPath()] = true
		for _, child := range command.Commands() {
			walk(child)
		}
	}
	walk(root)

	for key := range commandHelp {
		if !paths[key] {
			t.Errorf("help entry %q does not match a command", key)
		}
	}
	for path := range paths {
		if path == root.CommandPath() {
			continue
		}
		if _, ok := commandHelp[path]; !ok {
			t.Errorf("command %q has no help entry", path)
		}
	}
}
