package cli

import (
	"testing"

	"github.com/jpugliesi/tmux-worktree/internal/domain"
)

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
