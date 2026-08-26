package ticket_test

import (
	"testing"

	ticketservice "github.com/jpugliesi/tmux-worktree/internal/ticket"
	"github.com/jpugliesi/tmux-worktree/internal/ticket/conformance"
)

// TestMarkdownStoreConformance runs the backend contract suite against the
// reference markdown-plus-git implementation (sync disabled: the contract
// covers single-store semantics; the cross-clone CAS lives in sync_test.go).
func TestMarkdownStoreConformance(t *testing.T) {
	conformance.Run(t, "markdown", func(t *testing.T) ticketservice.Store {
		service := ticketservice.NewService(ticketservice.Options{
			Home:     t.TempDir(),
			StateDir: t.TempDir(),
		})
		if _, err := service.Init(false); err != nil {
			t.Fatalf("init: %v", err)
		}
		return service
	})
}
