package cli

import (
	"fmt"
	"io"
	"os"
	"time"

	agentservice "github.com/jpugliesi/tmux-worktree/internal/agent"
	"github.com/jpugliesi/tmux-worktree/internal/domain"
	projectservice "github.com/jpugliesi/tmux-worktree/internal/project"
	transcriptservice "github.com/jpugliesi/tmux-worktree/internal/transcript"
	"github.com/spf13/cobra"
)

type agentOutput struct {
	ID           string            `json:"id"`
	ProjectID    string            `json:"projectId"`
	Provider     string            `json:"provider"`
	Label        string            `json:"label"`
	Status       string            `json:"status"`
	UpdatedAt    string            `json:"updatedAt"`
	Capabilities agentCapabilities `json:"capabilities"`
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
}

func newAgentsCommand(options Options) *cobra.Command {
	agents := agentservice.NewService(options.StateDir, options.TmuxSocket)
	projects := projectservice.NewService(projectservice.Options{StateDir: options.StateDir, DataDir: options.DataDir, TmuxSocket: options.TmuxSocket})
	command := &cobra.Command{Use: "agents", Short: "Manage Agent Sessions for Projects"}
	command.AddCommand(newAgentsRegisterCommand(agents, projects))
	command.AddCommand(newAgentsListCommand(agents, projects))
	command.AddCommand(newAgentsResumeCommand(agents, projects))
	command.AddCommand(newAgentsFocusCommand(agents))
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
		PreRunE: func(command *cobra.Command, _ []string) error {
			if provider == "" {
				return invalidUsage(command, "missing required flag --provider PROVIDER")
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
	command.Flags().StringVar(&provider, "provider", "", "Set the provider: codex, claude, cursor, or command")
	command.Flags().StringVar(&label, "label", "", "Set the display label")
	command.Flags().StringVar(&pane, "pane", "", "Set an owned tmux pane ID, or use current")
	command.Flags().StringVar(&providerSessionID, "session", "", "Link the provider session ID for transcript loading")
	_ = command.MarkFlagRequired("provider")
	return command
}

func newAgentTranscriptCommand(agents *agentservice.Service, projects *projectservice.Service, stateDir string) *cobra.Command {
	command := &cobra.Command{Use: "transcript", Short: "Read linked Agent Session transcripts"}
	command.AddCommand(newAgentTranscriptShowCommand(agents, projects))
	command.AddCommand(newAgentTranscriptSnapshotCommand(projects, stateDir))
	command.AddCommand(newAgentTranscriptLinkCommand(agents, projects))
	return command
}

func newAgentTranscriptSnapshotCommand(projects *projectservice.Service, stateDir string) *cobra.Command {
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
			value, agent, err := transcriptservice.New(home).Snapshot(stateDir, args[0], project.ID, !isDryRun(command))
			if err != nil {
				return err
			}
			status := "applied"
			if isDryRun(command) {
				status = "valid"
			}
			if WantsJSON(command) {
				return writeJSONOutput(command, agentSnapshotOutput{
					SchemaVersion: jsonSchemaVersion, ProjectID: project.ID, AgentID: agent.ID,
					Provider: value.Provider, RepositoryName: value.RepositoryName,
					UpdatedAt: value.UpdatedAt.Format(time.RFC3339), Status: status,
				})
			}
			_, err = fmt.Fprintf(command.OutOrStdout(), "Transcript Snapshot %s for Agent Session %s\n", status, agent.ID)
			return err
		},
	}
	command.Flags().StringVar(&projectReference, "project", "current", "Select the Project by name or ID")
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
	return command
}

func newAgentTranscriptShowCommand(agents *agentservice.Service, projects *projectservice.Service) *cobra.Command {
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
			value, err := transcriptservice.New(home).ReadLinked(agent, project)
			if err != nil {
				return err
			}
			return writeAgentTranscript(command, project.ID, agent.ID, value)
		},
	}
	command.Flags().StringVar(&projectReference, "project", "current", "Select the Project by name or ID")
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
	var format string
	var limit int
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
			values, err = applyLimit(values, limit)
			if err != nil {
				return err
			}
			outputs := make([]agentOutput, 0, len(values))
			for _, value := range values {
				outputs = append(outputs, toAgentOutput(agents, value, project.Status == domain.ProjectActive))
			}
			if wantsJSON(command, format) {
				return writeJSONOutput(command, agentsListOutput{SchemaVersion: jsonSchemaVersion, ProjectID: project.ID, Agents: outputs})
			}
			if format != "text" {
				return fmt.Errorf("unsupported format %q", format)
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
	command.Flags().StringVar(&format, "format", "text", "Set the output format: text or json")
	command.Flags().IntVar(&limit, "limit", 0, "Limit the number of results; zero returns all results")
	return command
}

func newAgentsResumeCommand(agents *agentservice.Service, projects *projectservice.Service) *cobra.Command {
	var format string
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
			if wantsJSON(command, format) {
				return writeJSONOutput(command, agentActionOutput{SchemaVersion: jsonSchemaVersion, Agent: toAgentOutput(agents, agent, project.Status == domain.ProjectActive)})
			}
			if format != "text" {
				return fmt.Errorf("unsupported format %q", format)
			}
			_, err = fmt.Fprintf(command.OutOrStdout(), "Resumed Agent Session %s\n", agent.ID)
			return err
		},
	}
	command.Flags().StringVar(&format, "format", "text", "Set the output format: text or json")
	return command
}

func newAgentsFocusCommand(agents *agentservice.Service) *cobra.Command {
	return &cobra.Command{
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
					return fmt.Errorf("the Agent Session does not have a live pane")
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
}

func newAgentsSendCommand(agents *agentservice.Service, projects *projectservice.Service) *cobra.Command {
	var useStdin bool
	var format string
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
				if len(data) == 0 || !agents.IsLive(agent) {
					return fmt.Errorf("feedback requires text and a live owned Agent Session pane")
				}
				return writeMutation(command, "agents.send", "valid", agent.ID, agent.Label)
			}
			if err := agents.Send(agent, project.ID, string(data)); err != nil {
				return err
			}
			if wantsJSON(command, format) {
				return writeJSONOutput(command, agentSendOutput{SchemaVersion: jsonSchemaVersion, AgentID: agent.ID, Status: "sent"})
			}
			if format != "text" {
				return fmt.Errorf("unsupported format %q", format)
			}
			_, err = fmt.Fprintf(command.OutOrStdout(), "Sent feedback to Agent Session %s\n", agent.ID)
			return err
		},
	}
	command.Flags().BoolVar(&useStdin, "stdin", false, "Read feedback from standard input")
	command.Flags().StringVar(&format, "format", "text", "Set the output format: text or json")
	command.Flags().StringVar(&projectReference, "project", "current", "Select the Project by name or ID")
	_ = command.MarkFlagRequired("stdin")
	return command
}

func resolveProject(projects *projectservice.Service, reference string) (domain.Project, error) {
	if reference != "current" {
		return projects.Find(reference)
	}
	directory, err := os.Getwd()
	if err != nil {
		return domain.Project{}, err
	}
	return projects.Current(directory, os.Getenv("TWT2_PROJECT_ID"), os.Getenv("TMUX_PANE"))
}

func toAgentOutput(service *agentservice.Service, agent domain.AgentSession, projectActive bool) agentOutput {
	live := service.IsLive(agent)
	status := "stopped"
	if live {
		status = "live"
	}
	return agentOutput{
		ID: agent.ID, ProjectID: agent.ProjectID, Provider: agent.Provider, Label: agent.Label,
		Status: status, UpdatedAt: agent.UpdatedAt.Format("2006-01-02T15:04:05Z07:00"),
		Capabilities: agentCapabilities{
			CanResume: projectActive && (live || len(agent.ResumeCommand) > 0), CanSend: live, CanFocus: live,
			CanReadTranscript: agent.ProviderSessionID != "" && transcriptservice.SupportsProvider(agent.Provider),
		},
	}
}
