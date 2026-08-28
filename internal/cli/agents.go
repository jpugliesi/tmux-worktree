package cli

import (
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	agentservice "github.com/jpugliesi/tmux-worktree/internal/agent"
	"github.com/jpugliesi/tmux-worktree/internal/clierr"
	"github.com/jpugliesi/tmux-worktree/internal/domain"
	transcriptservice "github.com/jpugliesi/tmux-worktree/internal/transcript"
	workspaceservice "github.com/jpugliesi/tmux-worktree/internal/workspace"
	"github.com/spf13/cobra"
)

func newAgentsCommand(options Options) *cobra.Command {
	agents := agentservice.NewService(options.StateDir, options.TmuxSocket)
	workspaces := options.workspaceService()
	command := groupCommand(&cobra.Command{Use: "agents", Short: "Manage Agent Sessions for Workspaces"})
	command.AddCommand(newAgentsRegisterCommand(agents, workspaces))
	command.AddCommand(newAgentsAdoptCommand(agents, workspaces, options.StateDir))
	command.AddCommand(newAgentsListCommand(agents, workspaces, options.StateDir))
	command.AddCommand(newAgentsShowCommand(agents, workspaces, options.StateDir))
	command.AddCommand(newAgentsDiscoverCommand(agents, workspaces, options.StateDir))
	command.AddCommand(newAgentsRemoveCommand(agents, workspaces, options.StateDir))
	command.AddCommand(newAgentsResumeCommand(agents, workspaces, options.StateDir))
	command.AddCommand(newAgentsFocusCommand(agents, workspaces, options.StateDir))
	command.AddCommand(newAgentsOpenCommand(options, agents, workspaces, options.StateDir))
	command.AddCommand(newAgentsSendCommand(agents, workspaces, options.StateDir))
	command.AddCommand(newAgentTranscriptCommand(agents, workspaces, options.StateDir))
	return command
}

func newAgentsAdoptCommand(agents *agentservice.Service, workspaces *workspaceservice.Service, stateDir string) *cobra.Command {
	var workspaceReference string
	command := &cobra.Command{
		Use:   "adopt AGENT_ID",
		Short: "Register one discovered Agent Session",
		Args:  exactArgs("AGENT_ID"),
		RunE: func(command *cobra.Command, args []string) error {
			workspace, err := resolveWorkspace(workspaces, workspaceReference)
			if err != nil {
				return err
			}
			if existing, findErr := agents.Find(args[0]); findErr == nil {
				return clierr.New(clierr.AlreadyExists, "Agent Session %q is already registered", existing.ID)
			} else if clierr.CodeOf(findErr) != clierr.NotFound {
				return findErr
			}
			agent, adopted, err := findOrAdoptAgent(command, agents, workspace, stateDir, args[0])
			if err != nil {
				return err
			}
			if !adopted {
				return clierr.New(clierr.NotFound, "discovered Agent Session %q does not exist", args[0])
			}
			if WantsJSON(command) {
				return writeJSONOutput(command, agentActionOutput{
					SchemaVersion: jsonSchemaVersion,
					Agent:         toAgentOutput(agents, agent, workspace.Status == domain.WorkspaceActive, !isDryRun(command)),
				})
			}
			if isDryRun(command) {
				_, err = fmt.Fprintf(command.OutOrStdout(), "Agent Session %s is valid for adoption\n", agent.ID)
			} else {
				_, err = fmt.Fprintf(command.OutOrStdout(), "Adopted Agent Session %s (%s)\n", agent.ID, agent.Label)
			}
			return err
		},
	}
	command.Flags().StringVar(&workspaceReference, "workspace", "current", "Select the Workspace by name or ID")
	setAgentCommandCompletion(command, agents, workspaces, stateDir)
	return command
}

func newAgentsRegisterCommand(agents *agentservice.Service, workspaces *workspaceservice.Service) *cobra.Command {
	var workspaceReference string
	var provider string
	var label string
	var pane string
	var providerSessionID string
	command := &cobra.Command{
		Use:   "register [-- RESUME_COMMAND...]",
		Short: "Register an Agent Session with a Workspace",
		Args: func(command *cobra.Command, args []string) error {
			if len(args) > 0 && command.ArgsLenAtDash() != 0 {
				return invalidUsage(command, "put RESUME_COMMAND after --")
			}
			return nil
		},
		PreRunE: func(command *cobra.Command, args []string) error {
			if provider == "" && len(args) == 0 && pane == "" {
				return invalidUsage(command, "set --provider PROVIDER, or give a resume command after --")
			}
			return nil
		},
		RunE: func(command *cobra.Command, args []string) error {
			workspace, err := resolveWorkspace(workspaces, workspaceReference)
			if err != nil {
				return err
			}
			return registerAgent(command, agents, workspace, provider, label, pane, providerSessionID, args)
		},
	}
	command.Flags().StringVar(&workspaceReference, "workspace", "current", "Select the Workspace by name or ID")
	command.Flags().StringVar(&provider, "provider", "", fmt.Sprintf("Set the provider: %s. twt infers it from the resume command", strings.Join(agentProviderNames, ", ")))
	command.Flags().StringVar(&label, "label", "", "Set the display label. The default label is the provider name")
	command.Flags().StringVar(&pane, "pane", "", "Set an owned tmux pane ID, or use current")
	command.Flags().StringVar(&providerSessionID, "session", "", "Link the provider session ID for transcript loading. twt infers it from the resume command")
	setArguments(command, variadicArgument("resume_command", false, "required when --pane is empty"))
	_ = command.RegisterFlagCompletionFunc("workspace", workspaceFlagCompletion(workspaces))
	setFlagEnum(command, "provider", agentProviderNames...)
	return command
}

// currentPaneReference is the literal --pane value that selects the tmux pane
// of the calling terminal.
const currentPaneReference = "current"

// registerAgent registers one Agent Session with a Workspace. Both the agents
// register command and apply use it.
func registerAgent(command *cobra.Command, agents *agentservice.Service, workspace domain.Workspace, provider, label, pane, providerSessionID string, resumeCommand []string) error {
	if pane == currentPaneReference {
		pane = os.Getenv("TMUX_PANE")
	}
	return runMutation(command, "agents.register",
		func() (string, string, error) {
			if err := agents.ValidateRegistration(workspace, provider, pane, providerSessionID, resumeCommand); err != nil {
				return "", "", err
			}
			return "", label, agents.ValidateLabel(workspace.ID, label)
		},
		func() (string, string, error) {
			agent, err := agents.Register(workspace, provider, label, pane, providerSessionID, resumeCommand)
			return agent.ID, agent.Label, err
		},
		func(out io.Writer, id, name string) error {
			_, err := fmt.Fprintf(out, "Registered Agent Session %s (%s)\n", id, name)
			return err
		})
}

// requireAgentInWorkspace checks that the Agent Session belongs to the Workspace.
func requireAgentInWorkspace(agent domain.AgentSession, workspace domain.Workspace) error {
	if agent.WorkspaceID != workspace.ID {
		return transcriptservice.NotInWorkspaceError(agent.ID, workspace.Name)
	}
	return nil
}

// setAgentCommandCompletion declares the AGENT_ID argument of one Agent
// Session command with its completion and the --workspace flag completion.
func setAgentCommandCompletion(command *cobra.Command, agents *agentservice.Service, workspaces *workspaceservice.Service, stateDir string) {
	setAgentIDArgument(command)
	command.ValidArgsFunction = agentReferenceCompletion(agents, workspaces, stateDir)
	if command.Flags().Lookup("workspace") != nil {
		_ = command.RegisterFlagCompletionFunc("workspace", workspaceFlagCompletion(workspaces))
	}
}

func newAgentsListCommand(agents *agentservice.Service, workspaces *workspaceservice.Service, stateDir string) *cobra.Command {
	var workspaceReference string
	var limit, offset int
	var live bool
	var registered bool
	command := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List Agent Sessions for a Workspace",
		Args:    noArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			workspace, err := resolveWorkspace(workspaces, workspaceReference)
			if err != nil {
				return err
			}
			outputs, complete, diagnostics, err := workspaceAgentOutputs(agents, workspace, live, registered)
			if err != nil {
				return err
			}
			outputs, total, truncated, err := applyWindow(outputs, offset, limit)
			if err != nil {
				return err
			}
			if format := resolvedOutputFormat(command); format != outputText {
				if format == outputNDJSON {
					return writeDiscoveryNDJSONList(command, outputs, total, truncated, complete, diagnostics)
				}
				return writeReadJSON(command, agentsListOutput{
					SchemaVersion: jsonSchemaVersion, WorkspaceID: workspace.ID, Agents: outputs,
					Complete: complete, Diagnostics: diagnostics, TotalCount: total, Truncated: truncated,
				}, "agents")
			}
			now := time.Now()
			rows := make([][]string, 0, len(outputs))
			for _, output := range outputs {
				rows = append(rows, []string{output.Provider, output.ID, formatAge(now.Sub(output.recency))})
			}
			if err := writeTable(command.OutOrStdout(), []string{"PROVIDER", "ID", "AGE"}, rows); err != nil {
				return err
			}
			if !complete {
				if _, err := fmt.Fprintln(command.ErrOrStderr(), "Warning: Agent Session discovery is incomplete."); err != nil {
					return err
				}
				for _, diagnostic := range diagnostics {
					if _, err := fmt.Fprintf(command.ErrOrStderr(), "Warning: %s\n", diagnostic); err != nil {
						return err
					}
				}
			}
			return nil
		},
	}
	command.Flags().StringVar(&workspaceReference, "workspace", "current", "Select the Workspace by name or ID")
	addListReadFlags(command, &limit, &offset, agentOutput{})
	command.Flags().BoolVar(&live, "live", true, "Probe tmux for live state and scan the providers for discovered sessions. --live=false is the cheap statusline path: it does not probe tmux and does not scan the providers")
	command.Flags().BoolVar(&registered, "registered", false, "List only registered Agent Sessions; do not scan providers")
	_ = command.RegisterFlagCompletionFunc("workspace", workspaceFlagCompletion(workspaces))
	return command
}

// workspaceAgentOutputs lists the Agent Sessions of one Workspace in the same
// order as `twt agents list`: newest first. Registered and discovered
// sessions share one recency order. The scan only reads.
func workspaceAgentOutputs(agents *agentservice.Service, workspace domain.Workspace, live, registered bool) ([]agentOutput, bool, []string, error) {
	if live && !registered {
		catalog, err := agents.Catalog(workspace)
		if err != nil {
			return nil, false, nil, err
		}
		outputs := make([]agentOutput, 0, len(catalog.Entries))
		for _, entry := range catalog.Entries {
			outputs = append(outputs, catalogAgentOutput(workspace, entry))
		}
		sortAgentsForDisplay(outputs)
		return outputs, catalog.Complete, catalog.Diagnostics, nil
	}
	values, err := agents.List(workspace.ID)
	if err != nil {
		return nil, false, nil, err
	}
	outputs := make([]agentOutput, 0, len(values))
	for _, value := range values {
		outputs = append(outputs, toAgentOutput(agents, value, workspace.Status == domain.WorkspaceActive, live))
	}
	sortAgentsForDisplay(outputs)
	return outputs, true, nil, nil
}

func newAgentsShowCommand(agents *agentservice.Service, workspaces *workspaceservice.Service, stateDir string) *cobra.Command {
	var workspaceReference string
	command := &cobra.Command{
		Use:   "get AGENT_ID",
		Short: "Get one Agent Session and each liveness check",
		Args:  exactArgs("AGENT_ID"),
		RunE: func(command *cobra.Command, args []string) error {
			workspace, err := resolveWorkspace(workspaces, workspaceReference)
			if err != nil {
				return err
			}
			agent, err := agents.Find(args[0])
			if err != nil {
				return err
			}
			if err := requireAgentInWorkspace(agent, workspace); err != nil {
				return err
			}
			probe := agents.Probe(agent)
			output := toAgentOutputWithProbe(agent, workspace.Status == domain.WorkspaceActive, probe)
			checks := []agentCheck{}
			for _, check := range probe.Checks {
				checks = append(checks, agentCheck{Name: check.Name, OK: check.OK, Advisory: check.Advisory})
			}
			if WantsJSON(command) {
				return writeReadJSON(command, agentShowOutput{SchemaVersion: jsonSchemaVersion, Agent: output, Liveness: checks}, "")
			}
			linked := "no"
			if output.ProviderSessionID != "" {
				linked = output.ProviderSessionID
			}
			if err := writeFields(command.OutOrStdout(), [][2]string{
				{"ID", output.ID},
				{"Provider", output.Provider},
				{"Label", output.Label},
				{"Status", output.Status},
				{"Created", output.CreatedAt},
				{"Provider session", linked},
				{"Can resume", boolText(output.Capabilities.CanResume)},
				{"Can send", boolText(output.Capabilities.CanSend)},
				{"Can focus", boolText(output.Capabilities.CanFocus)},
				{"Can read transcript", boolText(output.Capabilities.CanReadTranscript)},
			}); err != nil {
				return err
			}
			if len(checks) == 0 {
				return nil
			}
			if _, err := fmt.Fprintln(command.OutOrStdout()); err != nil {
				return err
			}
			rows := make([][]string, 0, len(checks))
			for _, check := range checks {
				state := "fail"
				if check.OK {
					state = "pass"
				}
				if check.Advisory {
					state += " (advisory)"
				}
				rows = append(rows, []string{check.Name, state})
			}
			return writeTable(command.OutOrStdout(), []string{"CHECK", "RESULT"}, rows)
		},
	}
	command.Flags().StringVar(&workspaceReference, "workspace", "current", "Select the Workspace by name or ID")
	addFieldsFlag(command, agentShowOutput{})
	setAgentCommandCompletion(command, agents, workspaces, stateDir)
	return command
}

func newAgentsRemoveCommand(agents *agentservice.Service, workspaces *workspaceservice.Service, stateDir string) *cobra.Command {
	var workspaceReference string
	command := &cobra.Command{
		Use:     "rm AGENT_ID",
		Aliases: []string{"remove"},
		Short:   "Delete an Agent Session record",
		Args:    exactArgs("AGENT_ID"),
		RunE: func(command *cobra.Command, args []string) error {
			workspace, err := resolveWorkspace(workspaces, workspaceReference)
			if err != nil {
				return err
			}
			return removeAgentSession(command, agents, args[0], workspace, stateDir)
		},
	}
	command.Flags().StringVar(&workspaceReference, "workspace", "current", "Select the Workspace by name or ID")
	setAgentCommandCompletion(command, agents, workspaces, stateDir)
	return command
}

// removeAgentSession deletes one Agent Session record of a Workspace. Both the
// agents rm command and apply use it. A reference that names a discovered but
// unregistered provider session is invalid usage: rm does not adopt.
func removeAgentSession(command *cobra.Command, agents *agentservice.Service, agentID string, workspace domain.Workspace, stateDir string) error {
	return runMutation(command, "agents.rm",
		func() (string, string, error) {
			agent, err := agents.ValidateRemove(agentID, workspace.ID)
			return agent.ID, agent.Label, notRegisteredForRemoval(agents, workspace, stateDir, agentID, err)
		},
		func() (string, string, error) {
			agent, err := agents.Remove(agentID, workspace.ID)
			return agent.ID, agent.Label, notRegisteredForRemoval(agents, workspace, stateDir, agentID, err)
		},
		func(out io.Writer, id, name string) error {
			_, err := fmt.Fprintf(out, "Removed Agent Session %s (%s)\n", id, name)
			return err
		})
}

func newAgentsDiscoverCommand(agents *agentservice.Service, workspaces *workspaceservice.Service, stateDir string) *cobra.Command {
	var workspaceReference string
	var adopt bool
	var limit, offset int
	command := &cobra.Command{
		Use:   "discover",
		Short: "Find provider sessions of a Workspace that no Agent Session uses",
		Args:  noArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			workspace, err := resolveWorkspace(workspaces, workspaceReference)
			if err != nil {
				return err
			}
			registered, err := agents.List(workspace.ID)
			if err != nil {
				return err
			}
			found, err := discoverWorkspaceSessions(workspace, stateDir, registered)
			if err != nil {
				return err
			}
			found, total, truncated, err := applyWindow(found, offset, limit)
			if err != nil {
				return err
			}
			sessions := make([]discoveredOutput, 0, len(found))
			for _, session := range found {
				sessions = append(sessions, discoveredOutput{
					Provider: session.Provider, SessionID: session.SessionID,
					Repository: session.RepositoryName, LastActivity: session.LastActivity.UTC().Format(time.RFC3339),
				})
			}
			result := agentsDiscoverOutput{
				SchemaVersion: jsonSchemaVersion, WorkspaceID: workspace.ID, Sessions: sessions,
				TotalCount: total, Truncated: truncated,
			}
			if adopt {
				result.Status = statusApplied
				if isDryRun(command) {
					result.Status = statusValid
				}
				result.Adopted, err = adoptSessions(command, agents, workspace, found)
				if err != nil {
					return err
				}
			}
			if format := resolvedOutputFormat(command); format != outputText {
				if format == outputNDJSON {
					return writeNDJSONList(command, sessions, total, truncated)
				}
				return writeReadJSON(command, result, "sessions")
			}
			now := time.Now()
			rows := make([][]string, 0, len(found))
			for _, session := range found {
				rows = append(rows, []string{session.Provider, session.SessionID, session.RepositoryName, formatAge(now.Sub(session.LastActivity))})
			}
			if err := writeTable(command.OutOrStdout(), []string{"PROVIDER", "ID", "REPOSITORY", "AGE"}, rows); err != nil {
				return err
			}
			for _, agentID := range result.Adopted {
				if _, err := fmt.Fprintf(command.OutOrStdout(), "Registered Agent Session %s\n", agentID); err != nil {
					return err
				}
			}
			return nil
		},
	}
	command.Flags().StringVar(&workspaceReference, "workspace", "current", "Select the Workspace by name or ID")
	command.Flags().BoolVar(&adopt, "adopt", false, "Register each discovered provider session as an Agent Session")
	addListReadFlags(command, &limit, &offset, discoveredOutput{})
	_ = command.RegisterFlagCompletionFunc("workspace", workspaceFlagCompletion(workspaces))
	return command
}

// adoptSessions registers each discovered provider session with a generated
// resume command. A dry run makes no record.
func adoptSessions(command *cobra.Command, agents *agentservice.Service, workspace domain.Workspace, sessions []transcriptservice.DiscoveredSession) ([]string, error) {
	adopted := []string{}
	for _, session := range sessions {
		if isDryRun(command) {
			if _, err := validateAdoption(agents, workspace, session); err != nil {
				return nil, err
			}
			continue
		}
		agent, err := adoptDiscoveredSession(agents, workspace, session)
		if err != nil {
			return nil, err
		}
		adopted = append(adopted, agent.ID)
	}
	return adopted, nil
}

func newAgentsResumeCommand(agents *agentservice.Service, workspaces *workspaceservice.Service, stateDir string) *cobra.Command {
	command := &cobra.Command{
		Use:   "resume AGENT_ID",
		Short: "Resume or focus an Agent Session",
		Args:  exactArgs("AGENT_ID"),
		RunE: func(command *cobra.Command, args []string) error {
			return resumeAgentSession(command, agents, workspaces, stateDir, args[0], "")
		},
	}
	setAgentCommandCompletion(command, agents, workspaces, stateDir)
	return command
}

// resumeAgentSession resumes or focuses one Agent Session. An empty Workspace
// reference uses the Workspace of the Agent Session record; a set reference
// must name that same Workspace. A reference that names a discovered provider
// session adopts it first. Both the agents resume command and apply use it.
func resumeAgentSession(command *cobra.Command, agents *agentservice.Service, workspaces *workspaceservice.Service, stateDir, agentID, workspaceReference string) error {
	agent, err := agents.Find(agentID)
	if err != nil {
		agent, err = adoptForResume(command, agents, workspaces, stateDir, agentID, workspaceReference, err)
		if err != nil {
			return err
		}
	}
	workspace, err := findAgentWorkspace(workspaces, agent, workspaceReference)
	if err != nil {
		return err
	}
	if isDryRun(command) {
		if err := agents.ValidateResume(agent, workspace); err != nil {
			return err
		}
		return writeMutation(command, "agents.resume", statusValid, agent.ID, agent.Label)
	}
	agent, err = agents.Resume(agent, workspace)
	if err != nil {
		return err
	}
	if WantsJSON(command) {
		return writeJSONOutput(command, agentActionOutput{SchemaVersion: jsonSchemaVersion, Agent: toAgentOutput(agents, agent, workspace.Status == domain.WorkspaceActive, true)})
	}
	_, err = fmt.Fprintf(command.OutOrStdout(), "Resumed Agent Session %s\n", agent.ID)
	return err
}

// adoptForResume resolves a missed resume reference against the discovered
// provider sessions of the Workspace. An empty Workspace reference uses the
// current Workspace. When no Workspace resolves, the original lookup error stays.
func adoptForResume(command *cobra.Command, agents *agentservice.Service, workspaces *workspaceservice.Service, stateDir, agentID, workspaceReference string, lookupErr error) (domain.AgentSession, error) {
	if clierr.CodeOf(lookupErr) != clierr.NotFound {
		return domain.AgentSession{}, lookupErr
	}
	if workspaceReference == "" {
		workspaceReference = currentWorkspaceReference
	}
	workspace, err := resolveWorkspace(workspaces, workspaceReference)
	if err != nil {
		return domain.AgentSession{}, lookupErr
	}
	agent, _, err := findOrAdoptAgent(command, agents, workspace, stateDir, agentID)
	return agent, err
}

// findAgentWorkspace finds the Workspace of one Agent Session. An empty
// reference uses the Workspace ID of the Agent Session record. Every other
// reference resolves as a WORKSPACE value and must name that same Workspace.
func findAgentWorkspace(workspaces *workspaceservice.Service, agent domain.AgentSession, workspaceReference string) (domain.Workspace, error) {
	if workspaceReference == "" {
		return workspaces.Find(agent.WorkspaceID)
	}
	workspace, err := resolveWorkspace(workspaces, workspaceReference)
	if err != nil {
		return domain.Workspace{}, err
	}
	return workspace, requireAgentInWorkspace(agent, workspace)
}

func newAgentsFocusCommand(agents *agentservice.Service, workspaces *workspaceservice.Service, stateDir string) *cobra.Command {
	command := &cobra.Command{
		Use:   "focus AGENT_ID",
		Short: "Focus a live Agent Session pane",
		Args:  exactArgs("AGENT_ID"),
		RunE: func(command *cobra.Command, args []string) error {
			agent, err := agents.Find(args[0])
			adopted := false
			if clierr.CodeOf(err) == clierr.NotFound {
				workspace, workspaceErr := resolveWorkspace(workspaces, currentWorkspaceReference)
				if workspaceErr != nil {
					return err
				}
				agent, adopted, err = findOrAdoptAgent(command, agents, workspace, stateDir, args[0])
			}
			if err != nil {
				return err
			}
			return runMutation(command, "agents.focus",
				func() (string, string, error) {
					if adopted && isDryRun(command) {
						return agent.ID, agent.Label, nil
					}
					if !agents.IsLive(agent) {
						return "", "", agentservice.NotLiveError(agent.ID)
					}
					return agent.ID, agent.Label, nil
				},
				func() (string, string, error) {
					return agent.ID, agent.Label, agents.Focus(agent)
				},
				// A successful focus is visible in tmux itself; text mode
				// prints nothing.
				func(io.Writer, string, string) error { return nil })
		},
	}
	setAgentCommandCompletion(command, agents, workspaces, stateDir)
	return command
}

func newAgentsSendCommand(agents *agentservice.Service, workspaces *workspaceservice.Service, stateDir string) *cobra.Command {
	var workspaceReference string
	command := &cobra.Command{
		Use:   "send AGENT_ID -",
		Short: "Send feedback to a live Agent Session pane",
		Args:  requireResourceThenStdin("AGENT_ID"),
		RunE: func(command *cobra.Command, args []string) error {
			workspace, err := resolveWorkspace(workspaces, workspaceReference)
			if err != nil {
				return err
			}
			// Every twt command that reads standard input reads at most
			// 1 MiB, so one caller cannot fill memory or a tmux pane.
			data, err := io.ReadAll(io.LimitReader(command.InOrStdin(), 1024*1024))
			if err != nil {
				return fmt.Errorf("read feedback: %w", err)
			}
			return sendAgentFeedback(command, agents, workspace, stateDir, args[0], string(data))
		},
	}
	command.Flags().StringVar(&workspaceReference, "workspace", "current", "Select the Workspace by name or ID")
	setAgentCommandCompletion(command, agents, workspaces, stateDir)
	setArguments(command, requiredArgument("agent_id"), stdinTokenArgument(true))
	return command
}

// sendAgentFeedback sends one feedback text to the live pane of an Agent
// Session of the Workspace. Both the agents send command and apply use it. A
// reference that names a discovered provider session adopts it first; the
// send then reports that the Agent Session is not live, with the resume hint.
func sendAgentFeedback(command *cobra.Command, agents *agentservice.Service, workspace domain.Workspace, stateDir, agentID, text string) error {
	if text == "" {
		return clierr.New(clierr.InvalidUsage, "feedback input is empty")
	}
	agent, adopted, err := findOrAdoptAgent(command, agents, workspace, stateDir, agentID)
	if err != nil {
		return err
	}
	if err := requireAgentInWorkspace(agent, workspace); err != nil {
		return err
	}
	if isDryRun(command) {
		if text == "" {
			return clierr.New(clierr.InvalidUsage, "feedback input is empty")
		}
		if !(adopted && agent.ProcessID > 0) && !agents.CanSend(agent) {
			return agentservice.NotLiveError(agent.ID)
		}
		return writeMutation(command, "agents.send", statusValid, agent.ID, agent.Label)
	}
	if err := agents.Send(agent, workspace.ID, text); err != nil {
		return err
	}
	if WantsJSON(command) {
		return writeJSONOutput(command, agentSendOutput{SchemaVersion: jsonSchemaVersion, AgentID: agent.ID, Status: "sent"})
	}
	_, err = fmt.Fprintf(command.OutOrStdout(), "Sent feedback to Agent Session %s\n", agent.ID)
	return err
}
