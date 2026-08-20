package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/jpugliesi/tmux-worktree/internal/domain"
	projectservice "github.com/jpugliesi/tmux-worktree/internal/project"
	"github.com/jpugliesi/tmux-worktree/internal/store"
	"github.com/spf13/cobra"
)

const jsonSchemaVersion = 1

type projectOutput struct {
	SchemaVersion int                  `json:"schemaVersion"`
	ID            string               `json:"id"`
	Name          string               `json:"name"`
	Template      string               `json:"template"`
	Status        domain.ProjectStatus `json:"status"`
	CreatedAt     string               `json:"createdAt"`
	ArchivedAt    string               `json:"archivedAt,omitempty"`
	Repositories  []repositoryOutput   `json:"repositories"`
}

type repositoryOutput struct {
	Name       string `json:"name"`
	WindowName string `json:"windowName"`
}

type projectsListOutput struct {
	SchemaVersion int             `json:"schemaVersion"`
	Projects      []projectOutput `json:"projects"`
}

type contextOutput struct {
	SchemaVersion  int           `json:"schemaVersion"`
	Project        projectOutput `json:"project"`
	RepositoryName string        `json:"repositoryName,omitempty"`
}

type removalOutput struct {
	SchemaVersion int                             `json:"schemaVersion"`
	Applied       bool                            `json:"applied"`
	Plan          projectservice.RemovalPlan      `json:"plan"`
	Blockers      []projectservice.RemovalBlocker `json:"blockers"`
	Bytes         int64                           `json:"bytes"`
}

func newProjectsCommand(options Options) *cobra.Command {
	service := projectservice.NewService(projectservice.Options{
		StateDir:   options.StateDir,
		DataDir:    options.DataDir,
		TmuxSocket: options.TmuxSocket,
	})
	projects := groupCommand(&cobra.Command{Use: "projects", Short: "Manage Projects"})
	projects.AddCommand(newProjectsCreateCommand(options, service))
	projects.AddCommand(newProjectsListCommand(service))
	projects.AddCommand(newProjectsShowCommand(service))
	projects.AddCommand(newProjectsCurrentCommand(service))
	projects.AddCommand(newProjectsOpenCommand(options, service))
	projects.AddCommand(newProjectsArchiveCommand(service))
	projects.AddCommand(newProjectsSetupCommand(service))
	projects.AddCommand(newProjectsRemoveCommand(service))
	return projects
}

func newProjectsArchiveCommand(service *projectservice.Service) *cobra.Command {
	return &cobra.Command{
		Use:   "archive PROJECT",
		Short: "Archive a Project without removing its data",
		Args:  exactArgs("PROJECT"),
		RunE: func(command *cobra.Command, args []string) error {
			return archiveProject(command, service, args[0])
		},
	}
}

func newArchiveCommand(options Options) *cobra.Command {
	service := projectservice.NewService(projectservice.Options{StateDir: options.StateDir, DataDir: options.DataDir, TmuxSocket: options.TmuxSocket})
	return &cobra.Command{
		Use:   "archive [PROJECT]",
		Short: "Archive the current Project or a specified Project",
		Args:  optionalArg("PROJECT"),
		RunE: func(command *cobra.Command, args []string) error {
			reference := ""
			if len(args) == 1 {
				reference = args[0]
			} else {
				directory, err := os.Getwd()
				if err != nil {
					return err
				}
				current, err := service.Current(directory, os.Getenv("TWT2_PROJECT_ID"), os.Getenv("TMUX_PANE"))
				if err != nil {
					return err
				}
				reference = current.ID
			}
			return archiveProject(command, service, reference)
		},
	}
}

func archiveProject(command *cobra.Command, service *projectservice.Service, reference string) error {
	if isDryRun(command) {
		if err := service.ValidateArchive(reference, os.Getenv("TMUX_PANE")); err != nil {
			return err
		}
		return writeMutation(command, "projects.archive", "valid", "", reference)
	}
	result, err := service.Archive(reference, os.Getenv("TMUX_PANE"))
	if err != nil {
		return err
	}
	if WantsJSON(command) {
		return writeMutation(command, "projects.archive", "applied", result.Project.ID, result.Project.Name)
	}
	if err := printStoppedAgents(command.OutOrStdout(), result.StoppedAgents); err != nil {
		return err
	}
	_, err = fmt.Fprintf(command.OutOrStdout(), "Archived Project %q\n", result.Project.Name)
	return err
}

func printStoppedAgents(out io.Writer, agents []domain.AgentSession) error {
	if len(agents) == 0 {
		return nil
	}
	if _, err := fmt.Fprintf(out, "Stopping %d live Agent Sessions:\n", len(agents)); err != nil {
		return err
	}
	for _, agent := range agents {
		if _, err := fmt.Fprintf(out, "  %s %s %s\n", agent.ID, agent.Provider, agent.Label); err != nil {
			return err
		}
	}
	return nil
}

func newProjectsRemoveCommand(service *projectservice.Service) *cobra.Command {
	var apply bool
	var allowUnpublished bool
	var cancel bool
	command := &cobra.Command{
		Use:   "remove PROJECT",
		Short: "Plan or apply safe Project removal",
		Args:  exactArgs("PROJECT"),
		RunE: func(command *cobra.Command, args []string) error {
			if apply && isDryRun(command) {
				return invalidUsage(command, "do not use --dry-run together with --apply; remove one of the two flags")
			}
			if cancel {
				if apply || allowUnpublished {
					return invalidUsage(command, "do not use --cancel together with --apply or --allow-unpublished")
				}
				project, err := service.CancelRemoval(args[0])
				if err != nil {
					return err
				}
				if WantsJSON(command) {
					return writeMutation(command, "projects.remove.cancel", "applied", project.ID, project.Name)
				}
				_, err = fmt.Fprintf(command.OutOrStdout(), "Removal of Project %q is canceled. The Project is archived.\n", project.Name)
				return err
			}
			options := projectservice.RemovalOptions{AllowUnpublished: allowUnpublished}
			var plan projectservice.RemovalPlan
			var err error
			applied := false
			if apply {
				plan, err = service.Remove(args[0], os.Getenv("TMUX_PANE"), options)
				if err != nil {
					return err
				}
				applied = true
			} else {
				plan, err = service.PlanRemoval(args[0], os.Getenv("TMUX_PANE"), options)
				if err != nil {
					return err
				}
			}
			if WantsJSON(command) {
				return writeJSONOutput(command, removalOutput{SchemaVersion: jsonSchemaVersion, Applied: applied, Plan: plan, Blockers: plan.Blockers, Bytes: plan.Bytes})
			}
			if applied {
				_, err = fmt.Fprintf(command.OutOrStdout(), "Removed Project %q\n", plan.ProjectName)
				return err
			}
			if _, err := fmt.Fprintf(command.OutOrStdout(), "Removal plan for Project %q:\n", plan.ProjectName); err != nil {
				return err
			}
			for _, action := range plan.Actions {
				if _, err := fmt.Fprintf(command.OutOrStdout(), "  %s %s\n", action.Kind, action.Target); err != nil {
					return err
				}
			}
			if len(plan.Blockers) == 0 {
				_, err = fmt.Fprintln(command.OutOrStdout(), "Run again with --apply to remove these items.")
				return err
			}
			if _, err := fmt.Fprintln(command.OutOrStdout(), "Blocked:"); err != nil {
				return err
			}
			for _, blocker := range plan.Blockers {
				if _, err := fmt.Fprintf(command.OutOrStdout(), "  %s\n", blocker.Message); err != nil {
					return err
				}
				for _, path := range blocker.Paths {
					if _, err := fmt.Fprintf(command.OutOrStdout(), "    %s\n", path); err != nil {
						return err
					}
				}
				if blocker.Hint != "" {
					if _, err := fmt.Fprintf(command.OutOrStdout(), "  Hint: %s\n", blocker.Hint); err != nil {
						return err
					}
				}
			}
			_, err = fmt.Fprintln(command.OutOrStdout(), "The removal is blocked. Correct the causes above, then run the command again.")
			return err
		},
	}
	command.Flags().BoolVar(&apply, "apply", false, "Apply the removal plan")
	command.Flags().BoolVar(&allowUnpublished, "allow-unpublished", false, "Remove a branch with unpublished commits")
	command.Flags().BoolVar(&cancel, "cancel", false, "Return a removing Project to the archived status")
	return command
}

func newProjectsCreateCommand(options Options, service *projectservice.Service) *cobra.Command {
	var templateName string
	var noOpen bool
	command := &cobra.Command{
		Use:   "create NAME",
		Short: "Create a Project from a Project Template",
		Args:  exactArgs("NAME"),
		PreRunE: func(command *cobra.Command, _ []string) error {
			if strings.TrimSpace(templateName) == "" {
				return invalidUsage(command, "missing required flag --template TEMPLATE")
			}
			return nil
		},
		RunE: func(command *cobra.Command, args []string) error {
			template, err := store.NewTemplateStore(options.ConfigDir).Load(templateName)
			if err != nil {
				return err
			}
			if isDryRun(command) {
				if err := service.ValidateCreate(args[0], templateName, template); err != nil {
					return err
				}
				return writeMutation(command, "projects.create", "valid", "", args[0])
			}
			project, err := service.Create(args[0], templateName, template)
			if project.EnvironmentID != "" {
				if refillErr := startPreparationRefill(options, templateName, template); refillErr != nil && !WantsJSON(command) {
					_, _ = fmt.Fprintf(command.ErrOrStderr(), "Warning: the next Prepared Environment was not started: %v\n", refillErr)
				}
			}
			if err != nil {
				return err
			}
			if !noOpen && !WantsJSON(command) {
				if err := openTmux(options, project.TmuxSession); err != nil {
					return err
				}
			}
			if WantsJSON(command) {
				return writeMutation(command, "projects.create", "applied", project.ID, project.Name)
			}
			_, err = fmt.Fprintf(command.OutOrStdout(), "Created Project %q (%s)\n", project.Name, project.ID)
			return err
		},
	}
	command.Flags().StringVar(&templateName, "template", "", "Select the Project Template")
	command.Flags().BoolVar(&noOpen, "no-open", false, "Do not open the tmux session")
	_ = command.MarkFlagRequired("template")
	return command
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
			sort.SliceStable(projects, func(i, j int) bool {
				iArchived := projects[i].Status == domain.ProjectArchived
				jArchived := projects[j].Status == domain.ProjectArchived
				if iArchived != jArchived {
					return !iArchived
				}
				return projects[i].CreatedAt.After(projects[j].CreatedAt)
			})
			projects, err = applyLimit(projects, limit)
			if err != nil {
				return err
			}
			if WantsJSON(command) {
				values := make([]projectOutput, 0, len(projects))
				for _, project := range projects {
					values = append(values, toProjectOutput(project))
				}
				return writeJSONOutput(command, projectsListOutput{SchemaVersion: jsonSchemaVersion, Projects: values})
			}
			for _, project := range projects {
				if _, err := fmt.Fprintf(command.OutOrStdout(), "%s\t%s\t%s\n", project.Name, project.TemplateName, project.Status); err != nil {
					return err
				}
			}
			return nil
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
			project, err := service.Find(args[0])
			if err != nil {
				return err
			}
			if WantsJSON(command) {
				return writeJSONOutput(command, toProjectOutput(project))
			}
			_, err = fmt.Fprintf(command.OutOrStdout(), "Project: %s\nID: %s\nTemplate: %s\nStatus: %s\n", project.Name, project.ID, project.TemplateName, project.Status)
			return err
		},
	}
	return command
}

func newProjectsOpenCommand(options Options, service *projectservice.Service) *cobra.Command {
	var noAttach bool
	command := &cobra.Command{
		Use:   "open PROJECT",
		Short: "Open or repair a Project tmux session",
		Args:  exactArgs("PROJECT"),
		RunE: func(command *cobra.Command, args []string) error {
			if isDryRun(command) {
				if err := service.ValidateOpen(args[0]); err != nil {
					return err
				}
				return writeMutation(command, "projects.open", "valid", "", args[0])
			}
			project, err := service.Open(args[0])
			if err != nil {
				return err
			}
			if !noAttach && !WantsJSON(command) {
				return openTmux(options, project.TmuxSession)
			}
			if WantsJSON(command) {
				return writeMutation(command, "projects.open", "applied", project.ID, project.Name)
			}
			_, err = fmt.Fprintf(command.OutOrStdout(), "Opened Project %q\n", project.Name)
			return err
		},
	}
	command.Flags().BoolVar(&noAttach, "no-attach", false, "Repair the session without attaching")
	return command
}

func newProjectsCurrentCommand(service *projectservice.Service) *cobra.Command {
	command := &cobra.Command{
		Use:   "current",
		Short: "Show the current Project",
		Args:  noArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			directory, err := os.Getwd()
			if err != nil {
				return err
			}
			project, err := service.Current(directory, os.Getenv("TWT2_PROJECT_ID"), os.Getenv("TMUX_PANE"))
			if err != nil {
				return err
			}
			if WantsJSON(command) {
				return writeJSONOutput(command, toProjectOutput(project))
			}
			_, err = fmt.Fprintln(command.OutOrStdout(), project.Name)
			return err
		},
	}
	return command
}

func newContextCommand(options Options) *cobra.Command {
	service := projectservice.NewService(projectservice.Options{StateDir: options.StateDir, DataDir: options.DataDir, TmuxSocket: options.TmuxSocket})
	var directory string
	command := &cobra.Command{
		Use:   "context",
		Short: "Show the current twt2 context",
		Args:  noArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			lookupDirectory := directory
			projectID := os.Getenv("TWT2_PROJECT_ID")
			tmuxPane := os.Getenv("TMUX_PANE")
			if command.Flags().Changed("directory") {
				projectID = ""
				tmuxPane = ""
			} else {
				var err error
				lookupDirectory, err = os.Getwd()
				if err != nil {
					return err
				}
			}
			project, err := service.Current(lookupDirectory, projectID, tmuxPane)
			if err != nil {
				return err
			}
			if WantsJSON(command) {
				return writeJSONOutput(command, contextOutput{SchemaVersion: jsonSchemaVersion, Project: toProjectOutput(project), RepositoryName: repositoryForDirectory(project, lookupDirectory)})
			}
			_, err = fmt.Fprintf(command.OutOrStdout(), "Project: %s\n", project.Name)
			return err
		},
	}
	command.Flags().StringVar(&directory, "directory", "", "Resolve context from this directory before tmux or environment context")
	return command
}

func repositoryForDirectory(project domain.Project, directory string) string {
	absDirectory, err := filepath.Abs(directory)
	if err != nil {
		return ""
	}
	for _, repository := range project.Repositories {
		relative, err := filepath.Rel(repository.Path, absDirectory)
		if err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return repository.Name
		}
	}
	return ""
}

func newProjectsSetupCommand(service *projectservice.Service) *cobra.Command {
	setup := groupCommand(&cobra.Command{Use: "setup", Short: "Manage Project setup"})
	setup.AddCommand(&cobra.Command{
		Use:   "retry PROJECT",
		Short: "Retry incomplete Project setup steps",
		Args:  exactArgs("PROJECT"),
		RunE: func(command *cobra.Command, args []string) error {
			if isDryRun(command) {
				if err := service.ValidateRetry(args[0]); err != nil {
					return err
				}
				return writeMutation(command, "projects.setup.retry", "valid", "", args[0])
			}
			project, err := service.Retry(args[0])
			if err != nil {
				return err
			}
			if WantsJSON(command) {
				return writeMutation(command, "projects.setup.retry", "applied", project.ID, project.Name)
			}
			_, err = fmt.Fprintf(command.OutOrStdout(), "Project %q setup is complete\n", project.Name)
			return err
		},
	})
	return setup
}

func toProjectOutput(project domain.Project) projectOutput {
	repositories := make([]repositoryOutput, 0, len(project.Repositories))
	for _, repository := range project.Repositories {
		repositories = append(repositories, repositoryOutput{Name: repository.Name, WindowName: repository.WindowName})
	}
	result := projectOutput{
		SchemaVersion: jsonSchemaVersion,
		ID:            project.ID,
		Name:          project.Name,
		Template:      project.TemplateName,
		Status:        project.Status,
		CreatedAt:     project.CreatedAt.Format("2006-01-02T15:04:05Z07:00"),
		Repositories:  repositories,
	}
	if project.ArchivedAt != nil {
		result.ArchivedAt = project.ArchivedAt.Format("2006-01-02T15:04:05Z07:00")
	}
	return result
}

func writeJSONOutput(command *cobra.Command, value any) error {
	encoder := json.NewEncoder(command.OutOrStdout())
	encoder.SetEscapeHTML(false)
	return encoder.Encode(value)
}

func openTmux(options Options, session string) error {
	args := make([]string, 0, 8)
	if options.TmuxSocket != "" {
		args = append(args, "-L", options.TmuxSocket, "-f", "/dev/null")
	}
	if os.Getenv("TMUX") != "" {
		args = append(args, "switch-client", "-t", "="+session)
	} else {
		args = append(args, "attach-session", "-t", "="+session)
	}
	process := exec.Command("tmux", args...)
	process.Stdin = os.Stdin
	process.Stdout = options.Stdout
	process.Stderr = options.Stderr
	if err := process.Run(); err != nil {
		return fmt.Errorf("open tmux session %q: %w", session, err)
	}
	return nil
}
