package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	agentservice "github.com/jpugliesi/tmux-worktree/internal/agent"
	"github.com/jpugliesi/tmux-worktree/internal/domain"
	projectservice "github.com/jpugliesi/tmux-worktree/internal/project"
	"github.com/spf13/cobra"
)

// applyRequest is the strict envelope of one apply request. Each operation
// decodes its own payload from the raw section.
type applyRequest struct {
	Operation string          `json:"operation"`
	Template  json.RawMessage `json:"template,omitempty"`
	Project   json.RawMessage `json:"project,omitempty"`
	Agent     json.RawMessage `json:"agent,omitempty"`
}

type templateCreateRequest struct {
	Name string `json:"name"`
}

type templateRepositoryAddRequest struct {
	Name       string                  `json:"name"`
	Repository *applyRepositoryRequest `json:"repository"`
}

type applyRepositoryRequest struct {
	Name          string            `json:"name"`
	URL           string            `json:"url"`
	Depth         int               `json:"depth,omitempty"`
	Remotes       map[string]string `json:"remotes,omitempty"`
	DefaultBranch string            `json:"defaultBranch,omitempty"`
	WindowName    string            `json:"windowName,omitempty"`
}

type projectCreateRequest struct {
	Name     string `json:"name"`
	Template string `json:"template"`
	NoOpen   *bool  `json:"noOpen,omitempty"`
}

type projectArchiveRequest struct {
	Reference string `json:"reference"`
}

type projectRemoveRequest struct {
	Reference        string `json:"reference"`
	Apply            bool   `json:"apply,omitempty"`
	AllowUnpublished bool   `json:"allowUnpublished,omitempty"`
}

type agentRegisterRequest struct {
	Project           string   `json:"project"`
	Provider          string   `json:"provider"`
	Label             string   `json:"label,omitempty"`
	Pane              string   `json:"pane,omitempty"`
	ProviderSessionID string   `json:"providerSessionId,omitempty"`
	ResumeCommand     []string `json:"resumeCommand,omitempty"`
}

// applyOperation pairs the request schema of one apply operation with its
// handler. The table drives the schema command, the request routing, and the
// unsupported-operation message.
type applyOperation struct {
	applyOperationSchema
	Run func(command *cobra.Command, options Options, request applyRequest) error
}

// applyOperations describes every operation that 'twt2 apply' accepts.
func applyOperations() []applyOperation {
	return []applyOperation{
		{applyOperationSchema{Operation: "templates.create", Payload: "template", Fields: []requestFieldSchema{
			{Path: "template.name", Type: "string", Required: true},
		}}, applyTemplatesCreate},
		{applyOperationSchema{Operation: "templates.repos.add", Payload: "template", Fields: []requestFieldSchema{
			{Path: "template.name", Type: "string", Required: true},
			{Path: "template.repository.name", Type: "string", Required: true},
			{Path: "template.repository.url", Type: "string", Required: true},
			{Path: "template.repository.depth", Type: "integer", Required: false},
			{Path: "template.repository.remotes", Type: "object[string]", Required: false},
			{Path: "template.repository.defaultBranch", Type: "string", Required: false},
			{Path: "template.repository.windowName", Type: "string", Required: false},
		}}, applyTemplatesReposAdd},
		{applyOperationSchema{Operation: "projects.create", Payload: "project", Fields: []requestFieldSchema{
			{Path: "project.name", Type: "string", Required: true},
			{Path: "project.template", Type: "string", Required: true},
			{Path: "project.noOpen", Type: "boolean", Required: false, Condition: "must be true or absent; apply never opens a tmux session"},
		}}, applyProjectsCreate},
		{applyOperationSchema{Operation: "projects.archive", Payload: "project", Fields: []requestFieldSchema{
			{Path: "project.reference", Type: "string", Required: true},
		}}, applyProjectsArchive},
		{applyOperationSchema{Operation: "projects.remove", Payload: "project", Fields: []requestFieldSchema{
			{Path: "project.reference", Type: "string", Required: true},
			{Path: "project.apply", Type: "boolean", Required: false, Condition: "false or absent returns the removal plan only"},
			{Path: "project.allowUnpublished", Type: "boolean", Required: false},
		}}, applyProjectsRemove},
		{applyOperationSchema{Operation: "agents.register", Payload: "agent", Fields: []requestFieldSchema{
			{Path: "agent.project", Type: "string", Required: true},
			{Path: "agent.provider", Type: "string", Required: true, Enum: agentProviderNames},
			{Path: "agent.label", Type: "string", Required: false},
			{Path: "agent.pane", Type: "string", Required: false, Condition: "required when agent.resumeCommand is empty"},
			{Path: "agent.providerSessionId", Type: "string", Required: false},
			{Path: "agent.resumeCommand", Type: "array[string]", Required: false, Condition: "required when agent.pane is empty"},
		}}, applyAgentsRegister},
	}
}

// applyOperationSchemas lists the request schema of every apply operation
// for the schema command.
func applyOperationSchemas() []applyOperationSchema {
	operations := applyOperations()
	schemas := make([]applyOperationSchema, 0, len(operations))
	for _, operation := range operations {
		schemas = append(schemas, operation.applyOperationSchema)
	}
	return schemas
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
	operations := applyOperations()
	for _, operation := range operations {
		if operation.Operation == request.Operation {
			return operation.Run(command, options, request)
		}
	}
	names := make([]string, 0, len(operations))
	for _, operation := range operations {
		names = append(names, operation.Operation)
	}
	return invalidUsage(command, "unsupported apply operation %q; use one of: %s", request.Operation, strings.Join(names, ", "))
}

// decodeApplyPayload decodes one operation payload strictly. An unknown
// field, such as a field from a different operation, is an error.
func decodeApplyPayload(operation, payload string, raw json.RawMessage, value any) error {
	if len(raw) == 0 {
		return fmt.Errorf("%s is required for %s", payload, operation)
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		return fmt.Errorf("invalid %s payload for %s: %v", payload, operation, err)
	}
	return nil
}

func applyTemplatesCreate(command *cobra.Command, options Options, request applyRequest) error {
	var payload templateCreateRequest
	if err := decodeApplyPayload("templates.create", "template", request.Template, &payload); err != nil {
		return err
	}
	if payload.Name == "" {
		return fmt.Errorf("template.name is required for templates.create")
	}
	return createTemplate(command, options, domain.NewTemplate(payload.Name))
}

func applyTemplatesReposAdd(command *cobra.Command, options Options, request applyRequest) error {
	var payload templateRepositoryAddRequest
	if err := decodeApplyPayload("templates.repos.add", "template", request.Template, &payload); err != nil {
		return err
	}
	if payload.Name == "" || payload.Repository == nil {
		return fmt.Errorf("template.name and template.repository are required for templates.repos.add")
	}
	repository := payload.Repository
	if repository.Name == "" || repository.URL == "" {
		return fmt.Errorf("template.repository.name and template.repository.url are required for templates.repos.add")
	}
	return addRepositoryToTemplate(command, options, payload.Name, domain.RepositorySpec{
		Name:          repository.Name,
		Clone:         domain.CloneSpec{URL: repository.URL, Depth: repository.Depth},
		Remotes:       repository.Remotes,
		DefaultBranch: repository.DefaultBranch,
		WindowName:    repository.WindowName,
	})
}

func applyProjectsCreate(command *cobra.Command, options Options, request applyRequest) error {
	var payload projectCreateRequest
	if err := decodeApplyPayload("projects.create", "project", request.Project, &payload); err != nil {
		return err
	}
	if payload.Name == "" || payload.Template == "" {
		return fmt.Errorf("project.name and project.template are required for projects.create")
	}
	if payload.NoOpen != nil && !*payload.NoOpen {
		return fmt.Errorf("apply never opens a tmux session; project.noOpen must be true or absent")
	}
	template, err := options.templateStore().Load(payload.Template)
	if err != nil {
		return err
	}
	if isDryRun(command) {
		if err := options.projectService().ValidateCreate(payload.Name, payload.Template, template); err != nil {
			return err
		}
		return writeMutation(command, "projects.create", statusValid, "", payload.Name)
	}
	project, err := createProject(command, options, payload.Name, payload.Template, template, projectservice.CreateOptions{})
	if err != nil {
		return err
	}
	return writeMutation(command, "projects.create", statusApplied, project.ID, project.Name)
}

func applyProjectsArchive(command *cobra.Command, options Options, request applyRequest) error {
	var payload projectArchiveRequest
	if err := decodeApplyPayload("projects.archive", "project", request.Project, &payload); err != nil {
		return err
	}
	if payload.Reference == "" {
		return fmt.Errorf("project.reference is required for projects.archive")
	}
	service := options.projectService()
	reference, err := resolveProjectReference(service, payload.Reference)
	if err != nil {
		return err
	}
	return archiveProjectRecord(command, service, reference)
}

func applyProjectsRemove(command *cobra.Command, options Options, request applyRequest) error {
	var payload projectRemoveRequest
	if err := decodeApplyPayload("projects.remove", "project", request.Project, &payload); err != nil {
		return err
	}
	if payload.Reference == "" {
		return fmt.Errorf("project.reference is required for projects.remove")
	}
	service := options.projectService()
	reference, err := resolveProjectReference(service, payload.Reference)
	if err != nil {
		return err
	}
	return runProjectRemoval(command, service, reference, payload.Apply, projectservice.RemovalOptions{AllowUnpublished: payload.AllowUnpublished})
}

func applyAgentsRegister(command *cobra.Command, options Options, request applyRequest) error {
	var payload agentRegisterRequest
	if err := decodeApplyPayload("agents.register", "agent", request.Agent, &payload); err != nil {
		return err
	}
	project, err := resolveProject(options.projectService(), payload.Project)
	if err != nil {
		return err
	}
	agents := agentservice.NewService(options.StateDir, options.TmuxSocket)
	return registerAgent(command, agents, project, payload.Provider, payload.Label, payload.Pane, payload.ProviderSessionID, payload.ResumeCommand)
}
