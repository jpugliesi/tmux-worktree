package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jpugliesi/tmux-worktree/internal/clierr"
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
	Root          string               `json:"root"`
	Bytes         int64                `json:"bytes"`
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
	projects.AddCommand(newProjectsPathCommand(service))
	projects.AddCommand(newProjectsOpenCommand(options, service))
	projects.AddCommand(newProjectsArchiveCommand(options, service))
	projects.AddCommand(newProjectsSetupCommand(service))
	projects.AddCommand(newProjectsRemoveCommand(service))
	return projects
}

func newProjectsArchiveCommand(options Options, service *projectservice.Service) *cobra.Command {
	return &cobra.Command{
		Use:   "archive PROJECT",
		Short: "Archive a Project without removing its data",
		Args:  exactArgs("PROJECT"),
		RunE: func(command *cobra.Command, args []string) error {
			return archiveProject(command, options, service, args[0])
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
			return archiveProject(command, options, service, reference)
		},
	}
}

func archiveProject(command *cobra.Command, options Options, service *projectservice.Service, reference string) error {
	if isDryRun(command) {
		if err := service.ValidateArchive(reference, os.Getenv("TMUX_PANE")); err != nil {
			return err
		}
		return writeMutation(command, "projects.archive", "valid", "", reference)
	}
	if !WantsJSON(command) {
		project, err := service.Find(reference)
		if err != nil {
			return err
		}
		currentPane := os.Getenv("TMUX_PANE")
		if insideOwnedSession(options, service, project.ID, currentPane) &&
			(options.FinishRelocate != nil || terminalWriter(command.OutOrStdout())) {
			return finishWithRelocation(command, options, service, project, currentPane, true, projectservice.RemovalOptions{})
		}
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
	var allArchived bool
	var olderThan string
	command := &cobra.Command{
		Use:   "remove [PROJECT]",
		Short: "Plan or apply safe Project removal",
		Args:  optionalArg("PROJECT"),
		RunE: func(command *cobra.Command, args []string) error {
			if apply && isDryRun(command) {
				return invalidUsage(command, "do not use --dry-run together with --apply; remove one of the two flags")
			}
			if allArchived {
				if len(args) != 0 {
					return invalidUsage(command, "do not use --all-archived together with a PROJECT argument")
				}
				if cancel {
					return invalidUsage(command, "do not use --all-archived together with --cancel")
				}
				return removeAllArchived(command, service, olderThan, apply, projectservice.RemovalOptions{AllowUnpublished: allowUnpublished})
			}
			if olderThan != "" {
				return invalidUsage(command, "--older-than requires --all-archived")
			}
			if len(args) != 1 {
				return invalidUsage(command, "missing required argument PROJECT")
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
			return printRemovalPlanText(command.OutOrStdout(), plan, true)
		},
	}
	command.Flags().BoolVar(&apply, "apply", false, "Apply the removal plan")
	command.Flags().BoolVar(&allowUnpublished, "allow-unpublished", false, "Remove a branch with unpublished commits")
	command.Flags().BoolVar(&cancel, "cancel", false, "Return a removing Project to the archived status")
	command.Flags().BoolVar(&allArchived, "all-archived", false, "Plan or apply removal of all archived Projects")
	command.Flags().StringVar(&olderThan, "older-than", "", "With --all-archived, select only Projects archived at least this long ago (for example 14d, 36h, or 30m)")
	return command
}

// printRemovalPlanText writes one removal plan with its actions and
// blockers. With applyHint, an unblocked plan invites --apply.
func printRemovalPlanText(out io.Writer, plan projectservice.RemovalPlan, applyHint bool) error {
	if _, err := fmt.Fprintf(out, "Removal plan for Project %q:\n", plan.ProjectName); err != nil {
		return err
	}
	for _, action := range plan.Actions {
		if _, err := fmt.Fprintf(out, "  %s %s\n", action.Kind, action.Target); err != nil {
			return err
		}
	}
	if len(plan.Blockers) == 0 {
		if !applyHint {
			return nil
		}
		_, err := fmt.Fprintln(out, "Run again with --apply to remove these items.")
		return err
	}
	if err := printRemovalBlockers(out, plan.Blockers, ""); err != nil {
		return err
	}
	_, err := fmt.Fprintln(out, "The removal is blocked. Correct the causes above, then run the command again.")
	return err
}

func printRemovalBlockers(out io.Writer, blockers []projectservice.RemovalBlocker, indent string) error {
	if len(blockers) == 0 {
		return nil
	}
	if _, err := fmt.Fprintf(out, "%sBlocked:\n", indent); err != nil {
		return err
	}
	for _, blocker := range blockers {
		if _, err := fmt.Fprintf(out, "%s  %s\n", indent, blocker.Message); err != nil {
			return err
		}
		for _, path := range blocker.Paths {
			if _, err := fmt.Fprintf(out, "%s    %s\n", indent, path); err != nil {
				return err
			}
		}
		if blocker.Hint != "" {
			if _, err := fmt.Fprintf(out, "%s  Hint: %s\n", indent, blocker.Hint); err != nil {
				return err
			}
		}
	}
	return nil
}

type bulkRemovalOutput struct {
	SchemaVersion int                          `json:"schemaVersion"`
	Plans         []projectservice.RemovalPlan `json:"plans"`
	Applied       bool                         `json:"applied"`
	RemovedCount  int                          `json:"removedCount"`
	SkippedCount  int                          `json:"skippedCount"`
}

// removeAllArchived plans or applies removal for all archived Projects that
// match the age filter. Apply removes the unblocked Projects and skips the
// blocked Projects.
func removeAllArchived(command *cobra.Command, service *projectservice.Service, olderThan string, apply bool, opts projectservice.RemovalOptions) error {
	age := time.Duration(0)
	if olderThan != "" {
		var err error
		age, err = ParseAgeDuration(olderThan)
		if err != nil {
			return invalidUsage(command, "invalid --older-than value %q: %v", olderThan, err)
		}
	}
	plans, err := service.BulkRemovalPlans(age, opts)
	if err != nil {
		return err
	}
	removed, skipped := 0, 0
	var reclaimed int64
	if apply {
		for index := range plans {
			if len(plans[index].Blockers) > 0 {
				skipped++
				continue
			}
			plan, err := service.Remove(plans[index].ProjectID, os.Getenv("TMUX_PANE"), opts)
			if err != nil {
				if len(plan.Blockers) > 0 {
					plans[index] = plan
					skipped++
					continue
				}
				return err
			}
			plans[index] = plan
			removed++
			reclaimed += plan.Bytes
		}
	}
	if WantsJSON(command) {
		return writeJSONOutput(command, bulkRemovalOutput{SchemaVersion: jsonSchemaVersion, Plans: plans, Applied: apply, RemovedCount: removed, SkippedCount: skipped})
	}
	out := command.OutOrStdout()
	if len(plans) == 0 {
		_, err := fmt.Fprintln(out, "No archived Projects match.")
		return err
	}
	now := time.Now().UTC()
	for _, plan := range plans {
		planAge := "unknown"
		if plan.ArchivedAt != nil {
			planAge = formatAge(now.Sub(*plan.ArchivedAt))
		}
		if _, err := fmt.Fprintf(out, "Project %q: age %s, size %s\n", plan.ProjectName, planAge, formatBytes(plan.Bytes)); err != nil {
			return err
		}
		if err := printRemovalBlockers(out, plan.Blockers, "  "); err != nil {
			return err
		}
	}
	if apply {
		_, err := fmt.Fprintf(out, "Removed %d Projects (%s). Skipped %d blocked Projects.\n", removed, formatBytes(reclaimed), skipped)
		return err
	}
	_, err = fmt.Fprintln(out, "Run again with --apply to remove the Projects that are not blocked.")
	return err
}

// ParseAgeDuration parses a short age value such as "14d", "36h", or "30m".
func ParseAgeDuration(value string) (time.Duration, error) {
	if len(value) < 2 {
		return 0, fmt.Errorf("use a number and a unit, for example 14d, 36h, or 30m")
	}
	number, err := strconv.Atoi(value[:len(value)-1])
	if err != nil || number < 0 {
		return 0, fmt.Errorf("use a number and a unit, for example 14d, 36h, or 30m")
	}
	switch value[len(value)-1] {
	case 'd':
		return time.Duration(number) * 24 * time.Hour, nil
	case 'h':
		return time.Duration(number) * time.Hour, nil
	case 'm':
		return time.Duration(number) * time.Minute, nil
	}
	return 0, fmt.Errorf("unknown unit %q; use d for days, h for hours, or m for minutes", string(value[len(value)-1]))
}

// formatAge writes a duration as a short age such as "3d", "5h", or "42m".
func formatAge(age time.Duration) string {
	if age < 0 {
		age = 0
	}
	if age >= 24*time.Hour {
		return fmt.Sprintf("%dd", int(age/(24*time.Hour)))
	}
	if age >= time.Hour {
		return fmt.Sprintf("%dh", int(age/time.Hour))
	}
	return fmt.Sprintf("%dm", int(age/time.Minute))
}

func newProjectsCreateCommand(options Options, service *projectservice.Service) *cobra.Command {
	var templateName string
	var noOpen bool
	var noFetch bool
	var branch string
	command := &cobra.Command{
		Use:   "create NAME",
		Short: "Create a Project from a Project Template",
		Args:  exactArgs("NAME"),
		RunE: func(command *cobra.Command, args []string) error {
			templateStore := store.NewTemplateStore(options.ConfigDir)
			selected := strings.TrimSpace(templateName)
			if selected == "" {
				inferred, source, err := inferTemplateName(command, options, templateStore)
				if err != nil {
					return err
				}
				selected = inferred
				if !WantsJSON(command) {
					_, _ = fmt.Fprintf(command.ErrOrStderr(), "Template: %s (%s)\n", selected, source)
				}
			}
			template, err := templateStore.Load(selected)
			if err != nil {
				return err
			}
			if isDryRun(command) {
				if err := service.ValidateCreate(args[0], selected, template); err != nil {
					return err
				}
				return writeMutation(command, "projects.create", "valid", "", args[0])
			}
			createService := newCreateService(command, options, selected, template)
			project, err := createService.CreateWithOptions(args[0], selected, template, projectservice.CreateOptions{Branch: branch, NoFetch: noFetch})
			if err != nil {
				return createFailureError(project, err)
			}
			_ = store.SaveLastTemplate(options.StateDir, selected)
			if !noOpen && !WantsJSON(command) {
				if err := openTmux(options, project.TmuxSession); err != nil {
					return err
				}
			}
			if WantsJSON(command) {
				return writeMutation(command, "projects.create", "applied", project.ID, project.Name)
			}
			if _, err := fmt.Fprintf(command.OutOrStdout(), "Created Project %q (%s)\n", project.Name, project.ID); err != nil {
				return err
			}
			_, err = fmt.Fprintf(command.OutOrStdout(), "Root: %s\n", project.Root)
			return err
		},
	}
	command.Flags().StringVar(&templateName, "template", "", "Select the Project Template")
	command.Flags().BoolVar(&noOpen, "no-open", false, "Do not open the tmux session")
	command.Flags().BoolVar(&noFetch, "no-fetch", false, "Do not refresh the default branch before the claim")
	command.Flags().StringVar(&branch, "branch", "", "Set a custom Project branch name")
	return command
}

// newCreateService builds a Project service that reports progress and starts
// the background Prepared Environment pool refill for one creation.
func newCreateService(command *cobra.Command, options Options, templateName string, template domain.Template) *projectservice.Service {
	serviceOptions := projectservice.Options{StateDir: options.StateDir, DataDir: options.DataDir, TmuxSocket: options.TmuxSocket}
	if !WantsJSON(command) {
		serviceOptions.Progress = func(message string) {
			_, _ = fmt.Fprintln(command.ErrOrStderr(), message)
		}
	}
	serviceOptions.AfterClaimReserved = func() {
		if err := startPreparationRefill(options, templateName, template); err != nil && !WantsJSON(command) {
			_, _ = fmt.Fprintf(command.ErrOrStderr(), "Warning: the next Prepared Environment was not started: %v\n", err)
		}
	}
	return projectservice.NewService(serviceOptions)
}

// createFailureError adds the setup retry hint when creation kept a Project
// record that the user can repair.
func createFailureError(project domain.Project, cause error) error {
	if project.ID == "" {
		return cause
	}
	wrapped := clierr.Wrap(clierr.CodeOf(cause), fmt.Errorf("new Project %q (%s) is incomplete: %w", project.Name, project.ID, cause))
	return clierr.WithHint(wrapped, "Run 'twt2 projects setup retry %s'.", project.Name)
}

// inferTemplateName selects a Project Template when --template is absent. It
// returns the template name and a short source description.
func inferTemplateName(command *cobra.Command, options Options, templateStore store.TemplateStore) (string, string, error) {
	names, err := templateStore.List()
	if err != nil {
		return "", "", err
	}
	if len(names) == 1 {
		return names[0], "only template", nil
	}
	last, err := store.LoadLastTemplate(options.StateDir)
	if err != nil {
		return "", "", err
	}
	if last != "" {
		for _, name := range names {
			if name == last {
				return last, "last used", nil
			}
		}
	}
	if len(names) == 0 {
		return "", "", invalidUsage(command, "no Project Templates exist; run 'twt2 templates create NAME' first")
	}
	return "", "", invalidUsage(command, "select a Project Template with --template TEMPLATE; available templates: %s", strings.Join(names, ", "))
}

func newProjectsPathCommand(service *projectservice.Service) *cobra.Command {
	return &cobra.Command{
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
			project, err := service.Find(args[0])
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
			now := time.Now().UTC()
			for _, project := range projects {
				reference := project.CreatedAt
				if project.Status == domain.ProjectArchived && project.ArchivedAt != nil {
					reference = *project.ArchivedAt
				}
				size := formatBytes(projectservice.DirectorySize(project.Root))
				if _, err := fmt.Fprintf(command.OutOrStdout(), "%s\t%s\t%s\t%s\t%s\n", project.Name, project.TemplateName, project.Status, formatAge(now.Sub(reference)), size); err != nil {
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
			_, err = fmt.Fprintf(command.OutOrStdout(), "Project: %s\nID: %s\nTemplate: %s\nStatus: %s\nRoot: %s\n", project.Name, project.ID, project.TemplateName, project.Status, project.Root)
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
		Root:          project.Root,
		Bytes:         projectservice.DirectorySize(project.Root),
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
