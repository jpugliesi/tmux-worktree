package cli

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	agentservice "github.com/jpugliesi/tmux-worktree/internal/agent"
	"github.com/jpugliesi/tmux-worktree/internal/clierr"
	"github.com/jpugliesi/tmux-worktree/internal/maintenance"
	"github.com/jpugliesi/tmux-worktree/internal/store"
	ticketservice "github.com/jpugliesi/tmux-worktree/internal/ticket"
	"github.com/jpugliesi/tmux-worktree/internal/version"
	workspaceservice "github.com/jpugliesi/tmux-worktree/internal/workspace"
	skillasset "github.com/jpugliesi/tmux-worktree/skills"
	"github.com/spf13/cobra"
)

// RelocationRequest describes one archive or removal that must move the
// calling tmux client out of the Workspace session first.
type RelocationRequest struct {
	// WorkspaceID is the Workspace that twt archives or removes.
	WorkspaceID string
	// DestinationWorkspaceID is the Workspace that receives the tmux client. It
	// is empty when no other active Workspace exists; twt then detaches the
	// client.
	DestinationWorkspaceID string
	// Keep stops the operation after the archive.
	Keep bool
	// AllowUnpublished permits removal of a branch with unpublished commits.
	AllowUnpublished bool
	// CurrentPane is the tmux pane that runs the command.
	CurrentPane string
	// CloseTicket is the slug of the Ticket that the worker closes after a
	// successful removal. It is empty when done closes no Ticket.
	CloseTicket string
	// CloseClaimant is the resolved claimant of the Ticket close.
	CloseClaimant string
}

type Options struct {
	ConfigDir  string
	StateDir   string
	DataDir    string
	TmuxSocket string
	// TicketsHome is the root directory of the Markdown ticket files. When it
	// is empty, twt resolves TWT_TICKETS_HOME and then the ticketsHome value
	// of config.yaml at command time.
	TicketsHome string
	// BranchPrefix is the user branch prefix for the {prefix} token of
	// Workspace branch patterns. When it is empty, twt resolves
	// TWT_BRANCH_PREFIX and then the branchPrefix value of config.yaml at
	// command time.
	BranchPrefix string
	Stdout       io.Writer
	Stderr       io.Writer
	// OpenEditor opens one file in the interactive editor and returns after
	// the editor closes. New installs the real VISUAL/EDITOR implementation
	// when it is nil; tests replace it with a fake.
	OpenEditor func(path string) error
	// QuickCreateSwitch moves the calling tmux client to a session. New
	// installs the real tmux implementation when it is nil; tests replace it
	// with a fake.
	QuickCreateSwitch func(clientName, session string) error
	// QuickCreateArchive archives the old Workspace after the client switch.
	// New installs the real relocation worker implementation when it is nil.
	QuickCreateArchive func(clientName, oldWorkspaceID, newWorkspaceID string) error
	// DoneRelocate moves the calling tmux client out of the Workspace session
	// and completes the archive or removal. New installs the real relocation
	// worker implementation when it is nil.
	DoneRelocate func(request RelocationRequest) error
	// SwitchPick selects one line index from the switch Workspace picker. New
	// installs the real fzf or numbered-list implementation when it is nil.
	SwitchPick func(command *cobra.Command, lines []string) (int, error)
	// AgentPick selects one line index from the agents open picker. New uses
	// the real fzf or numbered-list implementation when it is nil.
	AgentPick func(command *cobra.Command, lines []string) (int, error)
	// AgentOpenExec replaces the current process with the provider resume
	// command. New installs the real exec implementation when it is nil.
	AgentOpenExec func(name string, argv []string, env []string) error
	// TicketPick selects one line index from the start Ticket picker. New
	// uses the real fzf or numbered-list implementation when it is nil.
	TicketPick func(command *cobra.Command, lines []string) (int, error)
	// PickTicketProject selects one Project picker line. The result is "(none)",
	// an existing Project name, or a typed new name. New installs the real fzf
	// or numbered-list implementation when it is nil.
	PickTicketProject      func(command *cobra.Command, lines []string) (string, error)
	QuickCreateExecutable  string
	QuickCreateWaitTimeout time.Duration
	PreparationExecutable  string
}

// workspaceService builds the Workspace service for these Options.
func (o Options) workspaceService() *workspaceservice.Service {
	return workspaceservice.NewService(o.workspaceServiceOptions())
}

// workspaceServiceOptions builds the base Workspace service configuration.
func (o Options) workspaceServiceOptions() workspaceservice.Options {
	return workspaceservice.Options{StateDir: o.StateDir, DataDir: o.DataDir, TmuxSocket: o.TmuxSocket}
}

// agentService builds the Agent Session service for these Options.
func (o Options) agentService() *agentservice.Service {
	return agentservice.NewService(o.StateDir, o.TmuxSocket)
}

// templateStore builds the Workspace Template store for these Options.
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
	userHome, _ := os.UserHomeDir()
	return maintenance.NewService(o.ConfigDir, o.StateDir, o.DataDir, home).
		WithTmuxSocket(o.TmuxSocket).
		WithSkillCheck(version.Version, skillasset.UserPaths(userHome))
}

// resolveTicketsHome resolves the Tickets home: the injected Options value,
// then TWT_TICKETS_HOME, then the ticketsHome value of config.yaml. The
// result is empty when no source sets a home.
func (o Options) resolveTicketsHome() (string, error) {
	if o.TicketsHome != "" {
		return o.TicketsHome, nil
	}
	if value := os.Getenv("TWT_TICKETS_HOME"); value != "" {
		return value, nil
	}
	config, err := store.LoadConfig(o.ConfigDir)
	if err != nil {
		return "", err
	}
	return config.TicketsHome, nil
}

// resolveBranchPrefix resolves the user branch prefix: the injected Options
// value, then TWT_BRANCH_PREFIX, then the branchPrefix value of config.yaml.
// The result is empty when no source sets a prefix.
func (o Options) resolveBranchPrefix() (string, error) {
	if o.BranchPrefix != "" {
		return o.BranchPrefix, nil
	}
	if value := os.Getenv("TWT_BRANCH_PREFIX"); value != "" {
		return value, nil
	}
	config, err := store.LoadConfig(o.ConfigDir)
	if err != nil {
		return "", err
	}
	return config.BranchPrefix, nil
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
			"Set ticketsHome in ~/.config/twt/config.yaml or TWT_TICKETS_HOME.")
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
		ConfigDir:  envOr("TWT_CONFIG_DIR", filepath.Join(configHome, "twt")),
		StateDir:   envOr("TWT_STATE_DIR", filepath.Join(stateHome, "twt")),
		DataDir:    envOr("TWT_DATA_DIR", filepath.Join(dataHome, "twt")),
		TmuxSocket: os.Getenv("TWT_TMUX_SOCKET"),
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
	if options.PickTicketProject == nil {
		options.PickTicketProject = realPickTicketProject
	}
	if options.OpenEditor == nil {
		options.OpenEditor = realOpenEditor(options)
	}
	return options
}

// realOpenEditor starts the VISUAL or EDITOR command on one file and waits
// for it. The editor value splits on spaces; twt never starts a shell.
func realOpenEditor(options Options) func(path string) error {
	return func(path string) error {
		editor := os.Getenv("VISUAL")
		if strings.TrimSpace(editor) == "" {
			editor = os.Getenv("EDITOR")
		}
		if strings.TrimSpace(editor) == "" {
			return clierr.New(clierr.InvalidUsage, "set VISUAL or EDITOR to the editor command that twt must start")
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
		Use:     "twt",
		Version: version.Version,
		Short:   "Manage task-focused Workspaces with Git worktrees and tmux",
		Long: `Create task-focused Workspaces from reusable YAML templates.

Each Workspace can own multiple Git worktrees, one tmux window for each
repository, and a set of resumable coding Agent Sessions.`,
		Example: `  # Create, select, and claim a Ticket.
  twt tickets create "Fix auth token refresh"
  twt tickets ls --ready
  twt tickets claim fix-auth-tokens

  # Create the next Workspace and work in it.
  twt next fix-auth-tokens
  twt agents ls

  # Close the Ticket and remove the Workspace.
  twt tickets close fix-auth-tokens
  twt done

  # Or use a wizard to create a Workspace or select a Ticket.
  twt create
  twt tickets start

  # Restore active Workspace sessions after tmux restarts.
  twt workspaces open --all-active --dry-run
  twt workspaces open --all-active`,
		SilenceErrors: true,
		SilenceUsage:  true,
		PersistentPreRunE: func(command *cobra.Command, _ []string) error {
			format := resolvedOutputFormat(command)
			switch format {
			case outputText, outputJSON:
			case outputNDJSON:
				if command.Annotations[ndjsonAnnotation] != "true" {
					return invalidUsage(command, "this command does not support ndjson output; use ndjson only on list commands")
				}
			default:
				return invalidUsage(command, "unsupported output %q; use text, json, or ndjson", format)
			}
			if fields := command.Flags().Lookup("fields"); fields != nil && fields.Changed && format == outputText {
				return invalidUsage(command, "use --fields with --output json")
			}
			return nil
		},
	}
	root.SetOut(options.Stdout)
	root.SetErr(options.Stderr)
	root.SetFlagErrorFunc(func(command *cobra.Command, err error) error {
		return invalidUsage(command, "%v", err)
	})
	root.PersistentFlags().String("output", "text", "Set all command output to text, json, or ndjson. Without a terminal the default is json")
	root.PersistentFlags().Bool("dry-run", false, "Validate and show a mutation without applying it")
	setFlagEnum(root, "output", outputFormatNames...)
	root.AddGroup(
		&cobra.Group{ID: "workflows", Title: "Workflows:"},
		&cobra.Group{ID: "inspect", Title: "Inspect and maintain:"},
		&cobra.Group{ID: "automation", Title: "Automation:"},
	)
	templates := newTemplatesCommand(options)
	templates.GroupID = "workflows"
	workspaces := newWorkspacesCommand(options)
	workspaces.GroupID = "workflows"
	projects := newProjectsCommand(options)
	projects.GroupID = "workflows"
	create := newWorkspacesCreateCommand(options, options.workspaceService())
	create.GroupID = "workflows"
	next := newNextCommand(options)
	next.GroupID = "workflows"
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
	configCommand := newConfigCommand(options)
	configCommand.GroupID = "inspect"
	environments := newEnvironmentsCommand(options)
	environments.GroupID = "inspect"
	storage := newStorageCommand(options)
	storage.GroupID = "inspect"
	doctor := newDoctorCommand(options)
	doctor.GroupID = "inspect"
	schema := newSchemaCommand(root)
	schema.GroupID = "automation"
	skillsCommand := newSkillsCommand()
	skillsCommand.GroupID = "automation"
	apply := newApplyCommand(options)
	apply.GroupID = "automation"
	root.AddCommand(templates, workspaces, projects, create, next, switchCommand, archive, done, agents, tickets, context, configCommand, environments, storage, doctor, schema, skillsCommand, apply)
	root.SetHelpCommandGroupID("automation")
	root.SetCompletionCommandGroupID("automation")
	configureCommandHelp(root)
	return root
}

func WantsJSON(command *cobra.Command) bool {
	return resolvedOutputFormat(command) == outputJSON
}

// resolvedOutputFormat resolves the --output value of one command run in one
// place. When --output is not set and standard output is not a terminal, the
// format is json. An explicit --output value always wins.
func resolvedOutputFormat(command *cobra.Command) string {
	flag := command.Flags().Lookup("output")
	if flag == nil {
		return outputText
	}
	if !flag.Changed && !terminalWriter(command.OutOrStdout()) {
		return outputJSON
	}
	return flag.Value.String()
}

func envOr(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}
