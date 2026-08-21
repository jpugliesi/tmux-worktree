package cli

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	agentservice "github.com/jpugliesi/tmux-worktree/internal/agent"
	"github.com/jpugliesi/tmux-worktree/internal/domain"
	projectservice "github.com/jpugliesi/tmux-worktree/internal/project"
	"github.com/spf13/cobra"
)

func newQuickCreateCommand(options Options) *cobra.Command {
	service := options.projectService()
	var templateName string
	var keepCurrent bool
	var noFetch bool
	var branch string
	command := &cobra.Command{
		Use:   "new [NAME]",
		Short: "Create a new Project and archive the current Project",
		Args:  optionalArg("NAME"),
		PreRunE: func(command *cobra.Command, _ []string) error {
			if WantsJSON(command) {
				return invalidUsage(command, "quick create uses interactive text output; use 'twt projects create' for JSON automation")
			}
			return nil
		},
		RunE: func(command *cobra.Command, args []string) error {
			currentPane := os.Getenv("TMUX_PANE")
			directory, err := os.Getwd()
			if err != nil {
				return err
			}
			current, err := service.CurrentForQuickCreate(directory, os.Getenv("TWT_PROJECT_ID"), currentPane)
			known := err == nil
			if err != nil && !errors.Is(err, projectservice.ErrNotInProject) {
				return err
			}
			// The tmux client switch and the archive of the current Project
			// need the calling pane. Without a pane, quick create uses the
			// outside-session flow and keeps the current Project active.
			outside := !known || currentPane == ""
			templateStore := options.templateStore()
			selected := strings.TrimSpace(templateName)
			if selected == "" {
				if known {
					selected = current.TemplateName
				} else {
					inferred, source, err := inferTemplateName(command, options, templateStore)
					if err != nil {
						return err
					}
					selected = inferred
					_, _ = fmt.Fprintf(command.ErrOrStderr(), "Template: %s (%s)\n", selected, source)
				}
			}
			clientName := ""
			if !outside && !isDryRun(command) {
				clientName, err = callingTmuxClient(options, currentPane)
				if err != nil {
					return err
				}
			}
			template, err := templateStore.Load(selected)
			if err != nil {
				return err
			}
			name, err := quickCreateName(command, args)
			if err != nil {
				return err
			}
			if isDryRun(command) {
				if err := validateCreate(options, service, name, selected, template, projectservice.CreateOptions{Branch: branch, NoFetch: noFetch}); err != nil {
					return err
				}
				return writeMutation(command, "projects.quick_create", statusValid, "", name)
			}

			created, err := createProject(command, options, name, selected, template, projectservice.CreateOptions{Branch: branch, NoFetch: noFetch})
			if err != nil {
				return err
			}
			out := command.OutOrStdout()

			if outside {
				if _, err := fmt.Fprintf(out, "Created Project %q (%s)\n", created.Name, created.ID); err != nil {
					return err
				}
				if err := options.QuickCreateSwitch("", created.TmuxSession); err != nil {
					return quickCreateSwitchFailure(created, err)
				}
				return nil
			}

			if !keepCurrent {
				if liveAgents, liveErr := agentservice.NewService(options.StateDir, options.TmuxSocket).Live(current.ID); liveErr == nil {
					if err := printStoppedAgents(out, liveAgents); err != nil {
						return err
					}
				}
			}
			message := fmt.Sprintf("Created Project %q; switching to it and archiving Project %q\n", created.Name, current.Name)
			if keepCurrent {
				message = fmt.Sprintf("Created Project %q; switching to it; Project %q stays active\n", created.Name, current.Name)
			}
			if _, err := fmt.Fprint(out, message); err != nil {
				return quickCreateSwitchFailure(created, fmt.Errorf("write quick create result: %w", err))
			}
			if err := options.QuickCreateSwitch(clientName, created.TmuxSession); err != nil {
				return quickCreateSwitchFailure(created, err)
			}
			if keepCurrent {
				return nil
			}
			if err := options.QuickCreateArchive(clientName, current.ID, created.ID); err != nil {
				return fmt.Errorf("new Project %q is active, but old Project %q was not archived: %w; run 'twt archive %s' if the archive failure window appears", created.Name, current.Name, err, current.ID)
			}
			return nil
		},
	}
	command.Flags().StringVar(&templateName, "template", "", "Select the Project Template instead of the current Project's template")
	command.Flags().BoolVar(&keepCurrent, "keep-current", false, "Switch to the new Project and keep the current Project active")
	command.Flags().BoolVar(&noFetch, "no-fetch", false, "Do not refresh the default branch before the claim")
	command.Flags().StringVar(&branch, "branch", "", "Set a custom Project branch name")
	setArguments(command, optionalArgument("name", "the interactive prompt asks for it when absent"))
	_ = command.RegisterFlagCompletionFunc("template", templateFlagCompletion(options.templateStore()))
	return command
}

// quickCreateSwitchFailure keeps the new Project active after a failed tmux
// switch and tells the user how to open it.
func quickCreateSwitchFailure(created domain.Project, cause error) error {
	return fmt.Errorf("twt could not switch to the new Project: %w. The new Project %q is active. Run 'twt projects open %s'.", cause, created.Name, created.Name)
}

func quickCreateName(command *cobra.Command, args []string) (string, error) {
	if len(args) == 1 {
		return args[0], nil
	}
	if !interactiveInput(command.InOrStdin()) {
		return "", invalidUsage(command, "missing Project name; use 'twt new NAME' in a script")
	}
	if _, err := fmt.Fprint(command.ErrOrStderr(), "Project name: "); err != nil {
		return "", err
	}
	line, err := bufio.NewReader(command.InOrStdin()).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", fmt.Errorf("read Project name: %w", err)
	}
	name := strings.TrimSpace(line)
	if name == "" {
		return "", invalidUsage(command, "Project creation was canceled; no Project name was given")
	}
	return name, nil
}
