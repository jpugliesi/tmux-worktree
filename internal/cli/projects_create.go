package cli

import (
	"fmt"
	"io"
	"strings"

	"github.com/jpugliesi/tmux-worktree/internal/clierr"
	"github.com/jpugliesi/tmux-worktree/internal/domain"
	ticketservice "github.com/jpugliesi/tmux-worktree/internal/ticket"
	workspaceservice "github.com/jpugliesi/tmux-worktree/internal/workspace"
	"github.com/spf13/cobra"
)

// runProjectsCreate creates one Project. With no NAME in a text terminal it
// asks for the name, opens VISUAL or EDITOR on an empty plan file, then asks
// whether to start a Workspace. NAME skips the wizard.
func runProjectsCreate(command *cobra.Command, options Options, service ticketservice.Store, name, templateName string) error {
	if templateName != "" {
		if _, err := options.templateStore().Load(templateName); err != nil {
			return err
		}
	}
	wizard := strings.TrimSpace(name) == ""
	if wizard && !canPromptWorkspaceName(command) {
		return invalidUsage(command, "missing Project name; pass NAME in a script")
	}
	if wizard {
		prompted, err := promptTicketLine(command, "Project name: ")
		if err != nil {
			return err
		}
		if prompted == "" {
			return invalidUsageWithHint(command, "Pass NAME, or type a Project name.",
				"Project creation was canceled; no name was given")
		}
		name = prompted
		if _, err := service.Project(name); err == nil {
			return clierr.WithHint(
				clierr.New(clierr.AlreadyExists, "Project %q already exists", name),
				"Edit its plan with 'twt projects plan %s'.", name)
		} else if clierr.CodeOf(err) != clierr.NotFound {
			return err
		}
		if _, err := service.CreateProjectWithTemplate(name, templateName, true); err != nil {
			return err
		}
	}
	plan := ""
	startWorkspace := false
	workspaceName := ""
	if wizard {
		content, err := readPlanDraftInEditor(command, options, "",
			"Write the plan and save the file.")
		if err != nil {
			return err
		}
		plan = content
		if strings.TrimSpace(templateName) == "" {
			names, listErr := options.templateStore().List()
			if listErr != nil {
				return listErr
			}
			if len(names) > 0 {
				inferred, source, inferErr := inferTemplateName(command, options, options.templateStore())
				if inferErr != nil {
					return inferErr
				}
				templateName = inferred
				if !WantsJSON(command) {
					_, _ = fmt.Fprintf(command.ErrOrStderr(), "Template: %s (%s)\n", templateName, source)
				}
			}
		}
		startWorkspace, err = confirmStartWorkspace(command)
		if err != nil {
			return err
		}
		if startWorkspace {
			workspaceName, err = promptProjectWorkspaceName(command, name)
			if err != nil {
				return err
			}
		}
	}
	if err := createProject(command, options, service, name, templateName); err != nil {
		return err
	}
	if !wizard {
		return nil
	}
	if err := writeWizardProjectPlan(command, service, name, plan); err != nil {
		return err
	}
	if !startWorkspace {
		return nil
	}
	return startWorkspaceForNewProject(command, options, templateName, workspaceName)
}

// writeWizardProjectPlan writes the plan from the create wizard. A dry run
// reports valid without requiring the Project to exist on disk.
func writeWizardProjectPlan(command *cobra.Command, service ticketservice.Store, name, plan string) error {
	if isDryRun(command) {
		return writeMutation(command, "projects.plan", statusValid, name, name)
	}
	return editProjectPlan(command, service, name, plan)
}

// confirmStartWorkspace asks whether to create a Workspace after a Project.
// Enter means yes.
func confirmStartWorkspace(command *cobra.Command) (bool, error) {
	if _, err := fmt.Fprint(command.ErrOrStderr(), "Start a new Workspace? [Y/n] "); err != nil {
		return false, err
	}
	line, err := readTicketPromptLine(command)
	if err != nil {
		return false, err
	}
	yes, ok := parseYesDefault(line)
	if !ok {
		return false, invalidUsageWithHint(command, "Answer y or n.",
			"unrecognized confirm answer %q", line)
	}
	return yes, nil
}

// promptProjectWorkspaceName asks for a Workspace name. Enter uses the Project
// name.
func promptProjectWorkspaceName(command *cobra.Command, projectName string) (string, error) {
	if _, err := fmt.Fprintf(command.ErrOrStderr(), "Workspace name [%s]: ", projectName); err != nil {
		return "", err
	}
	line, err := readTicketPromptLine(command)
	if err != nil {
		return "", err
	}
	if line == "" {
		return projectName, nil
	}
	return line, nil
}

// startWorkspaceForNewProject creates one Workspace for a new Project. It
// uses the Template the wizard already selected, or infers one.
func startWorkspaceForNewProject(command *cobra.Command, options Options, templateName, workspaceName string) error {
	templateStore := options.templateStore()
	selected, source, err := resolveCreateTemplate(command, options, templateName, "")
	if err != nil {
		return err
	}
	if strings.TrimSpace(templateName) == "" && !WantsJSON(command) {
		_, _ = fmt.Fprintf(command.ErrOrStderr(), "Template: %s (%s)\n", selected, source)
	}
	template, err := templateStore.Load(selected)
	if err != nil {
		return err
	}
	createOptions := workspaceservice.CreateOptions{}
	var workspace domain.Workspace
	if err := runMutation(command, "workspaces.create",
		func() (string, string, error) {
			return "", workspaceName, validateCreate(options, options.workspaceService(), workspaceName, selected, template, createOptions)
		},
		func() (string, string, error) {
			var err error
			workspace, err = createWorkspace(command, options, workspaceName, selected, template, createOptions)
			return workspace.ID, workspace.Name, err
		},
		func(out io.Writer, _, _ string) error {
			_, err := fmt.Fprintf(out, "Created Workspace %q (%s)\nRoot: %s\n", workspace.Name, workspace.ID, workspace.Root)
			return err
		}); err != nil {
		return err
	}
	if isDryRun(command) || !terminalWriter(command.OutOrStdout()) {
		return nil
	}
	return openTmux(options, workspace.TmuxSession)
}
