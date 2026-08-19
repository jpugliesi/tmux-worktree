package cli

import (
	"fmt"

	"github.com/jpugliesi/tmux-worktree/internal/maintenance"
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
			_, err = fmt.Fprintf(command.OutOrStdout(), "Total: %s\nCaches: %s (%d)\nProjects: %s (%d Projects, %d worktrees)\n", formatBytes(result.TotalBytes), formatBytes(result.CacheBytes), result.CacheCount, formatBytes(result.ProjectBytes), result.ProjectCount, result.WorktreeCount)
			return err
		},
	}
	status.Flags().StringVar(&format, "format", "text", "Set the output format: text or json")
	storage.AddCommand(status)
	return storage
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
