package cli

import (
	"fmt"
	"io"
	"os"
	"strconv"
	"time"

	workspaceservice "github.com/jpugliesi/tmux-worktree/internal/workspace"
	"github.com/spf13/cobra"
)

type removalOutput struct {
	SchemaVersion int                               `json:"schemaVersion"`
	Applied       bool                              `json:"applied"`
	Plan          workspaceservice.RemovalPlan      `json:"plan"`
	Blockers      []workspaceservice.RemovalBlocker `json:"blockers"`
}

type bulkRemovalOutput struct {
	SchemaVersion int                            `json:"schemaVersion"`
	Plans         []workspaceservice.RemovalPlan `json:"plans"`
	Applied       bool                           `json:"applied"`
	RemovedCount  int                            `json:"removedCount"`
	SkippedCount  int                            `json:"skippedCount"`
}

func newWorkspacesRemoveCommand(service *workspaceservice.Service) *cobra.Command {
	var apply bool
	var force bool
	var cancel bool
	var allArchived bool
	var olderThan string
	command := &cobra.Command{
		Use:   "remove [WORKSPACE]",
		Short: "Plan or apply safe Workspace removal",
		Args:  optionalArg("WORKSPACE"),
		RunE: func(command *cobra.Command, args []string) error {
			if apply && isDryRun(command) {
				return invalidUsage(command, "do not use --dry-run together with --apply; remove one of the two flags")
			}
			if allArchived {
				if len(args) != 0 {
					return invalidUsage(command, "do not use --all-archived together with a WORKSPACE argument")
				}
				if cancel {
					return invalidUsage(command, "do not use --all-archived together with --cancel")
				}
				return removeAllArchived(command, service, olderThan, apply, workspaceservice.RemovalOptions{AllowUnpublished: force})
			}
			if olderThan != "" {
				return invalidUsage(command, "--older-than requires --all-archived")
			}
			if len(args) != 1 {
				return invalidUsage(command, "missing required argument WORKSPACE")
			}
			reference, err := resolveWorkspaceReference(service, args[0])
			if err != nil {
				return err
			}
			if cancel {
				if apply || force {
					return invalidUsage(command, "do not use --cancel together with --apply or --force")
				}
				workspace, err := service.CancelRemoval(reference)
				if err != nil {
					return err
				}
				if WantsJSON(command) {
					return writeMutation(command, "workspaces.remove.cancel", statusApplied, workspace.ID, workspace.Name)
				}
				_, err = fmt.Fprintf(command.OutOrStdout(), "Removal of Workspace %q is canceled. The Workspace is archived.\n", workspace.Name)
				return err
			}
			return runWorkspaceRemoval(command, service, reference, apply, workspaceservice.RemovalOptions{AllowUnpublished: force})
		},
	}
	command.Flags().BoolVar(&apply, "apply", false, "Apply the removal plan")
	command.Flags().BoolVar(&force, "force", false, "Remove a branch with unpublished commits")
	command.Flags().BoolVar(&cancel, "cancel", false, "Return a removing Workspace to the archived status")
	command.Flags().BoolVar(&allArchived, "all-archived", false, "Plan or apply removal of all archived Workspaces")
	command.Flags().StringVar(&olderThan, "older-than", "", "With --all-archived, select only Workspaces archived at least this long ago (for example 14d, 36h, or 30m)")
	setArguments(command, optionalArgument("workspace", "required when --all-archived is not set"))
	command.ValidArgsFunction = workspaceNameCompletion(service)
	return command
}

// runWorkspaceRemoval plans or applies one Workspace removal and writes the
// result. Both the workspaces remove command and apply use it. A dry run or a
// missing apply flag shows the plan only.
func runWorkspaceRemoval(command *cobra.Command, service *workspaceservice.Service, reference string, applyRemoval bool, opts workspaceservice.RemovalOptions) error {
	if applyRemoval && !isDryRun(command) {
		plan, err := service.Remove(reference, os.Getenv("TMUX_PANE"), opts)
		if err != nil {
			return err
		}
		if WantsJSON(command) {
			return writeJSONOutput(command, removalOutput{SchemaVersion: jsonSchemaVersion, Applied: true, Plan: plan, Blockers: plan.Blockers})
		}
		_, err = fmt.Fprintf(command.OutOrStdout(), "Removed Workspace %q\n", plan.WorkspaceName)
		return err
	}
	plan, err := service.PlanRemoval(reference, os.Getenv("TMUX_PANE"), opts)
	if err != nil {
		return err
	}
	if WantsJSON(command) {
		return writeJSONOutput(command, removalOutput{SchemaVersion: jsonSchemaVersion, Applied: false, Plan: plan, Blockers: plan.Blockers})
	}
	return printRemovalPlanText(command.OutOrStdout(), plan, !applyRemoval)
}

// printRemovalPlanText writes one removal plan with its actions and
// blockers. With applyHint, an unblocked plan invites --apply.
func printRemovalPlanText(out io.Writer, plan workspaceservice.RemovalPlan, applyHint bool) error {
	if _, err := fmt.Fprintf(out, "Removal plan for Workspace %q:\n", plan.WorkspaceName); err != nil {
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

func printRemovalBlockers(out io.Writer, blockers []workspaceservice.RemovalBlocker, indent string) error {
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

// removeAllArchived plans or applies removal for all archived Workspaces that
// match the age filter. Apply removes the unblocked Workspaces and skips the
// blocked Workspaces.
func removeAllArchived(command *cobra.Command, service *workspaceservice.Service, olderThan string, apply bool, opts workspaceservice.RemovalOptions) error {
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
			plan, err := service.Remove(plans[index].WorkspaceID, os.Getenv("TMUX_PANE"), opts)
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
		_, err := fmt.Fprintln(out, "No archived Workspaces match.")
		return err
	}
	now := time.Now().UTC()
	for _, plan := range plans {
		planAge := "unknown"
		if plan.ArchivedAt != nil {
			planAge = formatAge(now.Sub(*plan.ArchivedAt))
		}
		if _, err := fmt.Fprintf(out, "Workspace %q: age %s, size %s\n", plan.WorkspaceName, planAge, formatBytes(plan.Bytes)); err != nil {
			return err
		}
		if err := printRemovalBlockers(out, plan.Blockers, "  "); err != nil {
			return err
		}
	}
	if apply {
		_, err := fmt.Fprintf(out, "Removed %d Workspaces (%s). Skipped %d blocked Workspaces.\n", removed, formatBytes(reclaimed), skipped)
		return err
	}
	_, err = fmt.Fprintln(out, "Run again with --apply to remove the Workspaces that are not blocked.")
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
