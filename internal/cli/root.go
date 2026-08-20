package cli

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/jpugliesi/tmux-worktree/internal/clierr"
	"github.com/jpugliesi/tmux-worktree/internal/maintenance"
	projectservice "github.com/jpugliesi/tmux-worktree/internal/project"
	"github.com/jpugliesi/tmux-worktree/internal/store"
	ticketservice "github.com/jpugliesi/tmux-worktree/internal/ticket"
	"github.com/jpugliesi/tmux-worktree/internal/version"
	"github.com/spf13/cobra"
)

// RelocationRequest describes one archive or removal that must move the
// calling tmux client out of the Project session first.
type RelocationRequest struct {
	// ProjectID is the Project that twt2 archives or removes.
	ProjectID string
	// DestinationProjectID is the Project that receives the tmux client. It
	// is empty when no other active Project exists; twt2 then detaches the
	// client.
	DestinationProjectID string
	// Keep stops the operation after the archive.
	Keep bool
	// AllowUnpublished permits removal of a branch with unpublished commits.
	AllowUnpublished bool
	// CurrentPane is the tmux pane that runs the command.
	CurrentPane string
}

type Options struct {
	ConfigDir  string
	StateDir   string
	DataDir    string
	TmuxSocket string
	// TicketsHome is the root directory of the Markdown ticket files. When it
	// is empty, twt2 resolves TWT2_TICKETS_HOME and then the ticketsHome value
	// of config.yaml at command time.
	TicketsHome string
	Stdout      io.Writer
	Stderr      io.Writer
	// OpenEditor opens one file in the interactive editor and returns after
	// the editor closes. New installs the real VISUAL/EDITOR implementation
	// when it is nil; tests replace it with a fake.
	OpenEditor func(path string) error
	// QuickCreateSwitch moves the calling tmux client to a session. New
	// installs the real tmux implementation when it is nil; tests replace it
	// with a fake.
	QuickCreateSwitch func(clientName, session string) error
	// QuickCreateArchive archives the old Project after the client switch.
	// New installs the real relocation worker implementation when it is nil.
	QuickCreateArchive func(clientName, oldProjectID, newProjectID string) error
	// DoneRelocate moves the calling tmux client out of the Project session
	// and completes the archive or removal. New installs the real relocation
	// worker implementation when it is nil.
	DoneRelocate func(request RelocationRequest) error
	// SwitchPick selects one line index from the switch Project picker. New
	// installs the real fzf or numbered-list implementation when it is nil.
	SwitchPick             func(command *cobra.Command, lines []string) (int, error)
	QuickCreateExecutable  string
	QuickCreateWaitTimeout time.Duration
	PreparationExecutable  string
}

// projectService builds the Project service for these Options.
func (o Options) projectService() *projectservice.Service {
	return projectservice.NewService(o.projectServiceOptions())
}

// projectServiceOptions builds the base Project service configuration.
func (o Options) projectServiceOptions() projectservice.Options {
	return projectservice.Options{StateDir: o.StateDir, DataDir: o.DataDir, TmuxSocket: o.TmuxSocket}
}

// templateStore builds the Project Template store for these Options.
func (o Options) templateStore() store.TemplateStore {
	return store.NewTemplateStore(o.ConfigDir)
}

// maintenanceService builds the maintenance service for these Options. A
// config read failure leaves the Tickets home empty, and doctor then reports
// the unset home.
func (o Options) maintenanceService() *maintenance.Service {
	home, err := o.resolveTicketsHome()
	if err != nil {
		home = ""
	}
	return maintenance.NewService(o.ConfigDir, o.StateDir, o.DataDir, home)
}

// resolveTicketsHome resolves the Tickets home: the injected Options value,
// then TWT2_TICKETS_HOME, then the ticketsHome value of config.yaml. The
// result is empty when no source sets a home.
func (o Options) resolveTicketsHome() (string, error) {
	if o.TicketsHome != "" {
		return o.TicketsHome, nil
	}
	if value := os.Getenv("TWT2_TICKETS_HOME"); value != "" {
		return value, nil
	}
	config, err := store.LoadConfig(o.ConfigDir)
	if err != nil {
		return "", err
	}
	return config.TicketsHome, nil
}

// ticketService builds the ticket service for these Options. It fails when no
// Tickets home is set, so every tickets command reports the same
// precondition error.
func (o Options) ticketService() (*ticketservice.Service, error) {
	home, err := o.resolveTicketsHome()
	if err != nil {
		return nil, err
	}
	if home == "" {
		return nil, clierr.WithHint(
			clierr.New(clierr.PreconditionFailed, "no Tickets home is set"),
			"Set ticketsHome in ~/.config/twt2/config.yaml or TWT2_TICKETS_HOME.")
	}
	return ticketservice.NewService(ticketservice.Options{Home: home, StateDir: o.StateDir}), nil
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

// withRealWorkflows fills every workflow hook that the caller left nil with
// its real tmux implementation. Tests replace the hooks with fakes.
func withRealWorkflows(options Options) Options {
	if options.QuickCreateSwitch == nil {
		options.QuickCreateSwitch = realQuickCreateSwitch(options)
	}
	if options.QuickCreateArchive == nil {
		options.QuickCreateArchive = realQuickCreateArchive(options)
	}
	if options.DoneRelocate == nil {
		options.DoneRelocate = realDoneRelocate(options)
	}
	if options.SwitchPick == nil {
		options.SwitchPick = realSwitchPick
	}
	if options.OpenEditor == nil {
		options.OpenEditor = realOpenEditor(options)
	}
	return options
}

// realOpenEditor starts the VISUAL or EDITOR command on one file and waits
// for it. The editor value splits on spaces; twt2 never starts a shell.
func realOpenEditor(options Options) func(path string) error {
	return func(path string) error {
		editor := os.Getenv("VISUAL")
		if strings.TrimSpace(editor) == "" {
			editor = os.Getenv("EDITOR")
		}
		if strings.TrimSpace(editor) == "" {
			return clierr.New(clierr.InvalidUsage, "set VISUAL or EDITOR to the editor command that twt2 must start")
		}
		parts := strings.Fields(editor)
		process := exec.Command(parts[0], append(parts[1:], path)...)
		process.Stdin = os.Stdin
		process.Stdout = options.Stdout
		process.Stderr = options.Stderr
		if process.Stdout == nil {
			process.Stdout = os.Stdout
		}
		if process.Stderr == nil {
			process.Stderr = os.Stderr
		}
		if err := process.Run(); err != nil {
			return fmt.Errorf("run the editor %q: %w", editor, err)
		}
		return nil
	}
}

func New(options Options) *cobra.Command {
	options = withRealWorkflows(options)
	root := &cobra.Command{
		Use:     "twt2",
		Version: version.Version,
		Short:   "Manage task-focused Projects with Git worktrees and tmux",
		Long: `Create task-focused Projects from reusable YAML templates.

Each Project can own multiple Git worktrees, one tmux window for each
repository, and a set of resumable coding Agent Sessions.`,
		Example: `  twt2 templates list
  twt2 projects create fix-auth --template everysphere
  twt2 new fix-logout
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
	setFlagEnum(root, "output", outputFormatNames...)
	root.AddGroup(
		&cobra.Group{ID: "workflows", Title: "Workflows:"},
		&cobra.Group{ID: "inspect", Title: "Inspect and maintain:"},
		&cobra.Group{ID: "automation", Title: "Automation:"},
	)
	templates := newTemplatesCommand(options)
	templates.GroupID = "workflows"
	projects := newProjectsCommand(options)
	projects.GroupID = "workflows"
	quickCreate := newQuickCreateCommand(options)
	quickCreate.GroupID = "workflows"
	switchCommand := newSwitchCommand(options)
	switchCommand.GroupID = "workflows"
	archive := newArchiveCommand(options)
	archive.GroupID = "workflows"
	done := newDoneCommand(options)
	done.GroupID = "workflows"
	agents := newAgentsCommand(options)
	agents.GroupID = "workflows"
	tickets := newTicketsCommand(options)
	tickets.GroupID = "workflows"
	context := newContextCommand(options)
	context.GroupID = "inspect"
	environments := newEnvironmentsCommand(options)
	environments.GroupID = "inspect"
	storage := newStorageCommand(options)
	storage.GroupID = "inspect"
	doctor := newDoctorCommand(options)
	doctor.GroupID = "inspect"
	schema := newSchemaCommand(root)
	schema.GroupID = "automation"
	apply := newApplyCommand(options)
	apply.GroupID = "automation"
	root.AddCommand(templates, projects, quickCreate, switchCommand, archive, done, agents, tickets, context, environments, storage, doctor, schema, apply)
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
