package cli

import (
	"fmt"

	"github.com/jpugliesi/tmux-worktree/internal/maintenance"
	projectservice "github.com/jpugliesi/tmux-worktree/internal/project"
	"github.com/jpugliesi/tmux-worktree/internal/store"
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
	service := maintenance.NewService(options.ConfigDir, options.StateDir, options.DataDir)
	storage := groupCommand(&cobra.Command{Use: "storage", Short: "Inspect twt2 storage"})
	show := &cobra.Command{
		Use:   "show",
		Short: "Show Project and repository storage use",
		Args:  noArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			result, err := service.StorageStatus()
			if err != nil {
				return err
			}
			if WantsJSON(command) {
				return writeJSONOutput(command, storageOutput{SchemaVersion: jsonSchemaVersion, Storage: result})
			}
			_, err = fmt.Fprintf(command.OutOrStdout(), "Total: %s\nCaches: %s (%d)\nProjects (active): %s (%d)\nProjects (archived): %s (%d)\nWorktrees: %d\nPrepared: %s (%d environments: %d ready, %d preparing, %d failed; %d worktrees)\nSnapshots: %s\n",
				formatBytes(result.TotalBytes),
				formatBytes(result.CacheBytes), result.CacheCount,
				formatBytes(result.ActiveProjectBytes), result.ActiveProjectCount,
				formatBytes(result.ArchivedProjectBytes), result.ArchivedProjectCount,
				result.WorktreeCount,
				formatBytes(result.PreparedBytes), result.PreparedEnvironmentCount, result.ReadyEnvironmentCount, result.PreparingEnvironmentCount, result.FailedEnvironmentCount, result.PreparedWorktreeCount,
				formatBytes(result.SnapshotBytes))
			return err
		},
	}
	storage.AddCommand(show, newStorageCleanCommand(options))
	return storage
}

type storageCleanupOutput struct {
	SchemaVersion int                               `json:"schemaVersion"`
	Applied       bool                              `json:"applied"`
	Plan          projectservice.StorageCleanupPlan `json:"plan"`
}

func newStorageCleanCommand(options Options) *cobra.Command {
	var apply bool
	command := &cobra.Command{
		Use:   "clean",
		Short: "Remove unused twt2-owned data",
		Args:  noArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			templates, err := currentTemplateDigests(command, options.ConfigDir)
			if err != nil {
				return err
			}
			service := projectservice.NewService(projectservice.Options{StateDir: options.StateDir, DataDir: options.DataDir, TmuxSocket: options.TmuxSocket})
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
				if _, err := fmt.Fprintf(command.OutOrStdout(), "Remove %s %q for Project Template %q (%s)\n", item.Reason, item.ID, item.TemplateName, formatBytes(item.Bytes)); err != nil {
					return err
				}
			}
			for _, item := range plan.Snapshots {
				target := item.ProjectID
				if target == "" {
					target = item.Root
				}
				if _, err := fmt.Fprintf(command.OutOrStdout(), "Remove %s %q (%s)\n", item.Reason, target, formatBytes(item.Bytes)); err != nil {
					return err
				}
			}
			for _, item := range plan.Agents {
				if _, err := fmt.Fprintf(command.OutOrStdout(), "Remove %s %q for missing Project %q\n", item.Reason, item.ID, item.ProjectID); err != nil {
					return err
				}
			}
			if !applied {
				_, err = fmt.Fprintln(command.OutOrStdout(), "Run again with --apply to remove these items.")
				return err
			}
			_, err = fmt.Fprintf(command.OutOrStdout(), "Removed %d Prepared Environments, %d Transcript Snapshots, and %d Agent Session records\n", len(plan.Environments), len(plan.Snapshots), len(plan.Agents))
			return err
		},
	}
	command.Flags().BoolVar(&apply, "apply", false, "Apply the cleanup plan")
	return command
}

// currentTemplateDigests reads the digests of the current Project Templates. A
// Project Template that twt2 cannot load gives a warning, and twt2 keeps its
// Prepared Environments.
func currentTemplateDigests(command *cobra.Command, configDir string) (projectservice.TemplateDigests, error) {
	templates := store.NewTemplateStore(configDir)
	names, err := templates.List()
	if err != nil {
		return projectservice.TemplateDigests{}, err
	}
	result := make(projectservice.TemplateDigests, len(names))
	for _, name := range names {
		template, err := templates.Load(name)
		if err == nil {
			var digestSet store.DigestSet
			digestSet, err = store.Digests(template)
			if err == nil {
				result[name] = digestSet
				continue
			}
		}
		// An empty DigestSet keeps the Prepared Environments of this Project
		// Template.
		result[name] = store.DigestSet{}
		if _, writeErr := fmt.Fprintf(command.ErrOrStderr(), "Warning: Project Template %q is not valid. twt2 kept its Prepared Environments.\n", name); writeErr != nil {
			return projectservice.TemplateDigests{}, writeErr
		}
	}
	return result, nil
}

func newDoctorCommand(options Options) *cobra.Command {
	service := maintenance.NewService(options.ConfigDir, options.StateDir, options.DataDir)
	return &cobra.Command{
		Use:   "doctor",
		Short: "Check twt2 tools and state",
		Args:  noArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			report := service.Doctor()
			if WantsJSON(command) {
				if err := writeJSONOutput(command, doctorOutput{SchemaVersion: jsonSchemaVersion, Report: report}); err != nil {
					return err
				}
			} else {
				for _, check := range report.Checks {
					if _, err := fmt.Fprintf(command.OutOrStdout(), "%s\t%s\t%s\n", check.Status, check.Name, check.Message); err != nil {
						return err
					}
				}
			}
			if !report.Healthy {
				return fmt.Errorf("doctor found one or more problems")
			}
			return nil
		},
	}
}

func formatBytes(value int64) string {
	const unit = int64(1024)
	if value < unit {
		return fmt.Sprintf("%d B", value)
	}
	divisor := unit
	units := []string{"KiB", "MiB", "GiB", "TiB"}
	unitName := units[0]
	for _, candidate := range units[1:] {
		if value < divisor*unit {
			break
		}
		divisor *= unit
		unitName = candidate
	}
	return fmt.Sprintf("%.1f %s", float64(value)/float64(divisor), unitName)
}
