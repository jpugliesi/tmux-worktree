package cli

import (
	"encoding/json"
	"sort"

	"github.com/jpugliesi/tmux-worktree/internal/clierr"
	"github.com/jpugliesi/tmux-worktree/internal/version"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

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

// argumentsAnnotation holds the JSON positional argument schema of one
// command. Each command declares its own arguments next to its Use value.
const argumentsAnnotation = "twt2.arguments"

// enumAnnotation stores the closed value set of one flag. setFlagEnum records
// it next to the flag definition, and the schema command reads it back.
const enumAnnotation = "twt2.enum"

// setFlagEnum declares the closed value set of one flag. The schema command
// reports the values, and shell completion offers them.
func setFlagEnum(command *cobra.Command, name string, values ...string) {
	flag := command.Flags().Lookup(name)
	if flag == nil {
		flag = command.PersistentFlags().Lookup(name)
	}
	if flag == nil {
		return
	}
	if flag.Annotations == nil {
		flag.Annotations = map[string][]string{}
	}
	flag.Annotations[enumAnnotation] = values
	_ = command.RegisterFlagCompletionFunc(name, fixedCompletion(values...))
}

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

// setAgentIDArgument declares the AGENT_ID argument of an Agent Session
// command.
func setAgentIDArgument(command *cobra.Command) {
	setArguments(command, requiredArgument("agent_id"))
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
					current.NonInheritedFlags().VisitAll(func(flag *pflag.Flag) { schema.Flags = append(schema.Flags, schemaForFlag(flag)) })
					current.InheritedFlags().VisitAll(func(flag *pflag.Flag) { schema.Flags = append(schema.Flags, schemaForFlag(flag)) })
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
				ApplyOperations: applyOperationSchemas(),
				ErrorCodes:      clierr.Codes(),
				ExitCodes:       map[string]string{"0": "success", "1": "internal", "2": "invalid_usage", "3": "precondition"},
			})
		},
	}
}

func schemaForFlag(flag *pflag.Flag) flagSchema {
	required := false
	if annotation := flag.Annotations[cobra.BashCompOneRequiredFlag]; len(annotation) > 0 && annotation[0] == "true" {
		required = true
	}
	return flagSchema{Name: flag.Name, Type: flag.Value.Type(), Default: flag.DefValue, Description: flag.Usage, Required: required, Enum: flag.Annotations[enumAnnotation]}
}
