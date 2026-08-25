package cli

import (
	"fmt"
	"io"

	"github.com/jpugliesi/tmux-worktree/internal/domain"
	ticketservice "github.com/jpugliesi/tmux-worktree/internal/ticket"
	"github.com/spf13/cobra"
)

type projectsListOutput struct {
	SchemaVersion int              `json:"schemaVersion"`
	Projects      []domain.Project `json:"projects"`
	TotalCount    int              `json:"totalCount"`
	Truncated     bool             `json:"truncated,omitempty"`
}

type projectShowOutput struct {
	SchemaVersion int               `json:"schemaVersion"`
	Project       domain.Project    `json:"project"`
	Ready         []domain.Ticket   `json:"ready"`
	InFlight      []domain.Ticket   `json:"inFlight"`
	Workspaces    []workspaceOutput `json:"workspaces"`
}

func newProjectsCommand(options Options) *cobra.Command {
	projects := groupCommand(&cobra.Command{Use: "projects", Short: "Manage Ticket Projects"})
	projects.AddCommand(newProjectsCreateCommand(options))
	projects.AddCommand(newProjectsSetCommand(options))
	projects.AddCommand(newProjectsListCommand(options))
	projects.AddCommand(newProjectsShowCommand(options))
	return projects
}

func newProjectsCreateCommand(options Options) *cobra.Command {
	var templateName string
	command := &cobra.Command{
		Use:   "create NAME",
		Short: "Create a Project directory",
		Args:  exactArgs("NAME"),
		RunE: func(command *cobra.Command, args []string) error {
			service, err := options.ticketService()
			if err != nil {
				return err
			}
			return createProject(command, options, service, args[0], templateName)
		},
	}
	command.Flags().StringVar(&templateName, "template", "", "Save the Workspace Template for this Project")
	setArguments(command, requiredArgument("name"))
	_ = command.RegisterFlagCompletionFunc("template", templateFlagCompletion(options.templateStore()))
	return command
}

// createProject creates one Project. Both the command and apply use it.
func createProject(command *cobra.Command, options Options, service *ticketservice.Service, name, templateName string) error {
	if templateName != "" {
		if _, err := options.templateStore().Load(templateName); err != nil {
			return err
		}
	}
	return runMutation(command, "projects.create",
		func() (string, string, error) {
			project, err := service.CreateProjectWithTemplate(name, templateName, true)
			return project.Name, project.Name, err
		},
		func() (string, string, error) {
			project, err := service.CreateProjectWithTemplate(name, templateName, false)
			return project.Name, project.Name, err
		},
		func(out io.Writer, _, projectName string) error {
			_, err := fmt.Fprintf(out, "Created Project %q\n", projectName)
			return err
		})
}

func newProjectsSetCommand(options Options) *cobra.Command {
	var templateName string
	command := &cobra.Command{
		Use:   "set NAME --template TEMPLATE",
		Short: "Set Project configuration",
		Args:  exactArgs("NAME"),
		RunE: func(command *cobra.Command, args []string) error {
			if !command.Flags().Changed("template") || templateName == "" {
				return invalidUsage(command, "pass --template TEMPLATE")
			}
			if _, err := options.templateStore().Load(templateName); err != nil {
				return err
			}
			service, err := options.ticketService()
			if err != nil {
				return err
			}
			return setProjectTemplate(command, service, args[0], templateName)
		},
	}
	command.Flags().StringVar(&templateName, "template", "", "Set the Workspace Template for this Project")
	setArguments(command, requiredArgument("name"))
	command.ValidArgsFunction = ticketProjectNameCompletion(options)
	_ = command.RegisterFlagCompletionFunc("template", templateFlagCompletion(options.templateStore()))
	return command
}

func setProjectTemplate(command *cobra.Command, service *ticketservice.Service, name, templateName string) error {
	return runMutation(command, "projects.set",
		func() (string, string, error) {
			project, err := service.SetProjectTemplate(name, templateName, true)
			return project.Name, project.Name, err
		},
		func() (string, string, error) {
			project, err := service.SetProjectTemplate(name, templateName, false)
			return project.Name, project.Name, err
		},
		func(out io.Writer, _, projectName string) error {
			_, err := fmt.Fprintf(out, "Set Workspace Template %q on Project %q\n", templateName, projectName)
			return err
		})
}

func newProjectsListCommand(options Options) *cobra.Command {
	var limit, offset int
	command := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List Projects",
		Args:    noArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			service, err := options.ticketService()
			if err != nil {
				return err
			}
			projects, err := service.Projects()
			if err != nil {
				return err
			}
			projects, total, truncated, err := applyWindow(projects, offset, limit)
			if err != nil {
				return err
			}
			if format := resolvedOutputFormat(command); format != outputText {
				if format == outputNDJSON {
					return writeNDJSONList(command, projects, total, truncated)
				}
				return writeReadJSON(command, projectsListOutput{SchemaVersion: jsonSchemaVersion, Projects: projects, TotalCount: total, Truncated: truncated}, "projects")
			}
			if total == 0 {
				_, err = fmt.Fprintln(command.ErrOrStderr(), "No Projects exist. Run 'twt projects create NAME'.")
				return err
			}
			rows := make([][]string, 0, len(projects))
			for _, project := range projects {
				rows = append(rows, []string{project.Name, fmt.Sprintf("%d", project.Tickets)})
			}
			return writeTable(command.OutOrStdout(), []string{"NAME", "TICKETS"}, rows)
		},
	}
	addListReadFlags(command, &limit, &offset, domain.Project{})
	return command
}

func newProjectsShowCommand(options Options) *cobra.Command {
	command := &cobra.Command{
		Use:   "show NAME",
		Short: "Show a Project",
		Args:  exactArgs("NAME"),
		RunE: func(command *cobra.Command, args []string) error {
			service, err := options.ticketService()
			if err != nil {
				return err
			}
			project, err := service.Project(args[0])
			if err != nil {
				return err
			}
			board, err := projectBoard(options, service, project)
			if err != nil {
				return err
			}
			if WantsJSON(command) {
				return writeReadJSON(command, board, "project")
			}
			return writeFields(command.OutOrStdout(), [][2]string{
				{"Project", project.Name},
				{"Path", project.Path},
				{"Tickets", fmt.Sprintf("%d", project.Tickets)},
				{"Ready", fmt.Sprintf("%d", len(board.Ready))},
				{"In flight", fmt.Sprintf("%d", len(board.InFlight))},
				{"Workspaces", fmt.Sprintf("%d", len(board.Workspaces))},
			})
		},
	}
	setArguments(command, requiredArgument("name"))
	addFieldsFlag(command, projectShowOutput{})
	command.ValidArgsFunction = ticketProjectNameCompletion(options)
	return command
}

// projectBoard is the coordinator read for one Project: pickable Tickets,
// claimed Tickets, and the Workspaces that belong to the Project.
func projectBoard(options Options, service *ticketservice.Service, project domain.Project) (projectShowOutput, error) {
	ready, err := service.List(ticketservice.ListFilter{Project: project.Name, ProjectSet: true, Ready: true})
	if err != nil {
		return projectShowOutput{}, err
	}
	if ready == nil {
		ready = []domain.Ticket{}
	}
	claimed, err := service.List(ticketservice.ListFilter{Project: project.Name, ProjectSet: true, Claimed: true})
	if err != nil {
		return projectShowOutput{}, err
	}
	if claimed == nil {
		claimed = []domain.Ticket{}
	}
	workspaces, err := options.workspaceService().List()
	if err != nil {
		return projectShowOutput{}, err
	}
	linked := filterWorkspaces(workspaces, project.Name, "", "", true, false)
	outputs := make([]workspaceOutput, 0, len(linked))
	for _, workspace := range linked {
		outputs = append(outputs, toWorkspaceOutput(workspace))
	}
	return projectShowOutput{
		SchemaVersion: jsonSchemaVersion,
		Project:       project,
		Ready:         ready,
		InFlight:      claimed,
		Workspaces:    outputs,
	}, nil
}
