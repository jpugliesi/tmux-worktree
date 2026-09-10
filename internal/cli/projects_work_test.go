package cli

import (
	"testing"

	"github.com/jpugliesi/tmux-worktree/internal/domain"
)

func TestProjectListRowsSortsActiveThenPausedThenClosed(t *testing.T) {
	rows := projectListRows([]domain.Project{
		{Name: "zebra", Closed: true},
		{Name: "beta", Paused: true},
		{Name: "alpha"},
		{Name: "gamma", Paused: true},
		{Name: "closed-a", Closed: true},
	}, nil, nil)
	got := make([]string, 0, len(rows))
	for _, row := range rows {
		got = append(got, row.Status+":"+row.Name)
	}
	want := []string{"active:alpha", "paused:beta", "paused:gamma", "closed:closed-a", "closed:zebra"}
	if len(got) != len(want) {
		t.Fatalf("rows = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("rows = %v, want %v", got, want)
		}
	}
}

func TestProjectListWorkUsesProgressPlusTodoOverOpen(t *testing.T) {
	row := projectListRow{
		Project:  domain.Project{Tickets: 33},
		Progress: 1,
		Todo:     2,
		Open:     10,
		Done:     23,
	}
	if got := projectListWork(row); got != "3/10" {
		t.Fatalf("WORK = %q, want 3/10", got)
	}
}
