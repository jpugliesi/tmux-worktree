package cli

import (
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/jpugliesi/tmux-worktree/internal/version"
	"github.com/spf13/cobra"
)

type Options struct {
	ConfigDir              string
	StateDir               string
	DataDir                string
	TmuxSocket             string
	Stdout                 io.Writer
	Stderr                 io.Writer
	QuickCreateSwitch      func(session string) error
	QuickCreateArchive     func(projectID string) error
	QuickCreateExecutable  string
	QuickCreateWaitTimeout time.Duration
	PreparationExecutable  string
}

func DefaultOptions() Options {
	home, _ := os.UserHomeDir()
	configHome := os.Getenv("XDG_CONFIG_HOME")
	if configHome == "" {
		configHome = filepath.Join(home, ".config")
	}
	stateHome := os.Getenv("XDG_STATE_HOME")
	if stateHome == "" {
		stateHome = filepath.Join(home, ".local", "state")
	}
	dataHome := os.Getenv("XDG_DATA_HOME")
	if dataHome == "" {
		dataHome = filepath.Join(home, ".local", "share")
	}
	return Options{
		ConfigDir:  envOr("TWT2_CONFIG_DIR", filepath.Join(configHome, "twt2")),
		StateDir:   envOr("TWT2_STATE_DIR", filepath.Join(stateHome, "twt2")),
		DataDir:    envOr("TWT2_DATA_DIR", filepath.Join(dataHome, "twt2")),
		TmuxSocket: os.Getenv("TWT2_TMUX_SOCKET"),
		Stdout:     os.Stdout,
		Stderr:     os.Stderr,
	}
}

func New(options Options) *cobra.Command {
	root := &cobra.Command{
		Use:     "twt2",
		Version: version.Version,
		Short:   "Manage task-focused Projects with Git worktrees and tmux",
		Long: `Create task-focused Projects from reusable YAML templates.

Each Project can own multiple Git worktrees, one tmux window for each
repository, and a set of resumable coding Agent Sessions.`,
		Example: `  twt2 templates list
  twt2 projects create fix-auth --template everysphere
  twt2 create fix-logout
  twt2 agents list --project current`,
		SilenceErrors: true,
		SilenceUsage:  true,
		PersistentPreRunE: func(command *cobra.Command, _ []string) error {
			output, _ := command.Flags().GetString("output")
			if output != "text" && output != "json" {
				return invalidUsage(command, "unsupported output %q; use text or json", output)
			}
			return nil
		},
	}
	root.SetOut(options.Stdout)
	root.SetErr(options.Stderr)
	root.SetFlagErrorFunc(func(command *cobra.Command, err error) error {
		return invalidUsage(command, "%v", err)
	})
	root.PersistentFlags().String("output", "text", "Set all command output to text or json")
	root.PersistentFlags().Bool("dry-run", false, "Validate and show a mutation without applying it")
	root.AddGroup(
		&cobra.Group{ID: "workflows", Title: "Workflows:"},
		&cobra.Group{ID: "inspect", Title: "Inspect and maintain:"},
		&cobra.Group{ID: "automation", Title: "Automation:"},
	)
	templates := newTemplatesCommand(options)
	templates.GroupID = "workflows"
	projects := newProjectsCommand(options)
	projects.GroupID = "workflows"
	create := newQuickCreateCommand(options)
	create.GroupID = "workflows"
	archive := newArchiveCommand(options)
	archive.GroupID = "workflows"
	agents := newAgentsCommand(options)
	agents.GroupID = "workflows"
	context := newContextCommand(options)
	context.GroupID = "inspect"
	storage := newStorageCommand(options)
	storage.GroupID = "inspect"
	doctor := newDoctorCommand(options)
	doctor.GroupID = "inspect"
	schema := newSchemaCommand(root)
	schema.GroupID = "automation"
	apply := newApplyCommand(options)
	apply.GroupID = "automation"
	root.AddCommand(templates, projects, create, archive, agents, context, storage, doctor, schema, apply)
	root.SetHelpCommandGroupID("automation")
	root.SetCompletionCommandGroupID("automation")
	configureCommandHelp(root)
	return root
}

func WantsJSON(command *cobra.Command) bool {
	value, _ := command.Flags().GetString("output")
	return value == "json"
}

func envOr(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}
