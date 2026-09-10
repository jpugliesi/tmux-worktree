package cli_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/jpugliesi/tmux-worktree/internal/clierr"
)

func TestProjectsPauseHidesFromDefaultListAndStaysWritable(t *testing.T) {
	options, _ := ticketTestOptions(t)
	executeWithOptions(t, options, nil, "tickets", "init")
	executeWithOptions(t, options, nil, "projects", "create", "learn")
	executeWithOptions(t, options, nil, "tickets", "create", "Open work", "--project", "learn")

	dry, _, err := executeCollectingInput(t, options, nil,
		"projects", "pause", "learn", "--dry-run", "--output", "json")
	if err != nil || !strings.Contains(dry, `"operation":"projects.pause"`) || !strings.Contains(dry, `"status":"valid"`) {
		t.Fatalf("projects pause dry run = %q, %v", dry, err)
	}

	applied, _, err := executeCollectingInput(t, options, nil,
		"projects", "pause", "learn", "--output", "json")
	if err != nil || !strings.Contains(applied, `"operation":"projects.pause"`) || !strings.Contains(applied, `"status":"applied"`) {
		t.Fatalf("projects pause = %q, %v", applied, err)
	}

	text := executeWithOptions(t, options, nil, "projects", "list")
	if strings.Contains(text, "learn") {
		t.Fatalf("default list includes a paused Project:\n%s", text)
	}
	allText := executeWithOptions(t, options, nil, "projects", "list", "--all")
	if got := strings.Join(projectTableRow(t, allText, "learn"), " "); got != "learn paused 1/1" {
		t.Fatalf("paused row = %q\n%s", got, allText)
	}

	if _, _, err := executeCollectingInput(t, options, nil,
		"tickets", "create", "More work", "--project", "learn"); err != nil {
		t.Fatalf("create in paused Project: %v", err)
	}
	hidden, _, err := executeCollectingInput(t, options, nil, "tickets", "list", "--output", "json")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(hidden, `"slug":"open-work"`) || strings.Contains(hidden, `"slug":"more-work"`) {
		t.Fatalf("unscoped tickets list includes paused Project Tickets: %s", hidden)
	}
	scoped := executeWithOptions(t, options, nil, "tickets", "list", "--project", "learn", "--output", "json")
	if !strings.Contains(scoped, `"slug":"open-work"`) || !strings.Contains(scoped, `"slug":"more-work"`) {
		t.Fatalf("scoped tickets list missed paused Project Tickets: %s", scoped)
	}
	widened := executeWithOptions(t, options, nil, "tickets", "list", "--all-projects", "--output", "json")
	if !strings.Contains(widened, `"slug":"more-work"`) {
		t.Fatalf("--all-projects missed paused Project Tickets: %s", widened)
	}

	resumed, _, err := executeCollectingInput(t, options, nil,
		"projects", "resume", "learn", "--output", "json")
	if err != nil || !strings.Contains(resumed, `"operation":"projects.resume"`) {
		t.Fatalf("projects resume = %q, %v", resumed, err)
	}
	after := executeWithOptions(t, options, nil, "projects", "list")
	if !strings.Contains(after, "learn") || !strings.Contains(after, "active") {
		t.Fatalf("list after resume = %s", after)
	}
}

func TestProjectsPauseApplyAndEmptyActiveList(t *testing.T) {
	options, _ := ticketTestOptions(t)
	executeWithOptions(t, options, nil, "tickets", "init")
	executeWithOptions(t, options, nil, "projects", "create", "learn")
	executeWithOptions(t, options, nil, "projects", "pause", "learn")

	_, stderr, err := executeCollectingInput(t, options, nil, "projects", "list")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stderr, "No active Projects") || !strings.Contains(stderr, "twt projects list --all") {
		t.Fatalf("empty active list = %q", stderr)
	}

	applied, _, err := executeCollectingInput(t, options,
		strings.NewReader(`{"operation":"projects.pause","project":{"name":"learn"}}`),
		"apply", "-", "--output", "json")
	if err != nil || !strings.Contains(applied, `"operation":"projects.pause"`) {
		t.Fatalf("apply projects.pause = %q, %v", applied, err)
	}

	executeWithOptions(t, options, nil, "projects", "close", "learn")
	_, _, err = executeCollectingInput(t, options, nil, "projects", "resume", "learn")
	if clierr.CodeOf(err) != clierr.PreconditionFailed {
		t.Fatalf("resume closed = %v", err)
	}
}

func TestProjectsListJSONReportsPaused(t *testing.T) {
	options, _ := ticketTestOptions(t)
	executeWithOptions(t, options, nil, "tickets", "init")
	executeWithOptions(t, options, nil, "projects", "create", "learn")
	executeWithOptions(t, options, nil, "projects", "pause", "learn")

	jsonOut := executeWithOptions(t, options, nil, "projects", "list", "--all", "--output", "json")
	var list struct {
		Projects []struct {
			Name   string `json:"name"`
			Paused bool   `json:"paused"`
			Closed bool   `json:"closed"`
			Status string `json:"status"`
		} `json:"projects"`
	}
	if err := json.Unmarshal([]byte(jsonOut), &list); err != nil {
		t.Fatalf("decode list: %v\n%s", err, jsonOut)
	}
	if len(list.Projects) != 1 || list.Projects[0].Name != "learn" ||
		!list.Projects[0].Paused || list.Projects[0].Closed || list.Projects[0].Status != "paused" {
		t.Fatalf("paused JSON = %s", jsonOut)
	}
}
