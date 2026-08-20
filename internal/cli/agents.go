package cli

import (
	"fmt"
	"io"
	"os"

	agentservice "github.com/jpugliesi/tmux-worktree/internal/agent"
	"github.com/jpugliesi/tmux-worktree/internal/domain"
	projectservice "github.com/jpugliesi/tmux-worktree/internal/project"
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
	CanResume bool `json:"canResume"`
	CanSend   bool `json:"canSend"`
	CanFocus  bool `json:"canFocus"`
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

func newAgentsCommand(options Options) *cobra.Command {
	agents := agentservice.NewService(options.StateDir, options.TmuxSocket)
	projects := projectservice.NewService(projectservice.Options{StateDir: options.StateDir, DataDir: options.DataDir, TmuxSocket: options.TmuxSocket})
	command := &cobra.Command{Use: "agents", Short: "Manage Agent Sessions for Projects"}
	command.AddCommand(newAgentsRegisterCommand(agents, projects))
	command.AddCommand(newAgentsListCommand(agents, projects))
	command.AddCommand(newAgentsResumeCommand(agents, projects))
	command.AddCommand(newAgentsFocusCommand(agents))
	command.AddCommand(newAgentsSendCommand(agents))
	return command
}

func newAgentsRegisterCommand(agents *agentservice.Service, projects *projectservice.Service) *cobra.Command {
	var projectReference string
	var provider string
	var label string
	var pane string
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
				if err := agents.ValidateRegistration(project, provider, pane, args); err != nil {
					return err
				}
				return writeMutation(command, "agents.register", "valid", "", label)
			}
			agent, err := agents.Register(project, provider, label, pane, args)
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
	_ = command.MarkFlagRequired("provider")
	return command
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

func newAgentsSendCommand(agents *agentservice.Service) *cobra.Command {
	var useStdin bool
	var format string
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
			data, err := io.ReadAll(command.InOrStdin())
			if err != nil {
				return fmt.Errorf("read feedback: %w", err)
			}
			agent, err := agents.Find(args[0])
			if err != nil {
				return err
			}
			if isDryRun(command) {
				if len(data) == 0 || !agents.IsLive(agent) {
					return fmt.Errorf("feedback requires text and a live owned Agent Session pane")
				}
				return writeMutation(command, "agents.send", "valid", agent.ID, agent.Label)
			}
			if err := agents.Send(agent, string(data)); err != nil {
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
		Capabilities: agentCapabilities{CanResume: projectActive && (live || len(agent.ResumeCommand) > 0), CanSend: live, CanFocus: live},
	}
}
