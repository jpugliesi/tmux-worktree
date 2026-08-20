package cli

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	projectservice "github.com/jpugliesi/tmux-worktree/internal/project"
	"github.com/jpugliesi/tmux-worktree/internal/store"
	"github.com/spf13/cobra"
)

func newQuickCreateCommand(options Options) *cobra.Command {
	service := projectservice.NewService(projectservice.Options{StateDir: options.StateDir, DataDir: options.DataDir, TmuxSocket: options.TmuxSocket})
	return &cobra.Command{
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
			if err != nil {
				return err
			}
			testHooks := options.QuickCreateSwitch != nil || options.QuickCreateArchive != nil
			if testHooks && (options.QuickCreateSwitch == nil || options.QuickCreateArchive == nil) {
				return fmt.Errorf("quick create test hooks are incomplete")
			}
			clientName := ""
			if !testHooks && !isDryRun(command) {
				clientName, err = callingTmuxClient(options, currentPane)
				if err != nil {
					return err
				}
			}
			template, err := store.NewTemplateStore(options.ConfigDir).Load(current.TemplateName)
			if err != nil {
				return err
			}
			name, err := quickCreateName(command, args)
			if err != nil {
				return err
			}
			if err := service.ValidateCreate(name, current.TemplateName, template); err != nil {
				return err
			}
			if isDryRun(command) {
				return writeMutation(command, "projects.quick_create", "valid", "", name)
			}

			created, err := service.Create(name, current.TemplateName, template)
			if created.EnvironmentID != "" {
				if refillErr := startPreparationRefill(options, current.TemplateName, template); refillErr != nil {
					_, _ = fmt.Fprintf(command.ErrOrStderr(), "Warning: the next Prepared Environment was not started: %v\n", refillErr)
				}
			}
			if err != nil {
				if created.ID != "" {
					return fmt.Errorf("new Project %q (%s) is incomplete: %w", created.Name, created.ID, err)
				}
				return err
			}
			if testHooks {
				if err := options.QuickCreateSwitch(created.TmuxSession); err != nil {
					return archiveNewAfterQuickCreateFailure(service, created.ID, created.Name, currentPane, fmt.Errorf("switch to new Project: %w", err))
				}
				if err := options.QuickCreateArchive(current.ID); err != nil {
					return fmt.Errorf("new Project %q is active, but old Project %q could not be archived: %w", created.Name, current.Name, err)
				}
				_, err = fmt.Fprintf(command.OutOrStdout(), "Created Project %q and archived Project %q\n", created.Name, current.Name)
				return err
			}

			helper, err := startQuickCreateHelper(options, service, current.ID, created.ID, clientName)
			if err != nil {
				return archiveNewAfterQuickCreateFailure(service, created.ID, created.Name, currentPane, fmt.Errorf("prepare old Project archive: %w", err))
			}
			if _, err := fmt.Fprintf(command.OutOrStdout(), "Created Project %q; switching to it and archiving Project %q\n", created.Name, current.Name); err != nil {
				helper.cancel()
				return archiveNewAfterQuickCreateFailure(service, created.ID, created.Name, currentPane, fmt.Errorf("write quick create result: %w", err))
			}
			if err := switchTmuxClient(options, clientName, helper.newSessionID); err != nil {
				helper.cancel()
				return archiveNewAfterQuickCreateFailure(service, created.ID, created.Name, currentPane, fmt.Errorf("switch to new Project: %w", err))
			}
			if err := helper.commit(); err != nil {
				return fmt.Errorf("new Project %q is active, but old Project %q archive signal failed: %w; use 'twt2 archive %s' if the archive failure window appears", created.Name, current.Name, err, current.ID)
			}
			return nil
		},
	}
}

func archiveNewAfterQuickCreateFailure(service *projectservice.Service, projectID, projectName, currentPane string, cause error) error {
	if _, err := service.Archive(projectID, currentPane); err != nil {
		return fmt.Errorf("%w; new Project %q also could not be archived: %v", cause, projectName, err)
	}
	return fmt.Errorf("%w; new Project %q was archived", cause, projectName)
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
