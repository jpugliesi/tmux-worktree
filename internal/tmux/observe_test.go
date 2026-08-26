package tmux

import (
	"io"
	"strings"
	"testing"

	"github.com/jpugliesi/tmux-worktree/internal/domain"
)

func TestObserveWorkspaceFindsOnlyForegroundDescendantsOfEachPane(t *testing.T) {
	var paneQueries, processQueries int
	client := Client{
		run: func(_ io.Reader, args ...string) (string, error) {
			switch args[0] {
			case "list-sessions":
				return "$1\tworkspace-one\t", nil
			case "list-panes":
				paneQueries++
				return "%1\x1f100\x1f/dev/ttys001\x1f0\x1fnode\x1fzsh\x1f/work/app\x1f\x1f\n%2\x1f400\x1f/dev/ttys002\x1f0\x1fzsh\x1fzsh\x1f/work/app\x1fagent-existing\x1f", nil
			}
			return "", nil
		},
		runProcesses: func() (string, error) {
			processQueries++
			return strings.Join([]string{
				"100 1 100 200 ttys001 Mon Aug 24 10:00:00 2026 zsh -zsh",
				"150 100 200 200 ttys001 Mon Aug 24 10:00:01 2026 host /tmp/host",
				"200 150 200 200 ttys001 Mon Aug 24 10:00:02 2026 node /home/me/.local/bin/agent --use-system-ca /home/me/.local/share/cursor-agent/versions/test/index.js",
				"300 100 300 200 ttys001 Mon Aug 24 10:00:03 2026 codex codex --yolo",
				"400 1 400 400 ttys002 Mon Aug 24 10:01:00 2026 zsh -zsh",
				"malformed process row",
			}, "\n"), nil
		},
	}

	panes, err := client.ObserveWorkspace(domain.Workspace{ID: "workspace-one", Name: "one"})
	if err != nil {
		t.Fatal(err)
	}
	if paneQueries != 1 || processQueries != 1 {
		t.Fatalf("queries = %d pane and %d process, want one each", paneQueries, processQueries)
	}
	if len(panes) != 2 {
		t.Fatalf("ObserveWorkspace() = %+v", panes)
	}
	first := panes[0]
	if first.ID != "%1" || first.AgentID != "" || first.CurrentPath != "/work/app" {
		t.Fatalf("first pane = %+v", first)
	}
	if len(first.Foreground) != 2 || first.Foreground[0].ID != 150 || first.Foreground[1].ID != 200 {
		t.Fatalf("foreground processes = %+v", first.Foreground)
	}
	for _, process := range first.Foreground {
		if process.ID == 300 {
			t.Fatalf("background process became foreground: %+v", first.Foreground)
		}
	}
	if panes[1].AgentID != "agent-existing" || len(panes[1].Foreground) != 1 || panes[1].Foreground[0].ID != 400 {
		t.Fatalf("second pane = %+v", panes[1])
	}
}

func TestParseProcessesKeepsStableIdentityAndArguments(t *testing.T) {
	rows := "200 150 200 200 ttys001 Mon Aug 24 10:00:02 2026 node /tmp/bin/agent --flag value with spaces\n"
	processes := parseProcesses(rows)
	if len(processes) != 1 {
		t.Fatalf("parseProcesses() = %+v", processes)
	}
	process := processes[0]
	if process.ID != 200 || process.ParentID != 150 || process.GroupID != 200 || process.ForegroundGroupID != 200 {
		t.Fatalf("process IDs = %+v", process)
	}
	if process.Started != "Mon Aug 24 10:00:02 2026" || process.Command != "node" {
		t.Fatalf("process identity = %+v", process)
	}
	if got := strings.Join(process.Args, "|"); got != "/tmp/bin/agent|--flag|value|with|spaces" {
		t.Fatalf("process arguments = %q", got)
	}
}

func TestParsePanesSurvivesMultiLineStartCommands(t *testing.T) {
	rows := "%1\x1f100\x1f/dev/ttys001\x1f0\x1fcursor-agent\x1fcursor-agent --force Line one.\nLine two.\nLine three.\x1f/work/app\x1f\x1f\n" +
		"%2\x1f400\x1f/dev/ttys002\x1f0\x1fzsh\x1fzsh\x1f/work/app\x1fagent-existing\x1f"
	panes := parsePanes(rows)
	if len(panes) != 2 {
		t.Fatalf("parsePanes() = %+v, want 2 panes", panes)
	}
	if panes[0].ID != "%1" || panes[0].CurrentCommand != "cursor-agent" {
		t.Fatalf("first pane = %+v", panes[0])
	}
	if !strings.Contains(panes[0].StartCommand, "Line two.") {
		t.Fatalf("multi-line start command lost: %q", panes[0].StartCommand)
	}
	if panes[1].ID != "%2" || panes[1].AgentID != "agent-existing" {
		t.Fatalf("second pane = %+v", panes[1])
	}
}
