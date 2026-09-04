package cli_test

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestProjectsListShowsStatusAndTicketBreakdown(t *testing.T) {
	t.Setenv("TWT_CLAIMANT", "")
	options, _ := ticketTestOptions(t)
	run := func(stdin string, args ...string) {
		t.Helper()
		var input *strings.Reader
		if stdin != "" {
			input = strings.NewReader(stdin)
		}
		if out, errOut, err := executeCollectingInput(t, options, input, args...); err != nil {
			t.Fatalf("%v: %v\n%s%s", args, err, out, errOut)
		}
	}
	run("", "tickets", "init")
	run("", "projects", "create", "core")
	run("", "projects", "create", "empty")
	run("", "projects", "create", "leftover")
	run("", "tickets", "create", "Waiting task", "--slug", "waiting-task", "--project", "core", "--status", "ready-for-agent")
	run("", "tickets", "claim", "waiting-task", "--as", "agent-a")
	run("Which schema version?", "tickets", "ask", "waiting-task", "--as", "agent-a", "-")
	run("", "tickets", "create", "Progress task", "--slug", "progress-task", "--project", "core", "--status", "ready-for-agent")
	run("", "tickets", "claim", "progress-task", "--as", "agent-b")
	run("", "tickets", "create", "Review task", "--slug", "review-task", "--project", "core", "--status", "ready-for-human")
	run("", "tickets", "pr", "add", "review-task", "--pr", "https://github.com/acme/app/pull/1")
	run("", "tickets", "create", "Ready task", "--slug", "ready-task", "--project", "core", "--status", "ready-for-agent")
	run("", "tickets", "create", "Blocked task", "--slug", "blocked-task", "--project", "core",
		"--status", "ready-for-agent", "--blocked-by", "ready-task")
	run("", "tickets", "create", "Triage task", "--slug", "triage-task", "--project", "core")
	run("", "tickets", "create", "Done task", "--slug", "done-task", "--project", "core", "--status", "done")
	run("", "tickets", "create", "Old work", "--slug", "old-work", "--project", "leftover")
	run("", "projects", "close", "leftover", "--force")

	text, _, err := executeCollectingInput(t, options, nil, "projects", "list")
	if err != nil {
		t.Fatal(err)
	}
	for _, header := range []string{"NAME", "STATUS", "TICKETS", "WAITING", "PROGRESS", "REVIEW", "READY", "BLOCKED", "TODO", "DONE"} {
		if !strings.Contains(text, header) {
			t.Fatalf("text list missing %s:\n%s", header, text)
		}
	}
	if strings.Contains(text, "leftover") {
		t.Fatalf("default list includes a closed Project:\n%s", text)
	}
	if got := strings.Join(projectTableRow(t, text, "core"), " "); got != "core active 7 1 1 1 1 1 1 1" {
		t.Fatalf("core row = %q\n%s", got, text)
	}
	if got := strings.Join(projectTableRow(t, text, "empty"), " "); got != "empty active 0 0 0 0 0 0 0 0" {
		t.Fatalf("empty row = %q\n%s", got, text)
	}

	jsonOut, _, err := executeCollectingInput(t, options, nil, "projects", "list", "--output", "json")
	if err != nil {
		t.Fatal(err)
	}
	var list struct {
		Projects []struct {
			Name     string `json:"name"`
			Closed   bool   `json:"closed"`
			Status   string `json:"status"`
			Tickets  int    `json:"tickets"`
			Waiting  int    `json:"waiting"`
			Progress int    `json:"progress"`
			Review   int    `json:"review"`
			Ready    int    `json:"ready"`
			Blocked  int    `json:"blocked"`
			Todo     int    `json:"todo"`
			Done     int    `json:"done"`
		} `json:"projects"`
	}
	if err := json.Unmarshal([]byte(jsonOut), &list); err != nil {
		t.Fatalf("decode list: %v\n%s", err, jsonOut)
	}
	if len(list.Projects) != 2 {
		t.Fatalf("default JSON list = %s", jsonOut)
	}
	core := list.Projects[0]
	if core.Name != "core" || core.Closed || core.Status != "active" ||
		core.Tickets != 7 || core.Waiting != 1 || core.Progress != 1 || core.Review != 1 ||
		core.Ready != 1 || core.Blocked != 1 || core.Todo != 1 || core.Done != 1 {
		t.Fatalf("core JSON = %+v\n%s", core, jsonOut)
	}

	allText, _, err := executeCollectingInput(t, options, nil, "projects", "list", "--all")
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(projectTableRow(t, allText, "leftover"), " "); got != "leftover closed 1 0 0 0 0 0 0 1" {
		t.Fatalf("closed row = %q\n%s", got, allText)
	}
}

func projectTableRow(t *testing.T, output, name string) []string {
	t.Helper()
	for _, line := range strings.Split(strings.TrimRight(output, "\n"), "\n") {
		fields := strings.Fields(line)
		if len(fields) > 0 && fields[0] == name {
			return fields
		}
	}
	t.Fatalf("no row for %s:\n%s", name, output)
	return nil
}
