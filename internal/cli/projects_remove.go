package cli

import (
	"fmt"
	"io"
	"strings"

	"github.com/jpugliesi/tmux-worktree/internal/clierr"
	"github.com/jpugliesi/tmux-worktree/internal/store"
	ticketservice "github.com/jpugliesi/tmux-worktree/internal/ticket"
	"github.com/spf13/cobra"
)

const projectRemovalWorkspaceLinked = "workspace_linked"

type projectRemovalOutput struct {
	SchemaVersion int                                   `json:"schemaVersion"`
	Applied       bool                                  `json:"applied"`
	Plan          ticketservice.ProjectRemovalPlan      `json:"plan"`
	Blockers      []ticketservice.ProjectRemovalBlocker `json:"blockers"`
}

func newProjectsRemoveCommand(options Options) *cobra.Command {
	var apply bool
	command := &cobra.Command{
		Use:   "remove NAME",
		Short: "Plan or apply Project removal",
		Args:  exactArgs("NAME"),
		RunE: func(command *cobra.Command, args []string) error {
			if apply && isDryRun(command) {
				return invalidUsage(command, "do not use --dry-run together with --apply; remove one of the two flags")
			}
			service, err := options.ticketService()
			if err != nil {
				return err
			}
			return runProjectRemoval(command, options, service, args[0], apply)
		},
	}
	command.Flags().BoolVar(&apply, "apply", false, "Apply the removal plan")
	setArguments(command, requiredArgument("name"))
	command.ValidArgsFunction = allProjectNameCompletion(options)
	return command
}

func runProjectRemoval(command *cobra.Command, options Options, service ticketservice.Store, name string, applyRemoval bool) error {
	plan, err := service.PlanProjectRemoval(name)
	if err != nil {
		return err
	}
	plan.Blockers = projectWorkspaceBlockers(options, name)
	if applyRemoval && !isDryRun(command) {
		if len(plan.Blockers) > 0 {
			if WantsJSON(command) {
				if writeErr := writeJSONOutput(command, projectRemovalOutput{
					SchemaVersion: jsonSchemaVersion, Applied: false, Plan: plan, Blockers: plan.Blockers,
				}); writeErr != nil {
					return writeErr
				}
			} else if printErr := printProjectRemovalPlan(command.OutOrStdout(), plan, false); printErr != nil {
				return printErr
			}
			return projectRemovalBlocked(name, plan.Blockers)
		}
		plan, err = service.RemoveProject(name, false)
		if err != nil {
			return err
		}
		if WantsJSON(command) {
			return writeJSONOutput(command, projectRemovalOutput{
				SchemaVersion: jsonSchemaVersion, Applied: true, Plan: plan, Blockers: plan.Blockers,
			})
		}
		_, err = fmt.Fprintf(command.OutOrStdout(), "Removed Project %q\n", name)
		return err
	}
	if WantsJSON(command) {
		return writeJSONOutput(command, projectRemovalOutput{
			SchemaVersion: jsonSchemaVersion, Applied: false, Plan: plan, Blockers: plan.Blockers,
		})
	}
	return printProjectRemovalPlan(command.OutOrStdout(), plan, true)
}

func printProjectRemovalPlan(out io.Writer, plan ticketservice.ProjectRemovalPlan, applyHint bool) error {
	if _, err := fmt.Fprintf(out, "Removal plan for Project %q:\n", plan.Project.Name); err != nil {
		return err
	}
	for _, action := range plan.Actions {
		if _, err := fmt.Fprintf(out, "  %s %s\n", action.Kind, action.Target); err != nil {
			return err
		}
	}
	if len(plan.Tickets) > 0 {
		if _, err := fmt.Fprintf(out, "Tickets: %s\n", strings.Join(plan.Tickets, ", ")); err != nil {
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
	if _, err := fmt.Fprintln(out, "Blocked:"); err != nil {
		return err
	}
	for _, blocker := range plan.Blockers {
		if _, err := fmt.Fprintf(out, "  %s\n", blocker.Message); err != nil {
			return err
		}
		if blocker.Hint != "" {
			if _, err := fmt.Fprintf(out, "  Hint: %s\n", blocker.Hint); err != nil {
				return err
			}
		}
	}
	_, err := fmt.Fprintln(out, "The removal is blocked. Correct the causes above, then run the command again.")
	return err
}

func projectWorkspaceBlockers(options Options, name string) []ticketservice.ProjectRemovalBlocker {
	workspaces, err := store.NewWorkspaceStore(options.StateDir).List()
	if err != nil {
		return []ticketservice.ProjectRemovalBlocker{{
			Code:    projectRemovalWorkspaceLinked,
			Message: fmt.Sprintf("twt could not list Workspaces: %v", err),
		}}
	}
	names := make([]string, 0)
	for _, workspace := range workspaces {
		if workspace.Project == name {
			names = append(names, workspace.Name)
		}
	}
	if len(names) == 0 {
		return nil
	}
	return []ticketservice.ProjectRemovalBlocker{{
		Code:    projectRemovalWorkspaceLinked,
		Message: fmt.Sprintf("Project %q is used by %d Workspaces: %s", name, len(names), strings.Join(names, ", ")),
		Hint:    "Archive each Workspace, then run 'twt workspaces remove WORKSPACE --apply'.",
	}}
}

func projectRemovalBlocked(name string, blockers []ticketservice.ProjectRemovalBlocker) error {
	if len(blockers) == 0 {
		return nil
	}
	return clierr.WithHint(
		clierr.New(clierr.PreconditionFailed, "Project %q cannot be removed", name),
		"%s", blockers[0].Hint)
}

func allProjectNameCompletion(options Options) completionFunc {
	return func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) > 0 {
			return nil, noFileCompletion
		}
		service, err := options.ticketService()
		if err != nil {
			return nil, noFileCompletion
		}
		projects, err := service.AllProjects()
		if err != nil {
			return nil, noFileCompletion
		}
		names := make([]string, 0, len(projects))
		for _, project := range projects {
			names = append(names, project.Name)
		}
		return matching(names, toComplete), noFileCompletion
	}
}
