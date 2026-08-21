package cli

import (
	"fmt"
	"io"
	"os"
	"time"

	agentservice "github.com/jpugliesi/tmux-worktree/internal/agent"
	"github.com/jpugliesi/tmux-worktree/internal/clierr"
	"github.com/jpugliesi/tmux-worktree/internal/domain"
	projectservice "github.com/jpugliesi/tmux-worktree/internal/project"
	transcriptservice "github.com/jpugliesi/tmux-worktree/internal/transcript"
	"github.com/spf13/cobra"
)

func newAgentsCommand(options Options) *cobra.Command {
	agents := agentservice.NewService(options.StateDir, options.TmuxSocket)
	projects := options.projectService()
	command := groupCommand(&cobra.Command{Use: "agents", Short: "Manage Agent Sessions for Projects"})
	command.AddCommand(newAgentsRegisterCommand(agents, projects))
	command.AddCommand(newAgentsListCommand(agents, projects, options.StateDir))
	command.AddCommand(newAgentsShowCommand(agents, projects, options.StateDir))
	command.AddCommand(newAgentsDiscoverCommand(agents, projects, options.StateDir))
	command.AddCommand(newAgentsRemoveCommand(agents, projects, options.StateDir))
	command.AddCommand(newAgentsResumeCommand(agents, projects, options.StateDir))
	command.AddCommand(newAgentsFocusCommand(agents, projects))
	command.AddCommand(newAgentsSendCommand(agents, projects, options.StateDir))
	command.AddCommand(newAgentTranscriptCommand(agents, projects, options.StateDir))
	return command
}

func newAgentsRegisterCommand(agents *agentservice.Service, projects *projectservice.Service) *cobra.Command {
	var projectReference string
	var provider string
	var label string
	var pane string
	var providerSessionID string
	command := &cobra.Command{
		Use:   "register [-- RESUME_COMMAND...]",
		Short: "Register an Agent Session with a Project",
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
			project, err := resolveProject(projects, projectReference)
			if err != nil {
				return err
			}
			return registerAgent(command, agents, project, provider, label, pane, providerSessionID, args)
		},
	}
	command.Flags().StringVar(&projectReference, "project", "current", "Select the Project by name or ID")
	command.Flags().StringVar(&provider, "provider", "", "Set the provider: codex, claude, cursor, or command. twt infers it from the resume command")
	command.Flags().StringVar(&label, "label", "", "Set the display label. The default label is the provider name")
	command.Flags().StringVar(&pane, "pane", "", "Set an owned tmux pane ID, or use current")
	command.Flags().StringVar(&providerSessionID, "session", "", "Link the provider session ID for transcript loading. twt infers it from the resume command")
	setArguments(command, variadicArgument("resume_command", false, "required when --pane is empty"))
	_ = command.RegisterFlagCompletionFunc("project", projectFlagCompletion(projects))
	setFlagEnum(command, "provider", agentProviderNames...)
	return command
}

// currentPaneReference is the literal --pane value that selects the tmux pane
// of the calling terminal.
const currentPaneReference = "current"

// registerAgent registers one Agent Session with a Project. Both the agents
// register command and apply use it.
func registerAgent(command *cobra.Command, agents *agentservice.Service, project domain.Project, provider, label, pane, providerSessionID string, resumeCommand []string) error {
	if pane == currentPaneReference {
		pane = os.Getenv("TMUX_PANE")
	}
	return runMutation(command, "agents.register",
		func() (string, string, error) {
			if err := agents.ValidateRegistration(project, provider, pane, providerSessionID, resumeCommand); err != nil {
				return "", "", err
			}
			return "", label, agents.ValidateLabel(project.ID, label)
		},
		func() (string, string, error) {
			agent, err := agents.Register(project, provider, label, pane, providerSessionID, resumeCommand)
			return agent.ID, agent.Label, err
		},
		func(out io.Writer, id, name string) error {
			_, err := fmt.Fprintf(out, "Registered Agent Session %s (%s)\n", id, name)
			return err
		})
}

// requireAgentInProject checks that the Agent Session belongs to the Project.
func requireAgentInProject(agent domain.AgentSession, project domain.Project) error {
	if agent.ProjectID != project.ID {
		return transcriptservice.NotInProjectError(agent.ID, project.Name)
	}
	return nil
}

// setAgentCommandCompletion declares the AGENT_ID argument of one Agent
// Session command with its completion and the --project flag completion.
func setAgentCommandCompletion(command *cobra.Command, agents *agentservice.Service, projects *projectservice.Service) {
	setAgentIDArgument(command)
	command.ValidArgsFunction = agentIDCompletion(agents, projects)
	if command.Flags().Lookup("project") != nil {
		_ = command.RegisterFlagCompletionFunc("project", projectFlagCompletion(projects))
	}
}

func newAgentsListCommand(agents *agentservice.Service, projects *projectservice.Service, stateDir string) *cobra.Command {
	var projectReference string
	var limit, offset int
	var live bool
	var registered bool
	command := &cobra.Command{
		Use:   "list",
		Short: "List Agent Sessions for a Project",
		Args:  noArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			project, err := resolveProject(projects, projectReference)
			if err != nil {
				return err
			}
			values, err := agents.List(project.ID)
			if err != nil {
				return err
			}
			outputs := make([]agentOutput, 0, len(values))
			for _, value := range values {
				outputs = append(outputs, toAgentOutput(agents, value, project.Status == domain.ProjectActive, live))
			}
			// The list also shows the discovered provider sessions after the
			// registered Agent Sessions, newest first. The scan only reads;
			// the first action on a discovered session adopts it.
			if live && !registered {
				found, err := discoverProjectSessions(project, stateDir, values)
				if err != nil {
					return err
				}
				for _, session := range found {
					outputs = append(outputs, discoveredAgentOutput(project, session))
				}
			}
			outputs, total, truncated, err := applyWindow(outputs, offset, limit)
			if err != nil {
				return err
			}
			if format := resolvedOutputFormat(command); format != outputText {
				if format == outputNDJSON {
					return writeNDJSONList(command, outputs, total, truncated)
				}
				return writeReadJSON(command, agentsListOutput{
					SchemaVersion: jsonSchemaVersion, ProjectID: project.ID, Agents: outputs,
					TotalCount: total, Truncated: truncated,
				}, "agents")
			}
			for _, output := range outputs {
				if _, err := fmt.Fprintf(command.OutOrStdout(), "%s\t%s\t%s\t%s\n", output.ID, output.Provider, output.Status, output.Label); err != nil {
					return err
				}
			}
			return nil
		},
	}
	command.Flags().StringVar(&projectReference, "project", "current", "Select the Project by name or ID")
	addListReadFlags(command, &limit, &offset, agentOutput{})
	command.Flags().BoolVar(&live, "live", true, "Probe tmux for live state and scan the providers for discovered sessions. --live=false is the cheap statusline path: it does not probe tmux and does not scan the providers")
	command.Flags().BoolVar(&registered, "registered", false, "List only registered Agent Sessions; do not scan providers")
	_ = command.RegisterFlagCompletionFunc("project", projectFlagCompletion(projects))
	return command
}

func newAgentsShowCommand(agents *agentservice.Service, projects *projectservice.Service, stateDir string) *cobra.Command {
	var projectReference string
	command := &cobra.Command{
		Use:   "show AGENT_ID",
		Short: "Show one Agent Session and each liveness check",
		Args:  exactArgs("AGENT_ID"),
		RunE: func(command *cobra.Command, args []string) error {
			project, err := resolveProject(projects, projectReference)
			if err != nil {
				return err
			}
			agent, _, err := findOrAdoptAgent(command, agents, project, stateDir, args[0])
			if err != nil {
				return err
			}
			if err := requireAgentInProject(agent, project); err != nil {
				return err
			}
			output := toAgentOutput(agents, agent, project.Status == domain.ProjectActive, true)
			checks := []agentCheck{}
			for _, check := range agents.ExplainLiveness(agent) {
				checks = append(checks, agentCheck{Name: check.Name, OK: check.OK, Advisory: check.Advisory})
			}
			if WantsJSON(command) {
				return writeReadJSON(command, agentShowOutput{SchemaVersion: jsonSchemaVersion, Agent: output, Liveness: checks}, "")
			}
			writer := command.OutOrStdout()
			linked := "no"
			if output.ProviderSessionID != "" {
				linked = output.ProviderSessionID
			}
			rows := [][2]string{
				{"id", output.ID},
				{"provider", output.Provider},
				{"label", output.Label},
				{"status", output.Status},
				{"created", output.CreatedAt},
				{"provider session", linked},
				{"can resume", boolText(output.Capabilities.CanResume)},
				{"can send", boolText(output.Capabilities.CanSend)},
				{"can focus", boolText(output.Capabilities.CanFocus)},
				{"can read transcript", boolText(output.Capabilities.CanReadTranscript)},
			}
			for _, row := range rows {
				if _, err := fmt.Fprintf(writer, "%s\t%s\n", row[0], row[1]); err != nil {
					return err
				}
			}
			for _, check := range checks {
				state := "fail"
				if check.OK {
					state = "pass"
				}
				if check.Advisory {
					state += " (advisory)"
				}
				if _, err := fmt.Fprintf(writer, "%s\t%s\n", check.Name, state); err != nil {
					return err
				}
			}
			return nil
		},
	}
	command.Flags().StringVar(&projectReference, "project", "current", "Select the Project by name or ID")
	addFieldsFlag(command, agentShowOutput{})
	setAgentCommandCompletion(command, agents, projects)
	return command
}

func newAgentsRemoveCommand(agents *agentservice.Service, projects *projectservice.Service, stateDir string) *cobra.Command {
	var projectReference string
	command := &cobra.Command{
		Use:     "rm AGENT_ID",
		Aliases: []string{"remove"},
		Short:   "Delete an Agent Session record",
		Args:    exactArgs("AGENT_ID"),
		RunE: func(command *cobra.Command, args []string) error {
			project, err := resolveProject(projects, projectReference)
			if err != nil {
				return err
			}
			return removeAgentSession(command, agents, args[0], project, stateDir)
		},
	}
	command.Flags().StringVar(&projectReference, "project", "current", "Select the Project by name or ID")
	setAgentCommandCompletion(command, agents, projects)
	return command
}

// removeAgentSession deletes one Agent Session record of a Project. Both the
// agents rm command and apply use it. A reference that names a discovered but
// unregistered provider session is invalid usage: rm does not adopt.
func removeAgentSession(command *cobra.Command, agents *agentservice.Service, agentID string, project domain.Project, stateDir string) error {
	return runMutation(command, "agents.rm",
		func() (string, string, error) {
			agent, err := agents.ValidateRemove(agentID, project.ID)
			return agent.ID, agent.Label, notRegisteredForRemoval(agents, project, stateDir, agentID, err)
		},
		func() (string, string, error) {
			agent, err := agents.Remove(agentID, project.ID)
			return agent.ID, agent.Label, notRegisteredForRemoval(agents, project, stateDir, agentID, err)
		},
		func(out io.Writer, id, name string) error {
			_, err := fmt.Fprintf(out, "Removed Agent Session %s (%s)\n", id, name)
			return err
		})
}

func newAgentsDiscoverCommand(agents *agentservice.Service, projects *projectservice.Service, stateDir string) *cobra.Command {
	var projectReference string
	var adopt bool
	var limit, offset int
	command := &cobra.Command{
		Use:   "discover",
		Short: "Find provider sessions of a Project that no Agent Session uses",
		Args:  noArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			project, err := resolveProject(projects, projectReference)
			if err != nil {
				return err
			}
			registered, err := agents.List(project.ID)
			if err != nil {
				return err
			}
			found, err := discoverProjectSessions(project, stateDir, registered)
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
				SchemaVersion: jsonSchemaVersion, ProjectID: project.ID, Sessions: sessions,
				TotalCount: total, Truncated: truncated,
			}
			if adopt {
				result.Status = statusApplied
				if isDryRun(command) {
					result.Status = statusValid
				}
				result.Adopted, err = adoptSessions(command, agents, project, found)
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
			writer := command.OutOrStdout()
			now := time.Now()
			for _, session := range found {
				age := formatAge(now.Sub(session.LastActivity)) + " ago"
				if _, err := fmt.Fprintf(writer, "%s\t%s\t%s\t%s\n", session.Provider, session.SessionID, session.RepositoryName, age); err != nil {
					return err
				}
			}
			for _, agentID := range result.Adopted {
				if _, err := fmt.Fprintf(writer, "Registered Agent Session %s\n", agentID); err != nil {
					return err
				}
			}
			return nil
		},
	}
	command.Flags().StringVar(&projectReference, "project", "current", "Select the Project by name or ID")
	command.Flags().BoolVar(&adopt, "adopt", false, "Register each discovered provider session as an Agent Session")
	addListReadFlags(command, &limit, &offset, discoveredOutput{})
	_ = command.RegisterFlagCompletionFunc("project", projectFlagCompletion(projects))
	return command
}

// adoptSessions registers each discovered provider session with a generated
// resume command. A dry run makes no record.
func adoptSessions(command *cobra.Command, agents *agentservice.Service, project domain.Project, sessions []transcriptservice.DiscoveredSession) ([]string, error) {
	adopted := []string{}
	for _, session := range sessions {
		if isDryRun(command) {
			if _, err := validateAdoption(agents, project, session); err != nil {
				return nil, err
			}
			continue
		}
		agent, err := adoptDiscoveredSession(agents, project, session)
		if err != nil {
			return nil, err
		}
		adopted = append(adopted, agent.ID)
	}
	return adopted, nil
}

func newAgentsResumeCommand(agents *agentservice.Service, projects *projectservice.Service, stateDir string) *cobra.Command {
	command := &cobra.Command{
		Use:   "resume AGENT_ID",
		Short: "Resume or focus an Agent Session",
		Args:  exactArgs("AGENT_ID"),
		RunE: func(command *cobra.Command, args []string) error {
			return resumeAgentSession(command, agents, projects, stateDir, args[0], "")
		},
	}
	setAgentCommandCompletion(command, agents, projects)
	return command
}

// resumeAgentSession resumes or focuses one Agent Session. An empty Project
// reference uses the Project of the Agent Session record; a set reference
// must name that same Project. A reference that names a discovered provider
// session adopts it first. Both the agents resume command and apply use it.
func resumeAgentSession(command *cobra.Command, agents *agentservice.Service, projects *projectservice.Service, stateDir, agentID, projectReference string) error {
	agent, err := agents.Find(agentID)
	if err != nil {
		agent, err = adoptForResume(command, agents, projects, stateDir, agentID, projectReference, err)
		if err != nil {
			return err
		}
	}
	project, err := findAgentProject(projects, agent, projectReference)
	if err != nil {
		return err
	}
	if isDryRun(command) {
		if err := agents.ValidateResume(agent, project); err != nil {
			return err
		}
		return writeMutation(command, "agents.resume", statusValid, agent.ID, agent.Label)
	}
	agent, err = agents.Resume(agent, project)
	if err != nil {
		return err
	}
	if WantsJSON(command) {
		return writeJSONOutput(command, agentActionOutput{SchemaVersion: jsonSchemaVersion, Agent: toAgentOutput(agents, agent, project.Status == domain.ProjectActive, true)})
	}
	_, err = fmt.Fprintf(command.OutOrStdout(), "Resumed Agent Session %s\n", agent.ID)
	return err
}

// adoptForResume resolves a missed resume reference against the discovered
// provider sessions of the Project. An empty Project reference uses the
// current Project. When no Project resolves, the original lookup error stays.
func adoptForResume(command *cobra.Command, agents *agentservice.Service, projects *projectservice.Service, stateDir, agentID, projectReference string, lookupErr error) (domain.AgentSession, error) {
	if clierr.CodeOf(lookupErr) != clierr.NotFound {
		return domain.AgentSession{}, lookupErr
	}
	if projectReference == "" {
		projectReference = currentProjectReference
	}
	project, err := resolveProject(projects, projectReference)
	if err != nil {
		return domain.AgentSession{}, lookupErr
	}
	agent, _, err := findOrAdoptAgent(command, agents, project, stateDir, agentID)
	return agent, err
}

// findAgentProject finds the Project of one Agent Session. An empty
// reference uses the Project ID of the Agent Session record. Every other
// reference resolves as a PROJECT value and must name that same Project.
func findAgentProject(projects *projectservice.Service, agent domain.AgentSession, projectReference string) (domain.Project, error) {
	if projectReference == "" {
		return projects.Find(agent.ProjectID)
	}
	project, err := resolveProject(projects, projectReference)
	if err != nil {
		return domain.Project{}, err
	}
	return project, requireAgentInProject(agent, project)
}

func newAgentsFocusCommand(agents *agentservice.Service, projects *projectservice.Service) *cobra.Command {
	command := &cobra.Command{
		Use:   "focus AGENT_ID",
		Short: "Focus a live Agent Session pane",
		Args:  exactArgs("AGENT_ID"),
		RunE: func(command *cobra.Command, args []string) error {
			agent, err := agents.Find(args[0])
			if err != nil {
				return err
			}
			return runMutation(command, "agents.focus",
				func() (string, string, error) {
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
	setAgentCommandCompletion(command, agents, projects)
	return command
}

func newAgentsSendCommand(agents *agentservice.Service, projects *projectservice.Service, stateDir string) *cobra.Command {
	var useStdin bool
	var projectReference string
	command := &cobra.Command{
		Use:   "send AGENT_ID",
		Short: "Send feedback to a live Agent Session pane",
		Args:  exactArgs("AGENT_ID"),
		PreRunE: func(command *cobra.Command, _ []string) error {
			if !useStdin {
				return invalidUsage(command, "missing required flag --stdin")
			}
			return nil
		},
		RunE: func(command *cobra.Command, args []string) error {
			project, err := resolveProject(projects, projectReference)
			if err != nil {
				return err
			}
			// Every twt command that reads standard input reads at most
			// 1 MiB, so one caller cannot fill memory or a tmux pane.
			data, err := io.ReadAll(io.LimitReader(command.InOrStdin(), 1024*1024))
			if err != nil {
				return fmt.Errorf("read feedback: %w", err)
			}
			return sendAgentFeedback(command, agents, project, stateDir, args[0], string(data))
		},
	}
	command.Flags().BoolVar(&useStdin, "stdin", false, "Read feedback from standard input")
	command.Flags().StringVar(&projectReference, "project", "current", "Select the Project by name or ID")
	_ = command.MarkFlagRequired("stdin")
	setAgentCommandCompletion(command, agents, projects)
	return command
}

// sendAgentFeedback sends one feedback text to the live pane of an Agent
// Session of the Project. Both the agents send command and apply use it. A
// reference that names a discovered provider session adopts it first; the
// send then reports that the Agent Session is not live, with the resume hint.
func sendAgentFeedback(command *cobra.Command, agents *agentservice.Service, project domain.Project, stateDir, agentID, text string) error {
	agent, _, err := findOrAdoptAgent(command, agents, project, stateDir, agentID)
	if err != nil {
		return err
	}
	if err := requireAgentInProject(agent, project); err != nil {
		return err
	}
	if isDryRun(command) {
		if text == "" {
			return clierr.New(clierr.InvalidUsage, "feedback input is empty")
		}
		if !agents.IsLive(agent) {
			return agentservice.NotLiveError(agent.ID)
		}
		return writeMutation(command, "agents.send", statusValid, agent.ID, agent.Label)
	}
	if err := agents.Send(agent, project.ID, text); err != nil {
		return err
	}
	if WantsJSON(command) {
		return writeJSONOutput(command, agentSendOutput{SchemaVersion: jsonSchemaVersion, AgentID: agent.ID, Status: "sent"})
	}
	_, err = fmt.Fprintf(command.OutOrStdout(), "Sent feedback to Agent Session %s\n", agent.ID)
	return err
}
