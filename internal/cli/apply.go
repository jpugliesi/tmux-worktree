package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/jpugliesi/tmux-worktree/internal/domain"
	projectservice "github.com/jpugliesi/tmux-worktree/internal/project"
	ticketservice "github.com/jpugliesi/tmux-worktree/internal/ticket"
	"github.com/spf13/cobra"
)

// applyRequest is the strict envelope of one apply request. Each operation
// decodes its own payload from the raw section.
type applyRequest struct {
	Operation string          `json:"operation"`
	Template  json.RawMessage `json:"template,omitempty"`
	Project   json.RawMessage `json:"project,omitempty"`
	Agent     json.RawMessage `json:"agent,omitempty"`
	Storage   json.RawMessage `json:"storage,omitempty"`
	Ticket    json.RawMessage `json:"ticket,omitempty"`
	Board     json.RawMessage `json:"board,omitempty"`
}

// templateNameRequest is the payload of each operation that only names a
// Project Template.
type templateNameRequest struct {
	Name string `json:"name"`
}

// templateInitializeSetRequest sets Project or repository initialization. A
// set repo selects repository initialization; an empty repo selects Project
// initialization in cwd.
type templateInitializeSetRequest struct {
	Name             string   `json:"name"`
	Repository       string   `json:"repo,omitempty"`
	WorkingDirectory string   `json:"cwd,omitempty"`
	Command          []string `json:"command"`
}

type templateRepositoryRemoveRequest struct {
	Name       string `json:"name"`
	Repository string `json:"repo"`
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

// projectReferenceRequest is the payload of each operation that only names a
// Project.
type projectReferenceRequest struct {
	Reference string `json:"reference"`
}

// projectOpenRequest opens or repairs one Project tmux session. Apply never
// attaches a tmux client, so noAttach must be true or absent.
type projectOpenRequest struct {
	Reference string `json:"reference"`
	NoAttach  *bool  `json:"noAttach,omitempty"`
}

type projectRemoveRequest struct {
	Reference string `json:"reference"`
	Apply     bool   `json:"apply,omitempty"`
	Force     bool   `json:"force,omitempty"`
}

// storageCleanApplyRequest plans, and with apply set removes, the unused
// twt-owned data.
type storageCleanApplyRequest struct {
	Apply bool `json:"apply,omitempty"`
}

type ticketCreateApplyRequest struct {
	Title    string `json:"title"`
	Body     string `json:"body,omitempty"`
	Board    string `json:"board,omitempty"`
	Slug     string `json:"slug,omitempty"`
	Status   string `json:"status,omitempty"`
	Priority *int   `json:"priority,omitempty"`
}

// ticketSetApplyRequest uses pointers so apply can tell an absent field from
// an empty value, the same as a flag presence check.
type ticketSetApplyRequest struct {
	Reference string  `json:"reference"`
	Status    *string `json:"status,omitempty"`
	Priority  *int    `json:"priority,omitempty"`
	Board     *string `json:"board,omitempty"`
}

// ticketEditApplyRequest replaces the body of one Ticket. Body is a pointer,
// so apply can tell an absent body from an empty body that clears the text.
type ticketEditApplyRequest struct {
	Reference string  `json:"reference"`
	Body      *string `json:"body"`
}

type ticketClaimApplyRequest struct {
	Reference string `json:"reference"`
	As        string `json:"as"`
}

type ticketCommentApplyRequest struct {
	Reference string `json:"reference"`
	Text      string `json:"text"`
}

type boardCreateApplyRequest struct {
	Name string `json:"name"`
}

type agentRegisterRequest struct {
	Project           string   `json:"project"`
	Provider          string   `json:"provider"`
	Label             string   `json:"label,omitempty"`
	Pane              string   `json:"pane,omitempty"`
	ProviderSessionID string   `json:"providerSessionId,omitempty"`
	ResumeCommand     []string `json:"resumeCommand,omitempty"`
}

// agentReferenceRequest is the payload of each operation that only names one
// Agent Session of a Project.
type agentReferenceRequest struct {
	Reference string `json:"reference"`
	Project   string `json:"project,omitempty"`
}

// agentSendRequest sends feedback to one Agent Session. The text replaces the
// standard input of the agents send command.
type agentSendRequest struct {
	Reference string `json:"reference"`
	Project   string `json:"project,omitempty"`
	Text      string `json:"text"`
}

type agentTranscriptLinkRequest struct {
	Reference string `json:"reference"`
	Project   string `json:"project,omitempty"`
	Session   string `json:"session"`
}

// applyOperation pairs the request schema of one apply operation with its
// handler. The table drives the schema command, the request routing, and the
// unsupported-operation message.
type applyOperation struct {
	applyOperationSchema
	Run func(command *cobra.Command, options Options, request applyRequest) error
}

// applyOperations describes every operation that 'twt apply' accepts.
func applyOperations() []applyOperation {
	return []applyOperation{
		{applyOperationSchema{Operation: "templates.create", Payload: "template", Fields: []requestFieldSchema{
			{Path: "template.name", Type: "string", Required: true},
		}}, applyTemplatesCreate},
		{applyOperationSchema{Operation: "templates.remove", Payload: "template", Fields: []requestFieldSchema{
			{Path: "template.name", Type: "string", Required: true, Condition: "no Project record can name the Project Template"},
		}}, applyTemplatesRemove},
		{applyOperationSchema{Operation: "templates.prepare", Payload: "template", Fields: []requestFieldSchema{
			{Path: "template.name", Type: "string", Required: true},
		}}, applyTemplatesPrepare},
		{applyOperationSchema{Operation: "templates.init.set", Payload: "template", Fields: []requestFieldSchema{
			{Path: "template.name", Type: "string", Required: true},
			{Path: "template.command", Type: "array[string]", Required: true},
			{Path: "template.repo", Type: "string", Required: false, Condition: "sets repository initialization, which runs in the repository worktree"},
			{Path: "template.cwd", Type: "string", Required: false, Condition: "required when template.repo is empty; the path is relative to the Project root"},
		}}, applyTemplatesInitSet},
		{applyOperationSchema{Operation: "templates.repos.add", Payload: "template", Fields: []requestFieldSchema{
			{Path: "template.name", Type: "string", Required: true},
			{Path: "template.repository.name", Type: "string", Required: true},
			{Path: "template.repository.url", Type: "string", Required: true},
			{Path: "template.repository.depth", Type: "integer", Required: false},
			{Path: "template.repository.remotes", Type: "object[string]", Required: false},
			{Path: "template.repository.defaultBranch", Type: "string", Required: false},
			{Path: "template.repository.windowName", Type: "string", Required: false},
		}}, applyTemplatesReposAdd},
		{applyOperationSchema{Operation: "templates.repos.remove", Payload: "template", Fields: []requestFieldSchema{
			{Path: "template.name", Type: "string", Required: true},
			{Path: "template.repo", Type: "string", Required: true},
		}}, applyTemplatesReposRemove},
		{applyOperationSchema{Operation: "projects.create", Payload: "project", Fields: []requestFieldSchema{
			{Path: "project.name", Type: "string", Required: true},
			{Path: "project.template", Type: "string", Required: true},
			{Path: "project.noOpen", Type: "boolean", Required: false, Condition: "must be true or absent; apply never opens a tmux session"},
		}}, applyProjectsCreate},
		{applyOperationSchema{Operation: "projects.open", Payload: "project", Fields: []requestFieldSchema{
			{Path: "project.reference", Type: "string", Required: true},
			{Path: "project.noAttach", Type: "boolean", Required: false, Condition: "must be true or absent; apply repairs the tmux session but attaches no tmux client"},
		}}, applyProjectsOpen},
		{applyOperationSchema{Operation: "projects.setup.retry", Payload: "project", Fields: []requestFieldSchema{
			{Path: "project.reference", Type: "string", Required: true},
		}}, applyProjectsSetupRetry},
		{applyOperationSchema{Operation: "projects.archive", Payload: "project", Fields: []requestFieldSchema{
			{Path: "project.reference", Type: "string", Required: true},
		}}, applyProjectsArchive},
		{applyOperationSchema{Operation: "projects.remove", Payload: "project", Fields: []requestFieldSchema{
			{Path: "project.reference", Type: "string", Required: true},
			{Path: "project.apply", Type: "boolean", Required: false, Condition: "false or absent returns the removal plan only"},
			{Path: "project.force", Type: "boolean", Required: false},
		}}, applyProjectsRemove},
		{applyOperationSchema{Operation: "agents.register", Payload: "agent", Fields: []requestFieldSchema{
			{Path: "agent.project", Type: "string", Required: true},
			{Path: "agent.provider", Type: "string", Required: true, Enum: agentProviderNames},
			{Path: "agent.label", Type: "string", Required: false},
			{Path: "agent.pane", Type: "string", Required: false, Condition: "required when agent.resumeCommand is empty; a tmux pane ID, because apply cannot use the value current"},
			{Path: "agent.providerSessionId", Type: "string", Required: false},
			{Path: "agent.resumeCommand", Type: "array[string]", Required: false, Condition: "required when agent.pane is empty"},
		}}, applyAgentsRegister},
		{applyOperationSchema{Operation: "agents.send", Payload: "agent", Fields: []requestFieldSchema{
			{Path: "agent.reference", Type: "string", Required: true, Condition: "the Agent Session pane must be live"},
			{Path: "agent.text", Type: "string", Required: true, Condition: "the text replaces the standard input of 'twt agents send'"},
			{Path: "agent.project", Type: "string", Required: false, Condition: "absent selects the current Project"},
		}}, applyAgentsSend},
		{applyOperationSchema{Operation: "agents.resume", Payload: "agent", Fields: []requestFieldSchema{
			{Path: "agent.reference", Type: "string", Required: true},
			{Path: "agent.project", Type: "string", Required: false, Condition: "absent selects the Project of the Agent Session"},
		}}, applyAgentsResume},
		{applyOperationSchema{Operation: "agents.rm", Payload: "agent", Fields: []requestFieldSchema{
			{Path: "agent.reference", Type: "string", Required: true},
			{Path: "agent.project", Type: "string", Required: false, Condition: "absent selects the current Project"},
		}}, applyAgentsRemove},
		{applyOperationSchema{Operation: "agents.transcript.link", Payload: "agent", Fields: []requestFieldSchema{
			{Path: "agent.reference", Type: "string", Required: true},
			{Path: "agent.session", Type: "string", Required: true},
			{Path: "agent.project", Type: "string", Required: false, Condition: "absent selects the current Project"},
		}}, applyAgentsTranscriptLink},
		{applyOperationSchema{Operation: "storage.clean", Payload: "storage", Fields: []requestFieldSchema{
			{Path: "storage.apply", Type: "boolean", Required: false, Condition: "false or absent returns the cleanup plan only"},
		}}, applyStorageClean},
		{applyOperationSchema{Operation: "tickets.init", Payload: "", Fields: []requestFieldSchema{}}, applyTicketsInit},
		{applyOperationSchema{Operation: "tickets.create", Payload: "ticket", Fields: []requestFieldSchema{
			{Path: "ticket.title", Type: "string", Required: true},
			{Path: "ticket.body", Type: "string", Required: false},
			{Path: "ticket.board", Type: "string", Required: false, Condition: "the Board must exist"},
			{Path: "ticket.slug", Type: "string", Required: false, Condition: "absent derives the slug from the title"},
			{Path: "ticket.status", Type: "string", Required: false, Enum: domain.TicketStatuses(), Condition: "absent selects needs-triage"},
			{Path: "ticket.priority", Type: "integer", Required: false, Condition: "0 (highest) to 4 (lowest); absent selects 2"},
		}}, applyTicketsCreate},
		{applyOperationSchema{Operation: "tickets.edit", Payload: "ticket", Fields: []requestFieldSchema{
			{Path: "ticket.reference", Type: "string", Required: true},
			{Path: "ticket.body", Type: "string", Required: true, Condition: "the text replaces the whole body; an empty string clears the body"},
		}}, applyTicketsEdit},
		{applyOperationSchema{Operation: "tickets.set", Payload: "ticket", Fields: []requestFieldSchema{
			{Path: "ticket.reference", Type: "string", Required: true},
			{Path: "ticket.status", Type: "string", Required: false, Enum: domain.TicketStatuses(), Condition: "set at least one of ticket.status, ticket.priority, or ticket.board"},
			{Path: "ticket.priority", Type: "integer", Required: false},
			{Path: "ticket.board", Type: "string", Required: false},
		}}, applyTicketsSet},
		{applyOperationSchema{Operation: "tickets.claim", Payload: "ticket", Fields: []requestFieldSchema{
			{Path: "ticket.reference", Type: "string", Required: true},
			{Path: "ticket.as", Type: "string", Required: true, Condition: "apply is never a terminal, so the claimant has no default"},
		}}, applyTicketsClaim},
		{applyOperationSchema{Operation: "tickets.unclaim", Payload: "ticket", Fields: []requestFieldSchema{
			{Path: "ticket.reference", Type: "string", Required: true},
			{Path: "ticket.as", Type: "string", Required: true, Condition: "apply is never a terminal, so the claimant has no default"},
		}}, applyTicketsUnclaim},
		{applyOperationSchema{Operation: "tickets.close", Payload: "ticket", Fields: []requestFieldSchema{
			{Path: "ticket.reference", Type: "string", Required: true},
			{Path: "ticket.as", Type: "string", Required: true, Condition: "apply is never a terminal, so the claimant has no default"},
		}}, applyTicketsClose},
		{applyOperationSchema{Operation: "tickets.comment", Payload: "ticket", Fields: []requestFieldSchema{
			{Path: "ticket.reference", Type: "string", Required: true},
			{Path: "ticket.text", Type: "string", Required: true},
		}}, applyTicketsComment},
		{applyOperationSchema{Operation: "tickets.boards.create", Payload: "board", Fields: []requestFieldSchema{
			{Path: "board.name", Type: "string", Required: true},
		}}, applyTicketsBoardsCreate},
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
	return invalidUsageWithHint(command, unsupportedApplyOperationHint,
		"unsupported apply operation %q; use one of: %s", request.Operation, strings.Join(names, ", "))
}

// unsupportedApplyOperationHint tells which mutations apply does not do. Each
// one needs a terminal or moves the calling tmux client, so no typed request
// can replace it.
const unsupportedApplyOperationHint = "Apply does no interactive mutation and no tmux client action. " +
	"Run these in a terminal: 'twt start', 'twt tickets start', 'twt switch', 'twt done', 'twt archive', " +
	"'twt templates edit', 'twt tickets home', 'twt agents focus', and 'twt agents register --pane current'. " +
	"Apply replaces the editor with typed text: use tickets.create or tickets.edit with a body."

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

// decodeTemplateNamePayload decodes one payload that only names a Project
// Template.
func decodeTemplateNamePayload(operation string, raw json.RawMessage) (templateNameRequest, error) {
	var payload templateNameRequest
	if err := decodeApplyPayload(operation, "template", raw, &payload); err != nil {
		return payload, err
	}
	if payload.Name == "" {
		return payload, fmt.Errorf("template.name is required for %s", operation)
	}
	return payload, nil
}

// applyProjectReference maps the optional project field of a payload to a
// PROJECT reference. An empty value selects the current Project.
func applyProjectReference(value string) string {
	if strings.TrimSpace(value) == "" {
		return currentProjectReference
	}
	return value
}

func applyTemplatesCreate(command *cobra.Command, options Options, request applyRequest) error {
	payload, err := decodeTemplateNamePayload("templates.create", request.Template)
	if err != nil {
		return err
	}
	return createTemplate(command, options, domain.NewTemplate(payload.Name))
}

func applyTemplatesRemove(command *cobra.Command, options Options, request applyRequest) error {
	payload, err := decodeTemplateNamePayload("templates.remove", request.Template)
	if err != nil {
		return err
	}
	return removeTemplate(command, options, options.templateStore(), payload.Name)
}

func applyTemplatesPrepare(command *cobra.Command, options Options, request applyRequest) error {
	payload, err := decodeTemplateNamePayload("templates.prepare", request.Template)
	if err != nil {
		return err
	}
	return prepareTemplateEnvironments(command, options, options.templateStore(), payload.Name)
}

func applyTemplatesInitSet(command *cobra.Command, options Options, request applyRequest) error {
	var payload templateInitializeSetRequest
	if err := decodeApplyPayload("templates.init.set", "template", request.Template, &payload); err != nil {
		return err
	}
	if payload.Name == "" || len(payload.Command) == 0 {
		return fmt.Errorf("template.name and template.command are required for templates.init.set")
	}
	repository := strings.TrimSpace(payload.Repository)
	workingDirectory := strings.TrimSpace(payload.WorkingDirectory)
	if repository != "" && workingDirectory != "" {
		return fmt.Errorf("do not set template.cwd together with template.repo; repository initialization runs in the repository worktree")
	}
	if repository == "" && workingDirectory == "" {
		return fmt.Errorf("template.cwd is required for Project initialization; set template.repo for repository initialization")
	}
	return setTemplateInitialization(command, options.templateStore(), options.StateDir, payload.Name, repository, workingDirectory, payload.Command)
}

func applyTemplatesReposRemove(command *cobra.Command, options Options, request applyRequest) error {
	var payload templateRepositoryRemoveRequest
	if err := decodeApplyPayload("templates.repos.remove", "template", request.Template, &payload); err != nil {
		return err
	}
	if payload.Name == "" || payload.Repository == "" {
		return fmt.Errorf("template.name and template.repo are required for templates.repos.remove")
	}
	return removeRepositoryFromTemplate(command, options.templateStore(), options.StateDir, payload.Name, payload.Repository)
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
		if err := validateCreate(options, options.projectService(), payload.Name, payload.Template, template, projectservice.CreateOptions{}); err != nil {
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

// resolveApplyProjectReference decodes one payload that only names a Project
// and maps it to a stable Project reference.
func resolveApplyProjectReference(operation string, raw json.RawMessage, service *projectservice.Service) (string, error) {
	var payload projectReferenceRequest
	if err := decodeApplyPayload(operation, "project", raw, &payload); err != nil {
		return "", err
	}
	if payload.Reference == "" {
		return "", fmt.Errorf("project.reference is required for %s", operation)
	}
	return resolveProjectReference(service, payload.Reference)
}

func applyProjectsOpen(command *cobra.Command, options Options, request applyRequest) error {
	var payload projectOpenRequest
	if err := decodeApplyPayload("projects.open", "project", request.Project, &payload); err != nil {
		return err
	}
	if payload.Reference == "" {
		return fmt.Errorf("project.reference is required for projects.open")
	}
	if payload.NoAttach != nil && !*payload.NoAttach {
		return fmt.Errorf("apply never attaches a tmux client; project.noAttach must be true or absent")
	}
	service := options.projectService()
	reference, err := resolveProjectReference(service, payload.Reference)
	if err != nil {
		return err
	}
	_, err = openProjectSession(command, service, reference)
	return err
}

func applyProjectsSetupRetry(command *cobra.Command, options Options, request applyRequest) error {
	service := options.projectService()
	reference, err := resolveApplyProjectReference("projects.setup.retry", request.Project, service)
	if err != nil {
		return err
	}
	return retryProjectSetup(command, service, reference)
}

func applyProjectsArchive(command *cobra.Command, options Options, request applyRequest) error {
	service := options.projectService()
	reference, err := resolveApplyProjectReference("projects.archive", request.Project, service)
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
	return runProjectRemoval(command, service, reference, payload.Apply, projectservice.RemovalOptions{AllowUnpublished: payload.Force})
}

func applyAgentsRegister(command *cobra.Command, options Options, request applyRequest) error {
	var payload agentRegisterRequest
	if err := decodeApplyPayload("agents.register", "agent", request.Agent, &payload); err != nil {
		return err
	}
	if payload.Pane == currentPaneReference {
		return fmt.Errorf("apply cannot use the pane value %q, because it needs the tmux pane of a terminal; set agent.pane to a tmux pane ID", currentPaneReference)
	}
	project, err := resolveProject(options.projectService(), payload.Project)
	if err != nil {
		return err
	}
	return registerAgent(command, options.agentService(), project, payload.Provider, payload.Label, payload.Pane, payload.ProviderSessionID, payload.ResumeCommand)
}

// decodeAgentReferencePayload decodes one payload that only names an Agent
// Session of a Project.
func decodeAgentReferencePayload(operation string, raw json.RawMessage) (agentReferenceRequest, error) {
	var payload agentReferenceRequest
	if err := decodeApplyPayload(operation, "agent", raw, &payload); err != nil {
		return payload, err
	}
	if payload.Reference == "" {
		return payload, fmt.Errorf("agent.reference is required for %s", operation)
	}
	return payload, nil
}

func applyAgentsSend(command *cobra.Command, options Options, request applyRequest) error {
	var payload agentSendRequest
	if err := decodeApplyPayload("agents.send", "agent", request.Agent, &payload); err != nil {
		return err
	}
	if payload.Reference == "" || payload.Text == "" {
		return fmt.Errorf("agent.reference and agent.text are required for agents.send")
	}
	project, err := resolveProject(options.projectService(), applyProjectReference(payload.Project))
	if err != nil {
		return err
	}
	return sendAgentFeedback(command, options.agentService(), project, options.StateDir, payload.Reference, payload.Text)
}

func applyAgentsResume(command *cobra.Command, options Options, request applyRequest) error {
	payload, err := decodeAgentReferencePayload("agents.resume", request.Agent)
	if err != nil {
		return err
	}
	return resumeAgentSession(command, options.agentService(), options.projectService(), options.StateDir, payload.Reference, strings.TrimSpace(payload.Project))
}

func applyAgentsRemove(command *cobra.Command, options Options, request applyRequest) error {
	payload, err := decodeAgentReferencePayload("agents.rm", request.Agent)
	if err != nil {
		return err
	}
	project, err := resolveProject(options.projectService(), applyProjectReference(payload.Project))
	if err != nil {
		return err
	}
	return removeAgentSession(command, options.agentService(), payload.Reference, project, options.StateDir)
}

func applyAgentsTranscriptLink(command *cobra.Command, options Options, request applyRequest) error {
	var payload agentTranscriptLinkRequest
	if err := decodeApplyPayload("agents.transcript.link", "agent", request.Agent, &payload); err != nil {
		return err
	}
	if payload.Reference == "" || payload.Session == "" {
		return fmt.Errorf("agent.reference and agent.session are required for agents.transcript.link")
	}
	project, err := resolveProject(options.projectService(), applyProjectReference(payload.Project))
	if err != nil {
		return err
	}
	return linkAgentTranscript(command, options.agentService(), project, options.StateDir, payload.Reference, payload.Session)
}

func applyStorageClean(command *cobra.Command, options Options, request applyRequest) error {
	var payload storageCleanApplyRequest
	if err := decodeApplyPayload("storage.clean", "storage", request.Storage, &payload); err != nil {
		return err
	}
	return cleanStorage(command, options, payload.Apply)
}

func applyTicketsInit(command *cobra.Command, options Options, _ applyRequest) error {
	service, err := options.ticketService()
	if err != nil {
		return err
	}
	return initializeTicketsHome(command, service)
}

func applyTicketsEdit(command *cobra.Command, options Options, request applyRequest) error {
	var payload ticketEditApplyRequest
	if err := decodeApplyPayload("tickets.edit", "ticket", request.Ticket, &payload); err != nil {
		return err
	}
	if payload.Reference == "" || payload.Body == nil {
		return fmt.Errorf("ticket.reference and ticket.body are required for tickets.edit")
	}
	service, err := options.ticketService()
	if err != nil {
		return err
	}
	return editTicket(command, service, payload.Reference, *payload.Body)
}

func applyTicketsCreate(command *cobra.Command, options Options, request applyRequest) error {
	var payload ticketCreateApplyRequest
	if err := decodeApplyPayload("tickets.create", "ticket", request.Ticket, &payload); err != nil {
		return err
	}
	if payload.Title == "" {
		return fmt.Errorf("ticket.title is required for tickets.create")
	}
	service, err := options.ticketService()
	if err != nil {
		return err
	}
	priority := -1
	if payload.Priority != nil {
		priority = *payload.Priority
	}
	return createTicket(command, service, ticketservice.CreateRequest{
		Title:    payload.Title,
		Slug:     payload.Slug,
		Board:    payload.Board,
		Body:     payload.Body,
		Status:   domain.TicketStatus(payload.Status),
		Priority: priority,
	})
}

func applyTicketsSet(command *cobra.Command, options Options, request applyRequest) error {
	var payload ticketSetApplyRequest
	if err := decodeApplyPayload("tickets.set", "ticket", request.Ticket, &payload); err != nil {
		return err
	}
	if payload.Reference == "" {
		return fmt.Errorf("ticket.reference is required for tickets.set")
	}
	setRequest := ticketservice.SetRequest{}
	if payload.Status != nil {
		setRequest.Status, setRequest.StatusSet = *payload.Status, true
	}
	if payload.Priority != nil {
		setRequest.Priority, setRequest.PrioritySet = *payload.Priority, true
	}
	if payload.Board != nil {
		setRequest.Board, setRequest.BoardSet = *payload.Board, true
	}
	if !setRequest.StatusSet && !setRequest.PrioritySet && !setRequest.BoardSet {
		return fmt.Errorf("tickets.set requires at least one of ticket.status, ticket.priority, or ticket.board")
	}
	service, err := options.ticketService()
	if err != nil {
		return err
	}
	return setTicket(command, service, payload.Reference, setRequest)
}

// decodeTicketClaimPayload decodes one claim or unclaim payload. Apply is
// never a terminal, so ticket.as is required.
func decodeTicketClaimPayload(operation string, raw json.RawMessage) (ticketClaimApplyRequest, error) {
	var payload ticketClaimApplyRequest
	if err := decodeApplyPayload(operation, "ticket", raw, &payload); err != nil {
		return payload, err
	}
	if payload.Reference == "" || payload.As == "" {
		return payload, fmt.Errorf("ticket.reference and ticket.as are required for %s", operation)
	}
	return payload, nil
}

func applyTicketsClaim(command *cobra.Command, options Options, request applyRequest) error {
	payload, err := decodeTicketClaimPayload("tickets.claim", request.Ticket)
	if err != nil {
		return err
	}
	service, err := options.ticketService()
	if err != nil {
		return err
	}
	return claimTicket(command, service, payload.Reference, payload.As)
}

func applyTicketsUnclaim(command *cobra.Command, options Options, request applyRequest) error {
	payload, err := decodeTicketClaimPayload("tickets.unclaim", request.Ticket)
	if err != nil {
		return err
	}
	service, err := options.ticketService()
	if err != nil {
		return err
	}
	return unclaimTicket(command, service, payload.Reference, payload.As)
}

func applyTicketsClose(command *cobra.Command, options Options, request applyRequest) error {
	payload, err := decodeTicketClaimPayload("tickets.close", request.Ticket)
	if err != nil {
		return err
	}
	service, err := options.ticketService()
	if err != nil {
		return err
	}
	return closeTicket(command, service, payload.Reference, payload.As)
}

func applyTicketsComment(command *cobra.Command, options Options, request applyRequest) error {
	var payload ticketCommentApplyRequest
	if err := decodeApplyPayload("tickets.comment", "ticket", request.Ticket, &payload); err != nil {
		return err
	}
	if payload.Reference == "" || payload.Text == "" {
		return fmt.Errorf("ticket.reference and ticket.text are required for tickets.comment")
	}
	service, err := options.ticketService()
	if err != nil {
		return err
	}
	return commentTicket(command, service, payload.Reference, payload.Text)
}

func applyTicketsBoardsCreate(command *cobra.Command, options Options, request applyRequest) error {
	var payload boardCreateApplyRequest
	if err := decodeApplyPayload("tickets.boards.create", "board", request.Board, &payload); err != nil {
		return err
	}
	if payload.Name == "" {
		return fmt.Errorf("board.name is required for tickets.boards.create")
	}
	service, err := options.ticketService()
	if err != nil {
		return err
	}
	return createBoard(command, service, payload.Name)
}
