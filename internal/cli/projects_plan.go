package cli

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/jpugliesi/tmux-worktree/internal/clierr"
	ticketservice "github.com/jpugliesi/tmux-worktree/internal/ticket"
	"github.com/spf13/cobra"
)

type projectPlanOutput struct {
	SchemaVersion int                             `json:"schemaVersion"`
	Plan          ticketservice.ProjectPlanResult `json:"plan"`
}

func newProjectsPlanCommand(options Options) *cobra.Command {
	plan := &cobra.Command{
		Use:   "plan [PROJECT] [-]",
		Short: "Manage the plan document of a Project",
		Args:  optionalResourceThenStdin("PROJECT"),
		RunE: func(command *cobra.Command, args []string) error {
			name, fromStdin := "", false
			switch {
			case len(args) == 0:
			case len(args) == 1 && isStdinToken(args[0]):
				fromStdin = true
			case len(args) == 1:
				name = args[0]
			default:
				name = args[0]
				fromStdin = true
			}
			service, err := options.ticketService()
			if err != nil {
				return err
			}
			if name == "" {
				name, err = currentPlanProject(command, options)
				if err != nil {
					return err
				}
			} else if suggestions := command.SuggestionsFor(name); len(suggestions) > 0 {
				if _, projErr := service.Project(name); projErr != nil {
					return invalidUsage(command, "twt does not know the command %q; did you mean %q?", name, suggestions[0])
				}
			}
			return runProjectPlanEdit(command, options, service, name, fromStdin)
		},
	}
	if plan.SuggestionsMinimumDistance <= 0 {
		plan.SuggestionsMinimumDistance = 2
	}
	setArguments(plan, optionalArgument("project", "the current Project when absent"), stdinTokenArgument(false))
	plan.ValidArgsFunction = ticketProjectNameCompletion(options)
	plan.AddCommand(newProjectsPlanShowCommand(options))
	plan.AddCommand(newProjectsPlanEditCommand(options))
	plan.AddCommand(newProjectsPlanInitCommand(options))
	plan.AddCommand(newProjectsPlanPathCommand(options))
	return plan
}

// currentPlanProject resolves the Project for a no-arg plan command. The
// order is TWT_PROJECT, then the current Workspace Project.
func currentPlanProject(command *cobra.Command, options Options) (string, error) {
	scope, err := resolveTicketProject(command, options, "", false, false)
	if err != nil {
		return "", err
	}
	if !scope.Set || scope.Project == "" {
		return "", invalidUsageWithHint(command,
			"Set TWT_PROJECT, run this from a Workspace with a Project, or pass a Project to 'twt projects plan PROJECT'.",
			"no Project is in scope")
	}
	return scope.Project, nil
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
	command := &cobra.Command{
		Use:   "edit PROJECT [-]",
		Short: "Replace the plan document of a Project",
		Args:  optionalStdinAfter("PROJECT"),
		RunE: func(command *cobra.Command, args []string) error {
			service, err := options.ticketService()
			if err != nil {
				return err
			}
			return runProjectPlanEdit(command, options, service, args[0], len(args) == 2)
		},
	}
	setArguments(command, requiredArgument("project"), stdinTokenArgument(false))
	command.ValidArgsFunction = ticketProjectNameCompletion(options)
	return command
}

// runProjectPlanEdit replaces the plan document of one Project. With
// - it is an upsert. Without - it opens VISUAL or EDITOR. A missing plan
// opens a blank draft. The write creates plan.md when it is missing.
func runProjectPlanEdit(command *cobra.Command, options Options, service ticketservice.Store, name string, fromStdin bool) error {
	if fromStdin {
		content, err := readTicketStdin(command)
		if err != nil {
			return err
		}
		return editProjectPlan(command, service, name, content)
	}
	// The editor is an interactive escape: it opens only for a person
	// at a terminal, so a piped call never blocks in an editor.
	if !interactiveTicketSession(command) {
		return invalidUsageWithHint(command, "Pass - to read the plan content from standard input.",
			"%s has no terminal", command.CommandPath())
	}
	seed, err := projectPlanEditorSeed(service, name)
	if err != nil {
		return err
	}
	content, err := readPlanDraftInEditor(command, options, seed,
		"Write the plan and save the file, or pass -.")
	if err != nil {
		return err
	}
	return editProjectPlan(command, service, name, content)
}

// projectPlanEditorSeed returns the current plan text, or an empty draft
// when the Project exists and has no plan.md.
func projectPlanEditorSeed(service ticketservice.Store, name string) (string, error) {
	if _, err := service.Project(name); err != nil {
		return "", err
	}
	current, err := service.ProjectPlan(name)
	if err == nil {
		return current.Content, nil
	}
	if clierr.CodeOf(err) != clierr.NotFound {
		return "", err
	}
	return "", nil
}

// readPlanDraftInEditor opens VISUAL or EDITOR on a draft copy of the plan
// and returns the saved text. The store file itself changes only through
// the matching write, so git sync sees every edit.
func readPlanDraftInEditor(command *cobra.Command, options Options, seed, emptyHint string) (string, error) {
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
		return "", invalidUsageWithHint(command, emptyHint,
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
