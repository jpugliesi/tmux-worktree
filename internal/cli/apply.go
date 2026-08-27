package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/jpugliesi/tmux-worktree/internal/domain"
	ticketservice "github.com/jpugliesi/tmux-worktree/internal/ticket"
	workspaceservice "github.com/jpugliesi/tmux-worktree/internal/workspace"
	"github.com/spf13/cobra"
)

// applyRequest is the strict envelope of one apply request. Each operation
// decodes its own payload from the raw section.
type applyRequest struct {
	Operation string          `json:"operation"`
	Template  json.RawMessage `json:"template,omitempty"`
	Workspace json.RawMessage `json:"workspace,omitempty"`
	Agent     json.RawMessage `json:"agent,omitempty"`
	Storage   json.RawMessage `json:"storage,omitempty"`
	Ticket    json.RawMessage `json:"ticket,omitempty"`
	Project   json.RawMessage `json:"project,omitempty"`
}

// templateNameRequest is the payload of each operation that only names a
// Workspace Template.
type templateNameRequest struct {
	Name string `json:"name"`
}

// templateInitializeSetRequest sets Workspace or repository initialization. A
// set repo selects repository initialization; an empty repo selects Workspace
// initialization in cwd.
type templateInitializeSetRequest struct {
	Name             string   `json:"name"`
	Repository       string   `json:"repo,omitempty"`
	WorkingDirectory string   `json:"cwd,omitempty"`
	Command          []string `json:"command"`
}

type templateRecycleRequest struct {
	Name       string   `json:"name"`
	Repository string   `json:"repo"`
	Command    []string `json:"command,omitempty"`
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

type workspaceCreateRequest struct {
	Name     string   `json:"name"`
	Template string   `json:"template"`
	NoOpen   *bool    `json:"noOpen,omitempty"`
	Fresh    bool     `json:"fresh,omitempty"`
	Branch   string   `json:"branch,omitempty"`
	Tickets  []string `json:"tickets,omitempty"`
}

// workspaceReferenceRequest is the payload of each operation that only names a
// Workspace.
type workspaceReferenceRequest struct {
	Reference string `json:"reference"`
}

type workspaceArchiveRequest struct {
	Reference string `json:"reference"`
	Force     bool   `json:"force,omitempty"`
}

type workspaceRenameRequest struct {
	Reference string `json:"reference"`
	Name      string `json:"name"`
}

// workspaceOpenRequest opens or repairs one Workspace tmux session. Apply never
// attaches a tmux client, so noAttach must be true or absent.
type workspaceOpenRequest struct {
	Reference string `json:"reference"`
	AllActive bool   `json:"allActive,omitempty"`
	NoAttach  *bool  `json:"noAttach,omitempty"`
}

type workspaceRemoveRequest struct {
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
	Title     string   `json:"title"`
	Body      string   `json:"body,omitempty"`
	Project   string   `json:"project,omitempty"`
	Slug      string   `json:"slug,omitempty"`
	Status    string   `json:"status,omitempty"`
	Priority  *int     `json:"priority,omitempty"`
	BlockedBy []string `json:"blockedBy,omitempty"`
}

// ticketSetApplyRequest uses pointers so apply can tell an absent field from
// an empty value, the same as a flag presence check.
type ticketSetApplyRequest struct {
	Reference string    `json:"reference"`
	Status    *string   `json:"status,omitempty"`
	Priority  *int      `json:"priority,omitempty"`
	Project   *string   `json:"project,omitempty"`
	BlockedBy *[]string `json:"blockedBy,omitempty"`
}

// ticketEditApplyRequest replaces the body of one Ticket. Body is a pointer,
// so apply can tell an absent body from an empty body that clears the text.
type ticketClaimApplyRequest struct {
	Reference string `json:"reference"`
	As        string `json:"as"`
}

type ticketCommentApplyRequest struct {
	Reference string `json:"reference"`
	Text      string `json:"text"`
}

type ticketAskApplyRequest struct {
	Reference string `json:"reference"`
	As        string `json:"as"`
	Text      string `json:"text"`
}

type ticketAnswerApplyRequest struct {
	Reference string `json:"reference"`
	Text      string `json:"text"`
	Agent     string `json:"agent,omitempty"`
}

type ticketApproveApplyRequest struct {
	Reference string `json:"reference"`
	As        string `json:"as"`
	Note      string `json:"note,omitempty"`
}

type ticketCompleteApplyRequest struct {
	Reference    string   `json:"reference"`
	As           string   `json:"as"`
	Status       string   `json:"status,omitempty"`
	PullRequests []string `json:"pullRequests,omitempty"`
}

type ticketDispatchApplyRequest struct {
	Reference      string `json:"reference"`
	Plan           bool   `json:"plan,omitempty"`
	MaxConcurrency int    `json:"maxConcurrency,omitempty"`
}

type ticketCloudSyncApplyRequest struct {
	Project string `json:"project"`
}

type ticketCloudAbandonApplyRequest struct {
	Session string `json:"session"`
	Force   bool   `json:"force"`
}

type projectCreateApplyRequest struct {
	Name     string `json:"name"`
	Template string `json:"template,omitempty"`
}

type projectSetApplyRequest struct {
	Name     string `json:"name"`
	Template string `json:"template"`
}

type projectCloseApplyRequest struct {
	Name  string `json:"name"`
	Force bool   `json:"force,omitempty"`
}

type agentRegisterRequest struct {
	Workspace         string   `json:"workspace"`
	Provider          string   `json:"provider"`
	Label             string   `json:"label,omitempty"`
	Pane              string   `json:"pane,omitempty"`
	ProviderSessionID string   `json:"providerSessionId,omitempty"`
	ResumeCommand     []string `json:"resumeCommand,omitempty"`
}

// agentReferenceRequest is the payload of each operation that only names one
// Agent Session of a Workspace.
type agentReferenceRequest struct {
	Reference string `json:"reference"`
	Workspace string `json:"workspace,omitempty"`
}

// agentSendRequest sends feedback to one Agent Session. The text replaces the
// standard input of the agents send command.
type agentSendRequest struct {
	Reference string `json:"reference"`
	Workspace string `json:"workspace,omitempty"`
	Text      string `json:"text"`
}

type agentTranscriptLinkRequest struct {
	Reference string `json:"reference"`
	Workspace string `json:"workspace,omitempty"`
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
			{Path: "template.name", Type: "string", Required: true, Condition: "no Workspace record can name the Workspace Template"},
		}}, applyTemplatesRemove},
		{applyOperationSchema{Operation: "templates.prepare", Payload: "template", Fields: []requestFieldSchema{
			{Path: "template.name", Type: "string", Required: true},
		}}, applyTemplatesPrepare},
		{applyOperationSchema{Operation: "templates.init.set", Payload: "template", Fields: []requestFieldSchema{
			{Path: "template.name", Type: "string", Required: true},
			{Path: "template.command", Type: "array[string]", Required: true},
			{Path: "template.repo", Type: "string", Required: false, Condition: "sets repository initialization, which runs in the repository worktree"},
			{Path: "template.cwd", Type: "string", Required: false, Condition: "required when template.repo is empty; the path is relative to the Workspace root"},
		}}, applyTemplatesInitSet},
		{applyOperationSchema{Operation: "templates.recycle.set", Payload: "template", Fields: []requestFieldSchema{
			{Path: "template.name", Type: "string", Required: true},
			{Path: "template.repo", Type: "string", Required: true},
			{Path: "template.command", Type: "array[string]", Required: true},
		}}, applyTemplatesRecycleSet},
		{applyOperationSchema{Operation: "templates.recycle.unset", Payload: "template", Fields: []requestFieldSchema{
			{Path: "template.name", Type: "string", Required: true},
			{Path: "template.repo", Type: "string", Required: true},
		}}, applyTemplatesRecycleUnset},
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
		{applyOperationSchema{Operation: "workspaces.create", Payload: "workspace", Fields: []requestFieldSchema{
			{Path: "workspace.name", Type: "string", Required: true},
			{Path: "workspace.template", Type: "string", Required: true},
			{Path: "workspace.noOpen", Type: "boolean", Required: false, Condition: "must be true or absent; apply never opens a tmux session"},
			{Path: "workspace.fresh", Type: "boolean", Required: false, Condition: "fetches the default branch before the claim"},
			{Path: "workspace.branch", Type: "string", Required: false, Condition: "sets a custom Workspace branch name"},
			{Path: "workspace.tickets", Type: "array[string]", Required: false, Condition: "all Tickets must be open and belong to one Project"},
		}}, applyWorkspacesCreate},
		{applyOperationSchema{Operation: "workspaces.open", Payload: "workspace", Fields: []requestFieldSchema{
			{Path: "workspace.reference", Type: "string", Required: false, Condition: "required when workspace.allActive is not true"},
			{Path: "workspace.allActive", Type: "boolean", Required: false, Condition: "repairs every active Workspace and attaches no tmux client"},
			{Path: "workspace.noAttach", Type: "boolean", Required: false, Condition: "must be true or absent; apply repairs the tmux session but attaches no tmux client"},
		}}, applyWorkspacesOpen},
		{applyOperationSchema{Operation: "workspaces.rename", Payload: "workspace", Fields: []requestFieldSchema{
			{Path: "workspace.reference", Type: "string", Required: true},
			{Path: "workspace.name", Type: "string", Required: true},
		}}, applyWorkspacesRename},
		{applyOperationSchema{Operation: "workspaces.setup.retry", Payload: "workspace", Fields: []requestFieldSchema{
			{Path: "workspace.reference", Type: "string", Required: true},
		}}, applyWorkspacesSetupRetry},
		{applyOperationSchema{Operation: "workspaces.archive", Payload: "workspace", Fields: []requestFieldSchema{
			{Path: "workspace.reference", Type: "string", Required: true},
			{Path: "workspace.force", Type: "boolean", Required: false, Condition: "discards uncommitted changes and preserves ignored files"},
		}}, applyWorkspacesArchive},
		{applyOperationSchema{Operation: "workspaces.remove", Payload: "workspace", Fields: []requestFieldSchema{
			{Path: "workspace.reference", Type: "string", Required: true},
			{Path: "workspace.apply", Type: "boolean", Required: false, Condition: "false or absent returns the removal plan only"},
			{Path: "workspace.force", Type: "boolean", Required: false},
		}}, applyWorkspacesRemove},
		{applyOperationSchema{Operation: "agents.register", Payload: "agent", Fields: []requestFieldSchema{
			{Path: "agent.workspace", Type: "string", Required: true},
			{Path: "agent.provider", Type: "string", Required: true, Enum: agentProviderNames},
			{Path: "agent.label", Type: "string", Required: false},
			{Path: "agent.pane", Type: "string", Required: false, Condition: "required when agent.resumeCommand is empty; a tmux pane ID, because apply cannot use the value current"},
			{Path: "agent.providerSessionId", Type: "string", Required: false},
			{Path: "agent.resumeCommand", Type: "array[string]", Required: false, Condition: "required when agent.pane is empty"},
		}}, applyAgentsRegister},
		{applyOperationSchema{Operation: "agents.send", Payload: "agent", Fields: []requestFieldSchema{
			{Path: "agent.reference", Type: "string", Required: true, Condition: "the Agent Session pane must be live"},
			{Path: "agent.text", Type: "string", Required: true, Condition: "the text replaces the standard input of 'twt agents send'"},
			{Path: "agent.workspace", Type: "string", Required: false, Condition: "absent selects the current Workspace"},
		}}, applyAgentsSend},
		{applyOperationSchema{Operation: "agents.resume", Payload: "agent", Fields: []requestFieldSchema{
			{Path: "agent.reference", Type: "string", Required: true},
			{Path: "agent.workspace", Type: "string", Required: false, Condition: "absent selects the Workspace of the Agent Session"},
		}}, applyAgentsResume},
		{applyOperationSchema{Operation: "agents.rm", Payload: "agent", Fields: []requestFieldSchema{
			{Path: "agent.reference", Type: "string", Required: true},
			{Path: "agent.workspace", Type: "string", Required: false, Condition: "absent selects the current Workspace"},
		}}, applyAgentsRemove},
		{applyOperationSchema{Operation: "agents.transcript.link", Payload: "agent", Fields: []requestFieldSchema{
			{Path: "agent.reference", Type: "string", Required: true},
			{Path: "agent.session", Type: "string", Required: true},
			{Path: "agent.workspace", Type: "string", Required: false, Condition: "absent selects the current Workspace"},
		}}, applyAgentsTranscriptLink},
		{applyOperationSchema{Operation: "storage.clean", Payload: "storage", Fields: []requestFieldSchema{
			{Path: "storage.apply", Type: "boolean", Required: false, Condition: "false or absent returns the cleanup plan only"},
		}}, applyStorageClean},
		{applyOperationSchema{Operation: "tickets.init", Payload: "", Fields: []requestFieldSchema{}}, applyTicketsInit},
		{applyOperationSchema{Operation: "tickets.create", Payload: "ticket", Fields: []requestFieldSchema{
			{Path: "ticket.title", Type: "string", Required: true},
			{Path: "ticket.body", Type: "string", Required: false},
			{Path: "ticket.project", Type: "string", Required: false, Condition: "the Project must exist"},
			{Path: "ticket.slug", Type: "string", Required: false, Condition: "absent derives the slug from the title"},
			{Path: "ticket.status", Type: "string", Required: false, Enum: domain.TicketStatuses(), Condition: "absent selects needs-triage"},
			{Path: "ticket.priority", Type: "integer", Required: false, Condition: "0 (highest) to 4 (lowest); absent selects 2"},
			{Path: "ticket.blockedBy", Type: "array[string]", Required: false, Condition: "each value is a slug or wiki-link; absent writes an empty list"},
		}}, applyTicketsCreate},
		{applyOperationSchema{Operation: "tickets.set", Payload: "ticket", Fields: []requestFieldSchema{
			{Path: "ticket.reference", Type: "string", Required: true},
			{Path: "ticket.status", Type: "string", Required: false, Enum: domain.TicketStatuses(), Condition: "set at least one of ticket.status, ticket.priority, ticket.project, or ticket.blockedBy"},
			{Path: "ticket.priority", Type: "integer", Required: false},
			{Path: "ticket.project", Type: "string", Required: false},
			{Path: "ticket.blockedBy", Type: "array[string]", Required: false, Condition: "replaces the blocker list; an empty array clears it"},
		}}, applyTicketsSet},
		{applyOperationSchema{Operation: "tickets.claim", Payload: "ticket", Fields: []requestFieldSchema{
			{Path: "ticket.reference", Type: "string", Required: true},
			{Path: "ticket.as", Type: "string", Required: true, Condition: "apply is never a terminal, so the claimant has no default"},
		}}, applyTicketsClaim},
		{applyOperationSchema{Operation: "tickets.unclaim", Payload: "ticket", Fields: []requestFieldSchema{
			{Path: "ticket.reference", Type: "string", Required: true},
			{Path: "ticket.as", Type: "string", Required: true, Condition: "apply is never a terminal, so the claimant has no default"},
		}}, applyTicketsUnclaim},
		{applyOperationSchema{Operation: "tickets.complete", Payload: "ticket", Fields: []requestFieldSchema{
			{Path: "ticket.reference", Type: "string", Required: true},
			{Path: "ticket.as", Type: "string", Required: true, Condition: "apply is never a terminal, so the claimant has no default"},
			{Path: "ticket.status", Type: "string", Required: false, Enum: []string{"ready-for-human", "ready-for-agent"}, Condition: "absent selects ready-for-human"},
			{Path: "ticket.pullRequests", Type: "array[string]", Required: false, Condition: "HTTPS pull request URLs to record"},
		}}, applyTicketsComplete},
		{applyOperationSchema{Operation: "tickets.close", Payload: "ticket", Fields: []requestFieldSchema{
			{Path: "ticket.reference", Type: "string", Required: true},
			{Path: "ticket.as", Type: "string", Required: true, Condition: "apply is never a terminal, so the claimant has no default"},
		}}, applyTicketsClose},
		{applyOperationSchema{Operation: "tickets.comment", Payload: "ticket", Fields: []requestFieldSchema{
			{Path: "ticket.reference", Type: "string", Required: true},
			{Path: "ticket.text", Type: "string", Required: true},
		}}, applyTicketsComment},
		{applyOperationSchema{Operation: "tickets.plan", Payload: "ticket", Fields: []requestFieldSchema{
			{Path: "ticket.reference", Type: "string", Required: true},
			{Path: "ticket.plan", Type: "string", Required: true, Condition: "replaces the whole ## Plan section"},
			{Path: "ticket.as", Type: "string", Required: false, Condition: "required when the Ticket is claimed"},
		}}, applyTicketsPlan},
		{applyOperationSchema{Operation: "tickets.ask", Payload: "ticket", Fields: []requestFieldSchema{
			{Path: "ticket.reference", Type: "string", Required: true},
			{Path: "ticket.as", Type: "string", Required: true, Condition: "must match the Ticket's claimant"},
			{Path: "ticket.text", Type: "string", Required: true},
		}}, applyTicketsAsk},
		{applyOperationSchema{Operation: "tickets.answer", Payload: "ticket", Fields: []requestFieldSchema{
			{Path: "ticket.reference", Type: "string", Required: true},
			{Path: "ticket.text", Type: "string", Required: true},
			{Path: "ticket.agent", Type: "string", Required: false, Condition: "relay target when several agent sessions are live"},
		}}, applyTicketsAnswer},
		{applyOperationSchema{Operation: "tickets.approve", Payload: "ticket", Fields: []requestFieldSchema{
			{Path: "ticket.reference", Type: "string", Required: true, Condition: "the Ticket must carry a ## Plan section"},
			{Path: "ticket.as", Type: "string", Required: true, Condition: "the approver name"},
			{Path: "ticket.note", Type: "string", Required: false, Condition: "an approval note recorded with the reply"},
		}}, applyTicketsApprove},
		{applyOperationSchema{Operation: "tickets.pr.add", Payload: "ticket", Fields: []requestFieldSchema{
			{Path: "ticket.reference", Type: "string", Required: true},
			{Path: "ticket.pullRequests", Type: "array[string]", Required: true},
			{Path: "ticket.as", Type: "string", Required: false, Condition: "required when the Ticket is claimed"},
		}}, applyTicketsPRAdd},
		{applyOperationSchema{Operation: "tickets.pr.rm", Payload: "ticket", Fields: []requestFieldSchema{
			{Path: "ticket.reference", Type: "string", Required: true},
			{Path: "ticket.pullRequests", Type: "array[string]", Required: true},
			{Path: "ticket.as", Type: "string", Required: false, Condition: "required when the Ticket is claimed"},
		}}, applyTicketsPRRemove},
		{applyOperationSchema{Operation: "tickets.dispatch", Payload: "ticket", Fields: []requestFieldSchema{
			{Path: "ticket.reference", Type: "string", Required: true, Condition: "the Ticket must be ready in its Project queue"},
			{Path: "ticket.plan", Type: "boolean", Required: false, Condition: "true requests a plan; absent or false requests implementation and pull requests"},
			{Path: "ticket.maxConcurrency", Type: "integer", Required: false, Condition: "overrides the Project-wide active Session limit"},
		}}, applyTicketsDispatch},
		{applyOperationSchema{Operation: "tickets.sync", Payload: "ticket", Fields: []requestFieldSchema{
			{Path: "ticket.project", Type: "string", Required: false, Condition: "also reconcile this Project's dispatch Sessions"},
		}}, applyTicketsSync},
		{applyOperationSchema{Operation: "tickets.abandon", Payload: "ticket", Fields: []requestFieldSchema{
			{Path: "ticket.session", Type: "string", Required: true},
			{Path: "ticket.force", Type: "boolean", Required: true, Condition: "acknowledges that the Workspace and its agent keep running"},
		}}, applyTicketsAbandon},
		{applyOperationSchema{Operation: "tickets.repair", Payload: "", Fields: []requestFieldSchema{}}, applyTicketsRepair},
		{applyOperationSchema{Operation: "projects.plan.edit", Payload: "project", Fields: []requestFieldSchema{
			{Path: "project.name", Type: "string", Required: true},
			{Path: "project.plan", Type: "string", Required: true, Condition: "replaces the whole plan.md; creates it when missing"},
		}}, applyProjectsPlanEdit},
		{applyOperationSchema{Operation: "projects.create", Payload: "project", Fields: []requestFieldSchema{
			{Path: "project.name", Type: "string", Required: true},
			{Path: "project.template", Type: "string", Required: false},
		}}, applyTicketsProjectsCreate},
		{applyOperationSchema{Operation: "projects.close", Payload: "project", Fields: []requestFieldSchema{
			{Path: "project.name", Type: "string", Required: true},
			{Path: "project.force", Type: "boolean", Required: false, Condition: "required when the Project has open Tickets"},
		}}, applyTicketsProjectsClose},
		{applyOperationSchema{Operation: "projects.set", Payload: "project", Fields: []requestFieldSchema{
			{Path: "project.name", Type: "string", Required: true},
			{Path: "project.template", Type: "string", Required: true},
		}}, applyTicketsProjectsSet},
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
	command := &cobra.Command{
		Use:   "apply -",
		Short: "Apply a typed JSON mutation request",
		Args:  requireStdinToken(),
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
	setArguments(command, stdinTokenArgument(true))
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
	"Run these in a terminal: 'twt next', 'twt tickets start', 'twt switch', 'twt done', 'twt archive', " +
	"'twt templates edit', 'twt tickets home', 'twt agents focus', 'twt agents open', and 'twt agents register --pane current'. " +
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

// decodeTemplateNamePayload decodes one payload that only names a Workspace
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

// applyWorkspaceReference maps the optional workspace field of a payload to a
// WORKSPACE reference. An empty value selects the current Workspace.
func applyWorkspaceReference(value string) string {
	if strings.TrimSpace(value) == "" {
		return currentWorkspaceReference
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
		return fmt.Errorf("template.cwd is required for Workspace initialization; set template.repo for repository initialization")
	}
	return setTemplateInitialization(command, options, options.templateStore(), options.StateDir, payload.Name, repository, workingDirectory, payload.Command)
}

func applyTemplatesRecycleSet(command *cobra.Command, options Options, request applyRequest) error {
	var payload templateRecycleRequest
	if err := decodeApplyPayload("templates.recycle.set", "template", request.Template, &payload); err != nil {
		return err
	}
	if payload.Name == "" || payload.Repository == "" || len(payload.Command) == 0 {
		return fmt.Errorf("template.name, template.repo, and template.command are required for templates.recycle.set")
	}
	return setTemplateRecycle(command, options, options.templateStore(), options.StateDir, payload.Name, payload.Repository, payload.Command)
}

func applyTemplatesRecycleUnset(command *cobra.Command, options Options, request applyRequest) error {
	var payload templateRecycleRequest
	if err := decodeApplyPayload("templates.recycle.unset", "template", request.Template, &payload); err != nil {
		return err
	}
	if payload.Name == "" || payload.Repository == "" {
		return fmt.Errorf("template.name and template.repo are required for templates.recycle.unset")
	}
	if len(payload.Command) != 0 {
		return fmt.Errorf("template.command is not supported for templates.recycle.unset")
	}
	return setTemplateRecycle(command, options, options.templateStore(), options.StateDir, payload.Name, payload.Repository, nil)
}

func applyTemplatesReposRemove(command *cobra.Command, options Options, request applyRequest) error {
	var payload templateRepositoryRemoveRequest
	if err := decodeApplyPayload("templates.repos.remove", "template", request.Template, &payload); err != nil {
		return err
	}
	if payload.Name == "" || payload.Repository == "" {
		return fmt.Errorf("template.name and template.repo are required for templates.repos.remove")
	}
	return removeRepositoryFromTemplate(command, options, options.templateStore(), options.StateDir, payload.Name, payload.Repository)
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

func applyWorkspacesCreate(command *cobra.Command, options Options, request applyRequest) error {
	var payload workspaceCreateRequest
	if err := decodeApplyPayload("workspaces.create", "workspace", request.Workspace, &payload); err != nil {
		return err
	}
	if payload.Name == "" || payload.Template == "" {
		return fmt.Errorf("workspace.name and workspace.template are required for workspaces.create")
	}
	if payload.NoOpen != nil && !*payload.NoOpen {
		return fmt.Errorf("apply never opens a tmux session; workspace.noOpen must be true or absent")
	}
	project, tickets, err := resolveWorkspaceTicketLinks(options, payload.Tickets)
	if err != nil {
		return err
	}
	template, err := options.templateStore().Load(payload.Template)
	if err != nil {
		return err
	}
	createOptions := workspaceservice.CreateOptions{
		Branch: payload.Branch, Fresh: payload.Fresh, Project: project, Tickets: tickets,
	}
	if isDryRun(command) {
		if err := validateCreate(options, options.workspaceService(), payload.Name, payload.Template, template, createOptions); err != nil {
			return err
		}
		return writeMutation(command, "workspaces.create", statusValid, "", payload.Name)
	}
	workspace, err := createWorkspace(command, options, payload.Name, payload.Template, template, createOptions)
	if err != nil {
		return err
	}
	return writeMutation(command, "workspaces.create", statusApplied, workspace.ID, workspace.Name)
}

// resolveApplyWorkspaceReference decodes one payload that only names a Workspace
// and maps it to a stable Workspace reference.
func resolveApplyWorkspaceReference(operation string, raw json.RawMessage, service *workspaceservice.Service) (string, error) {
	var payload workspaceReferenceRequest
	if err := decodeApplyPayload(operation, "workspace", raw, &payload); err != nil {
		return "", err
	}
	if payload.Reference == "" {
		return "", fmt.Errorf("workspace.reference is required for %s", operation)
	}
	return resolveWorkspaceReference(service, payload.Reference)
}

func applyWorkspacesOpen(command *cobra.Command, options Options, request applyRequest) error {
	var payload workspaceOpenRequest
	if err := decodeApplyPayload("workspaces.open", "workspace", request.Workspace, &payload); err != nil {
		return err
	}
	if payload.NoAttach != nil && !*payload.NoAttach {
		return fmt.Errorf("apply never attaches a tmux client; workspace.noAttach must be true or absent")
	}
	service := options.workspaceService()
	if payload.AllActive {
		if payload.Reference != "" {
			return fmt.Errorf("workspace.reference and workspace.allActive cannot be set together")
		}
		return openAllActiveSessions(command, service)
	}
	if payload.Reference == "" {
		return fmt.Errorf("workspace.reference is required for workspaces.open")
	}
	reference, err := resolveWorkspaceReference(service, payload.Reference)
	if err != nil {
		return err
	}
	_, err = openWorkspaceSession(command, service, reference)
	return err
}

func applyWorkspacesSetupRetry(command *cobra.Command, options Options, request applyRequest) error {
	service := options.workspaceService()
	reference, err := resolveApplyWorkspaceReference("workspaces.setup.retry", request.Workspace, service)
	if err != nil {
		return err
	}
	return retryWorkspaceSetup(command, service, reference)
}

func applyWorkspacesRename(command *cobra.Command, options Options, request applyRequest) error {
	var payload workspaceRenameRequest
	if err := decodeApplyPayload("workspaces.rename", "workspace", request.Workspace, &payload); err != nil {
		return err
	}
	if payload.Reference == "" || payload.Name == "" {
		return fmt.Errorf("workspace.reference and workspace.name are required for workspaces.rename")
	}
	service := options.workspaceService()
	workspace, err := resolveWorkspace(service, payload.Reference)
	if err != nil {
		return err
	}
	return renameWorkspace(command, service, workspace.ID, workspace.Name, payload.Name)
}

func applyWorkspacesArchive(command *cobra.Command, options Options, request applyRequest) error {
	var payload workspaceArchiveRequest
	if err := decodeApplyPayload("workspaces.archive", "workspace", request.Workspace, &payload); err != nil {
		return err
	}
	if payload.Reference == "" {
		return fmt.Errorf("workspace.reference is required for workspaces.archive")
	}
	service := options.workspaceService()
	reference, err := resolveWorkspaceReference(service, payload.Reference)
	if err != nil {
		return err
	}
	return archiveWorkspaceRecord(command, service, reference, workspaceservice.ReleaseOptions{Force: payload.Force})
}

func applyWorkspacesRemove(command *cobra.Command, options Options, request applyRequest) error {
	var payload workspaceRemoveRequest
	if err := decodeApplyPayload("workspaces.remove", "workspace", request.Workspace, &payload); err != nil {
		return err
	}
	if payload.Reference == "" {
		return fmt.Errorf("workspace.reference is required for workspaces.remove")
	}
	service := options.workspaceService()
	reference, err := resolveWorkspaceReference(service, payload.Reference)
	if err != nil {
		return err
	}
	return runWorkspaceRemoval(command, service, reference, payload.Apply, workspaceservice.RemovalOptions{AllowUnpublished: payload.Force})
}

func applyAgentsRegister(command *cobra.Command, options Options, request applyRequest) error {
	var payload agentRegisterRequest
	if err := decodeApplyPayload("agents.register", "agent", request.Agent, &payload); err != nil {
		return err
	}
	if payload.Pane == currentPaneReference {
		return fmt.Errorf("apply cannot use the pane value %q, because it needs the tmux pane of a terminal; set agent.pane to a tmux pane ID", currentPaneReference)
	}
	workspace, err := resolveWorkspace(options.workspaceService(), payload.Workspace)
	if err != nil {
		return err
	}
	return registerAgent(command, options.agentService(), workspace, payload.Provider, payload.Label, payload.Pane, payload.ProviderSessionID, payload.ResumeCommand)
}

// decodeAgentReferencePayload decodes one payload that only names an Agent
// Session of a Workspace.
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
	workspace, err := resolveWorkspace(options.workspaceService(), applyWorkspaceReference(payload.Workspace))
	if err != nil {
		return err
	}
	return sendAgentFeedback(command, options.agentService(), workspace, options.StateDir, payload.Reference, payload.Text)
}

func applyAgentsResume(command *cobra.Command, options Options, request applyRequest) error {
	payload, err := decodeAgentReferencePayload("agents.resume", request.Agent)
	if err != nil {
		return err
	}
	return resumeAgentSession(command, options.agentService(), options.workspaceService(), options.StateDir, payload.Reference, strings.TrimSpace(payload.Workspace))
}

func applyAgentsRemove(command *cobra.Command, options Options, request applyRequest) error {
	payload, err := decodeAgentReferencePayload("agents.rm", request.Agent)
	if err != nil {
		return err
	}
	workspace, err := resolveWorkspace(options.workspaceService(), applyWorkspaceReference(payload.Workspace))
	if err != nil {
		return err
	}
	return removeAgentSession(command, options.agentService(), payload.Reference, workspace, options.StateDir)
}

func applyAgentsTranscriptLink(command *cobra.Command, options Options, request applyRequest) error {
	var payload agentTranscriptLinkRequest
	if err := decodeApplyPayload("agents.transcript.link", "agent", request.Agent, &payload); err != nil {
		return err
	}
	if payload.Reference == "" || payload.Session == "" {
		return fmt.Errorf("agent.reference and agent.session are required for agents.transcript.link")
	}
	workspace, err := resolveWorkspace(options.workspaceService(), applyWorkspaceReference(payload.Workspace))
	if err != nil {
		return err
	}
	return linkAgentTranscript(command, options.agentService(), workspace, options.StateDir, payload.Reference, payload.Session)
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
		Title:     payload.Title,
		Slug:      payload.Slug,
		Project:   payload.Project,
		Body:      payload.Body,
		Status:    domain.TicketStatus(payload.Status),
		Priority:  priority,
		BlockedBy: payload.BlockedBy,
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
	if payload.Project != nil {
		setRequest.Project, setRequest.ProjectSet = *payload.Project, true
	}
	if payload.BlockedBy != nil {
		setRequest.BlockedBy, setRequest.BlockedBySet = *payload.BlockedBy, true
	}
	if !setRequest.StatusSet && !setRequest.PrioritySet && !setRequest.ProjectSet && !setRequest.BlockedBySet {
		return fmt.Errorf("tickets.set requires at least one of ticket.status, ticket.priority, ticket.project, or ticket.blockedBy")
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

type ticketPlanApplyRequest struct {
	Reference string `json:"reference"`
	Plan      string `json:"plan"`
	As        string `json:"as,omitempty"`
}

type projectPlanApplyRequest struct {
	Name string `json:"name"`
	Plan string `json:"plan"`
}

func applyProjectsPlanEdit(command *cobra.Command, options Options, request applyRequest) error {
	var payload projectPlanApplyRequest
	if err := decodeApplyPayload("projects.plan.edit", "project", request.Project, &payload); err != nil {
		return err
	}
	if payload.Name == "" || payload.Plan == "" {
		return fmt.Errorf("project.name and project.plan are required for projects.plan.edit")
	}
	service, err := options.ticketService()
	if err != nil {
		return err
	}
	return editProjectPlan(command, service, payload.Name, payload.Plan)
}

func applyTicketsPlan(command *cobra.Command, options Options, request applyRequest) error {
	var payload ticketPlanApplyRequest
	if err := decodeApplyPayload("tickets.plan", "ticket", request.Ticket, &payload); err != nil {
		return err
	}
	if payload.Reference == "" || payload.Plan == "" {
		return fmt.Errorf("ticket.reference and ticket.plan are required for tickets.plan")
	}
	service, err := options.ticketService()
	if err != nil {
		return err
	}
	return planTicket(command, service, payload.Reference, payload.As, payload.Plan)
}

func applyTicketsAsk(command *cobra.Command, options Options, request applyRequest) error {
	var payload ticketAskApplyRequest
	if err := decodeApplyPayload("tickets.ask", "ticket", request.Ticket, &payload); err != nil {
		return err
	}
	if payload.Reference == "" || payload.As == "" || payload.Text == "" {
		return fmt.Errorf("ticket.reference, ticket.as, and ticket.text are required for tickets.ask")
	}
	service, err := options.ticketService()
	if err != nil {
		return err
	}
	return askTicket(command, service, payload.Reference, payload.As, payload.Text)
}

func applyTicketsAnswer(command *cobra.Command, options Options, request applyRequest) error {
	var payload ticketAnswerApplyRequest
	if err := decodeApplyPayload("tickets.answer", "ticket", request.Ticket, &payload); err != nil {
		return err
	}
	if payload.Reference == "" || payload.Text == "" {
		return fmt.Errorf("ticket.reference and ticket.text are required for tickets.answer")
	}
	service, err := options.ticketService()
	if err != nil {
		return err
	}
	return answerTicket(command, options, service, payload.Reference, payload.Agent, payload.Text)
}

type ticketPRApplyRequest struct {
	Reference    string   `json:"reference"`
	PullRequests []string `json:"pullRequests"`
	As           string   `json:"as,omitempty"`
}

func applyTicketsApprove(command *cobra.Command, options Options, request applyRequest) error {
	var payload ticketApproveApplyRequest
	if err := decodeApplyPayload("tickets.approve", "ticket", request.Ticket, &payload); err != nil {
		return err
	}
	if payload.Reference == "" || payload.As == "" {
		return fmt.Errorf("ticket.reference and ticket.as are required for tickets.approve")
	}
	service, err := options.ticketService()
	if err != nil {
		return err
	}
	return approveTicket(command, options, service, payload.Reference, payload.As, payload.Note, "")
}

func applyTicketsPRAdd(command *cobra.Command, options Options, request applyRequest) error {
	return applyTicketPRs(command, options, request, "tickets.pr.add")
}

func applyTicketsPRRemove(command *cobra.Command, options Options, request applyRequest) error {
	return applyTicketPRs(command, options, request, "tickets.pr.rm")
}

func applyTicketPRs(command *cobra.Command, options Options, request applyRequest, operation string) error {
	var payload ticketPRApplyRequest
	if err := decodeApplyPayload(operation, "ticket", request.Ticket, &payload); err != nil {
		return err
	}
	if payload.Reference == "" || len(payload.PullRequests) == 0 {
		return fmt.Errorf("ticket.reference and ticket.pullRequests are required for %s", operation)
	}
	service, err := options.ticketService()
	if err != nil {
		return err
	}
	run := service.AddPullRequests
	format := "Attached %d pull request(s) to ticket %q\n"
	if operation == "tickets.pr.rm" {
		run = service.RemovePullRequests
		format = "Detached %d pull request(s) from ticket %q\n"
	}
	return mutateTicketPRs(command, service, operation, payload.Reference, payload.As, payload.PullRequests, run, format)
}

func applyTicketsComplete(command *cobra.Command, options Options, request applyRequest) error {
	var payload ticketCompleteApplyRequest
	if err := decodeApplyPayload("tickets.complete", "ticket", request.Ticket, &payload); err != nil {
		return err
	}
	if payload.Reference == "" || payload.As == "" {
		return fmt.Errorf("ticket.reference and ticket.as are required for tickets.complete")
	}
	if payload.Status == "" {
		payload.Status = string(domain.TicketReadyForHuman)
	}
	service, err := options.ticketService()
	if err != nil {
		return err
	}
	return completeTicketWork(command, service, payload.Reference, payload.As, domain.TicketStatus(payload.Status), payload.PullRequests)
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

func applyTicketsDispatch(command *cobra.Command, options Options, request applyRequest) error {
	var payload ticketDispatchApplyRequest
	if err := decodeApplyPayload("tickets.dispatch", "ticket", request.Ticket, &payload); err != nil {
		return err
	}
	if payload.Reference == "" {
		return fmt.Errorf("ticket.reference is required for tickets.dispatch")
	}
	return runLocalDispatch(command, options, payload.Reference, payload.Plan, payload.MaxConcurrency)
}

func applyTicketsSync(command *cobra.Command, options Options, request applyRequest) error {
	var payload ticketCloudSyncApplyRequest
	if err := decodeApplyPayload("tickets.sync", "ticket", request.Ticket, &payload); err != nil {
		return err
	}
	return runTicketsSync(command, options, payload.Project)
}

func applyTicketsAbandon(command *cobra.Command, options Options, request applyRequest) error {
	var payload ticketCloudAbandonApplyRequest
	if err := decodeApplyPayload("tickets.abandon", "ticket", request.Ticket, &payload); err != nil {
		return err
	}
	if payload.Session == "" || !payload.Force {
		return fmt.Errorf("ticket.session and ticket.force=true are required for tickets.abandon")
	}
	return runLocalDispatchAbandon(command, options, payload.Session)
}

func applyTicketsRepair(command *cobra.Command, options Options, request applyRequest) error {
	if len(request.Template) > 0 || len(request.Workspace) > 0 || len(request.Agent) > 0 ||
		len(request.Storage) > 0 || len(request.Ticket) > 0 || len(request.Project) > 0 {
		return fmt.Errorf("tickets.repair accepts no payload")
	}
	service, err := options.ticketService()
	if err != nil {
		return err
	}
	return repairTickets(command, service)
}

func applyTicketsProjectsCreate(command *cobra.Command, options Options, request applyRequest) error {
	var payload projectCreateApplyRequest
	if err := decodeApplyPayload("projects.create", "project", request.Project, &payload); err != nil {
		return err
	}
	if payload.Name == "" {
		return fmt.Errorf("project.name is required for projects.create")
	}
	service, err := options.ticketService()
	if err != nil {
		return err
	}
	return createProject(command, options, service, payload.Name, payload.Template)
}

func applyTicketsProjectsClose(command *cobra.Command, options Options, request applyRequest) error {
	var payload projectCloseApplyRequest
	if err := decodeApplyPayload("projects.close", "project", request.Project, &payload); err != nil {
		return err
	}
	if payload.Name == "" {
		return fmt.Errorf("project.name is required for projects.close")
	}
	service, err := options.ticketService()
	if err != nil {
		return err
	}
	return closeProject(command, service, payload.Name, payload.Force, false)
}

func applyTicketsProjectsSet(command *cobra.Command, options Options, request applyRequest) error {
	var payload projectSetApplyRequest
	if err := decodeApplyPayload("projects.set", "project", request.Project, &payload); err != nil {
		return err
	}
	if payload.Name == "" || payload.Template == "" {
		return fmt.Errorf("project.name and project.template are required for projects.set")
	}
	if _, err := options.templateStore().Load(payload.Template); err != nil {
		return err
	}
	service, err := options.ticketService()
	if err != nil {
		return err
	}
	return setProjectTemplate(command, service, payload.Name, payload.Template)
}
