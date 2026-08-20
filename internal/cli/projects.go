package cli

import (
	"fmt"
	"io"
	"text/tabwriter"
	"time"

	"github.com/jpugliesi/tmux-worktree/internal/clierr"
	"github.com/jpugliesi/tmux-worktree/internal/domain"
	projectservice "github.com/jpugliesi/tmux-worktree/internal/project"
	"github.com/spf13/cobra"
)

func newProjectsCommand(options Options) *cobra.Command {
	service := options.projectService()
	projects := groupCommand(&cobra.Command{Use: "projects", Short: "Manage Projects"})
	projects.AddCommand(newProjectsCreateCommand(options, service))
	projects.AddCommand(newProjectsListCommand(service))
	projects.AddCommand(newProjectsShowCommand(service))
	projects.AddCommand(newProjectsCurrentCommand(service))
	projects.AddCommand(newProjectsPathCommand(service))
	projects.AddCommand(newProjectsOpenCommand(options, service))
	projects.AddCommand(newProjectsArchiveCommand(options, service))
	projects.AddCommand(newProjectsSetupCommand(service))
	projects.AddCommand(newProjectsRemoveCommand(service))
	return projects
}

func newProjectsListCommand(service *projectservice.Service) *cobra.Command {
	var limit int
	command := &cobra.Command{
		Use:   "list",
		Short: "List Projects",
		Args:  noArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			projects, err := service.List()
			if err != nil {
				return err
			}
			sortProjectsForDisplay(projects)
			projects, total, truncated, err := applyLimit(projects, limit)
			if err != nil {
				return err
			}
			if WantsJSON(command) {
				values := make([]projectOutput, 0, len(projects))
				for _, project := range projects {
					values = append(values, toProjectOutput(project))
				}
				return writeJSONOutput(command, projectsListOutput{SchemaVersion: jsonSchemaVersion, Projects: values, TotalCount: total, Truncated: truncated})
			}
			if total == 0 {
				_, err = fmt.Fprintln(command.ErrOrStderr(), "No Projects exist. Run 'twt projects create NAME'.")
				return err
			}
			now := time.Now().UTC()
			writer := tabwriter.NewWriter(command.OutOrStdout(), 0, 4, 2, ' ', 0)
			if _, err := fmt.Fprintln(writer, "NAME\tTEMPLATE\tSTATUS\tAGE"); err != nil {
				return err
			}
			for _, project := range projects {
				age := formatAge(now.Sub(projectAgeReference(project)))
				if _, err := fmt.Fprintf(writer, "%s\t%s\t%s\t%s\n", project.Name, project.TemplateName, project.Status, age); err != nil {
					return err
				}
			}
			return writer.Flush()
		},
	}
	command.Flags().IntVar(&limit, "limit", 0, "Limit the number of results; zero returns all results")
	return command
}

func newProjectsShowCommand(service *projectservice.Service) *cobra.Command {
	command := &cobra.Command{
		Use:   "show PROJECT",
		Short: "Show a Project",
		Args:  exactArgs("PROJECT"),
		RunE: func(command *cobra.Command, args []string) error {
			project, err := resolveProject(service, args[0])
			if err != nil {
				return err
			}
			return writeProject(command, project)
		},
	}
	setArguments(command, requiredArgument("project"))
	command.ValidArgsFunction = projectNameCompletion(service)
	return command
}

// writeProject writes one Project as the show envelope or as text.
func writeProject(command *cobra.Command, project domain.Project) error {
	if WantsJSON(command) {
		return writeJSONOutput(command, projectShowOutput{SchemaVersion: jsonSchemaVersion, Project: toProjectOutput(project)})
	}
	_, err := fmt.Fprintf(command.OutOrStdout(), "Project: %s\nID: %s\nTemplate: %s\nStatus: %s\nRoot: %s\n", project.Name, project.ID, project.TemplateName, project.Status, project.Root)
	return err
}

func newProjectsCurrentCommand(service *projectservice.Service) *cobra.Command {
	command := &cobra.Command{
		Use:   "current",
		Short: "Show the current Project",
		Args:  noArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			project, err := resolveProject(service, currentProjectReference)
			if err != nil {
				return err
			}
			if WantsJSON(command) {
				return writeJSONOutput(command, projectShowOutput{SchemaVersion: jsonSchemaVersion, Project: toProjectOutput(project)})
			}
			_, err = fmt.Fprintln(command.OutOrStdout(), project.Name)
			return err
		},
	}
	return command
}

func newProjectsPathCommand(service *projectservice.Service) *cobra.Command {
	command := &cobra.Command{
		Use:   "path PROJECT [REPO]",
		Short: "Print the Project root path or a repository checkout path",
		Args: func(command *cobra.Command, args []string) error {
			if len(args) < 1 {
				return invalidUsage(command, "missing required argument PROJECT")
			}
			if len(args) > 2 {
				return invalidUsage(command, "unexpected argument %q; expected PROJECT [REPO]", args[2])
			}
			return nil
		},
		RunE: func(command *cobra.Command, args []string) error {
			project, err := resolveProject(service, args[0])
			if err != nil {
				return err
			}
			path := project.Root
			if len(args) == 2 {
				found := false
				for _, repository := range project.Repositories {
					if repository.Name == args[1] {
						path = repository.Path
						found = true
						break
					}
				}
				if !found {
					return clierr.New(clierr.NotFound, "repository %q is not in Project %q", args[1], project.Name)
				}
			}
			_, err = fmt.Fprintln(command.OutOrStdout(), path)
			return err
		},
	}
	setArguments(command, requiredArgument("project"), optionalArgument("repo", "the Project root when absent"))
	command.ValidArgsFunction = projectRepositoryCompletion(service)
	return command
}

func newProjectsOpenCommand(options Options, service *projectservice.Service) *cobra.Command {
	var noAttach bool
	command := &cobra.Command{
		Use:   "open PROJECT",
		Short: "Open or repair a Project tmux session",
		Args:  exactArgs("PROJECT"),
		RunE: func(command *cobra.Command, args []string) error {
			reference, err := resolveProjectReference(service, args[0])
			if err != nil {
				return err
			}
			var project domain.Project
			if err := runMutation(command, "projects.open",
				func() (string, string, error) {
					return "", reference, service.ValidateOpen(reference)
				},
				func() (string, string, error) {
					var err error
					project, err = service.Open(reference)
					return project.ID, project.Name, err
				},
				func(out io.Writer, _, name string) error {
					_, err := fmt.Fprintf(out, "Opened Project %q\n", name)
					return err
				}); err != nil {
				return err
			}
			if isDryRun(command) || noAttach || !terminalWriter(command.OutOrStdout()) {
				return nil
			}
			return openTmux(options, project.TmuxSession)
		},
	}
	command.Flags().BoolVar(&noAttach, "no-attach", false, "Repair the session without attaching")
	setArguments(command, requiredArgument("project"))
	command.ValidArgsFunction = projectNameCompletion(service)
	return command
}

func newProjectsSetupCommand(service *projectservice.Service) *cobra.Command {
	setup := groupCommand(&cobra.Command{Use: "setup", Short: "Manage Project setup"})
	retry := &cobra.Command{
		Use:   "retry PROJECT",
		Short: "Retry incomplete Project setup steps",
		Args:  exactArgs("PROJECT"),
		RunE: func(command *cobra.Command, args []string) error {
			reference, err := resolveProjectReference(service, args[0])
			if err != nil {
				return err
			}
			return runMutation(command, "projects.setup.retry",
				func() (string, string, error) {
					return "", reference, service.ValidateRetry(reference)
				},
				func() (string, string, error) {
					project, err := service.Retry(reference)
					return project.ID, project.Name, err
				},
				func(out io.Writer, _, name string) error {
					_, err := fmt.Fprintf(out, "Project %q setup is complete\n", name)
					return err
				})
		},
	}
	setArguments(retry, requiredArgument("project"))
	retry.ValidArgsFunction = projectNameCompletion(service)
	setup.AddCommand(retry)
	return setup
}
