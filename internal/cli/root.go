package cli

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	agentservice "github.com/jpugliesi/tmux-worktree/internal/agent"
	"github.com/jpugliesi/tmux-worktree/internal/clierr"
	"github.com/jpugliesi/tmux-worktree/internal/maintenance"
	"github.com/jpugliesi/tmux-worktree/internal/prstate"
	"github.com/jpugliesi/tmux-worktree/internal/store"
	ticketservice "github.com/jpugliesi/tmux-worktree/internal/ticket"
	"github.com/jpugliesi/tmux-worktree/internal/version"
	workspaceservice "github.com/jpugliesi/tmux-worktree/internal/workspace"
	skillasset "github.com/jpugliesi/tmux-worktree/skills"
	"github.com/spf13/cobra"
)

type Options struct {
	ConfigDir  string
	StateDir   string
	DataDir    string
	TmuxSocket string
	// Home is the shared twt home. When it is empty, twt resolves TWT_HOME
	// and then the home value of config.yaml. Tickets live at <home>/tickets
	// and shared Workspace Templates at <home>/templates.
	Home string
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
	PickTicketProject func(command *cobra.Command, lines []string) (string, error)
	// PickTemplate selects one Workspace Template name from the picker lines.
	// New installs the real fzf or numbered-list implementation when it is nil.
	PickTemplate          func(command *cobra.Command, lines []string) (int, error)
	PreparationExecutable string
	// PRResolvers replaces the live gh/origin PR-state resolvers. Tests use
	// fakes; nil installs the real ones.
	PRResolvers []prstate.Resolver
}

// workspaceService builds the Workspace service for these Options.
func (o Options) workspaceService() *workspaceservice.Service {
	return workspaceservice.NewService(o.workspaceServiceOptions())
}

// workspaceServiceOptions builds the base Workspace service configuration.
func (o Options) workspaceServiceOptions() workspaceservice.Options {
	return workspaceservice.Options{
		StateDir: o.StateDir, DataDir: o.DataDir, TmuxSocket: o.TmuxSocket,
		AfterReleaseFinalized: o.startReleaseRefill,
	}
}

// startReleaseRefill tops up the Prepared Environment pool after a release.
// The released environment returns to the pool by itself, so the refill only
// replaces environments that failed or no longer match the Workspace
// Template. It is best effort: a refill failure must not fail the release.
func (o Options) startReleaseRefill(templateName string) {
	if strings.TrimSpace(templateName) == "" {
		return
	}
	template, err := o.templateStore().Load(templateName)
	if err != nil {
		return
	}
	if err := startPreparationRefill(o, templateName, template); err != nil && o.Stderr != nil {
		_, _ = fmt.Fprintf(o.Stderr, "Warning: the next Prepared Environment was not started: %v\n", err)
	}
}

// agentService builds the Agent Session service for these Options.
func (o Options) agentService() *agentservice.Service {
	return agentservice.NewService(o.StateDir, o.TmuxSocket)
}

// templateStore builds the Workspace Template store for these Options. A
// configured twt home layers <home>/templates as the shared root: config-dir
// templates win name collisions, and new templates land in the shared root.
func (o Options) templateStore() store.TemplateStore {
	templates := store.NewTemplateStore(o.ConfigDir)
	if home := o.resolveTwtHome(); home != "" {
		templates = templates.WithSharedDir(filepath.Join(home, "templates"))
	}
	return templates
}

// resolveTwtHome resolves the shared twt home: the injected Options value,
// then TWT_HOME, then the home value of config.yaml. Empty means no shared
// home is configured.
func (o Options) resolveTwtHome() string {
	if o.Home != "" {
		return o.Home
	}
	if value := os.Getenv("TWT_HOME"); value != "" {
		return value
	}
	config, err := store.LoadConfig(o.ConfigDir)
	if err != nil {
		return ""
	}
	return config.Home
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
		WithSkillCheck(version.Version, skillasset.UserPaths(userHome)).
		WithTemplateStore(o.templateStore())
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
	if config.TicketsHome != "" {
		return config.TicketsHome, nil
	}
	// A shared twt home carries the tickets at <home>/tickets.
	if home := o.resolveTwtHome(); home != "" {
		return filepath.Join(home, "tickets"), nil
	}
	return "", nil
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

// resolveTicketsSync resolves the tickets git sync configuration:
// TWT_TICKETS_SYNC and TWT_TICKETS_SYNC_REMOTE, then the ticketsSync block of
// config.yaml.
func (o Options) resolveTicketsSync() (store.TicketsSyncConfig, error) {
	config, err := store.LoadConfig(o.ConfigDir)
	if err != nil {
		return store.TicketsSyncConfig{}, err
	}
	resolved := config.TicketsSync
	if value := os.Getenv("TWT_TICKETS_SYNC"); value != "" {
		resolved.Mode = value
	}
	if value := os.Getenv("TWT_TICKETS_SYNC_REMOTE"); value != "" {
		resolved.Remote = value
	}
	if err := validateTicketsSyncConfig(resolved); err != nil {
		return store.TicketsSyncConfig{}, err
	}
	return resolved, nil
}

// ticketService builds the ticket service for these Options. It fails when no
// Tickets home is set, so every tickets command reports the same
// precondition error.
func (o Options) ticketService() (ticketservice.Store, error) {
	home, err := o.resolveTicketsHome()
	if err != nil {
		return nil, err
	}
	if home == "" {
		return nil, clierr.WithHint(
			clierr.New(clierr.PreconditionFailed, "no Tickets home is set"),
			"Set home or ticketsHome in ~/.config/twt/config.yaml, or TWT_HOME or TWT_TICKETS_HOME.")
	}
	sync, err := o.resolveTicketsSync()
	if err != nil {
		return nil, err
	}
	return ticketservice.NewService(ticketservice.Options{
		Home:     home,
		StateDir: o.StateDir,
		Sync:     ticketservice.SyncOptions{Mode: sync.Mode, Remote: sync.Remote, Root: o.resolveTwtHome()},
		Logf: func(format string, a ...any) {
			fmt.Fprintf(os.Stderr, format+"\n", a...)
		},
	}), nil
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
	if options.SwitchPick == nil {
		options.SwitchPick = realSwitchPick
	}
	if options.PickTicketProject == nil {
		options.PickTicketProject = realPickTicketProject
	}
	if options.PickTemplate == nil {
		options.PickTemplate = realPickTemplate
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
	root.PersistentFlags().String("output", "text", "Set all command output to text, json, or ndjson")
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
	syncCommand := newTicketsSyncCommand(options)
	syncCommand.GroupID = "workflows"
	labels := newLabelsCommand(options)
	labels.GroupID = "workflows"
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
	daemon := newDaemonCommand(options)
	daemon.GroupID = "automation"
	root.AddCommand(templates, workspaces, projects, labels, create, next, switchCommand, archive, done, agents, tickets, syncCommand, context, configCommand, environments, storage, doctor, schema, skillsCommand, apply, daemon)
	root.SetHelpCommandGroupID("automation")
	root.SetCompletionCommandGroupID("automation")
	configureCommandHelp(root)
	return root
}

func WantsJSON(command *cobra.Command) bool {
	return resolvedOutputFormat(command) == outputJSON
}

// resolvedOutputFormat resolves the --output value of one command run in one
// place. The default is text. An explicit --output value always wins. A pipe
// does not change the format.
func resolvedOutputFormat(command *cobra.Command) string {
	flag := command.Flags().Lookup("output")
	if flag == nil {
		return outputText
	}
	return flag.Value.String()
}

func envOr(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

// prStateService builds the PR-state reader. Tests inject fake resolvers
// through Options.PRResolvers.
func (o Options) prStateService() *prstate.Service {
	return prstate.NewService(o.StateDir, o.PRResolvers)
}
