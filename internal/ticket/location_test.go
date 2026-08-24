package ticket

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/jpugliesi/tmux-worktree/internal/clierr"
	"github.com/jpugliesi/tmux-worktree/internal/domain"
)

func TestClassifyTicketPath(t *testing.T) {
	home := filepath.Join(t.TempDir(), "tickets")
	tests := []struct {
		name    string
		path    string
		want    ticketLocation
		wantErr bool
	}{
		{"active ungrouped", filepath.Join(home, "work.md"), ticketLocation{}, false},
		{"active Project", filepath.Join(home, "core", "work.md"), ticketLocation{Project: "core"}, false},
		{"closed ungrouped", filepath.Join(home, closedDirectoryName, "work.md"), ticketLocation{Closed: true}, false},
		{"closed Project", filepath.Join(home, closedDirectoryName, "core", "work.md"), ticketLocation{Closed: true, Project: "core"}, false},
		{"nested active", filepath.Join(home, "core", "nested", "work.md"), ticketLocation{}, true},
		{"nested closed", filepath.Join(home, closedDirectoryName, "core", "nested", "work.md"), ticketLocation{}, true},
		{"outside home", filepath.Join(t.TempDir(), "work.md"), ticketLocation{}, true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := classifyTicketPath(home, test.path)
			if test.wantErr {
				if err == nil {
					t.Fatalf("classifyTicketPath(%q) = %+v, want an error", test.path, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("classifyTicketPath(%q): %v", test.path, err)
			}
			if got != test.want {
				t.Fatalf("classifyTicketPath(%q) = %+v, want %+v", test.path, got, test.want)
			}
		})
	}
}

func TestCanonicalTicketPath(t *testing.T) {
	home := filepath.Join(t.TempDir(), "tickets")
	tests := []struct {
		name    string
		status  domain.TicketStatus
		project string
		want    string
	}{
		{"active ungrouped", domain.TicketNeedsTriage, "", filepath.Join(home, "work.md")},
		{"active Project", domain.TicketReadyForAgent, "core", filepath.Join(home, "core", "work.md")},
		{"done ungrouped", domain.TicketDone, "", filepath.Join(home, closedDirectoryName, "work.md")},
		{"done Project", domain.TicketDone, "core", filepath.Join(home, closedDirectoryName, "core", "work.md")},
		{"wontfix Project", domain.TicketWontfix, "core", filepath.Join(home, closedDirectoryName, "core", "work.md")},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := canonicalTicketPath(home, test.status, test.project, "work"); got != test.want {
				t.Fatalf("canonicalTicketPath() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestClosedProjectNameIsReservedWithoutCaseSensitivity(t *testing.T) {
	for _, name := range []string{"closed", "Closed", "CLOSED"} {
		if !reservedProjectName(name) {
			t.Fatalf("reservedProjectName(%q) = false", name)
		}
	}
	if reservedProjectName("close") {
		t.Fatal("reservedProjectName(close) = true")
	}
}

func TestClosedRootRejectsAConflictingMarker(t *testing.T) {
	home := t.TempDir()
	root := filepath.Join(home, closedDirectoryName)
	if err := os.Mkdir(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, closedMarkerName), []byte("owned by something else\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	exists, err := closedRootExists(home)
	if exists || clierr.CodeOf(err) != clierr.UnsafeState {
		t.Fatalf("closedRootExists() = %v, %v; want an unsafe-state conflict", exists, err)
	}
}
