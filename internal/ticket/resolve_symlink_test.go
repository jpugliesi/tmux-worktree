package ticket

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/jpugliesi/tmux-worktree/internal/domain"
)

// TestTicketsReadThroughASymlinkedHome: a home that is itself a symlink (a
// vault alias, or a migration compatibility link) must read and resolve
// exactly like the real directory, with observable paths under the symlink.
func TestTicketsReadThroughASymlinkedHome(t *testing.T) {
	real := filepath.Join(t.TempDir(), "real-home")
	service := NewService(Options{Home: real, StateDir: t.TempDir()})
	if _, err := service.Init(false); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Create(CreateRequest{Title: "Linked", Slug: "linked", Status: domain.TicketReadyForAgent}, false); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "linked-home")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	linked := NewService(Options{Home: link, StateDir: t.TempDir()})
	tickets, err := linked.List(ListFilter{All: true})
	if err != nil {
		t.Fatalf("List through symlink: %v", err)
	}
	if len(tickets) != 1 || tickets[0].Slug != "linked" {
		t.Fatalf("List through symlink = %+v", tickets)
	}
	if tickets[0].Path != filepath.Join(link, "linked.md") {
		t.Fatalf("observable path = %q, want it under the symlink", tickets[0].Path)
	}
	if _, err := linked.Resolve("linked"); err != nil {
		t.Fatalf("Resolve through symlink: %v", err)
	}
}
