package cursorcloud

import (
	"context"
	"encoding/json"
	"testing"
)

type runnerFunc func(context.Context, []byte) ([]byte, []byte, error)

func (f runnerFunc) Run(ctx context.Context, request []byte) ([]byte, []byte, error) {
	return f(ctx, request)
}

func TestClientDispatchUsesVersionedProtocol(t *testing.T) {
	runner := runnerFunc(func(_ context.Context, request []byte) ([]byte, []byte, error) {
		var wire struct {
			SchemaVersion int               `json:"schemaVersion"`
			Operation     string            `json:"operation"`
			SessionID     string            `json:"sessionId"`
			Mode          string            `json:"mode"`
			Metadata      map[string]string `json:"metadata"`
		}
		if err := json.Unmarshal(request, &wire); err != nil {
			t.Fatal(err)
		}
		if wire.SchemaVersion != HarnessSchemaVersion || wire.Operation != "dispatch" || wire.SessionID != "session-one" || wire.Mode != "agent" {
			t.Fatalf("request = %+v", wire)
		}
		if wire.Metadata["ticket"] != "fix-auth" {
			t.Fatalf("metadata = %+v", wire.Metadata)
		}
		return []byte(`{"schemaVersion":1,"operation":"dispatch","result":{"agentId":"bc-agent","runId":"run-one","requestId":"request-one","effort":{"kind":"prompt","value":"large"}}}`), nil, nil
	})
	client := NewClient(runner)
	result, err := client.Dispatch(context.Background(), DispatchRequest{
		SessionID: "session-one", TicketSlug: "fix-auth", Project: "core", Mode: "agent",
		Prompt: "Implement the Ticket.", Effort: "large",
		CreateIdempotencyKey: "create-one", SendIdempotencyKey: "send-one",
		Repositories: []Repository{{Name: "api", URL: "https://github.com/acme/api.git", StartingRef: "main"}},
	})
	if err != nil {
		t.Fatalf("Dispatch() error = %v", err)
	}
	if result.AgentID != "bc-agent" || result.RunID != "run-one" || result.Effort.Kind != "prompt" {
		t.Fatalf("Dispatch() = %+v", result)
	}
}

func TestClientSyncDecodesTerminalGitResult(t *testing.T) {
	runner := runnerFunc(func(_ context.Context, _ []byte) ([]byte, []byte, error) {
		return []byte(`{"schemaVersion":1,"operation":"sync","result":{"sessions":[{"sessionId":"session-one","status":"finished","result":"Implemented.","repositories":[{"url":"https://github.com/acme/api.git","branch":"cursor/fix-auth","prUrl":"https://github.com/acme/api/pull/12"}]}]}}`), nil, nil
	})
	result, err := NewClient(runner).Sync(context.Background(), SyncRequest{Sessions: []SessionReference{{SessionID: "session-one", AgentID: "bc-agent", RunID: "run-one"}}})
	if err != nil {
		t.Fatalf("Sync() error = %v", err)
	}
	if len(result.Sessions) != 1 || result.Sessions[0].Status != "finished" || result.Sessions[0].Repositories[0].PRURL == "" {
		t.Fatalf("Sync() = %+v", result)
	}
}

func TestClientReturnsStructuredHarnessError(t *testing.T) {
	runner := runnerFunc(func(_ context.Context, _ []byte) ([]byte, []byte, error) {
		return []byte(`{"schemaVersion":1,"operation":"dispatch","error":{"kind":"configuration","code":"integration_not_connected","message":"GitHub is not connected","retryable":false,"helpUrl":"https://cursor.com/setup"}}`), nil, nil
	})
	_, err := NewClient(runner).Dispatch(context.Background(), DispatchRequest{SessionID: "session-one"})
	cloudErr, ok := err.(*Error)
	if !ok || cloudErr.Code != "integration_not_connected" || cloudErr.Uncertain() {
		t.Fatalf("Dispatch() error = %#v", err)
	}
}
