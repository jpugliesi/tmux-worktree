package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

type usageError struct {
	message     string
	helpCommand string
}

func (e usageError) Error() string { return e.message }

func invalidUsage(command *cobra.Command, format string, values ...any) error {
	return usageError{
		message:     fmt.Sprintf(format, values...),
		helpCommand: command.CommandPath() + " --help",
	}
}

// groupCommand configures a command that only holds subcommands. With no
// arguments the command shows its help. An unknown subcommand causes an
// invalid_usage error instead of a silent, successful help screen.
func groupCommand(command *cobra.Command) *cobra.Command {
	if command.SuggestionsMinimumDistance <= 0 {
		command.SuggestionsMinimumDistance = 2
	}
	command.RunE = func(command *cobra.Command, args []string) error {
		if len(args) == 0 {
			return command.Help()
		}
		if suggestions := command.SuggestionsFor(args[0]); len(suggestions) > 0 {
			return invalidUsage(command, "twt2 does not know the command %q; did you mean %q?", args[0], suggestions[0])
		}
		return invalidUsage(command, "twt2 does not know the command %q", args[0])
	}
	return command
}

func exactArgs(names ...string) cobra.PositionalArgs {
	return func(command *cobra.Command, args []string) error {
		if len(args) < len(names) {
			return invalidUsage(command, "missing required argument %s", names[len(args)])
		}
		if len(args) > len(names) {
			return invalidUsage(command, "unexpected argument %q; expected %s", args[len(names)], strings.Join(names, " "))
		}
		return nil
	}
}

func noArgs(command *cobra.Command, args []string) error {
	if len(args) > 0 {
		return invalidUsage(command, "unexpected argument %q; this command accepts no arguments", args[0])
	}
	return nil
}

func optionalArg(name string) cobra.PositionalArgs {
	return func(command *cobra.Command, args []string) error {
		if len(args) > 1 {
			return invalidUsage(command, "unexpected argument %q; expected [%s]", args[1], name)
		}
		return nil
	}
}
