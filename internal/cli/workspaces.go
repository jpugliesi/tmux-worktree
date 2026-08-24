package cli

import (
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/jpugliesi/tmux-worktree/internal/clierr"
	"github.com/jpugliesi/tmux-worktree/internal/domain"
	workspaceservice "github.com/jpugliesi/tmux-worktree/internal/workspace"
	"github.com/spf13/cobra"
)

func newWorkspacesCommand(options Options) *cobra.Command {
	service := options.workspaceService()
	workspaces := groupCommand(&cobra.Command{Use: "workspaces", Aliases: []string{"w"}, Short: "Manage Workspaces"})
	workspaces.AddCommand(newWorkspacesCreateCommand(options, service))
	workspaces.AddCommand(newWorkspacesAdoptCommand(service))
	workspaces.AddCommand(newWorkspacesListCommand(service))
	workspaces.AddCommand(newWorkspacesShowCommand(service))
	workspaces.AddCommand(newWorkspacesCurrentCommand(service))
	workspaces.AddCommand(newWorkspacesPathCommand(service))
	workspaces.AddCommand(newWorkspacesOpenCommand(options, service))
	workspaces.AddCommand(newWorkspacesArchiveCommand(options, service))
	workspaces.AddCommand(newWorkspacesSetupCommand(service))
	workspaces.AddCommand(newWorkspacesRemoveCommand(service))
	return workspaces
}

func newWorkspacesListCommand(service *workspaceservice.Service) *cobra.Command {
	var limit, offset int
	command := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List Workspaces",
		Args:    noArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			workspaces, err := service.List()
			if err != nil {
				return err
			}
			sortWorkspacesForDisplay(workspaces)
			workspaces, total, truncated, err := applyWindow(workspaces, offset, limit)
			if err != nil {
				return err
			}
			if format := resolvedOutputFormat(command); format != outputText {
				values := make([]workspaceOutput, 0, len(workspaces))
				for _, workspace := range workspaces {
					values = append(values, toWorkspaceOutput(workspace))
				}
				if format == outputNDJSON {
					return writeNDJSONList(command, values, total, truncated)
				}
				return writeReadJSON(command, workspacesListOutput{SchemaVersion: jsonSchemaVersion, Workspaces: values, TotalCount: total, Truncated: truncated}, "workspaces")
			}
			if total == 0 {
				_, err = fmt.Fprintln(command.ErrOrStderr(), "No Workspaces exist. Run 'twt workspaces create NAME'.")
				return err
			}
			now := time.Now().UTC()
			rows := make([][]string, 0, len(workspaces))
			for _, workspace := range workspaces {
				rows = append(rows, []string{workspace.Name, workspace.TemplateName, string(workspace.Status), formatAge(now.Sub(workspaceAgeReference(workspace)))})
			}
			return writeTable(command.OutOrStdout(), []string{"NAME", "TEMPLATE", "STATUS", "AGE"}, rows)
		},
	}
	addListReadFlags(command, &limit, &offset, workspaceOutput{})
	return command
}

func newWorkspacesShowCommand(service *workspaceservice.Service) *cobra.Command {
	command := &cobra.Command{
		Use:   "show WORKSPACE",
		Short: "Show a Workspace",
		Args:  exactArgs("WORKSPACE"),
		RunE: func(command *cobra.Command, args []string) error {
			workspace, err := resolveWorkspace(service, args[0])
			if err != nil {
				return err
			}
			return writeWorkspace(command, workspace)
		},
	}
	setArguments(command, requiredArgument("workspace"))
	addFieldsFlag(command, workspaceOutput{})
	command.ValidArgsFunction = workspaceNameCompletion(service)
	return command
}

// writeWorkspace writes one Workspace as the show envelope or as text.
func writeWorkspace(command *cobra.Command, workspace domain.Workspace) error {
	if WantsJSON(command) {
		return writeReadJSON(command, workspaceShowOutput{SchemaVersion: jsonSchemaVersion, Workspace: toWorkspaceOutput(workspace)}, "workspace")
	}
	fields := [][2]string{
		{"Workspace", workspace.Name},
		{"ID", workspace.ID},
		{"Template", workspace.TemplateName},
		{"Status", string(workspace.Status)},
		{"Root", workspace.Root},
	}
	if workspace.Project != "" {
		fields = append(fields, [2]string{"Project", workspace.Project})
	}
	if len(workspace.Tickets) > 0 {
		fields = append(fields, [2]string{"Tickets", strings.Join(workspace.Tickets, ", ")})
	}
	return writeFields(command.OutOrStdout(), fields)
}

func newWorkspacesCurrentCommand(service *workspaceservice.Service) *cobra.Command {
	command := &cobra.Command{
		Use:   "current",
		Short: "Show the current Workspace",
		Args:  noArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			workspace, err := resolveWorkspace(service, currentWorkspaceReference)
			if err != nil {
				return err
			}
			if WantsJSON(command) {
				return writeReadJSON(command, workspaceShowOutput{SchemaVersion: jsonSchemaVersion, Workspace: toWorkspaceOutput(workspace)}, "workspace")
			}
			_, err = fmt.Fprintln(command.OutOrStdout(), workspace.Name)
			return err
		},
	}
	addFieldsFlag(command, workspaceOutput{})
	return command
}

func newWorkspacesPathCommand(service *workspaceservice.Service) *cobra.Command {
	command := &cobra.Command{
		Use:   "path WORKSPACE [REPO]",
		Short: "Print the Workspace root path or a repository checkout path",
		Args: func(command *cobra.Command, args []string) error {
			if len(args) < 1 {
				return invalidUsage(command, "missing required argument WORKSPACE")
			}
			if len(args) > 2 {
				return invalidUsage(command, "unexpected argument %q; expected WORKSPACE [REPO]", args[2])
			}
			return nil
		},
		RunE: func(command *cobra.Command, args []string) error {
			workspace, err := resolveWorkspace(service, args[0])
			if err != nil {
				return err
			}
			path := workspace.Root
			if len(args) == 2 {
				found := false
				for _, repository := range workspace.Repositories {
					if repository.Name == args[1] {
						path = repository.Path
						found = true
						break
					}
				}
				if !found {
					return clierr.New(clierr.NotFound, "repository %q is not in Workspace %q", args[1], workspace.Name)
				}
			}
			_, err = fmt.Fprintln(command.OutOrStdout(), path)
			return err
		},
	}
	setArguments(command, requiredArgument("workspace"), optionalArgument("repo", "the Workspace root when absent"))
	command.ValidArgsFunction = workspaceRepositoryCompletion(service)
	return command
}

func newWorkspacesOpenCommand(options Options, service *workspaceservice.Service) *cobra.Command {
	var noAttach bool
	command := &cobra.Command{
		Use:   "open WORKSPACE",
		Short: "Open or repair a Workspace tmux session",
		Args:  exactArgs("WORKSPACE"),
		RunE: func(command *cobra.Command, args []string) error {
			reference, err := resolveWorkspaceReference(service, args[0])
			if err != nil {
				return err
			}
			workspace, err := openWorkspaceSession(command, service, reference)
			if err != nil {
				return err
			}
			if isDryRun(command) || noAttach || !terminalWriter(command.OutOrStdout()) {
				return nil
			}
			return openTmux(options, workspace.TmuxSession)
		},
	}
	command.Flags().BoolVar(&noAttach, "no-attach", false, "Repair the session without attaching")
	setArguments(command, requiredArgument("workspace"))
	command.ValidArgsFunction = workspaceNameCompletion(service)
	return command
}

// openWorkspaceSession opens or repairs the tmux session of one Workspace and
// returns the Workspace. It attaches no tmux client; the caller attaches. Both
// the workspaces open command and apply use it.
func openWorkspaceSession(command *cobra.Command, service *workspaceservice.Service, reference string) (domain.Workspace, error) {
	var workspace domain.Workspace
	err := runMutation(command, "workspaces.open",
		func() (string, string, error) {
			return "", reference, service.ValidateOpen(reference)
		},
		func() (string, string, error) {
			var err error
			workspace, err = service.Open(reference)
			return workspace.ID, workspace.Name, err
		},
		func(out io.Writer, _, name string) error {
			_, err := fmt.Fprintf(out, "Opened Workspace %q\n", name)
			return err
		})
	return workspace, err
}

func newWorkspacesSetupCommand(service *workspaceservice.Service) *cobra.Command {
	setup := groupCommand(&cobra.Command{Use: "setup", Short: "Manage Workspace setup"})
	retry := &cobra.Command{
		Use:   "retry WORKSPACE",
		Short: "Retry incomplete Workspace setup steps",
		Args:  exactArgs("WORKSPACE"),
		RunE: func(command *cobra.Command, args []string) error {
			reference, err := resolveWorkspaceReference(service, args[0])
			if err != nil {
				return err
			}
			return retryWorkspaceSetup(command, service, reference)
		},
	}
	setArguments(retry, requiredArgument("workspace"))
	retry.ValidArgsFunction = workspaceNameCompletion(service)
	setup.AddCommand(retry)
	return setup
}

// retryWorkspaceSetup runs the incomplete setup steps of one Workspace. Both the
// workspaces setup retry command and apply use it.
func retryWorkspaceSetup(command *cobra.Command, service *workspaceservice.Service, reference string) error {
	return runMutation(command, "workspaces.setup.retry",
		func() (string, string, error) {
			return "", reference, service.ValidateRetry(reference)
		},
		func() (string, string, error) {
			workspace, err := service.Retry(reference)
			return workspace.ID, workspace.Name, err
		},
		func(out io.Writer, _, name string) error {
			_, err := fmt.Fprintf(out, "Workspace %q setup is complete\n", name)
			return err
		})
}
