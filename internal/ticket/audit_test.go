package ticket

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jpugliesi/tmux-worktree/internal/clierr"
	"github.com/jpugliesi/tmux-worktree/internal/store"
)

func TestDoctorReportsLocationMismatchesWithoutWriting(t *testing.T) {
	service, home := newTestService(t)
	if _, err := service.CreateProject("core", false); err != nil {
		t.Fatal(err)
	}
	if err := ensureClosedRoot(home, false); err != nil {
		t.Fatal(err)
	}
	activeDone := filepath.Join(home, "core", "shipped.md")
	closedOpen := filepath.Join(home, closedDirectoryName, "core", "reopened.md")
	writeFixture(t, activeDone, fixture{title: "Shipped", status: "done"}.content())
	writeFixture(t, closedOpen, fixture{title: "Reopened", status: "ready-for-agent"}.content())
	activeBefore, closedBefore := readFile(t, activeDone), readFile(t, closedOpen)

	report, err := service.Doctor()
	if err != nil {
		t.Fatalf("Doctor: %v", err)
	}
	if report.Healthy || report.TicketCount != 2 || len(report.Issues) != 2 {
		t.Fatalf("Doctor report = %+v", report)
	}
	want := map[string]string{
		activeDone: filepath.Join(home, closedDirectoryName, "core", "shipped.md"),
		closedOpen: filepath.Join(home, "core", "reopened.md"),
	}
	for _, issue := range report.Issues {
		if issue.Code != issueLocationMismatch || !issue.Repairable {
			t.Fatalf("Doctor issue = %+v", issue)
		}
		if want[issue.Path] != issue.Destination {
			t.Fatalf("Doctor issue destination = %+v, want %q", issue, want[issue.Path])
		}
	}
	if readFile(t, activeDone) != activeBefore || readFile(t, closedOpen) != closedBefore {
		t.Fatal("Doctor changed a Ticket file")
	}
}

func TestRepairMovesLocationMismatchesAndIsIdempotent(t *testing.T) {
	service, home := newTestService(t)
	if _, err := service.CreateProject("core", false); err != nil {
		t.Fatal(err)
	}
	if err := ensureClosedRoot(home, false); err != nil {
		t.Fatal(err)
	}
	activeDone := filepath.Join(home, "core", "shipped.md")
	closedOpen := filepath.Join(home, closedDirectoryName, "core", "reopened.md")
	writeFixture(t, activeDone, fixture{title: "Shipped", status: "done"}.content())
	writeFixture(t, closedOpen, fixture{title: "Reopened", status: "ready-for-agent"}.content())
	activeBefore, closedBefore := readFile(t, activeDone), readFile(t, closedOpen)

	dry, err := service.Repair(true)
	if err != nil {
		t.Fatalf("Repair dry run: %v", err)
	}
	if dry.Applied || dry.MovedCount != 0 || len(dry.Plan.Moves) != 2 || len(dry.Plan.Blockers) != 0 {
		t.Fatalf("Repair dry-run result = %+v", dry)
	}
	if readFile(t, activeDone) != activeBefore || readFile(t, closedOpen) != closedBefore {
		t.Fatal("Repair dry run changed a Ticket file")
	}

	applied, err := service.Repair(false)
	if err != nil {
		t.Fatalf("Repair: %v", err)
	}
	if !applied.Applied || applied.MovedCount != 2 {
		t.Fatalf("Repair result = %+v", applied)
	}
	closedDone := filepath.Join(home, closedDirectoryName, "core", "shipped.md")
	activeOpen := filepath.Join(home, "core", "reopened.md")
	if readFile(t, closedDone) != activeBefore || readFile(t, activeOpen) != closedBefore {
		t.Fatal("Repair did not preserve Ticket bytes")
	}

	again, err := service.Repair(false)
	if err != nil {
		t.Fatalf("second Repair: %v", err)
	}
	if !again.Applied || again.MovedCount != 0 || len(again.Plan.Moves) != 0 {
		t.Fatalf("second Repair = %+v, want a no-op", again)
	}
}

func TestRepairAppliesNothingWhenTheAuditHasABlocker(t *testing.T) {
	service, home := newTestService(t)
	if err := ensureClosedRoot(home, false); err != nil {
		t.Fatal(err)
	}
	active := filepath.Join(home, "work.md")
	closed := filepath.Join(home, closedDirectoryName, "work.md")
	writeFixture(t, active, fixture{title: "Active", status: "done"}.content())
	writeFixture(t, closed, fixture{title: "Closed", status: "done"}.content())
	activeBefore, closedBefore := readFile(t, active), readFile(t, closed)

	result, err := service.Repair(false)
	if clierr.CodeOf(err) != clierr.PreconditionFailed {
		t.Fatalf("Repair with blockers = %v, want precondition_failed", err)
	}
	if result.Applied || len(result.Plan.Blockers) == 0 {
		t.Fatalf("blocked Repair result = %+v", result)
	}
	if readFile(t, active) != activeBefore || readFile(t, closed) != closedBefore {
		t.Fatal("blocked Repair changed a Ticket file")
	}
}

func TestDoctorReportsInvalidFilesAndClosedDirectoryConflicts(t *testing.T) {
	service, home := newTestService(t)
	writeFixture(t, filepath.Join(home, closedDirectoryName, "index.md"), "# Existing Project\n")
	writeFixture(t, filepath.Join(home, "broken.md"), "---\ntitle: Broken\n")
	writeFixture(t, filepath.Join(home, "core", "deep", "nested.md"), fixture{title: "Nested", status: "done"}.content())

	report, err := service.Doctor()
	if err != nil {
		t.Fatalf("Doctor: %v", err)
	}
	codes := []string{}
	for _, issue := range report.Issues {
		codes = append(codes, issue.Code)
	}
	joined := strings.Join(codes, ",")
	for _, want := range []string{issueClosedRootConflict, issueInvalidLocation, issueParseFailure} {
		if !strings.Contains(joined, want) {
			t.Fatalf("Doctor issue codes = %v, want %q", codes, want)
		}
	}
	if _, err := os.Stat(filepath.Join(home, closedDirectoryName, closedMarkerName)); !os.IsNotExist(err) {
		t.Fatal("Doctor added a marker to the conflicting closed directory")
	}
}

func TestRepairBlocksReopenWhenTheActiveProjectIsMissing(t *testing.T) {
	service, home := newTestService(t)
	if err := ensureClosedRoot(home, false); err != nil {
		t.Fatal(err)
	}
	writeFixture(t, filepath.Join(home, closedDirectoryName, "missing", "work.md"),
		fixture{title: "Work", status: "ready-for-agent"}.content())

	report, err := service.Doctor()
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Issues) != 1 || report.Issues[0].Code != issueMissingProject || report.Issues[0].Repairable {
		t.Fatalf("Doctor report = %+v", report)
	}
	result, err := service.Repair(false)
	if clierr.CodeOf(err) != clierr.PreconditionFailed || len(result.Plan.Blockers) != 1 {
		t.Fatalf("Repair missing Project = %+v, %v", result, err)
	}
}

func TestRepairUsesTheTicketSlugLock(t *testing.T) {
	service, home := newTestService(t)
	path := filepath.Join(home, "work.md")
	writeFixture(t, path, fixture{title: "Work", status: "done"}.content())
	lock, err := store.AcquireNamedLock(service.options.StateDir, "ticket", "work")
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Release()

	result, err := service.Repair(false)
	if clierr.CodeOf(err) != clierr.Locked {
		t.Fatalf("Repair while the Ticket lock is held = %+v, %v; want locked", result, err)
	}
	if _, statErr := os.Stat(path); statErr != nil {
		t.Fatalf("locked Repair moved the Ticket: %v", statErr)
	}
}

func TestDoctorReportsASymbolicLinkProjectDirectory(t *testing.T) {
	service, home := newTestService(t)
	if err := ensureClosedRoot(home, false); err != nil {
		t.Fatal(err)
	}
	external := t.TempDir()
	path := filepath.Join(home, closedDirectoryName, "core")
	if err := os.Symlink(external, path); err != nil {
		t.Skipf("create symlink: %v", err)
	}

	report, err := service.Doctor()
	if err != nil {
		t.Fatal(err)
	}
	if report.Healthy || len(report.Issues) != 1 || report.Issues[0].Code != issueInvalidLocation || report.Issues[0].Path != path {
		t.Fatalf("Doctor report = %+v", report)
	}
}
