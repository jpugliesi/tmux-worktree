package workspace

import (
	"testing"
)

// A tmux restore removes the session owner option. Rename still finds the
// session through its saved name, marks the owner again, and renames it.
func TestRenameRenamesARestoredUnownedSession(t *testing.T) {
	service, template, socket := busyTestService(t)
	workspace, err := service.Create("outbox", template.Name, template)
	if err != nil {
		t.Fatal(err)
	}
	if err := run("", "tmux", "-L", socket, "-f", "/dev/null", "set-option", "-t", workspace.TmuxSession, workspaceTmuxOption, ""); err != nil {
		t.Fatal(err)
	}

	renamed, err := service.Rename(workspace.ID, "otel")
	if err != nil {
		t.Fatal(err)
	}
	assertOwnedSessionName(t, service, renamed.ID, "example-otel")
}

// A restore after an earlier rename can leave an old session name. Rename
// adopts the unowned session when every pane in it works in the Workspace.
func TestRenameRenamesAnUnownedSessionThatWorksInTheWorkspace(t *testing.T) {
	service, template, socket := busyTestService(t)
	workspace, err := service.Create("outbox", template.Name, template)
	if err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"set-option", "-t", workspace.TmuxSession, workspaceTmuxOption, ""},
		{"rename-session", "-t", "=" + workspace.TmuxSession, "example-older"},
	} {
		if err := run("", "tmux", append([]string{"-L", socket, "-f", "/dev/null"}, args...)...); err != nil {
			t.Fatal(err)
		}
	}
	waitForPane(t, service, workspace.Root)

	renamed, err := service.Rename(workspace.ID, "otel")
	if err != nil {
		t.Fatal(err)
	}
	assertOwnedSessionName(t, service, renamed.ID, "example-otel")
}

func assertOwnedSessionName(t *testing.T, service *Service, workspaceID, want string) {
	t.Helper()
	row, ok, err := service.ownedSessionRow(workspaceID)
	if err != nil || !ok || row.name != want {
		t.Fatalf("owned session = %+v, %v, %v, want name %q", row, ok, err, want)
	}
}
