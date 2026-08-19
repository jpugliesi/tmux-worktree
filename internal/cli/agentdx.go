package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"

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
	Commands        []commandSchema        `json:"commands"`
	ApplyOperations []applyOperationSchema `json:"applyOperations"`
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
	HelpCommand string `json:"helpCommand,omitempty"`
}

func isDryRun(command *cobra.Command) bool {
	value, _ := command.Flags().GetBool("dry-run")
	return value
}

func wantsJSON(command *cobra.Command, localFormat string) bool {
	return WantsJSON(command) || localFormat == "json"
}

func applyLimit[T any](values []T, limit int) ([]T, error) {
	if limit < 0 {
		return nil, fmt.Errorf("--limit must be zero or greater")
	}
	if limit > 0 && len(values) > limit {
		return values[:limit], nil
	}
	return values, nil
}

func writeMutation(command *cobra.Command, operation, status, id, name string) error {
	if WantsJSON(command) {
		return writeJSONOutput(command, mutationOutput{SchemaVersion: jsonSchemaVersion, Operation: operation, Status: status, ID: id, Name: name})
	}
	_, err := fmt.Fprintf(command.OutOrStdout(), "%s: %s\n", operation, status)
	return err
}

func WriteError(command *cobra.Command, writer io.Writer, err error) error {
	code := "command_failed"
	helpCommand := ""
	var usage usageError
	if errors.As(err, &usage) {
		code = "invalid_usage"
		helpCommand = usage.helpCommand
	}
	if !WantsJSON(command) {
		if helpCommand != "" {
			_, writeErr := fmt.Fprintf(writer, "twt2: %v\nRun '%s' for usage and examples.\n", err, helpCommand)
			return writeErr
		}
		_, writeErr := fmt.Fprintf(writer, "twt2: %v\n", err)
		return writeErr
	}
	return json.NewEncoder(writer).Encode(commandErrorOutput{SchemaVersion: jsonSchemaVersion, Error: commandError{Code: code, Message: err.Error(), HelpCommand: helpCommand}})
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
				if current != root && current.Runnable() {
					schema := commandSchema{Path: current.CommandPath(), Use: current.Use, Description: current.Short, Arguments: argumentsForCommand(current.CommandPath()), Flags: []flagSchema{}}
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
			operations := []applyOperationSchema{
				{Operation: "templates.create", Payload: "template", Fields: []requestFieldSchema{{Path: "template.name", Type: "string", Required: true}}},
				{Operation: "projects.create", Payload: "project", Fields: []requestFieldSchema{
					{Path: "project.name", Type: "string", Required: true},
					{Path: "project.template", Type: "string", Required: true},
				}},
				{Operation: "agents.register", Payload: "agent", Fields: []requestFieldSchema{
					{Path: "agent.project", Type: "string", Required: true},
					{Path: "agent.provider", Type: "string", Required: true, Enum: []string{"codex", "claude", "cursor", "command"}},
					{Path: "agent.label", Type: "string", Required: false},
					{Path: "agent.pane", Type: "string", Required: false, Condition: "required when agent.resumeCommand is empty"},
					{Path: "agent.resumeCommand", Type: "array[string]", Required: false, Condition: "required when agent.pane is empty"},
				}},
			}
			return writeJSONOutput(command, schemaOutput{SchemaVersion: jsonSchemaVersion, Commands: schemas, ApplyOperations: operations})
		},
	}
}

func schemaForFlag(command *cobra.Command, flag *pflag.Flag) flagSchema {
	required := false
	if annotation := flag.Annotations[cobra.BashCompOneRequiredFlag]; len(annotation) > 0 && annotation[0] == "true" {
		required = true
	}
	enums := map[string][]string{
		"output":   {"text", "json"},
		"format":   {"text", "json"},
		"provider": {"codex", "claude", "cursor", "command"},
	}
	return flagSchema{Name: flag.Name, Type: flag.Value.Type(), Default: flag.DefValue, Description: flag.Usage, Required: required, Enum: enums[flag.Name]}
}

func argumentsForCommand(path string) []argumentSchema {
	stringArgument := func(name string) argumentSchema {
		return argumentSchema{Name: name, Type: "string", Required: true}
	}
	switch path {
	case "twt2 templates create", "twt2 templates show", "twt2 templates validate":
		return []argumentSchema{stringArgument("name")}
	case "twt2 templates repos add":
		return []argumentSchema{stringArgument("template"), stringArgument("repository"), stringArgument("url")}
	case "twt2 templates repos init set":
		return []argumentSchema{stringArgument("template"), stringArgument("repository"), {Name: "command", Type: "array[string]", Required: true, Variadic: true}}
	case "twt2 templates init set":
		return []argumentSchema{stringArgument("template"), {Name: "command", Type: "array[string]", Required: true, Variadic: true}}
	case "twt2 projects create":
		return []argumentSchema{stringArgument("name")}
	case "twt2 projects show", "twt2 projects open", "twt2 projects remove", "twt2 projects setup retry":
		return []argumentSchema{stringArgument("project")}
	case "twt2 agents register":
		return []argumentSchema{{Name: "resume_command", Type: "array[string]", Variadic: true, Condition: "required when --pane is empty"}}
	case "twt2 agents resume", "twt2 agents focus", "twt2 agents send":
		return []argumentSchema{stringArgument("agent_id")}
	default:
		return []argumentSchema{}
	}
}
