package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"

	"github.com/jpugliesi/tmux-worktree/internal/clierr"
	"github.com/jpugliesi/tmux-worktree/internal/version"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

type mutationOutput struct {
	SchemaVersion int    `json:"schemaVersion"`
	Operation     string `json:"operation"`
	Status        string `json:"status"`
	ID            string `json:"id,omitempty"`
	Name          string `json:"name,omitempty"`
}

type commandSchema struct {
	Path        string           `json:"path"`
	Use         string           `json:"use"`
	Description string           `json:"description"`
	Arguments   []argumentSchema `json:"arguments"`
	Flags       []flagSchema     `json:"flags"`
}

type argumentSchema struct {
	Name      string `json:"name"`
	Type      string `json:"type"`
	Required  bool   `json:"required"`
	Variadic  bool   `json:"variadic"`
	Condition string `json:"condition,omitempty"`
}

type flagSchema struct {
	Name        string   `json:"name"`
	Type        string   `json:"type"`
	Default     string   `json:"default,omitempty"`
	Description string   `json:"description"`
	Required    bool     `json:"required"`
	Enum        []string `json:"enum,omitempty"`
}

type schemaOutput struct {
	SchemaVersion   int                    `json:"schemaVersion"`
	Version         string                 `json:"version"`
	Commands        []commandSchema        `json:"commands"`
	ApplyOperations []applyOperationSchema `json:"applyOperations"`
	ErrorCodes      []string               `json:"errorCodes"`
	ExitCodes       map[string]string      `json:"exitCodes"`
}

type applyOperationSchema struct {
	Operation string               `json:"operation"`
	Payload   string               `json:"payload"`
	Fields    []requestFieldSchema `json:"fields"`
}

type requestFieldSchema struct {
	Path      string   `json:"path"`
	Type      string   `json:"type"`
	Required  bool     `json:"required"`
	Enum      []string `json:"enum,omitempty"`
	Condition string   `json:"condition,omitempty"`
}

type commandErrorOutput struct {
	SchemaVersion int          `json:"schemaVersion"`
	Error         commandError `json:"error"`
}

type commandError struct {
	Code        string `json:"code"`
	Message     string `json:"message"`
	Hint        string `json:"hint,omitempty"`
	HelpCommand string `json:"helpCommand,omitempty"`
}

func isDryRun(command *cobra.Command) bool {
	value, _ := command.Flags().GetBool("dry-run")
	return value
}

// applyLimit cuts a result list to the requested size. It returns the kept
// values, the number of results before the cut, and whether the cut removed
// results. Every list output reports totalCount and truncated.
func applyLimit[T any](values []T, limit int) ([]T, int, bool, error) {
	total := len(values)
	if limit < 0 {
		return nil, total, false, clierr.New(clierr.InvalidUsage, "--limit must be zero or greater")
	}
	if limit > 0 && total > limit {
		return values[:limit], total, true, nil
	}
	return values, total, false, nil
}

// argumentsAnnotation holds the JSON positional argument schema of one
// command. Each command declares its own arguments next to its Use value.
const argumentsAnnotation = "twt2.arguments"

// setArguments records the positional argument schema of a command. The
// schema command reads this annotation instead of a central table.
func setArguments(command *cobra.Command, args ...argumentSchema) {
	data, err := json.Marshal(args)
	if err != nil {
		return
	}
	if command.Annotations == nil {
		command.Annotations = map[string]string{}
	}
	command.Annotations[argumentsAnnotation] = string(data)
}

// argumentsForCommand reads the declared positional arguments of a command.
func argumentsForCommand(command *cobra.Command) []argumentSchema {
	value := command.Annotations[argumentsAnnotation]
	if value == "" {
		return []argumentSchema{}
	}
	var arguments []argumentSchema
	if err := json.Unmarshal([]byte(value), &arguments); err != nil || arguments == nil {
		return []argumentSchema{}
	}
	return arguments
}

// requiredArgument declares one required positional string argument.
func requiredArgument(name string) argumentSchema {
	return argumentSchema{Name: name, Type: "string", Required: true}
}

// optionalArgument declares one optional positional string argument.
func optionalArgument(name string, condition string) argumentSchema {
	return argumentSchema{Name: name, Type: "string", Required: false, Condition: condition}
}

// variadicArgument declares the trailing list of positional arguments.
func variadicArgument(name string, required bool, condition string) argumentSchema {
	return argumentSchema{Name: name, Type: "array[string]", Required: required, Variadic: true, Condition: condition}
}

// skipSchemaCommand reports whether a command is a generated help or
// completion command. These commands are not part of the twt2 contract.
func skipSchemaCommand(command *cobra.Command) bool {
	switch command.Name() {
	case "help", "completion", cobra.ShellCompRequestCmd, cobra.ShellCompNoDescRequestCmd:
		return true
	}
	return false
}

func writeMutation(command *cobra.Command, operation, status, id, name string) error {
	if WantsJSON(command) {
		return writeJSONOutput(command, mutationOutput{SchemaVersion: jsonSchemaVersion, Operation: operation, Status: status, ID: id, Name: name})
	}
	_, err := fmt.Fprintf(command.OutOrStdout(), "%s: %s\n", operation, status)
	return err
}

func WriteError(command *cobra.Command, writer io.Writer, err error) error {
	code := clierr.CodeOf(err)
	hint := clierr.HintOf(err)
	helpCommand := ""
	var usage usageError
	if errors.As(err, &usage) {
		helpCommand = usage.helpCommand
	}
	if !WantsJSON(command) {
		text := fmt.Sprintf("twt2: %v\n", err)
		if hint != "" {
			text += hint + "\n"
		}
		if helpCommand != "" {
			text += fmt.Sprintf("Run '%s' for usage and examples.\n", helpCommand)
		}
		_, writeErr := io.WriteString(writer, text)
		return writeErr
	}
	return json.NewEncoder(writer).Encode(commandErrorOutput{SchemaVersion: jsonSchemaVersion, Error: commandError{Code: string(code), Message: err.Error(), Hint: hint, HelpCommand: helpCommand}})
}

func newSchemaCommand(root *cobra.Command) *cobra.Command {
	return &cobra.Command{
		Use:   "schema",
		Short: "Show the machine-readable command schema",
		Args:  noArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			var schemas []commandSchema
			var walk func(*cobra.Command)
			walk = func(current *cobra.Command) {
				if skipSchemaCommand(current) {
					return
				}
				if current != root && current.Runnable() {
					schema := commandSchema{Path: current.CommandPath(), Use: current.Use, Description: current.Short, Arguments: argumentsForCommand(current), Flags: []flagSchema{}}
					current.NonInheritedFlags().VisitAll(func(flag *pflag.Flag) { schema.Flags = append(schema.Flags, schemaForFlag(current, flag)) })
					current.InheritedFlags().VisitAll(func(flag *pflag.Flag) { schema.Flags = append(schema.Flags, schemaForFlag(current, flag)) })
					sort.Slice(schema.Flags, func(i, j int) bool { return schema.Flags[i].Name < schema.Flags[j].Name })
					schemas = append(schemas, schema)
				}
				for _, child := range current.Commands() {
					walk(child)
				}
			}
			walk(root)
			sort.Slice(schemas, func(i, j int) bool { return schemas[i].Path < schemas[j].Path })
			return writeJSONOutput(command, schemaOutput{
				SchemaVersion:   jsonSchemaVersion,
				Version:         version.Version,
				Commands:        schemas,
				ApplyOperations: applyOperations(),
				ErrorCodes:      clierr.Codes(),
				ExitCodes:       map[string]string{"0": "success", "1": "internal", "2": "invalid_usage", "3": "precondition"},
			})
		},
	}
}

func schemaForFlag(command *cobra.Command, flag *pflag.Flag) flagSchema {
	required := false
	if annotation := flag.Annotations[cobra.BashCompOneRequiredFlag]; len(annotation) > 0 && annotation[0] == "true" {
		required = true
	}
	enums := map[string][]string{
		"output":   outputFormatNames,
		"provider": agentProviderNames,
	}
	return flagSchema{Name: flag.Name, Type: flag.Value.Type(), Default: flag.DefValue, Description: flag.Usage, Required: required, Enum: enums[flag.Name]}
}

// setAgentIDArgument declares the AGENT_ID argument of an Agent Session
// command.
func setAgentIDArgument(command *cobra.Command) {
	setArguments(command, requiredArgument("agent_id"))
}
