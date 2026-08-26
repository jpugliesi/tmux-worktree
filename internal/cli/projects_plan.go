package cli

import (
	"fmt"
	"io"
	"os"
	"strings"

	ticketservice "github.com/jpugliesi/tmux-worktree/internal/ticket"
	"github.com/spf13/cobra"
)

type projectPlanOutput struct {
	SchemaVersion int                             `json:"schemaVersion"`
	Plan          ticketservice.ProjectPlanResult `json:"plan"`
}

func newProjectsPlanCommand(options Options) *cobra.Command {
	plan := groupCommand(&cobra.Command{
		Use:   "plan",
		Short: "Manage the plan document of a Project",
	})
	plan.AddCommand(newProjectsPlanShowCommand(options))
	plan.AddCommand(newProjectsPlanEditCommand(options))
	plan.AddCommand(newProjectsPlanInitCommand(options))
	plan.AddCommand(newProjectsPlanPathCommand(options))
	return plan
}

func newProjectsPlanShowCommand(options Options) *cobra.Command {
	command := &cobra.Command{
		Use:   "show PROJECT",
		Short: "Show the plan document of a Project",
		Args:  exactArgs("PROJECT"),
		RunE: func(command *cobra.Command, args []string) error {
			service, err := options.ticketService()
			if err != nil {
				return err
			}
			result, err := service.ProjectPlan(args[0])
			if err != nil {
				return err
			}
			if WantsJSON(command) {
				return writeReadJSON(command, projectPlanOutput{SchemaVersion: jsonSchemaVersion, Plan: result}, "plan")
			}
			_, err = fmt.Fprint(command.OutOrStdout(), result.Content)
			return err
		},
	}
	setArguments(command, requiredArgument("project"))
	command.ValidArgsFunction = ticketProjectNameCompletion(options)
	addFieldsFlag(command, projectPlanOutput{})
	return command
}

func newProjectsPlanEditCommand(options Options) *cobra.Command {
	var fromStdin bool
	command := &cobra.Command{
		Use:   "edit PROJECT [--stdin]",
		Short: "Replace the plan document of a Project",
		Args:  exactArgs("PROJECT"),
		RunE: func(command *cobra.Command, args []string) error {
			service, err := options.ticketService()
			if err != nil {
				return err
			}
			if fromStdin {
				content, err := readTicketStdin(command)
				if err != nil {
					return err
				}
				return editProjectPlan(command, service, args[0], content)
			}
			// The editor is an interactive escape: it opens only for a person
			// at a terminal, so a piped call never blocks in an editor.
			if !interactiveInput(command.InOrStdin()) {
				return invalidUsageWithHint(command, "Pass the plan content on standard input with --stdin.",
					"twt projects plan edit has no terminal")
			}
			current, err := service.ProjectPlan(args[0])
			if err != nil {
				return err
			}
			content, err := readProjectPlanInEditor(command, options, current.Content)
			if err != nil {
				return err
			}
			return editProjectPlan(command, service, args[0], content)
		},
	}
	command.Flags().BoolVar(&fromStdin, "stdin", false, "Read the plan content from standard input")
	setArguments(command, requiredArgument("project"))
	command.ValidArgsFunction = ticketProjectNameCompletion(options)
	return command
}

// readProjectPlanInEditor opens VISUAL or EDITOR on a draft copy of the plan
// and returns the saved text. The plan.md file itself changes only through
// WriteProjectPlan, so git sync sees every edit.
func readProjectPlanInEditor(command *cobra.Command, options Options, seed string) (string, error) {
	temp, err := os.CreateTemp("", "twt-plan-*.md")
	if err != nil {
		return "", fmt.Errorf("create the plan draft file: %w", err)
	}
	path := temp.Name()
	defer os.Remove(path)
	if _, err := temp.WriteString(seed); err != nil {
		_ = temp.Close()
		return "", fmt.Errorf("write the plan draft file: %w", err)
	}
	if err := temp.Close(); err != nil {
		return "", fmt.Errorf("write the plan draft file: %w", err)
	}
	if err := options.OpenEditor(path); err != nil {
		return "", err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read the plan draft file: %w", err)
	}
	if strings.TrimSpace(string(data)) == "" {
		return "", invalidUsageWithHint(command, "Write the plan and save the file, or pass --stdin.",
			"the editor saved an empty plan")
	}
	return string(data), nil
}

// editProjectPlan replaces the plan document. Both the projects plan edit
// command and apply use it.
func editProjectPlan(command *cobra.Command, service ticketservice.Store, name, content string) error {
	return runMutation(command, "projects.plan.edit",
		func() (string, string, error) {
			result, err := service.WriteProjectPlan(name, content, true)
			return result.Project, result.Path, err
		},
		func() (string, string, error) {
			result, err := service.WriteProjectPlan(name, content, false)
			return result.Project, result.Path, err
		},
		func(out io.Writer, id, _ string) error {
			_, err := fmt.Fprintf(out, "Wrote the plan of Project %q\n", id)
			return err
		})
}

func newProjectsPlanInitCommand(options Options) *cobra.Command {
	command := &cobra.Command{
		Use:   "init PROJECT",
		Short: "Create the plan document scaffold of a Project",
		Args:  exactArgs("PROJECT"),
		RunE: func(command *cobra.Command, args []string) error {
			service, err := options.ticketService()
			if err != nil {
				return err
			}
			return runMutation(command, "projects.plan.init",
				func() (string, string, error) {
					result, err := service.InitProjectPlan(args[0], true)
					return result.Project, result.Path, err
				},
				func() (string, string, error) {
					result, err := service.InitProjectPlan(args[0], false)
					return result.Project, result.Path, err
				},
				func(out io.Writer, id, _ string) error {
					_, err := fmt.Fprintf(out, "Created plan.md for Project %q\n", id)
					return err
				})
		},
	}
	setArguments(command, requiredArgument("project"))
	command.ValidArgsFunction = ticketProjectNameCompletion(options)
	return command
}

func newProjectsPlanPathCommand(options Options) *cobra.Command {
	command := &cobra.Command{
		Use:   "path PROJECT",
		Short: "Print the plan document path of a Project",
		Args:  exactArgs("PROJECT"),
		RunE: func(command *cobra.Command, args []string) error {
			service, err := options.ticketService()
			if err != nil {
				return err
			}
			result, err := service.ProjectPlanPath(args[0])
			if err != nil {
				return err
			}
			if WantsJSON(command) {
				return writeReadJSON(command, projectPlanOutput{SchemaVersion: jsonSchemaVersion, Plan: result}, "plan")
			}
			_, err = fmt.Fprintln(command.OutOrStdout(), result.Path)
			return err
		},
	}
	setArguments(command, requiredArgument("project"))
	command.ValidArgsFunction = ticketProjectNameCompletion(options)
	addFieldsFlag(command, projectPlanOutput{})
	return command
}
