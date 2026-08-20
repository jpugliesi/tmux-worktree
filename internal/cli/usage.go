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
