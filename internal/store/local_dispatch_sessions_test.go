package store

import (
	"testing"
	"time"

	"github.com/jpugliesi/tmux-worktree/internal/clierr"
	"github.com/jpugliesi/tmux-worktree/internal/domain"
)

func localDispatchTestSession(id string) domain.LocalDispatchSession {
	created := time.Date(2026, 8, 25, 12, 0, 0, 0, time.UTC)
	return domain.LocalDispatchSession{
		Version:        domain.LocalDispatchSessionVersion,
		ID:             id,
		TicketSlug:     "fix-auth",
		Project:        "core",
		TemplateName:   "product",
		Mode:           domain.CursorCloudModeAgent,
		Provider:       "grok",
		Status:         domain.LocalDispatchCreating,
		Claimant:       "twt-local-" + id[:8],
		PromptSnapshot: "Implement the Ticket.",
		CreatedAt:      created,
		UpdatedAt:      created,
	}
}

func TestLocalDispatchSessionStoreRoundTrip(t *testing.T) {
	store := NewLocalDispatchSessionStore(t.TempDir())
	session := localDispatchTestSession("0123456789abcdef0123456789abcdef")
	if err := store.Save(session); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	loaded, err := store.Find("01234567")
	if err != nil {
		t.Fatalf("Find() error = %v", err)
	}
	if loaded.ID != session.ID || loaded.Claimant != session.Claimant || loaded.Status != domain.LocalDispatchCreating {
		t.Fatalf("Find() = %+v", loaded)
	}
	listed, err := store.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(listed) != 1 {
		t.Fatalf("List() count = %d, want 1", len(listed))
	}
	if err := store.Delete(session.ID); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	if _, err := store.Find(session.ID); clierr.CodeOf(err) != clierr.NotFound {
		t.Fatalf("Find() after Delete error = %v, want not_found", err)
	}
}

func TestLocalDispatchSessionStoreRejectsAmbiguousPrefixes(t *testing.T) {
	store := NewLocalDispatchSessionStore(t.TempDir())
	if err := store.Save(localDispatchTestSession("aaaa0000000000000000000000000001")); err != nil {
		t.Fatal(err)
	}
	if err := store.Save(localDispatchTestSession("aaaa0000000000000000000000000002")); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Find("aaaa"); clierr.CodeOf(err) != clierr.InvalidUsage {
		t.Fatalf("ambiguous Find() error = %v, want invalid_usage", err)
	}
}

func TestLocalDispatchSessionStoreRejectsInvalidSessions(t *testing.T) {
	store := NewLocalDispatchSessionStore(t.TempDir())
	session := localDispatchTestSession("0123456789abcdef0123456789abcdef")
	session.Status = "paused"
	if err := store.Save(session); err == nil {
		t.Fatal("Save() accepted an invalid status")
	}
}
