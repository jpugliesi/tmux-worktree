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

type agentOutput struct {
	ID string `json:"id"`
	// ProviderSessionID is the raw provider session ID. twt2 never returns
	// the provider file path.
	ProviderSessionID string            `json:"providerSessionId,omitempty"`
	ProjectID         string            `json:"projectId"`
	Provider          string            `json:"provider"`
	Label             string            `json:"label"`
	Status            string            `json:"status"`
	CreatedAt         string            `json:"createdAt"`
	UpdatedAt         string            `json:"updatedAt"`
	Capabilities      agentCapabilities `json:"capabilities"`
}

type agentCapabilities struct {
	CanResume         bool `json:"canResume"`
	CanSend           bool `json:"canSend"`
	CanFocus          bool `json:"canFocus"`
	CanReadTranscript bool `json:"canReadTranscript"`
}

type agentsListOutput struct {
	SchemaVersion int           `json:"schemaVersion"`
	ProjectID     string        `json:"projectId"`
	Agents        []agentOutput `json:"agents"`
	TotalCount    int           `json:"totalCount"`
	Truncated     bool          `json:"truncated,omitempty"`
}

type agentShowOutput struct {
	SchemaVersion int          `json:"schemaVersion"`
	Agent         agentOutput  `json:"agent"`
	Liveness      []agentCheck `json:"liveness"`
}

type agentCheck struct {
	Name     string `json:"name"`
	OK       bool   `json:"ok"`
	Advisory bool   `json:"advisory,omitempty"`
}

type agentsDiscoverOutput struct {
	SchemaVersion int                `json:"schemaVersion"`
	ProjectID     string             `json:"projectId"`
	Sessions      []discoveredOutput `json:"sessions"`
	TotalCount    int                `json:"totalCount"`
	Truncated     bool               `json:"truncated,omitempty"`
	Adopted       []string           `json:"adopted,omitempty"`
	Status        string             `json:"status,omitempty"`
}

type discoveredOutput struct {
	Provider     string `json:"provider"`
	SessionID    string `json:"sessionId"`
	Repository   string `json:"repository"`
	LastActivity string `json:"lastActivity"`
}

type agentActionOutput struct {
	SchemaVersion int         `json:"schemaVersion"`
	Agent         agentOutput `json:"agent"`
}

type agentSendOutput struct {
	SchemaVersion int    `json:"schemaVersion"`
	AgentID       string `json:"agentId"`
	Status        string `json:"status"`
}

type agentTranscriptOutput struct {
	SchemaVersion  int    `json:"schemaVersion"`
	ProjectID      string `json:"projectId"`
	AgentID        string `json:"agentId"`
	Provider       string `json:"provider"`
	RepositoryName string `json:"repositoryName"`
	UpdatedAt      string `json:"updatedAt"`
	Markdown       string `json:"markdown"`
}

type agentSnapshotOutput struct {
	SchemaVersion  int    `json:"schemaVersion"`
	ProjectID      string `json:"projectId"`
	AgentID        string `json:"agentId"`
	Provider       string `json:"provider"`
	RepositoryName string `json:"repositoryName"`
	UpdatedAt      string `json:"updatedAt"`
	Status         string `json:"status"`
	// Path is the private Project-owned file of the Agent Session snapshot.
	// It is empty for a dry run, because a dry run writes no file.
	Path string `json:"path,omitempty"`
}

func newAgentsCommand(options Options) *cobra.Command {
	agents := agentservice.NewService(options.StateDir, options.TmuxSocket)
	projects := projectservice.NewService(projectservice.Options{StateDir: options.StateDir, DataDir: options.DataDir, TmuxSocket: options.TmuxSocket})
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
			if pane == "current" {
				pane = os.Getenv("TMUX_PANE")
			}
			if isDryRun(command) {
				if err := agents.ValidateRegistration(project, provider, pane, providerSessionID, args); err != nil {
					return err
				}
				if err := agents.ValidateLabel(project.ID, label); err != nil {
					return err
				}
				return writeMutation(command, "agents.register", "valid", "", label)
			}
			agent, err := agents.Register(project, provider, label, pane, providerSessionID, args)
			if err != nil {
				return err
			}
			if WantsJSON(command) {
				return writeMutation(command, "agents.register", "applied", agent.ID, agent.Label)
			}
			_, err = fmt.Fprintf(command.OutOrStdout(), "Registered Agent Session %s (%s)\n", agent.ID, agent.Label)
			return err
		},
	}
	command.Flags().StringVar(&projectReference, "project", "current", "Select the Project by name or ID")
	command.Flags().StringVar(&provider, "provider", "", "Set the provider: codex, claude, cursor, or command. twt2 infers it from the resume command")
	command.Flags().StringVar(&label, "label", "", "Set the display label. The default label is the provider name")
	command.Flags().StringVar(&pane, "pane", "", "Set an owned tmux pane ID, or use current")
	command.Flags().StringVar(&providerSessionID, "session", "", "Link the provider session ID for transcript loading. twt2 infers it from the resume command")
	setArguments(command, variadicArgument("resume_command", false, "required when --pane is empty"))
	_ = command.RegisterFlagCompletionFunc("project", projectFlagCompletion(projects))
	_ = command.RegisterFlagCompletionFunc("provider", fixedCompletion(agentProviderNames...))
	return command
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

func newAgentTranscriptCommand(agents *agentservice.Service, projects *projectservice.Service, stateDir string) *cobra.Command {
	command := groupCommand(&cobra.Command{Use: "transcript", Short: "Read linked Agent Session transcripts"})
	command.AddCommand(newAgentTranscriptShowCommand(agents, projects, stateDir))
	command.AddCommand(newAgentTranscriptSnapshotCommand(agents, projects, stateDir))
	command.AddCommand(newAgentTranscriptLinkCommand(agents, projects))
	return command
}

func newAgentTranscriptSnapshotCommand(agents *agentservice.Service, projects *projectservice.Service, stateDir string) *cobra.Command {
	var projectReference string
	command := &cobra.Command{
		Use:   "snapshot AGENT_ID",
		Short: "Save a Project-owned Agent Session transcript snapshot",
		Args:  exactArgs("AGENT_ID"),
		RunE: func(command *cobra.Command, args []string) error {
			project, err := resolveProject(projects, projectReference)
			if err != nil {
				return err
			}
			home, err := os.UserHomeDir()
			if err != nil {
				return fmt.Errorf("find home directory: %w", err)
			}
			result, err := transcriptservice.New(home).Snapshot(stateDir, args[0], project.ID, !isDryRun(command))
			if err != nil {
				return err
			}
			value, agent := result.Transcript, result.Agent
			status := "applied"
			if isDryRun(command) {
				status = "valid"
			}
			if WantsJSON(command) {
				return writeJSONOutput(command, agentSnapshotOutput{
					SchemaVersion: jsonSchemaVersion, ProjectID: project.ID, AgentID: agent.ID,
					Provider: value.Provider, RepositoryName: value.RepositoryName,
					UpdatedAt: value.UpdatedAt.Format(time.RFC3339), Status: status, Path: result.Path,
				})
			}
			if _, err := fmt.Fprintf(command.OutOrStdout(), "Transcript Snapshot %s for Agent Session %s\n", status, agent.ID); err != nil {
				return err
			}
			if result.Path == "" {
				return nil
			}
			_, err = fmt.Fprintf(command.OutOrStdout(), "Snapshot: %s\n", result.Path)
			return err
		},
	}
	command.Flags().StringVar(&projectReference, "project", "current", "Select the Project by name or ID")
	setAgentCommandCompletion(command, agents, projects)
	return command
}

func newAgentTranscriptLinkCommand(agents *agentservice.Service, projects *projectservice.Service) *cobra.Command {
	var projectReference string
	var providerSessionID string
	command := &cobra.Command{
		Use:   "link AGENT_ID",
		Short: "Link an Agent Session to its provider transcript",
		Args:  exactArgs("AGENT_ID"),
		PreRunE: func(command *cobra.Command, _ []string) error {
			if providerSessionID == "" {
				return invalidUsage(command, "missing required flag --session SESSION_ID")
			}
			return nil
		},
		RunE: func(command *cobra.Command, args []string) error {
			project, err := resolveProject(projects, projectReference)
			if err != nil {
				return err
			}
			if isDryRun(command) {
				if err := agents.ValidateTranscriptLink(args[0], project.ID, providerSessionID); err != nil {
					return err
				}
				return writeMutation(command, "agents.transcript.link", "valid", args[0], providerSessionID)
			}
			agent, err := agents.LinkTranscript(args[0], project.ID, providerSessionID)
			if err != nil {
				return err
			}
			return writeMutation(command, "agents.transcript.link", "applied", agent.ID, agent.Label)
		},
	}
	command.Flags().StringVar(&projectReference, "project", "current", "Select the Project by name or ID")
	command.Flags().StringVar(&providerSessionID, "session", "", "Set the provider session ID")
	_ = command.MarkFlagRequired("session")
	setAgentCommandCompletion(command, agents, projects)
	return command
}

func newAgentTranscriptShowCommand(agents *agentservice.Service, projects *projectservice.Service, stateDir string) *cobra.Command {
	var projectReference string
	command := &cobra.Command{
		Use:   "show AGENT_ID",
		Short: "Read a linked Agent Session transcript",
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
			home, err := os.UserHomeDir()
			if err != nil {
				return fmt.Errorf("find home directory: %w", err)
			}
			value, err := transcriptservice.NewWithState(home, stateDir).ReadLinked(agent, project)
			if err != nil {
				return err
			}
			return writeAgentTranscript(command, project.ID, agent.ID, value)
		},
	}
	command.Flags().StringVar(&projectReference, "project", "current", "Select the Project by name or ID")
	setAgentCommandCompletion(command, agents, projects)
	return command
}

func writeAgentTranscript(command *cobra.Command, projectID, agentID string, value transcriptservice.Transcript) error {
	if WantsJSON(command) {
		return writeJSONOutput(command, agentTranscriptOutput{
			SchemaVersion: jsonSchemaVersion, ProjectID: projectID, AgentID: agentID,
			Provider: value.Provider, RepositoryName: value.RepositoryName,
			UpdatedAt: value.UpdatedAt.Format(time.RFC3339), Markdown: value.Markdown,
		})
	}
	_, err := io.WriteString(command.OutOrStdout(), value.Markdown)
	return err
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
			if agent.ProjectID != project.ID {
				return clierr.New(clierr.PreconditionFailed, "Agent Session %q does not belong to Project %q", agent.ID, project.Name)
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
			if isDryRun(command) {
				agent, err := agents.ValidateRemove(args[0], project.ID)
				if err != nil {
					return err
				}
				return writeMutation(command, "agents.rm", "valid", agent.ID, agent.Label)
			}
			agent, err := agents.Remove(args[0], project.ID)
			if err != nil {
				return err
			}
			if WantsJSON(command) {
				return writeMutation(command, "agents.rm", "applied", agent.ID, agent.Label)
			}
			_, err = fmt.Fprintf(command.OutOrStdout(), "Removed Agent Session %s (%s)\n", agent.ID, agent.Label)
			return err
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
			found, err := transcriptservice.NewWithState(home, stateDir).Discover(project, transcriptservice.DiscoverOptions{Linked: registered})
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
				result.Status = "applied"
				if isDryRun(command) {
					result.Status = "valid"
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
				if _, err := fmt.Fprintf(writer, "%s\t%s\t%s\t%s\n", session.Provider, session.SessionID, session.RepositoryName, relativeAge(now, session.LastActivity)); err != nil {
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

func boolText(value bool) string {
	if value {
		return "yes"
	}
	return "no"
}

// relativeAge writes an age that a person can read, such as "5m ago".
func relativeAge(now, value time.Time) string {
	age := now.Sub(value)
	switch {
	case age < time.Minute:
		return "now"
	case age < time.Hour:
		return fmt.Sprintf("%dm ago", int(age.Minutes()))
	case age < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(age.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(age.Hours()/24))
	}
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
				return writeMutation(command, "agents.resume", "valid", agent.ID, agent.Label)
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
			if isDryRun(command) {
				if !agents.IsLive(agent) {
					return agentservice.NotLiveError(agent.ID)
				}
				return writeMutation(command, "agents.focus", "valid", agent.ID, agent.Label)
			}
			if err := agents.Focus(agent); err != nil {
				return err
			}
			if WantsJSON(command) {
				return writeMutation(command, "agents.focus", "applied", agent.ID, agent.Label)
			}
			return nil
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
			if agent.ProjectID != project.ID {
				return fmt.Errorf("Agent Session %q does not belong to Project %q", agent.ID, project.Name)
			}
			if isDryRun(command) {
				if len(data) == 0 {
					return clierr.New(clierr.InvalidUsage, "feedback input is empty")
				}
				if !agents.IsLive(agent) {
					return agentservice.NotLiveError(agent.ID)
				}
				return writeMutation(command, "agents.send", "valid", agent.ID, agent.Label)
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

// toAgentOutput describes one Agent Session. With probeLive false, twt2 does
// not ask tmux for the state of the pane: the status is "unknown" and the
// capabilities that need a live pane are false.
func toAgentOutput(service *agentservice.Service, agent domain.AgentSession, projectActive, probeLive bool) agentOutput {
	live := false
	status := "unknown"
	if probeLive {
		live = service.IsLive(agent)
		status = "stopped"
		if live {
			status = "live"
		}
	}
	return agentOutput{
		ID: agent.ID, ProviderSessionID: agent.ProviderSessionID, ProjectID: agent.ProjectID,
		Provider: agent.Provider, Label: agent.Label, Status: status,
		CreatedAt: agent.CreatedAt.Format("2006-01-02T15:04:05Z07:00"),
		UpdatedAt: agent.UpdatedAt.Format("2006-01-02T15:04:05Z07:00"),
		Capabilities: agentCapabilities{
			CanResume: projectActive && (live || len(agent.ResumeCommand) > 0), CanSend: live, CanFocus: live,
			CanReadTranscript: agent.ProviderSessionID != "" && transcriptservice.SupportsProvider(agent.Provider),
		},
	}
}
