package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"

	agentservice "github.com/jpugliesi/tmux-worktree/internal/agent"
	"github.com/jpugliesi/tmux-worktree/internal/domain"
	projectservice "github.com/jpugliesi/tmux-worktree/internal/project"
	"github.com/jpugliesi/tmux-worktree/internal/store"
	"github.com/spf13/cobra"
)

type applyRequest struct {
	Operation string                `json:"operation"`
	Template  *applyTemplateRequest `json:"template,omitempty"`
	Project   *applyProjectRequest  `json:"project,omitempty"`
	Agent     *applyAgentRequest    `json:"agent,omitempty"`
}

type applyTemplateRequest struct {
	Name string `json:"name"`
}

type applyProjectRequest struct {
	Name      *string `json:"name,omitempty"`
	Template  *string `json:"template,omitempty"`
	Reference *string `json:"reference,omitempty"`
}

type applyAgentRequest struct {
	Project           string   `json:"project"`
	Provider          string   `json:"provider"`
	Label             string   `json:"label,omitempty"`
	Pane              string   `json:"pane,omitempty"`
	ProviderSessionID string   `json:"providerSessionId,omitempty"`
	ResumeCommand     []string `json:"resumeCommand,omitempty"`
}

func newApplyCommand(options Options) *cobra.Command {
	var useStdin bool
	command := &cobra.Command{
		Use:   "apply --stdin",
		Short: "Apply a typed JSON mutation request",
		Args:  noArgs,
		PreRunE: func(command *cobra.Command, _ []string) error {
			if !useStdin {
				return invalidUsage(command, "missing required flag --stdin")
			}
			return nil
		},
		RunE: func(command *cobra.Command, _ []string) error {
			decoder := json.NewDecoder(io.LimitReader(command.InOrStdin(), 1024*1024))
			decoder.DisallowUnknownFields()
			var request applyRequest
			if err := decoder.Decode(&request); err != nil {
				return fmt.Errorf("decode apply request: %w", err)
			}
			var extra any
			if err := decoder.Decode(&extra); err != io.EOF {
				return fmt.Errorf("apply request must contain one JSON value")
			}
			return applyJSONRequest(command, options, request)
		},
	}
	command.Flags().BoolVar(&useStdin, "stdin", false, "Read one JSON request from standard input")
	_ = command.MarkFlagRequired("stdin")
	return command
}

func applyJSONRequest(command *cobra.Command, options Options, request applyRequest) error {
	switch request.Operation {
	case "templates.create":
		if request.Template == nil || request.Template.Name == "" {
			return fmt.Errorf("template.name is required for templates.create")
		}
		template := domain.NewTemplate(request.Template.Name)
		templateStore := store.NewTemplateStore(options.ConfigDir)
		lock, err := store.AcquireMutationLock(options.StateDir)
		if err != nil {
			return err
		}
		defer lock.Release()
		if err := templateStore.ValidateCreate(template); err != nil {
			return err
		}
		if isDryRun(command) {
			return writeMutation(command, request.Operation, "valid", "", request.Template.Name)
		}
		if err := templateStore.Create(template); err != nil {
			return err
		}
		return writeMutation(command, request.Operation, "applied", "", request.Template.Name)
	case "projects.create":
		if request.Project == nil || request.Project.Name == nil || *request.Project.Name == "" || request.Project.Template == nil || *request.Project.Template == "" {
			return fmt.Errorf("project.name and project.template are required for projects.create")
		}
		if request.Project.Reference != nil {
			return fmt.Errorf("projects.create accepts only project.name and project.template")
		}
		name := *request.Project.Name
		templateName := *request.Project.Template
		template, err := store.NewTemplateStore(options.ConfigDir).Load(templateName)
		if err != nil {
			return err
		}
		if isDryRun(command) {
			service := projectservice.NewService(projectservice.Options{StateDir: options.StateDir, DataDir: options.DataDir, TmuxSocket: options.TmuxSocket})
			if err := service.ValidateCreate(name, templateName, template); err != nil {
				return err
			}
			return writeMutation(command, request.Operation, "valid", "", name)
		}
		service := projectservice.NewService(projectservice.Options{StateDir: options.StateDir, DataDir: options.DataDir, TmuxSocket: options.TmuxSocket})
		project, err := service.Create(name, templateName, template)
		if err != nil {
			return err
		}
		return writeMutation(command, request.Operation, "applied", project.ID, project.Name)
	case "projects.archive":
		if request.Project == nil || request.Project.Reference == nil || *request.Project.Reference == "" {
			return fmt.Errorf("project.reference is required for projects.archive")
		}
		if request.Project.Name != nil || request.Project.Template != nil {
			return fmt.Errorf("projects.archive accepts only project.reference")
		}
		reference := *request.Project.Reference
		service := projectservice.NewService(projectservice.Options{StateDir: options.StateDir, DataDir: options.DataDir, TmuxSocket: options.TmuxSocket})
		if isDryRun(command) {
			if err := service.ValidateArchive(reference, os.Getenv("TMUX_PANE")); err != nil {
				return err
			}
			return writeMutation(command, request.Operation, "valid", "", reference)
		}
		project, err := service.Archive(reference, os.Getenv("TMUX_PANE"))
		if err != nil {
			return err
		}
		return writeMutation(command, request.Operation, "applied", project.ID, project.Name)
	case "agents.register":
		if request.Agent == nil {
			return fmt.Errorf("agent is required for agents.register")
		}
		projects := projectservice.NewService(projectservice.Options{StateDir: options.StateDir, DataDir: options.DataDir, TmuxSocket: options.TmuxSocket})
		project, err := resolveProject(projects, request.Agent.Project)
		if err != nil {
			return err
		}
		pane := request.Agent.Pane
		if pane == "current" {
			pane = os.Getenv("TMUX_PANE")
		}
		agents := agentservice.NewService(options.StateDir, options.TmuxSocket)
		if isDryRun(command) {
			if err := agents.ValidateRegistration(project, request.Agent.Provider, pane, request.Agent.ProviderSessionID, request.Agent.ResumeCommand); err != nil {
				return err
			}
			return writeMutation(command, request.Operation, "valid", "", request.Agent.Label)
		}
		agent, err := agents.Register(project, request.Agent.Provider, request.Agent.Label, pane, request.Agent.ProviderSessionID, request.Agent.ResumeCommand)
		if err != nil {
			return err
		}
		return writeMutation(command, request.Operation, "applied", agent.ID, agent.Label)
	default:
		return fmt.Errorf("unsupported apply operation %q", request.Operation)
	}
}
