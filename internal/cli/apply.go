package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

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
	Name       string                  `json:"name"`
	Repository *applyRepositoryRequest `json:"repository,omitempty"`
}

type applyRepositoryRequest struct {
	Name          string            `json:"name"`
	URL           string            `json:"url"`
	Depth         int               `json:"depth,omitempty"`
	Remotes       map[string]string `json:"remotes,omitempty"`
	DefaultBranch string            `json:"defaultBranch,omitempty"`
	WindowName    string            `json:"windowName,omitempty"`
}

type applyProjectRequest struct {
	Name             *string `json:"name,omitempty"`
	Template         *string `json:"template,omitempty"`
	NoOpen           *bool   `json:"noOpen,omitempty"`
	Reference        *string `json:"reference,omitempty"`
	Apply            *bool   `json:"apply,omitempty"`
	AllowUnpublished *bool   `json:"allowUnpublished,omitempty"`
}

type applyAgentRequest struct {
	Project           string   `json:"project"`
	Provider          string   `json:"provider"`
	Label             string   `json:"label,omitempty"`
	Pane              string   `json:"pane,omitempty"`
	ProviderSessionID string   `json:"providerSessionId,omitempty"`
	ResumeCommand     []string `json:"resumeCommand,omitempty"`
}

// applyOperations describes every operation that 'twt2 apply' accepts. The
// table drives the request schema and the unsupported-operation message.
func applyOperations() []applyOperationSchema {
	return []applyOperationSchema{
		{Operation: "templates.create", Payload: "template", Fields: []requestFieldSchema{
			{Path: "template.name", Type: "string", Required: true},
		}},
		{Operation: "templates.repos.add", Payload: "template", Fields: []requestFieldSchema{
			{Path: "template.name", Type: "string", Required: true},
			{Path: "template.repository.name", Type: "string", Required: true},
			{Path: "template.repository.url", Type: "string", Required: true},
			{Path: "template.repository.depth", Type: "integer", Required: false},
			{Path: "template.repository.remotes", Type: "object[string]", Required: false},
			{Path: "template.repository.defaultBranch", Type: "string", Required: false},
			{Path: "template.repository.windowName", Type: "string", Required: false},
		}},
		{Operation: "projects.create", Payload: "project", Fields: []requestFieldSchema{
			{Path: "project.name", Type: "string", Required: true},
			{Path: "project.template", Type: "string", Required: true},
			{Path: "project.noOpen", Type: "boolean", Required: false, Condition: "must be true or absent; apply never opens a tmux session"},
		}},
		{Operation: "projects.archive", Payload: "project", Fields: []requestFieldSchema{
			{Path: "project.reference", Type: "string", Required: true},
		}},
		{Operation: "projects.remove", Payload: "project", Fields: []requestFieldSchema{
			{Path: "project.reference", Type: "string", Required: true},
			{Path: "project.apply", Type: "boolean", Required: false, Condition: "false or absent returns the removal plan only"},
			{Path: "project.allowUnpublished", Type: "boolean", Required: false},
		}},
		{Operation: "agents.register", Payload: "agent", Fields: []requestFieldSchema{
			{Path: "agent.project", Type: "string", Required: true},
			{Path: "agent.provider", Type: "string", Required: true, Enum: agentProviderNames},
			{Path: "agent.label", Type: "string", Required: false},
			{Path: "agent.pane", Type: "string", Required: false, Condition: "required when agent.resumeCommand is empty"},
			{Path: "agent.providerSessionId", Type: "string", Required: false},
			{Path: "agent.resumeCommand", Type: "array[string]", Required: false, Condition: "required when agent.pane is empty"},
		}},
	}
}

// applyOperationNames lists the supported operation names in table order.
func applyOperationNames() []string {
	operations := applyOperations()
	names := make([]string, 0, len(operations))
	for _, operation := range operations {
		names = append(names, operation.Operation)
	}
	return names
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
		if request.Template.Repository != nil {
			return fmt.Errorf("templates.create accepts only template.name")
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
	case "templates.repos.add":
		if request.Template == nil || request.Template.Name == "" || request.Template.Repository == nil {
			return fmt.Errorf("template.name and template.repository are required for templates.repos.add")
		}
		repository := request.Template.Repository
		if repository.Name == "" || repository.URL == "" {
			return fmt.Errorf("template.repository.name and template.repository.url are required for templates.repos.add")
		}
		if err := store.ValidateResourceName(repository.Name); err != nil {
			return fmt.Errorf("invalid repository name: %w", err)
		}
		templateStore := store.NewTemplateStore(options.ConfigDir)
		lock, err := store.AcquireMutationLock(options.StateDir)
		if err != nil {
			return err
		}
		defer lock.Release()
		template, err := templateStore.Load(request.Template.Name)
		if err != nil {
			return err
		}
		updated, err := addTemplateRepository(template, domain.RepositorySpec{
			Name:          repository.Name,
			Clone:         domain.CloneSpec{URL: repository.URL, Depth: repository.Depth},
			Remotes:       repository.Remotes,
			DefaultBranch: repository.DefaultBranch,
			WindowName:    repository.WindowName,
		})
		if err != nil {
			return err
		}
		if isDryRun(command) {
			return writeMutation(command, request.Operation, "valid", "", repository.Name)
		}
		if err := templateStore.Save(updated); err != nil {
			return err
		}
		return writeMutation(command, request.Operation, "applied", "", repository.Name)
	case "projects.create":
		if request.Project == nil || request.Project.Name == nil || *request.Project.Name == "" || request.Project.Template == nil || *request.Project.Template == "" {
			return fmt.Errorf("project.name and project.template are required for projects.create")
		}
		if request.Project.Reference != nil || request.Project.Apply != nil || request.Project.AllowUnpublished != nil {
			return fmt.Errorf("projects.create accepts only project.name, project.template, and project.noOpen")
		}
		if request.Project.NoOpen != nil && !*request.Project.NoOpen {
			return fmt.Errorf("apply never opens a tmux session; project.noOpen must be true or absent")
		}
		name := *request.Project.Name
		templateName := *request.Project.Template
		template, err := store.NewTemplateStore(options.ConfigDir).Load(templateName)
		if err != nil {
			return err
		}
		service := projectservice.NewService(projectservice.Options{StateDir: options.StateDir, DataDir: options.DataDir, TmuxSocket: options.TmuxSocket})
		if isDryRun(command) {
			if err := service.ValidateCreate(name, templateName, template); err != nil {
				return err
			}
			return writeMutation(command, request.Operation, "valid", "", name)
		}
		project, err := service.Create(name, templateName, template)
		if err != nil {
			return err
		}
		return writeMutation(command, request.Operation, "applied", project.ID, project.Name)
	case "projects.archive":
		if request.Project == nil || request.Project.Reference == nil || *request.Project.Reference == "" {
			return fmt.Errorf("project.reference is required for projects.archive")
		}
		if request.Project.Name != nil || request.Project.Template != nil || request.Project.NoOpen != nil ||
			request.Project.Apply != nil || request.Project.AllowUnpublished != nil {
			return fmt.Errorf("projects.archive accepts only project.reference")
		}
		service := projectservice.NewService(projectservice.Options{StateDir: options.StateDir, DataDir: options.DataDir, TmuxSocket: options.TmuxSocket})
		reference, err := resolveProjectReference(service, *request.Project.Reference)
		if err != nil {
			return err
		}
		if isDryRun(command) {
			if err := service.ValidateArchive(reference, os.Getenv("TMUX_PANE")); err != nil {
				return err
			}
			return writeMutation(command, request.Operation, "valid", "", reference)
		}
		result, err := service.Archive(reference, os.Getenv("TMUX_PANE"))
		if err != nil {
			return err
		}
		return writeMutation(command, request.Operation, "applied", result.Project.ID, result.Project.Name)
	case "projects.remove":
		if request.Project == nil || request.Project.Reference == nil || *request.Project.Reference == "" {
			return fmt.Errorf("project.reference is required for projects.remove")
		}
		if request.Project.Name != nil || request.Project.Template != nil || request.Project.NoOpen != nil {
			return fmt.Errorf("projects.remove accepts only project.reference, project.apply, and project.allowUnpublished")
		}
		service := projectservice.NewService(projectservice.Options{StateDir: options.StateDir, DataDir: options.DataDir, TmuxSocket: options.TmuxSocket})
		reference, err := resolveProjectReference(service, *request.Project.Reference)
		if err != nil {
			return err
		}
		removalOptions := projectservice.RemovalOptions{AllowUnpublished: request.Project.AllowUnpublished != nil && *request.Project.AllowUnpublished}
		apply := request.Project.Apply != nil && *request.Project.Apply
		if isDryRun(command) || !apply {
			plan, err := service.PlanRemoval(reference, os.Getenv("TMUX_PANE"), removalOptions)
			if err != nil {
				return err
			}
			if WantsJSON(command) {
				return writeJSONOutput(command, removalOutput{SchemaVersion: jsonSchemaVersion, Applied: false, Plan: plan, Blockers: plan.Blockers, Bytes: plan.Bytes})
			}
			return printRemovalPlanText(command.OutOrStdout(), plan, !apply)
		}
		plan, err := service.Remove(reference, os.Getenv("TMUX_PANE"), removalOptions)
		if err != nil {
			return err
		}
		if WantsJSON(command) {
			return writeJSONOutput(command, removalOutput{SchemaVersion: jsonSchemaVersion, Applied: true, Plan: plan, Blockers: plan.Blockers, Bytes: plan.Bytes})
		}
		return writeMutation(command, request.Operation, "applied", plan.ProjectID, plan.ProjectName)
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
		return invalidUsage(command, "unsupported apply operation %q; use one of: %s", request.Operation, strings.Join(applyOperationNames(), ", "))
	}
}
