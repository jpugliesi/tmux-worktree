package cli

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/jpugliesi/tmux-worktree/internal/domain"
	projectservice "github.com/jpugliesi/tmux-worktree/internal/project"
	"github.com/jpugliesi/tmux-worktree/internal/store"
	"github.com/spf13/cobra"
)

func newQuickCreateCommand(options Options) *cobra.Command {
	service := projectservice.NewService(projectservice.Options{StateDir: options.StateDir, DataDir: options.DataDir, TmuxSocket: options.TmuxSocket})
	var templateName string
	var keepCurrent bool
	var noFetch bool
	var branch string
	command := &cobra.Command{
		Use:   "create [NAME]",
		Short: "Create a new Project and archive the current Project",
		Args:  optionalArg("NAME"),
		PreRunE: func(command *cobra.Command, _ []string) error {
			if WantsJSON(command) {
				return invalidUsage(command, "quick create uses interactive text output; use 'twt2 projects create' for JSON automation")
			}
			return nil
		},
		RunE: func(command *cobra.Command, args []string) error {
			currentPane := os.Getenv("TMUX_PANE")
			current, err := service.CurrentFromPane(currentPane)
			outside := false
			if err != nil {
				if !errors.Is(err, projectservice.ErrNotInProject) {
					return err
				}
				outside = true
			}
			testHooks := options.QuickCreateSwitch != nil || options.QuickCreateArchive != nil
			if testHooks && (options.QuickCreateSwitch == nil || options.QuickCreateArchive == nil) {
				return fmt.Errorf("quick create test hooks are incomplete")
			}
			templateStore := store.NewTemplateStore(options.ConfigDir)
			selected := strings.TrimSpace(templateName)
			if selected == "" {
				if outside {
					inferred, source, err := inferTemplateName(command, options, templateStore)
					if err != nil {
						return err
					}
					selected = inferred
					_, _ = fmt.Fprintf(command.ErrOrStderr(), "Template: %s (%s)\n", selected, source)
				} else {
					selected = current.TemplateName
				}
			}
			clientName := ""
			if !outside && !testHooks && !isDryRun(command) {
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
				if err := service.ValidateCreate(name, selected, template); err != nil {
					return err
				}
				return writeMutation(command, "projects.quick_create", "valid", "", name)
			}

			createService := newCreateService(command, options, selected, template)
			created, err := createService.CreateWithOptions(name, selected, template, projectservice.CreateOptions{Branch: branch, NoFetch: noFetch})
			if err != nil {
				return createFailureError(created, err)
			}
			_ = store.SaveLastTemplate(options.StateDir, selected)

			if outside {
				if _, err := fmt.Fprintf(command.OutOrStdout(), "Created Project %q (%s)\n", created.Name, created.ID); err != nil {
					return err
				}
				if testHooks {
					if err := options.QuickCreateSwitch(created.TmuxSession); err != nil {
						return quickCreateSwitchFailure(created, err)
					}
					return nil
				}
				return openTmux(options, created.TmuxSession)
			}

			if !keepCurrent {
				if liveAgents, liveErr := service.LiveAgents(current.ID); liveErr == nil {
					if err := printStoppedAgents(command.OutOrStdout(), liveAgents); err != nil {
						return err
					}
				}
			}
			if testHooks {
				if err := options.QuickCreateSwitch(created.TmuxSession); err != nil {
					return quickCreateSwitchFailure(created, err)
				}
				if keepCurrent {
					_, err = fmt.Fprintf(command.OutOrStdout(), "Created Project %q; Project %q stays active\n", created.Name, current.Name)
					return err
				}
				if err := options.QuickCreateArchive(current.ID); err != nil {
					return fmt.Errorf("new Project %q is active, but old Project %q could not be archived: %w", created.Name, current.Name, err)
				}
				_, err = fmt.Fprintf(command.OutOrStdout(), "Created Project %q and archived Project %q\n", created.Name, current.Name)
				return err
			}

			if keepCurrent {
				if _, err := fmt.Fprintf(command.OutOrStdout(), "Created Project %q; switching to it; Project %q stays active\n", created.Name, current.Name); err != nil {
					return err
				}
				newSessionID, err := service.OwnedSessionID(created.ID)
				if err != nil {
					return quickCreateSwitchFailure(created, err)
				}
				if err := switchTmuxClient(options, clientName, newSessionID); err != nil {
					return quickCreateSwitchFailure(created, err)
				}
				return nil
			}

			helper, err := startQuickCreateHelper(options, service, current.ID, created.ID, clientName)
			if err != nil {
				return quickCreateSwitchFailure(created, fmt.Errorf("prepare old Project archive: %w", err))
			}
			if _, err := fmt.Fprintf(command.OutOrStdout(), "Created Project %q; switching to it and archiving Project %q\n", created.Name, current.Name); err != nil {
				helper.cancel()
				return quickCreateSwitchFailure(created, fmt.Errorf("write quick create result: %w", err))
			}
			if err := switchTmuxClient(options, clientName, helper.newSessionID); err != nil {
				helper.cancel()
				return quickCreateSwitchFailure(created, err)
			}
			if err := helper.commit(); err != nil {
				return fmt.Errorf("new Project %q is active, but old Project %q archive signal failed: %w; use 'twt2 archive %s' if the archive failure window appears", created.Name, current.Name, err, current.ID)
			}
			return nil
		},
	}
	command.Flags().StringVar(&templateName, "template", "", "Select the Project Template instead of the current Project's template")
	command.Flags().BoolVar(&keepCurrent, "keep-current", false, "Switch to the new Project and keep the current Project active")
	command.Flags().BoolVar(&noFetch, "no-fetch", false, "Do not refresh the default branch before the claim")
	command.Flags().StringVar(&branch, "branch", "", "Set a custom Project branch name")
	return command
}

// quickCreateSwitchFailure keeps the new Project active after a failed tmux
// switch and tells the user how to open it.
func quickCreateSwitchFailure(created domain.Project, cause error) error {
	return fmt.Errorf("twt2 could not switch to the new Project: %w. The new Project %q is active. Run 'twt2 projects open %s'.", cause, created.Name, created.Name)
}

func quickCreateName(command *cobra.Command, args []string) (string, error) {
	if len(args) == 1 {
		return args[0], nil
	}
	if !interactiveInput(command.InOrStdin()) {
		return "", invalidUsage(command, "missing Project name; use 'twt2 create NAME' in a script")
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

func interactiveInput(input io.Reader) bool {
	if input != os.Stdin {
		return true
	}
	info, err := os.Stdin.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}
