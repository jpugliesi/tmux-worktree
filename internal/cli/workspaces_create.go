package cli

import (
	"fmt"
	"io"
	"strings"

	"github.com/jpugliesi/tmux-worktree/internal/clierr"
	"github.com/jpugliesi/tmux-worktree/internal/domain"
	"github.com/jpugliesi/tmux-worktree/internal/store"
	workspaceservice "github.com/jpugliesi/tmux-worktree/internal/workspace"
	"github.com/spf13/cobra"
)

func newWorkspacesCreateCommand(options Options, service *workspaceservice.Service) *cobra.Command {
	var templateName string
	var noOpen bool
	var fresh bool
	var branch string
	var ticketRefs []string
	command := &cobra.Command{
		Use:   "create [NAME]",
		Short: "Create a Workspace from a Workspace Template",
		Args:  optionalArg("NAME"),
		RunE: func(command *cobra.Command, args []string) error {
			name := ""
			if len(args) > 0 {
				name = args[0]
			} else if !canPromptWorkspaceName(command) {
				return invalidUsage(command, "missing Workspace name; use '%s NAME' in a script", command.CommandPath())
			}
			project, tickets, err := resolveWorkspaceTicketLinks(options, ticketRefs)
			if err != nil {
				return err
			}
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
			if name == "" {
				name, err = promptWorkspaceName(command, fmt.Sprintf("missing Workspace name; use '%s NAME' in a script", command.CommandPath()))
				if err != nil {
					return err
				}
			}
			createOptions := workspaceservice.CreateOptions{Branch: branch, Fresh: fresh, Project: project, Tickets: tickets}
			var workspace domain.Workspace
			if err := runMutation(command, "workspaces.create",
				func() (string, string, error) {
					return "", name, validateCreate(options, service, name, selected, template, createOptions)
				},
				func() (string, string, error) {
					var err error
					workspace, err = createWorkspace(command, options, name, selected, template, createOptions)
					return workspace.ID, workspace.Name, err
				},
				func(out io.Writer, _, _ string) error {
					_, err := fmt.Fprintf(out, "Created Workspace %q (%s)\nRoot: %s\n", workspace.Name, workspace.ID, workspace.Root)
					return err
				}); err != nil {
				return err
			}
			if isDryRun(command) || noOpen || !terminalWriter(command.OutOrStdout()) {
				return nil
			}
			return openTmux(options, workspace.TmuxSession)
		},
	}
	command.Flags().StringVar(&templateName, "template", "", "Select the Workspace Template")
	command.Flags().BoolVar(&noOpen, "no-open", false, "Do not open the tmux session")
	command.Flags().BoolVar(&fresh, "fresh", false, "Fetch the default branch before the claim")
	command.Flags().StringVar(&branch, "branch", "", "Set a custom Workspace branch name")
	command.Flags().StringArrayVar(&ticketRefs, "ticket", nil, "Link a Ticket; repeat for more Tickets")
	setArguments(command, optionalArgument("name", "the prompt asks for it when stdin is a terminal and output is text"))
	_ = command.RegisterFlagCompletionFunc("template", templateFlagCompletion(options.templateStore()))
	_ = command.RegisterFlagCompletionFunc("ticket", ticketFlagCompletion(options))
	return command
}

func resolveWorkspaceTicketLinks(options Options, refs []string) (string, []string, error) {
	if len(refs) == 0 {
		return "", nil, nil
	}
	tickets, err := resolveStartTicketRefs(options, refs)
	if err != nil {
		return "", nil, err
	}
	project, err := validateStartTickets(tickets)
	if err != nil {
		return "", nil, err
	}
	slugs := make([]string, 0, len(tickets))
	for _, ticket := range tickets {
		slugs = append(slugs, ticket.Slug)
	}
	return project, slugs, nil
}

func ticketFlagCompletion(options Options) completionFunc {
	return func(_ *cobra.Command, _ []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		service, err := options.ticketService()
		if err != nil {
			return nil, noFileCompletion
		}
		slugs, err := service.Slugs()
		if err != nil {
			return nil, noFileCompletion
		}
		return matching(slugs, toComplete), noFileCompletion
	}
}

// createWorkspace creates one Workspace through the shared progress, Prepared
// Environment refill, and last-template path. Every create entry point (the
// workspaces create command, quick create, and apply) uses it. It resolves the
// user branch prefix for the {prefix} token of the Workspace branch pattern.
// validateCreate is the dry-run twin of createWorkspace: it validates the same
// branch selection, so a valid dry run never precedes a refused create.
func validateCreate(options Options, service *workspaceservice.Service, name, templateName string, template domain.Template, createOptions workspaceservice.CreateOptions) error {
	prefix, err := options.resolveBranchPrefix()
	if err != nil {
		return err
	}
	createOptions.BranchPrefix = prefix
	return service.ValidateCreateWithOptions(name, templateName, template, createOptions)
}

func createWorkspace(command *cobra.Command, options Options, name, templateName string, template domain.Template, createOptions workspaceservice.CreateOptions) (domain.Workspace, error) {
	prefix, err := options.resolveBranchPrefix()
	if err != nil {
		return domain.Workspace{}, err
	}
	createOptions.BranchPrefix = prefix
	service := newCreateService(command, options, templateName, template)
	workspace, err := service.CreateWithOptions(name, templateName, template, createOptions)
	if err != nil {
		return workspace, createFailureError(workspace, err)
	}
	_ = store.SaveLastTemplate(options.StateDir, templateName)
	if err := stampTicketWorkspaces(command, options, workspace); err != nil {
		return workspace, err
	}
	return workspace, nil
}

// stampTicketWorkspaces writes the Workspace ID onto each linked Ticket. The
// stamp is the join key for coordinator reads. A stamp failure after create
// keeps the Workspace, the same as a start comment failure after claim.
func stampTicketWorkspaces(command *cobra.Command, options Options, workspace domain.Workspace) error {
	if len(workspace.Tickets) == 0 || strings.TrimSpace(workspace.ID) == "" {
		return nil
	}
	service, err := options.ticketService()
	if err != nil {
		return err
	}
	for _, slug := range workspace.Tickets {
		if _, err := service.SetWorkspace(slug, workspace.ID, false); err != nil {
			_, _ = fmt.Fprintf(command.ErrOrStderr(), "Warning: twt could not stamp Workspace %q on Ticket %q: %v\n", workspace.ID, slug, err)
		}
	}
	return nil
}

// newCreateService builds a Workspace service that reports progress and starts
// the background Prepared Environment pool refill for one creation.
func newCreateService(command *cobra.Command, options Options, templateName string, template domain.Template) *workspaceservice.Service {
	serviceOptions := options.workspaceServiceOptions()
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
	return workspaceservice.NewService(serviceOptions)
}

// createFailureError adds the setup retry hint when creation kept a Workspace
// record that the user can repair.
func createFailureError(workspace domain.Workspace, cause error) error {
	if workspace.ID == "" {
		return cause
	}
	wrapped := clierr.Wrap(clierr.CodeOf(cause), fmt.Errorf("new Workspace %q (%s) is incomplete: %w", workspace.Name, workspace.ID, cause))
	return clierr.WithHint(wrapped, "Run 'twt workspaces setup retry %s'.", workspace.Name)
}

// inferTemplateName selects a Workspace Template when --template is absent. It
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
		return "", "", invalidUsage(command, "no Workspace Templates exist; run 'twt templates create NAME' first")
	}
	return "", "", invalidUsage(command, "select a Workspace Template with --template TEMPLATE; available templates: %s", strings.Join(names, ", "))
}
