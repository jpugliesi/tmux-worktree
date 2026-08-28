package cli

import (
	"fmt"

	"github.com/jpugliesi/tmux-worktree/internal/maintenance"
	"github.com/jpugliesi/tmux-worktree/internal/store"
	workspaceservice "github.com/jpugliesi/tmux-worktree/internal/workspace"
	"github.com/spf13/cobra"
)

type storageOutput struct {
	SchemaVersion int                       `json:"schemaVersion"`
	Storage       maintenance.StorageStatus `json:"storage"`
}

type doctorOutput struct {
	SchemaVersion int                      `json:"schemaVersion"`
	Report        maintenance.DoctorReport `json:"report"`
}

func newStorageCommand(options Options) *cobra.Command {
	service := options.maintenanceService()
	storage := groupCommand(&cobra.Command{Use: "storage", Short: "Inspect twt storage"})
	get := &cobra.Command{
		Use:   "get",
		Short: "Get Workspace and repository storage use",
		Args:  noArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			result, err := service.StorageStatus()
			if err != nil {
				return err
			}
			if WantsJSON(command) {
				return writeReadJSON(command, storageOutput{SchemaVersion: jsonSchemaVersion, Storage: result}, "storage")
			}
			for _, warning := range result.Warnings {
				if _, err := fmt.Fprintf(command.ErrOrStderr(), "Warning: %s\n", warning); err != nil {
					return err
				}
			}
			prepared := fmt.Sprintf("%s (%d environments: %d ready, %d preparing, %d failed; %d worktrees)",
				formatBytes(result.PreparedBytes), result.PreparedEnvironmentCount, result.ReadyEnvironmentCount, result.PreparingEnvironmentCount, result.FailedEnvironmentCount, result.PreparedWorktreeCount)
			return writeFields(command.OutOrStdout(), [][2]string{
				{"Total", formatBytes(result.TotalBytes)},
				{"Caches", fmt.Sprintf("%s (%d)", formatBytes(result.CacheBytes), result.CacheCount)},
				{"Workspaces (active)", fmt.Sprintf("%s (%d)", formatBytes(result.ActiveWorkspaceBytes), result.ActiveWorkspaceCount)},
				{"Workspaces (archived)", fmt.Sprintf("%s (%d)", formatBytes(result.ArchivedWorkspaceBytes), result.ArchivedWorkspaceCount)},
				{"Worktrees", fmt.Sprintf("%d", result.WorktreeCount)},
				{"Prepared", prepared},
				{"Snapshots", formatBytes(result.SnapshotBytes)},
			})
		},
	}
	addFieldsFlag(get, maintenance.StorageStatus{})
	storage.AddCommand(get, newStorageCleanCommand(options))
	return storage
}

type storageCleanupOutput struct {
	SchemaVersion int                                 `json:"schemaVersion"`
	Applied       bool                                `json:"applied"`
	Plan          workspaceservice.StorageCleanupPlan `json:"plan"`
}

func newStorageCleanCommand(options Options) *cobra.Command {
	var apply bool
	command := &cobra.Command{
		Use:   "clean",
		Short: "Remove unused twt-owned data",
		Args:  noArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			return cleanStorage(command, options, apply)
		},
	}
	command.Flags().BoolVar(&apply, "apply", false, "Apply the cleanup plan")
	return command
}

// cleanStorage plans, and with apply set removes, the unused twt-owned data.
// Both the storage clean command and apply use it.
func cleanStorage(command *cobra.Command, options Options, apply bool) error {
	templates, err := currentTemplateDigests(command, options.templateStore())
	if err != nil {
		return err
	}
	service := options.workspaceService()
	plan, err := service.StorageCleanupPlan(templates)
	if err != nil {
		return err
	}
	applied := apply && !isDryRun(command)
	if applied {
		plan, err = service.CleanStorage(templates)
		if err != nil {
			return err
		}
	}
	if WantsJSON(command) {
		return writeJSONOutput(command, storageCleanupOutput{SchemaVersion: jsonSchemaVersion, Applied: applied, Plan: plan})
	}
	if len(plan.Environments) == 0 && len(plan.Snapshots) == 0 && len(plan.Agents) == 0 {
		_, err = fmt.Fprintln(command.OutOrStdout(), "Nothing to clean.")
		return err
	}
	for _, item := range plan.Environments {
		if _, err := fmt.Fprintf(command.OutOrStdout(), "Remove %s %q for Workspace Template %q (%s)\n", item.Reason, item.ID, item.TemplateName, formatBytes(item.Bytes)); err != nil {
			return err
		}
	}
	for _, item := range plan.Snapshots {
		target := item.WorkspaceID
		if target == "" {
			target = item.Root
		}
		if _, err := fmt.Fprintf(command.OutOrStdout(), "Remove %s %q (%s)\n", item.Reason, target, formatBytes(item.Bytes)); err != nil {
			return err
		}
	}
	for _, item := range plan.Agents {
		if _, err := fmt.Fprintf(command.OutOrStdout(), "Remove %s %q for missing Workspace %q\n", item.Reason, item.ID, item.WorkspaceID); err != nil {
			return err
		}
	}
	if !applied {
		_, err = fmt.Fprintln(command.OutOrStdout(), "Run again with --apply to remove these items.")
		return err
	}
	_, err = fmt.Fprintf(command.OutOrStdout(), "Removed %d Prepared Environments, %d Transcript Snapshots, and %d Agent Session records\n", len(plan.Environments), len(plan.Snapshots), len(plan.Agents))
	return err
}

// currentTemplateDigests reads the digests of the current Workspace Templates
// from the resolved template store, shared twt home included. A Workspace
// Template that twt cannot load gives a warning, and twt keeps its Prepared
// Environments.
func currentTemplateDigests(command *cobra.Command, templates store.TemplateStore) (workspaceservice.TemplateDigests, error) {
	catalog, warnings, err := store.CatalogFromStore(templates)
	if err != nil {
		return workspaceservice.TemplateDigests{}, err
	}
	for _, warning := range warnings {
		if _, writeErr := fmt.Fprintf(command.ErrOrStderr(), "Warning: %s\n", warning); writeErr != nil {
			return workspaceservice.TemplateDigests{}, writeErr
		}
	}
	return catalog, nil
}

func newDoctorCommand(options Options) *cobra.Command {
	service := options.maintenanceService()
	return &cobra.Command{
		Use:   "doctor",
		Short: "Check twt tools and state",
		Args:  noArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			report := service.Doctor()
			if WantsJSON(command) {
				if err := writeJSONOutput(command, doctorOutput{SchemaVersion: jsonSchemaVersion, Report: report}); err != nil {
					return err
				}
			} else {
				rows := make([][]string, 0, len(report.Checks))
				for _, check := range report.Checks {
					rows = append(rows, []string{check.Status, check.Name, check.Message})
				}
				if err := writeTable(command.OutOrStdout(), []string{"STATUS", "NAME", "MESSAGE"}, rows); err != nil {
					return err
				}
			}
			if !report.Healthy {
				return fmt.Errorf("doctor found one or more problems")
			}
			return nil
		},
	}
}
