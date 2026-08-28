package cli

import (
	"strings"

	"github.com/jpugliesi/tmux-worktree/internal/clierr"
	"github.com/spf13/cobra"
)

type usageError struct {
	err         *clierr.Error
	helpCommand string
}

func (e usageError) Error() string { return e.err.Error() }

func (e usageError) Unwrap() error { return e.err }

func invalidUsage(command *cobra.Command, format string, values ...any) error {
	return usageError{
		err:         clierr.New(clierr.InvalidUsage, format, values...),
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
			return invalidUsage(command, "twt does not know the command %q; did you mean %q?", args[0], suggestions[0])
		}
		return invalidUsage(command, "twt does not know the command %q", args[0])
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

// stdinToken is the positional that means "read standard input", the same
// convention as cat, kubectl -f, and git commit -F.
const stdinToken = "-"

func isStdinToken(value string) bool {
	return value == stdinToken
}

func stdinTokenArgument(required bool) argumentSchema {
	condition := "the literal \"-\" reads standard input"
	if required {
		return argumentSchema{Name: "-", Type: "string", Required: true, Condition: condition}
	}
	return optionalArgument("-", condition)
}

func missingStdinToken(command *cobra.Command) error {
	return invalidUsageWithHint(command, "Pass - to read standard input.",
		"missing required argument -")
}

func unexpectedStdinToken(command *cobra.Command, value string) error {
	return invalidUsageWithHint(command, "Pass - to read standard input.",
		"unexpected argument %q; expected -", value)
}

// requireStdinToken requires the single positional "-".
func requireStdinToken() cobra.PositionalArgs {
	return func(command *cobra.Command, args []string) error {
		if len(args) == 0 {
			return missingStdinToken(command)
		}
		if len(args) > 1 {
			return invalidUsage(command, "unexpected argument %q; expected -", args[1])
		}
		if !isStdinToken(args[0]) {
			return unexpectedStdinToken(command, args[0])
		}
		return nil
	}
}

// requireResourceThenStdin requires RESOURCE then "-".
func requireResourceThenStdin(resource string) cobra.PositionalArgs {
	return func(command *cobra.Command, args []string) error {
		if len(args) < 1 {
			return invalidUsage(command, "missing required argument %s", resource)
		}
		if len(args) < 2 {
			return missingStdinToken(command)
		}
		if len(args) > 2 {
			return invalidUsage(command, "unexpected argument %q; expected %s -", args[2], resource)
		}
		if !isStdinToken(args[1]) {
			return unexpectedStdinToken(command, args[1])
		}
		return nil
	}
}

// optionalResourceThenStdin accepts [RESOURCE] [-]. An absent RESOURCE uses
// the current context. A lone - reads standard input for the current resource.
func optionalResourceThenStdin(resource string) cobra.PositionalArgs {
	return func(command *cobra.Command, args []string) error {
		if len(args) > 2 {
			return invalidUsage(command, "unexpected argument %q; expected [%s] [-]", args[2], resource)
		}
		if len(args) == 2 {
			if isStdinToken(args[0]) {
				return unexpectedStdinToken(command, args[1])
			}
			if !isStdinToken(args[1]) {
				return unexpectedStdinToken(command, args[1])
			}
		}
		return nil
	}
}

// optionalStdinAfter requires RESOURCE and accepts an optional trailing "-".
func optionalStdinAfter(resource string) cobra.PositionalArgs {
	return func(command *cobra.Command, args []string) error {
		if len(args) < 1 {
			return invalidUsage(command, "missing required argument %s", resource)
		}
		if len(args) > 2 {
			return invalidUsage(command, "unexpected argument %q; expected %s [-]", args[2], resource)
		}
		if len(args) == 2 && !isStdinToken(args[1]) {
			return unexpectedStdinToken(command, args[1])
		}
		return nil
	}
}

func optionalArg(name string) cobra.PositionalArgs {
	return func(command *cobra.Command, args []string) error {
		if len(args) > 1 {
			return invalidUsage(command, "unexpected argument %q; expected [%s]", args[1], name)
		}
		return nil
	}
}

func oneOrMoreArgs(name string) cobra.PositionalArgs {
	return func(command *cobra.Command, args []string) error {
		if len(args) == 0 {
			return invalidUsage(command, "missing required argument %s", name)
		}
		return nil
	}
}
