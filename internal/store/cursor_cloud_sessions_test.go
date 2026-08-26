package store

import (
	"testing"
	"time"

	"github.com/jpugliesi/tmux-worktree/internal/domain"
)

func TestCursorCloudSessionStoreRoundTrip(t *testing.T) {
	store := NewCursorCloudSessionStore(t.TempDir())
	created := time.Date(2026, 8, 25, 12, 0, 0, 0, time.UTC)
	session := domain.CursorCloudSession{
		Version:              domain.CursorCloudSessionVersion,
		ID:                   "0123456789abcdef0123456789abcdef",
		TicketSlug:           "fix-auth",
		Project:              "core",
		TemplateName:         "product",
		TemplateSnapshot:     domain.Template{Version: domain.TemplateVersion, Name: "product"},
		Mode:                 domain.CursorCloudModeAgent,
		Status:               domain.CursorCloudCreating,
		Claimant:             "cursor-cloud-01234567",
		PromptSnapshot:       "Implement the Ticket.",
		CreateIdempotencyKey: "twt-create-0123456789abcdef0123456789abcdef",
		SendIdempotencyKey:   "twt-send-0123456789abcdef0123456789abcdef",
		CreatedAt:            created,
		UpdatedAt:            created,
	}
	if err := store.Save(session); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	loaded, err := store.Find("01234567")
	if err != nil {
		t.Fatalf("Find() error = %v", err)
	}
	if loaded.ID != session.ID || loaded.PromptSnapshot != session.PromptSnapshot || loaded.Status != domain.CursorCloudCreating {
		t.Fatalf("Find() = %+v", loaded)
	}
	listed, err := store.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(listed) != 1 || listed[0].TicketSlug != "fix-auth" {
		t.Fatalf("List() = %+v", listed)
	}
}

func TestCursorCloudSessionStoreRejectsInvalidState(t *testing.T) {
	store := NewCursorCloudSessionStore(t.TempDir())
	session := domain.CursorCloudSession{Version: 99, ID: "session", TicketSlug: "fix-auth"}
	if err := store.Save(session); err == nil {
		t.Fatal("Save() accepted unsupported state")
	}
}
