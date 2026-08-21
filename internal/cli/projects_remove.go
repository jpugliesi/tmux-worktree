package cli

import (
	"fmt"
	"io"
	"os"
	"strconv"
	"time"

	projectservice "github.com/jpugliesi/tmux-worktree/internal/project"
	"github.com/spf13/cobra"
)

type removalOutput struct {
	SchemaVersion int                             `json:"schemaVersion"`
	Applied       bool                            `json:"applied"`
	Plan          projectservice.RemovalPlan      `json:"plan"`
	Blockers      []projectservice.RemovalBlocker `json:"blockers"`
}

type bulkRemovalOutput struct {
	SchemaVersion int                          `json:"schemaVersion"`
	Plans         []projectservice.RemovalPlan `json:"plans"`
	Applied       bool                         `json:"applied"`
	RemovedCount  int                          `json:"removedCount"`
	SkippedCount  int                          `json:"skippedCount"`
}

func newProjectsRemoveCommand(service *projectservice.Service) *cobra.Command {
	var apply bool
	var force bool
	var cancel bool
	var allArchived bool
	var olderThan string
	command := &cobra.Command{
		Use:   "remove [PROJECT]",
		Short: "Plan or apply safe Project removal",
		Args:  optionalArg("PROJECT"),
		RunE: func(command *cobra.Command, args []string) error {
			if apply && isDryRun(command) {
				return invalidUsage(command, "do not use --dry-run together with --apply; remove one of the two flags")
			}
			if allArchived {
				if len(args) != 0 {
					return invalidUsage(command, "do not use --all-archived together with a PROJECT argument")
				}
				if cancel {
					return invalidUsage(command, "do not use --all-archived together with --cancel")
				}
				return removeAllArchived(command, service, olderThan, apply, projectservice.RemovalOptions{AllowUnpublished: force})
			}
			if olderThan != "" {
				return invalidUsage(command, "--older-than requires --all-archived")
			}
			if len(args) != 1 {
				return invalidUsage(command, "missing required argument PROJECT")
			}
			reference, err := resolveProjectReference(service, args[0])
			if err != nil {
				return err
			}
			if cancel {
				if apply || force {
					return invalidUsage(command, "do not use --cancel together with --apply or --force")
				}
				project, err := service.CancelRemoval(reference)
				if err != nil {
					return err
				}
				if WantsJSON(command) {
					return writeMutation(command, "projects.remove.cancel", statusApplied, project.ID, project.Name)
				}
				_, err = fmt.Fprintf(command.OutOrStdout(), "Removal of Project %q is canceled. The Project is archived.\n", project.Name)
				return err
			}
			return runProjectRemoval(command, service, reference, apply, projectservice.RemovalOptions{AllowUnpublished: force})
		},
	}
	command.Flags().BoolVar(&apply, "apply", false, "Apply the removal plan")
	command.Flags().BoolVar(&force, "force", false, "Remove a branch with unpublished commits")
	command.Flags().BoolVar(&cancel, "cancel", false, "Return a removing Project to the archived status")
	command.Flags().BoolVar(&allArchived, "all-archived", false, "Plan or apply removal of all archived Projects")
	command.Flags().StringVar(&olderThan, "older-than", "", "With --all-archived, select only Projects archived at least this long ago (for example 14d, 36h, or 30m)")
	setArguments(command, optionalArgument("project", "required when --all-archived is not set"))
	command.ValidArgsFunction = projectNameCompletion(service)
	return command
}

// runProjectRemoval plans or applies one Project removal and writes the
// result. Both the projects remove command and apply use it. A dry run or a
// missing apply flag shows the plan only.
func runProjectRemoval(command *cobra.Command, service *projectservice.Service, reference string, applyRemoval bool, opts projectservice.RemovalOptions) error {
	if applyRemoval && !isDryRun(command) {
		plan, err := service.Remove(reference, os.Getenv("TMUX_PANE"), opts)
		if err != nil {
			return err
		}
		if WantsJSON(command) {
			return writeJSONOutput(command, removalOutput{SchemaVersion: jsonSchemaVersion, Applied: true, Plan: plan, Blockers: plan.Blockers})
		}
		_, err = fmt.Fprintf(command.OutOrStdout(), "Removed Project %q\n", plan.ProjectName)
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
func printRemovalPlanText(out io.Writer, plan projectservice.RemovalPlan, applyHint bool) error {
	if _, err := fmt.Fprintf(out, "Removal plan for Project %q:\n", plan.ProjectName); err != nil {
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

func printRemovalBlockers(out io.Writer, blockers []projectservice.RemovalBlocker, indent string) error {
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

// removeAllArchived plans or applies removal for all archived Projects that
// match the age filter. Apply removes the unblocked Projects and skips the
// blocked Projects.
func removeAllArchived(command *cobra.Command, service *projectservice.Service, olderThan string, apply bool, opts projectservice.RemovalOptions) error {
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
			plan, err := service.Remove(plans[index].ProjectID, os.Getenv("TMUX_PANE"), opts)
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
		_, err := fmt.Fprintln(out, "No archived Projects match.")
		return err
	}
	now := time.Now().UTC()
	for _, plan := range plans {
		planAge := "unknown"
		if plan.ArchivedAt != nil {
			planAge = formatAge(now.Sub(*plan.ArchivedAt))
		}
		if _, err := fmt.Fprintf(out, "Project %q: age %s, size %s\n", plan.ProjectName, planAge, formatBytes(plan.Bytes)); err != nil {
			return err
		}
		if err := printRemovalBlockers(out, plan.Blockers, "  "); err != nil {
			return err
		}
	}
	if apply {
		_, err := fmt.Fprintf(out, "Removed %d Projects (%s). Skipped %d blocked Projects.\n", removed, formatBytes(reclaimed), skipped)
		return err
	}
	_, err = fmt.Fprintln(out, "Run again with --apply to remove the Projects that are not blocked.")
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
