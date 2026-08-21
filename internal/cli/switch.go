package cli

import (
	"fmt"
	"io"
	"time"

	"github.com/jpugliesi/tmux-worktree/internal/clierr"
	"github.com/jpugliesi/tmux-worktree/internal/domain"
	projectservice "github.com/jpugliesi/tmux-worktree/internal/project"
	"github.com/spf13/cobra"
)

func newSwitchCommand(options Options) *cobra.Command {
	service := options.projectService()
	command := &cobra.Command{
		Use:   "switch [PROJECT]",
		Short: "Switch the tmux client to a Project session",
		Args:  optionalArg("PROJECT"),
		PreRunE: func(command *cobra.Command, _ []string) error {
			if WantsJSON(command) {
				return invalidUsage(command, "switch is an interactive command and does not support JSON output")
			}
			return nil
		},
		RunE: func(command *cobra.Command, args []string) error {
			var project domain.Project
			var err error
			if len(args) == 1 {
				project, err = resolveProject(service, args[0])
			} else {
				project, err = pickSwitchProject(command, options, service)
			}
			if err != nil {
				return err
			}
			if isDryRun(command) {
				return writeSwitchPlan(command.OutOrStdout(), project)
			}
			if project.Status == domain.ProjectArchived {
				project, err = service.Open(project.ID)
				if err != nil {
					return err
				}
			}
			return openTmux(options, project.TmuxSession)
		},
	}
	setArguments(command, optionalArgument("project", "the interactive picker asks for it when absent"))
	command.ValidArgsFunction = projectNameCompletion(service)
	return command
}

// writeSwitchPlan tells the user what switch would do, without a change.
func writeSwitchPlan(out io.Writer, project domain.Project) error {
	if project.Status == domain.ProjectArchived {
		_, err := fmt.Fprintf(out, "Dry run: open archived Project %q, then switch the client to session %q.\n", project.Name, project.TmuxSession)
		return err
	}
	_, err := fmt.Fprintf(out, "Dry run: switch the client to session %q of Project %q.\n", project.TmuxSession, project.Name)
	return err
}

// pickSwitchProject shows the interactive Project picker and returns the
// selected Project. Active Projects come first, most recent first.
func pickSwitchProject(command *cobra.Command, options Options, service *projectservice.Service) (domain.Project, error) {
	projects, err := service.List()
	if err != nil {
		return domain.Project{}, err
	}
	if len(projects) == 0 {
		return domain.Project{}, clierr.New(clierr.NotFound, "no Projects exist; run 'twt start NAME' first")
	}
	sortProjectsForDisplay(projects)
	now := time.Now().UTC()
	lines := make([]string, 0, len(projects))
	for _, project := range projects {
		age := formatAge(now.Sub(projectAgeReference(project)))
		lines = append(lines, fmt.Sprintf("%s\t%s\t%s\t%s", project.Name, project.TemplateName, project.Status, age))
	}
	index, err := options.SwitchPick(command, lines)
	if err != nil {
		return domain.Project{}, err
	}
	if index < 0 || index >= len(projects) {
		return domain.Project{}, fmt.Errorf("the Project picker returned an invalid selection")
	}
	return projects[index], nil
}

// realSwitchPick selects one picker line with fzf when it is installed, or
// with a numbered list on the terminal.
func realSwitchPick(command *cobra.Command, lines []string) (int, error) {
	return pickLine(command, lines, pickOptions{
		Noun:        "Project",
		MissingHint: "missing PROJECT; use 'twt switch PROJECT' in a script",
	})
}
