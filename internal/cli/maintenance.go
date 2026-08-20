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
	storage := &cobra.Command{Use: "storage", Short: "Inspect twt2 storage"}
	var format string
	status := &cobra.Command{
		Use:   "status",
		Short: "Show Project and repository storage use",
		Args:  noArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			result, err := service.StorageStatus()
			if err != nil {
				return err
			}
			if wantsJSON(command, format) {
				return writeJSONOutput(command, storageOutput{SchemaVersion: jsonSchemaVersion, Storage: result})
			}
			if format != "text" {
				return fmt.Errorf("unsupported format %q", format)
			}
			_, err = fmt.Fprintf(command.OutOrStdout(), "Total: %s\nCaches: %s (%d)\nProjects: %s (%d Projects, %d worktrees)\nPrepared: %s (%d environments: %d ready, %d preparing, %d failed; %d worktrees)\n", formatBytes(result.TotalBytes), formatBytes(result.CacheBytes), result.CacheCount, formatBytes(result.ProjectBytes), result.ProjectCount, result.WorktreeCount, formatBytes(result.PreparedBytes), result.PreparedEnvironmentCount, result.ReadyEnvironmentCount, result.PreparingEnvironmentCount, result.FailedEnvironmentCount, result.PreparedWorktreeCount)
			return err
		},
	}
	status.Flags().StringVar(&format, "format", "text", "Set the output format: text or json")
	storage.AddCommand(status, newStorageCleanCommand(options))
	return storage
}

type preparedCleanupOutput struct {
	SchemaVersion int                                   `json:"schemaVersion"`
	Plan          projectservice.EnvironmentCleanupPlan `json:"plan"`
}

func newStorageCleanCommand(options Options) *cobra.Command {
	var apply bool
	command := &cobra.Command{
		Use:   "clean",
		Short: "Remove failed and obsolete Prepared Environments",
		Args:  noArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			digests, err := currentTemplateDigests(options.ConfigDir)
			if err != nil {
				return err
			}
			service := projectservice.NewService(projectservice.Options{StateDir: options.StateDir, DataDir: options.DataDir, TmuxSocket: options.TmuxSocket})
			plan, err := service.PreparedCleanupPlan(digests)
			if err != nil {
				return err
			}
			if apply && !isDryRun(command) {
				plan, err = service.CleanPrepared(digests)
				if err != nil {
					return err
				}
			}
			if WantsJSON(command) {
				return writeJSONOutput(command, preparedCleanupOutput{SchemaVersion: jsonSchemaVersion, Plan: plan})
			}
			for _, item := range plan.Environments {
				if _, err := fmt.Fprintf(command.OutOrStdout(), "Remove %s %q for Project Template %q\n", item.Reason, item.ID, item.TemplateName); err != nil {
					return err
				}
			}
			if !apply || isDryRun(command) {
				_, err = fmt.Fprintln(command.OutOrStdout(), "Run again with --apply to remove these items.")
				return err
			}
			_, err = fmt.Fprintf(command.OutOrStdout(), "Removed %d Prepared Environments\n", len(plan.Environments))
			return err
		},
	}
	command.Flags().BoolVar(&apply, "apply", false, "Apply the cleanup plan")
	return command
}

func currentTemplateDigests(configDir string) (map[string]string, error) {
	templates := store.NewTemplateStore(configDir)
	names, err := templates.List()
	if err != nil {
		return nil, err
	}
	digests := make(map[string]string, len(names))
	for _, name := range names {
		template, err := templates.Load(name)
		if err != nil {
			return nil, err
		}
		digest, err := store.TemplateDigest(template)
		if err != nil {
			return nil, err
		}
		digests[name] = digest
	}
	return digests, nil
}

func newDoctorCommand(options Options) *cobra.Command {
	service := maintenance.NewService(options.ConfigDir, options.StateDir, options.DataDir)
	var format string
	command := &cobra.Command{
		Use:   "doctor",
		Short: "Check twt2 tools and state",
		Args:  noArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			report := service.Doctor()
			if wantsJSON(command, format) {
				if err := writeJSONOutput(command, doctorOutput{SchemaVersion: jsonSchemaVersion, Report: report}); err != nil {
					return err
				}
			} else if format == "text" {
				for _, check := range report.Checks {
					if _, err := fmt.Fprintf(command.OutOrStdout(), "%s\t%s\t%s\n", check.Status, check.Name, check.Message); err != nil {
						return err
					}
				}
			} else {
				return fmt.Errorf("unsupported format %q", format)
			}
			if !report.Healthy {
				return fmt.Errorf("doctor found one or more problems")
			}
			return nil
		},
	}
	command.Flags().StringVar(&format, "format", "text", "Set the output format: text or json")
	return command
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
