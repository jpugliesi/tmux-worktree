package cursorcloud

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
)

const HarnessSchemaVersion = 1

type Runner interface {
	Run(context.Context, []byte) (stdout []byte, stderr []byte, err error)
}

type Client struct {
	runner Runner
}

func NewClient(runner Runner) *Client {
	return &Client{runner: runner}
}

type Repository struct {
	Name        string `json:"name"`
	URL         string `json:"url"`
	StartingRef string `json:"startingRef,omitempty"`
}

type DispatchRequest struct {
	SessionID            string
	TicketSlug           string
	Project              string
	Mode                 string
	Prompt               string
	Model                string
	Effort               string
	CreateIdempotencyKey string
	SendIdempotencyKey   string
	Repositories         []Repository
}

type EffectiveEffort struct {
	Kind  string `json:"kind"`
	Value string `json:"value"`
}

type DispatchResult struct {
	AgentID   string          `json:"agentId"`
	RunID     string          `json:"runId"`
	RequestID string          `json:"requestId,omitempty"`
	Effort    EffectiveEffort `json:"effort"`
}

type SessionReference struct {
	SessionID string `json:"sessionId"`
	AgentID   string `json:"agentId,omitempty"`
	RunID     string `json:"runId,omitempty"`
}

type SyncRequest struct {
	Sessions []SessionReference `json:"sessions"`
}

type RepositoryResult struct {
	URL    string `json:"url"`
	Branch string `json:"branch,omitempty"`
	PRURL  string `json:"prUrl,omitempty"`
}

type SyncObservation struct {
	SessionID    string             `json:"sessionId"`
	AgentID      string             `json:"agentId,omitempty"`
	RunID        string             `json:"runId,omitempty"`
	Status       string             `json:"status"`
	RequestID    string             `json:"requestId,omitempty"`
	Result       string             `json:"result,omitempty"`
	Error        *Error             `json:"error,omitempty"`
	Repositories []RepositoryResult `json:"repositories"`
}

type SyncResult struct {
	Sessions []SyncObservation `json:"sessions"`
}

type Error struct {
	Kind      string `json:"kind"`
	Code      string `json:"code,omitempty"`
	Message   string `json:"message"`
	Retryable bool   `json:"retryable,omitempty"`
	HelpURL   string `json:"helpUrl,omitempty"`
	RequestID string `json:"requestId,omitempty"`
}

func (e *Error) Error() string {
	if e.Code == "" {
		return e.Message
	}
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

func (e *Error) Uncertain() bool {
	return e.Kind == "network" || e.Kind == "unknown" || e.Kind == "harness"
}

type dispatchWireRequest struct {
	SchemaVersion        int               `json:"schemaVersion"`
	Operation            string            `json:"operation"`
	SessionID            string            `json:"sessionId"`
	Mode                 string            `json:"mode"`
	Prompt               string            `json:"prompt"`
	Model                string            `json:"model,omitempty"`
	Effort               string            `json:"effort"`
	CreateIdempotencyKey string            `json:"createIdempotencyKey"`
	SendIdempotencyKey   string            `json:"sendIdempotencyKey"`
	Metadata             map[string]string `json:"metadata"`
	Repositories         []Repository      `json:"repositories"`
}

type syncWireRequest struct {
	SchemaVersion int                `json:"schemaVersion"`
	Operation     string             `json:"operation"`
	Sessions      []SessionReference `json:"sessions"`
}

type dispatchWireResponse struct {
	SchemaVersion int             `json:"schemaVersion"`
	Operation     string          `json:"operation"`
	Result        *DispatchResult `json:"result,omitempty"`
	Error         *Error          `json:"error,omitempty"`
}

type syncWireResponse struct {
	SchemaVersion int         `json:"schemaVersion"`
	Operation     string      `json:"operation"`
	Result        *SyncResult `json:"result,omitempty"`
	Error         *Error      `json:"error,omitempty"`
}

func (c *Client) Dispatch(ctx context.Context, request DispatchRequest) (DispatchResult, error) {
	wire := dispatchWireRequest{
		SchemaVersion: HarnessSchemaVersion, Operation: "dispatch", SessionID: request.SessionID,
		Mode: request.Mode, Prompt: request.Prompt, Model: request.Model, Effort: request.Effort,
		CreateIdempotencyKey: request.CreateIdempotencyKey, SendIdempotencyKey: request.SendIdempotencyKey,
		Metadata:     map[string]string{"session": request.SessionID, "ticket": request.TicketSlug, "project": request.Project},
		Repositories: request.Repositories,
	}
	var response dispatchWireResponse
	if err := c.call(ctx, wire, "dispatch", &response); err != nil {
		return DispatchResult{}, err
	}
	if response.Error != nil {
		return DispatchResult{}, response.Error
	}
	if response.Result == nil {
		return DispatchResult{}, &Error{Kind: "harness", Message: "Cursor harness returned no dispatch result"}
	}
	return *response.Result, nil
}

func (c *Client) Sync(ctx context.Context, request SyncRequest) (SyncResult, error) {
	wire := syncWireRequest{SchemaVersion: HarnessSchemaVersion, Operation: "sync", Sessions: request.Sessions}
	var response syncWireResponse
	if err := c.call(ctx, wire, "sync", &response); err != nil {
		return SyncResult{}, err
	}
	if response.Error != nil {
		return SyncResult{}, response.Error
	}
	if response.Result == nil {
		return SyncResult{}, &Error{Kind: "harness", Message: "Cursor harness returned no sync result"}
	}
	return *response.Result, nil
}

func (c *Client) call(ctx context.Context, request any, operation string, response any) error {
	data, err := json.Marshal(request)
	if err != nil {
		return fmt.Errorf("encode Cursor harness request: %w", err)
	}
	stdout, stderr, err := c.runner.Run(ctx, append(data, '\n'))
	if err != nil {
		message := "Cursor harness failed"
		if text := string(bytes.TrimSpace(stderr)); text != "" {
			message += ": " + text
		}
		return &Error{Kind: "harness", Message: message}
	}
	decoder := json.NewDecoder(bytes.NewReader(stdout))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(response); err != nil {
		return &Error{Kind: "harness", Message: fmt.Sprintf("decode Cursor harness response: %v", err)}
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return &Error{Kind: "harness", Message: "Cursor harness returned more than one JSON value"}
	}
	switch value := response.(type) {
	case *dispatchWireResponse:
		if value.SchemaVersion != HarnessSchemaVersion || value.Operation != operation {
			return &Error{Kind: "harness", Message: "Cursor harness returned the wrong protocol version or operation"}
		}
	case *syncWireResponse:
		if value.SchemaVersion != HarnessSchemaVersion || value.Operation != operation {
			return &Error{Kind: "harness", Message: "Cursor harness returned the wrong protocol version or operation"}
		}
	default:
		return &Error{Kind: "harness", Message: "Cursor harness response type is invalid"}
	}
	return nil
}
