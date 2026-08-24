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
	var project, ticket, status string
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
			if status != "" && !validWorkspaceStatus(status) {
				return invalidUsage(command, "status %q is not valid; use one of: %s", status, strings.Join(workspaceStatusNames(), ", "))
			}
			workspaces = filterWorkspaces(workspaces, project, ticket, status, command.Flags().Changed("project"), command.Flags().Changed("ticket"))
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
				_, err = fmt.Fprintln(command.ErrOrStderr(), "No Workspaces exist. Run 'twt create NAME'.")
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
	command.Flags().StringVar(&project, "project", "", "List Workspaces linked to one Project")
	command.Flags().StringVar(&ticket, "ticket", "", "List Workspaces linked to one Ticket slug")
	command.Flags().StringVar(&status, "status", "", "List one Workspace status")
	setFlagEnum(command, "status", workspaceStatusNames()...)
	return command
}

func workspaceStatusNames() []string {
	return []string{
		string(domain.WorkspaceActive),
		string(domain.WorkspaceArchived),
		string(domain.WorkspaceInitializing),
		string(domain.WorkspaceRemoving),
		string(domain.WorkspaceSetupFailed),
	}
}

func validWorkspaceStatus(status string) bool {
	for _, name := range workspaceStatusNames() {
		if status == name {
			return true
		}
	}
	return false
}

func filterWorkspaces(workspaces []domain.Workspace, project, ticket, status string, projectSet, ticketSet bool) []domain.Workspace {
	filtered := make([]domain.Workspace, 0, len(workspaces))
	for _, workspace := range workspaces {
		if projectSet && workspace.Project != project {
			continue
		}
		if ticketSet && !workspaceHasTicket(workspace, ticket) {
			continue
		}
		if status != "" && string(workspace.Status) != status {
			continue
		}
		filtered = append(filtered, workspace)
	}
	return filtered
}

func workspaceHasTicket(workspace domain.Workspace, slug string) bool {
	for _, linked := range workspace.Tickets {
		if linked == slug {
			return true
		}
	}
	return false
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
	var allActive bool
	command := &cobra.Command{
		Use:   "open [WORKSPACE]",
		Short: "Open or repair a Workspace tmux session",
		Args:  optionalArg("WORKSPACE"),
		RunE: func(command *cobra.Command, args []string) error {
			if allActive {
				if len(args) != 0 {
					return invalidUsage(command, "do not use --all-active together with a WORKSPACE argument")
				}
				return openAllActiveSessions(command, service)
			}
			if len(args) != 1 {
				return invalidUsage(command, "missing required argument WORKSPACE")
			}
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
	command.Flags().BoolVar(&allActive, "all-active", false, "Repair the tmux session of every active Workspace and attach none")
	setArguments(command, optionalArgument("workspace", "required when --all-active is not set"))
	command.ValidArgsFunction = workspaceNameCompletion(service)
	return command
}

type bulkOpenOutput struct {
	SchemaVersion int                 `json:"schemaVersion"`
	Operation     string              `json:"operation"`
	Status        string              `json:"status"`
	Workspaces    []bulkOpenWorkspace `json:"workspaces"`
}

type bulkOpenWorkspace struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// openAllActiveSessions repairs the tmux session of every active Workspace.
// It never attaches a tmux client.
func openAllActiveSessions(command *cobra.Command, service *workspaceservice.Service) error {
	workspaces, err := service.List()
	if err != nil {
		return err
	}
	active := make([]domain.Workspace, 0, len(workspaces))
	for _, workspace := range workspaces {
		if workspace.Status == domain.WorkspaceActive {
			active = append(active, workspace)
		}
	}
	sortWorkspacesForDisplay(active)
	if isDryRun(command) {
		for _, workspace := range active {
			if err := service.ValidateOpen(workspace.ID); err != nil {
				return err
			}
		}
		return writeBulkOpen(command, statusValid, active)
	}
	opened := make([]domain.Workspace, 0, len(active))
	for _, workspace := range active {
		result, err := service.Open(workspace.ID)
		if err != nil {
			return err
		}
		opened = append(opened, result)
	}
	return writeBulkOpen(command, statusApplied, opened)
}

func writeBulkOpen(command *cobra.Command, status string, workspaces []domain.Workspace) error {
	if WantsJSON(command) {
		items := make([]bulkOpenWorkspace, 0, len(workspaces))
		for _, workspace := range workspaces {
			items = append(items, bulkOpenWorkspace{ID: workspace.ID, Name: workspace.Name})
		}
		return writeJSONOutput(command, bulkOpenOutput{
			SchemaVersion: jsonSchemaVersion,
			Operation:     "workspaces.open",
			Status:        status,
			Workspaces:    items,
		})
	}
	if len(workspaces) == 0 {
		_, err := fmt.Fprintln(command.OutOrStdout(), "No active Workspaces.")
		return err
	}
	verb := "Opened"
	if status == statusValid {
		verb = "Would open"
	}
	if _, err := fmt.Fprintf(command.OutOrStdout(), "%s %d active Workspace", verb, len(workspaces)); err != nil {
		return err
	}
	if len(workspaces) != 1 {
		if _, err := fmt.Fprint(command.OutOrStdout(), "s"); err != nil {
			return err
		}
	}
	if _, err := fmt.Fprint(command.OutOrStdout(), ":"); err != nil {
		return err
	}
	for _, workspace := range workspaces {
		if _, err := fmt.Fprintf(command.OutOrStdout(), " %s", workspace.Name); err != nil {
			return err
		}
	}
	_, err := fmt.Fprintln(command.OutOrStdout())
	return err
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
