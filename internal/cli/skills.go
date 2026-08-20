package cli

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/jpugliesi/tmux-worktree/internal/clierr"
	"github.com/jpugliesi/tmux-worktree/internal/version"
	skillasset "github.com/jpugliesi/tmux-worktree/skills"
	"github.com/spf13/cobra"
)

type skillsInstallOutput struct {
	SchemaVersion int                 `json:"schemaVersion"`
	Status        string              `json:"status"`
	Skills        []skillasset.Result `json:"skills"`
}

type skillShowOutput struct {
	SchemaVersion int         `json:"schemaVersion"`
	Skill         skillOutput `json:"skill"`
}

type skillOutput struct {
	Version string `json:"version"`
	Content string `json:"content"`
}

func newSkillsCommand() *cobra.Command {
	command := groupCommand(&cobra.Command{Use: "skills", Short: "Install the twt agent skill"})
	command.AddCommand(newSkillsInstallCommand(), newSkillsShowCommand())
	return command
}

func newSkillsInstallCommand() *cobra.Command {
	var user bool
	var directories []string
	command := &cobra.Command{
		Use:   "install",
		Short: "Install the twt agent skill into skill trees",
		Args:  noArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			paths, err := skillInstallPaths(user, directories)
			if err != nil {
				return err
			}
			dryRun := isDryRun(command)
			results, err := skillasset.Install(paths, version.Version, dryRun)
			if err != nil {
				return err
			}
			status := statusApplied
			if dryRun {
				status = statusValid
			}
			if WantsJSON(command) {
				return writeJSONOutput(command, skillsInstallOutput{SchemaVersion: jsonSchemaVersion, Status: status, Skills: results})
			}
			out := command.OutOrStdout()
			for _, result := range results {
				if _, err := fmt.Fprintf(out, "%s %s\n", result.Action, result.Path); err != nil {
					return err
				}
			}
			if dryRun {
				_, err = fmt.Fprintln(out, "Run again without --dry-run to write these files.")
				return err
			}
			_, err = fmt.Fprintf(out, "Installed the twt skill version %s in %d skill trees.\n", version.Version, len(results))
			return err
		},
	}
	command.Flags().BoolVar(&user, "user", false, "Install into the Cursor, Claude Code, and shared agent skill trees of the user")
	command.Flags().StringArrayVar(&directories, "dir", nil, "Install into this skill tree directory; repeat the flag for more trees")
	return command
}

func newSkillsShowCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "show",
		Short: "Show the twt agent skill of this build",
		Args:  noArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			content := skillasset.Stamped(version.Version)
			if WantsJSON(command) {
				return writeJSONOutput(command, skillShowOutput{
					SchemaVersion: jsonSchemaVersion,
					Skill:         skillOutput{Version: version.Version, Content: content},
				})
			}
			_, err := io.WriteString(command.OutOrStdout(), content)
			return err
		},
	}
}

// skillInstallPaths returns the skill file path of each install target. The
// three user skill trees are the default: --dir alone installs only into the
// named trees, and --dir with --user installs into both.
func skillInstallPaths(user bool, directories []string) ([]string, error) {
	paths := make([]string, 0, len(directories)+3)
	for _, directory := range directories {
		trimmed := strings.TrimSpace(directory)
		if trimmed == "" {
			return nil, clierr.New(clierr.InvalidUsage, "--dir needs a skill tree directory")
		}
		absolute, err := filepath.Abs(trimmed)
		if err != nil {
			return nil, fmt.Errorf("resolve the skill tree %q: %w", trimmed, err)
		}
		paths = append(paths, skillasset.PathIn(absolute))
	}
	if len(directories) > 0 && !user {
		return paths, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, clierr.WithHint(
			clierr.New(clierr.PreconditionFailed, "twt cannot find the home directory of the user"),
			"Run 'twt skills install --dir DIR' to select the skill tree yourself.")
	}
	userPaths := skillasset.UserPaths(home)
	if len(userPaths) == 0 {
		return nil, clierr.WithHint(
			clierr.New(clierr.PreconditionFailed, "twt cannot find the home directory of the user"),
			"Run 'twt skills install --dir DIR' to select the skill tree yourself.")
	}
	return append(paths, userPaths...), nil
}
