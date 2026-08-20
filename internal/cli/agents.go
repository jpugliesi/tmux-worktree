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
	command.AddCommand(newAgentsListCommand(agents, projects))
	command.AddCommand(newAgentsShowCommand(agents, projects))
	command.AddCommand(newAgentsDiscoverCommand(agents, projects, options.StateDir))
	command.AddCommand(newAgentsRemoveCommand(agents, projects))
	command.AddCommand(newAgentsResumeCommand(agents, projects))
	command.AddCommand(newAgentsFocusCommand(agents, projects))
	command.AddCommand(newAgentsSendCommand(agents, projects))
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

// registerAgent registers one Agent Session with a Project. Both the agents
// register command and apply use it.
func registerAgent(command *cobra.Command, agents *agentservice.Service, project domain.Project, provider, label, pane, providerSessionID string, resumeCommand []string) error {
	if pane == "current" {
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

func newAgentsListCommand(agents *agentservice.Service, projects *projectservice.Service) *cobra.Command {
	var projectReference string
	var limit int
	var live bool
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
			values, total, truncated, err := applyLimit(values, limit)
			if err != nil {
				return err
			}
			outputs := make([]agentOutput, 0, len(values))
			for _, value := range values {
				outputs = append(outputs, toAgentOutput(agents, value, project.Status == domain.ProjectActive, live))
			}
			if WantsJSON(command) {
				return writeJSONOutput(command, agentsListOutput{
					SchemaVersion: jsonSchemaVersion, ProjectID: project.ID, Agents: outputs,
					TotalCount: total, Truncated: truncated,
				})
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
	command.Flags().IntVar(&limit, "limit", 0, "Limit the number of results; zero returns all results")
	command.Flags().BoolVar(&live, "live", true, "Probe tmux for live state. Use --live=false to not probe tmux for live state")
	_ = command.RegisterFlagCompletionFunc("project", projectFlagCompletion(projects))
	return command
}

func newAgentsShowCommand(agents *agentservice.Service, projects *projectservice.Service) *cobra.Command {
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
			agent, err := agents.Find(args[0])
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
				return writeJSONOutput(command, agentShowOutput{SchemaVersion: jsonSchemaVersion, Agent: output, Liveness: checks})
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
	setAgentCommandCompletion(command, agents, projects)
	return command
}

func newAgentsRemoveCommand(agents *agentservice.Service, projects *projectservice.Service) *cobra.Command {
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
			return runMutation(command, "agents.rm",
				func() (string, string, error) {
					agent, err := agents.ValidateRemove(args[0], project.ID)
					return agent.ID, agent.Label, err
				},
				func() (string, string, error) {
					agent, err := agents.Remove(args[0], project.ID)
					return agent.ID, agent.Label, err
				},
				func(out io.Writer, id, name string) error {
					_, err := fmt.Fprintf(out, "Removed Agent Session %s (%s)\n", id, name)
					return err
				})
		},
	}
	command.Flags().StringVar(&projectReference, "project", "current", "Select the Project by name or ID")
	setAgentCommandCompletion(command, agents, projects)
	return command
}

func newAgentsDiscoverCommand(agents *agentservice.Service, projects *projectservice.Service, stateDir string) *cobra.Command {
	var projectReference string
	var adopt bool
	var limit int
	command := &cobra.Command{
		Use:   "discover",
		Short: "Find provider sessions of a Project that no Agent Session uses",
		Args:  noArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			project, err := resolveProject(projects, projectReference)
			if err != nil {
				return err
			}
			home, err := os.UserHomeDir()
			if err != nil {
				return fmt.Errorf("find home directory: %w", err)
			}
			registered, err := agents.List(project.ID)
			if err != nil {
				return err
			}
			found, err := transcriptservice.New(home, stateDir).Discover(project, transcriptservice.DiscoverOptions{Linked: registered})
			if err != nil {
				return err
			}
			found, total, truncated, err := applyLimit(found, limit)
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
			if WantsJSON(command) {
				return writeJSONOutput(command, result)
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
	command.Flags().IntVar(&limit, "limit", 0, "Limit the number of results; zero returns all results")
	_ = command.RegisterFlagCompletionFunc("project", projectFlagCompletion(projects))
	return command
}

// adoptSessions registers each discovered provider session with a generated
// resume command. A dry run makes no record.
func adoptSessions(command *cobra.Command, agents *agentservice.Service, project domain.Project, sessions []transcriptservice.DiscoveredSession) ([]string, error) {
	adopted := []string{}
	for _, session := range sessions {
		resumeCommand := transcriptservice.ResumeCommand(session.Provider, session.SessionID)
		if len(resumeCommand) == 0 {
			continue
		}
		if isDryRun(command) {
			if err := agents.ValidateRegistration(project, session.Provider, "", session.SessionID, resumeCommand); err != nil {
				return nil, err
			}
			continue
		}
		agent, err := agents.Register(project, session.Provider, "", "", session.SessionID, resumeCommand)
		if err != nil {
			return nil, err
		}
		adopted = append(adopted, agent.ID)
	}
	return adopted, nil
}

func newAgentsResumeCommand(agents *agentservice.Service, projects *projectservice.Service) *cobra.Command {
	command := &cobra.Command{
		Use:   "resume AGENT_ID",
		Short: "Resume or focus an Agent Session",
		Args:  exactArgs("AGENT_ID"),
		RunE: func(command *cobra.Command, args []string) error {
			agent, err := agents.Find(args[0])
			if err != nil {
				return err
			}
			project, err := projects.Find(agent.ProjectID)
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
		},
	}
	setAgentCommandCompletion(command, agents, projects)
	return command
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

func newAgentsSendCommand(agents *agentservice.Service, projects *projectservice.Service) *cobra.Command {
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
			data, err := io.ReadAll(command.InOrStdin())
			if err != nil {
				return fmt.Errorf("read feedback: %w", err)
			}
			agent, err := agents.Find(args[0])
			if err != nil {
				return err
			}
			if err := requireAgentInProject(agent, project); err != nil {
				return err
			}
			if isDryRun(command) {
				if len(data) == 0 {
					return clierr.New(clierr.InvalidUsage, "feedback input is empty")
				}
				if !agents.IsLive(agent) {
					return agentservice.NotLiveError(agent.ID)
				}
				return writeMutation(command, "agents.send", statusValid, agent.ID, agent.Label)
			}
			if err := agents.Send(agent, project.ID, string(data)); err != nil {
				return err
			}
			if WantsJSON(command) {
				return writeJSONOutput(command, agentSendOutput{SchemaVersion: jsonSchemaVersion, AgentID: agent.ID, Status: "sent"})
			}
			_, err = fmt.Fprintf(command.OutOrStdout(), "Sent feedback to Agent Session %s\n", agent.ID)
			return err
		},
	}
	command.Flags().BoolVar(&useStdin, "stdin", false, "Read feedback from standard input")
	command.Flags().StringVar(&projectReference, "project", "current", "Select the Project by name or ID")
	_ = command.MarkFlagRequired("stdin")
	setAgentCommandCompletion(command, agents, projects)
	return command
}
