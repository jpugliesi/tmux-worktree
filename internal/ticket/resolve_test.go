package ticket

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/jpugliesi/tmux-worktree/internal/clierr"
)

// resolverHome builds one Tickets home with tickets at both depths, a
// template, an index, and a broken file.
func resolverHome(t *testing.T) (*Service, string) {
	t.Helper()
	service, home := newTestService(t)
	writeFixture(t, filepath.Join(home, "index.md"), "# hub\n")
	writeFixture(t, filepath.Join(home, "templates", "ticket.md"), "template, not a ticket\n")
	writeFixture(t, filepath.Join(home, "auth.md"),
		fixture{title: "Auth core", status: "needs-triage"}.content())
	writeFixture(t, filepath.Join(home, "auth-refresh.md"),
		fixture{title: "Refresh the auth tokens", status: "needs-triage", aliases: []string{"Token Refresh"}}.content())
	writeFixture(t, filepath.Join(home, "auth-revoke.md"),
		fixture{title: "Revoke the auth tokens", status: "needs-triage"}.content())
	writeFixture(t, filepath.Join(home, "change-monitor", "index.md"), "# project hub\n")
	writeFixture(t, filepath.Join(home, "change-monitor", "vfs-tools.md"),
		fixture{title: "Reconnect VFS tools", status: "ready-for-agent"}.content())
	writeFixture(t, filepath.Join(home, "change-monitor", "deep", "too-deep.md"),
		fixture{title: "Too deep", status: "needs-triage"}.content())
	writeFixture(t, filepath.Join(home, "broken.md"), "---\ntitle: \"Broken\"\n")
	return service, home
}

func TestResolveTable(t *testing.T) {
	service, home := resolverHome(t)
	tests := []struct {
		name string
		ref  string
		want string
	}{
		{"exact slug beats prefix", "auth", "auth"},
		{"unique prefix", "auth-ref", "auth-refresh"},
		{"unique prefix in project", "vfs", "vfs-tools"},
		{"title case-insensitive", "reconnect vfs TOOLS", "vfs-tools"},
		{"alias case-insensitive", "token refresh", "auth-refresh"},
		{"wiki-link", "[[vfs-tools]]", "vfs-tools"},
		{"wiki-link with display", "[[auth-refresh|the refresh work]]", "auth-refresh"},
		{"relative path", "change-monitor/vfs-tools.md", "vfs-tools"},
		{"absolute path", filepath.Join(home, "auth.md"), "auth"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ticket, err := service.Resolve(test.ref)
			if err != nil {
				t.Fatalf("Resolve(%q): %v", test.ref, err)
			}
			if ticket.Slug != test.want {
				t.Fatalf("Resolve(%q) = %q, want %q", test.ref, ticket.Slug, test.want)
			}
		})
	}
}

func TestResolveAmbiguousPrefix(t *testing.T) {
	service, _ := resolverHome(t)
	_, err := service.Resolve("auth-re")
	if clierr.CodeOf(err) != clierr.InvalidUsage {
		t.Fatalf("ambiguous prefix = %v, want invalid_usage", err)
	}
	if !strings.Contains(err.Error(), `"auth-re" is ambiguous`) {
		t.Fatalf("message %q does not name the reference", err)
	}
	hint := clierr.HintOf(err)
	if !strings.Contains(hint, "auth-refresh") || !strings.Contains(hint, "auth-revoke") {
		t.Fatalf("hint %q does not list the sorted candidates", hint)
	}
	if strings.Index(hint, "auth-refresh") > strings.Index(hint, "auth-revoke") {
		t.Fatalf("hint %q is not sorted", hint)
	}
}

func TestResolveNotFound(t *testing.T) {
	service, _ := resolverHome(t)
	for _, ref := range []string{"missing", "[[missing]]", "nowhere/missing.md", "too-deep"} {
		_, err := service.Resolve(ref)
		if clierr.CodeOf(err) != clierr.NotFound {
			t.Fatalf("Resolve(%q) = %v, want not_found", ref, err)
		}
		if !strings.Contains(clierr.HintOf(err), "twt tickets list") {
			t.Fatalf("hint %q does not point at the list command", clierr.HintOf(err))
		}
	}
}

func TestResolvePathOutsideHome(t *testing.T) {
	service, _ := resolverHome(t)
	outside := filepath.Join(t.TempDir(), "auth.md")
	writeFixture(t, outside, fixture{title: "Impostor", status: "done"}.content())
	if _, err := service.Resolve(outside); clierr.CodeOf(err) != clierr.NotFound {
		t.Fatalf("path outside home = %v, want not_found", err)
	}
}

func TestResolveDuplicateSlug(t *testing.T) {
	service, home := resolverHome(t)
	writeFixture(t, filepath.Join(home, "change-monitor", "auth.md"),
		fixture{title: "Duplicate auth", status: "needs-triage"}.content())
	_, err := service.Resolve("auth")
	if clierr.CodeOf(err) != clierr.UnsafeState {
		t.Fatalf("duplicate slug = %v, want unsafe_state", err)
	}
	if !strings.Contains(err.Error(), filepath.Join(home, "auth.md")) ||
		!strings.Contains(err.Error(), filepath.Join(home, "change-monitor", "auth.md")) {
		t.Fatalf("error %q does not name both paths", err)
	}
}

func TestResolveIndexesClosedTicketLocations(t *testing.T) {
	service, home := newTestService(t)
	writeFixture(t, filepath.Join(home, closedDirectoryName, closedMarkerName), "twt closed tickets\n")
	writeFixture(t, filepath.Join(home, closedDirectoryName, "ungrouped.md"),
		fixture{title: "Closed ungrouped", status: "done"}.content())
	writeFixture(t, filepath.Join(home, closedDirectoryName, "core", "project-work.md"),
		fixture{title: "Closed Project work", status: "wontfix"}.content())
	writeFixture(t, filepath.Join(home, closedDirectoryName, "core", "deep", "too-deep.md"),
		fixture{title: "Too deep", status: "done"}.content())

	ungrouped, err := service.Resolve("ungrouped")
	if err != nil {
		t.Fatalf("Resolve closed ungrouped: %v", err)
	}
	if ungrouped.Project != "" || ungrouped.Path != filepath.Join(home, closedDirectoryName, "ungrouped.md") {
		t.Fatalf("closed ungrouped Ticket = %+v", ungrouped)
	}

	project, err := service.Resolve("project-work")
	if err != nil {
		t.Fatalf("Resolve closed Project Ticket: %v", err)
	}
	if project.Project != "core" || project.Path != filepath.Join(home, closedDirectoryName, "core", "project-work.md") {
		t.Fatalf("closed Project Ticket = %+v", project)
	}

	if _, err := service.Resolve("too-deep"); clierr.CodeOf(err) != clierr.NotFound {
		t.Fatalf("Resolve invalid nested closed Ticket = %v, want not_found", err)
	}

	all, err := service.List(ListFilter{All: true})
	if err != nil {
		t.Fatalf("List --all: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("List --all = %+v", all)
	}
}

func TestResolveSkippedFileReportsItsError(t *testing.T) {
	service, _ := resolverHome(t)
	_, err := service.Resolve("broken")
	if clierr.CodeOf(err) != clierr.UnsafeState {
		t.Fatalf("skipped file = %v, want unsafe_state", err)
	}
	if !strings.Contains(err.Error(), "unterminated frontmatter fence") {
		t.Fatalf("error %q is not the stored parse error", err)
	}
}

func TestListSkipsBrokenAndNonTicketFiles(t *testing.T) {
	service, _ := resolverHome(t)
	tickets, err := service.List(ListFilter{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	slugs := map[string]bool{}
	for _, ticket := range tickets {
		slugs[ticket.Slug] = true
	}
	for _, want := range []string{"auth", "auth-refresh", "auth-revoke", "vfs-tools"} {
		if !slugs[want] {
			t.Fatalf("List misses %q: %v", want, slugs)
		}
	}
	for _, unwanted := range []string{"broken", "index", "ticket", "too-deep"} {
		if slugs[unwanted] {
			t.Fatalf("List includes %q", unwanted)
		}
	}
}

func TestSlugsForCompletion(t *testing.T) {
	service, _ := resolverHome(t)
	slugs, err := service.Slugs()
	if err != nil {
		t.Fatalf("Slugs: %v", err)
	}
	want := []string{"auth", "auth-refresh", "auth-revoke", "broken", "vfs-tools"}
	if strings.Join(slugs, ",") != strings.Join(want, ",") {
		t.Fatalf("Slugs = %v, want %v", slugs, want)
	}

	empty := NewService(Options{Home: filepath.Join(t.TempDir(), "missing"), StateDir: t.TempDir()})
	slugs, err = empty.Slugs()
	if err != nil || len(slugs) != 0 {
		t.Fatalf("Slugs on a missing home = %v, %v; want empty and nil", slugs, err)
	}
}
