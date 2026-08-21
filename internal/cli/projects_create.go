package cli

import (
	"fmt"
	"io"
	"strings"

	"github.com/jpugliesi/tmux-worktree/internal/clierr"
	"github.com/jpugliesi/tmux-worktree/internal/domain"
	projectservice "github.com/jpugliesi/tmux-worktree/internal/project"
	"github.com/jpugliesi/tmux-worktree/internal/store"
	"github.com/spf13/cobra"
)

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
			templateStore := options.templateStore()
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
			var project domain.Project
			if err := runMutation(command, "projects.create",
				func() (string, string, error) {
					return "", args[0], validateCreate(options, service, args[0], selected, template, projectservice.CreateOptions{Branch: branch, NoFetch: noFetch})
				},
				func() (string, string, error) {
					var err error
					project, err = createProject(command, options, args[0], selected, template, projectservice.CreateOptions{Branch: branch, NoFetch: noFetch})
					return project.ID, project.Name, err
				},
				func(out io.Writer, _, _ string) error {
					_, err := fmt.Fprintf(out, "Created Project %q (%s)\nRoot: %s\n", project.Name, project.ID, project.Root)
					return err
				}); err != nil {
				return err
			}
			if isDryRun(command) || noOpen || !terminalWriter(command.OutOrStdout()) {
				return nil
			}
			return openTmux(options, project.TmuxSession)
		},
	}
	command.Flags().StringVar(&templateName, "template", "", "Select the Project Template")
	command.Flags().BoolVar(&noOpen, "no-open", false, "Do not open the tmux session")
	command.Flags().BoolVar(&noFetch, "no-fetch", false, "Do not refresh the default branch before the claim")
	command.Flags().StringVar(&branch, "branch", "", "Set a custom Project branch name")
	setArguments(command, requiredArgument("name"))
	_ = command.RegisterFlagCompletionFunc("template", templateFlagCompletion(options.templateStore()))
	return command
}

// createProject creates one Project through the shared progress, Prepared
// Environment refill, and last-template path. Every create entry point (the
// projects create command, quick create, and apply) uses it. It resolves the
// user branch prefix for the {prefix} token of the Project branch pattern.
// validateCreate is the dry-run twin of createProject: it validates the same
// branch selection, so a valid dry run never precedes a refused create.
func validateCreate(options Options, service *projectservice.Service, name, templateName string, template domain.Template, createOptions projectservice.CreateOptions) error {
	prefix, err := options.resolveBranchPrefix()
	if err != nil {
		return err
	}
	createOptions.BranchPrefix = prefix
	return service.ValidateCreateWithOptions(name, templateName, template, createOptions)
}

func createProject(command *cobra.Command, options Options, name, templateName string, template domain.Template, createOptions projectservice.CreateOptions) (domain.Project, error) {
	prefix, err := options.resolveBranchPrefix()
	if err != nil {
		return domain.Project{}, err
	}
	createOptions.BranchPrefix = prefix
	service := newCreateService(command, options, templateName, template)
	project, err := service.CreateWithOptions(name, templateName, template, createOptions)
	if err != nil {
		return project, createFailureError(project, err)
	}
	_ = store.SaveLastTemplate(options.StateDir, templateName)
	return project, nil
}

// newCreateService builds a Project service that reports progress and starts
// the background Prepared Environment pool refill for one creation.
func newCreateService(command *cobra.Command, options Options, templateName string, template domain.Template) *projectservice.Service {
	serviceOptions := options.projectServiceOptions()
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
	return clierr.WithHint(wrapped, "Run 'twt projects setup retry %s'.", project.Name)
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
		return "", "", invalidUsage(command, "no Project Templates exist; run 'twt templates create NAME' first")
	}
	return "", "", invalidUsage(command, "select a Project Template with --template TEMPLATE; available templates: %s", strings.Join(names, ", "))
}
