package cli_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jpugliesi/tmux-worktree/internal/cli"
	"github.com/jpugliesi/tmux-worktree/internal/clierr"
)

func TestTicketsCreateAndSetLabels(t *testing.T) {
	options, home := ticketTestOptions(t)
	if _, _, err := executeCollectingInput(t, options, nil, "tickets", "init"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := executeCollectingInput(t, options, nil, "projects", "create", "origin-pr-ux"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := executeCollectingInput(t, options, nil,
		"tickets", "create", "feature work", "--project", "origin-pr-ux",
		"--label", "change-monitor", "--label", "origin-ui"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := executeCollectingInput(t, options, nil,
		"tickets", "create", "spike the monitor", "--label", "change-monitor"); err != nil {
		t.Fatal(err)
	}
	feature := filepath.Join(home, "origin-pr-ux", "feature-work.md")
	spike := filepath.Join(home, "spike-the-monitor.md")
	if !strings.Contains(readTicketFile(t, feature), "labels:\n  - change-monitor\n  - origin-ui\n") {
		t.Fatalf("create --label:\n%s", readTicketFile(t, feature))
	}
	if !strings.Contains(readTicketFile(t, spike), "labels:\n  - change-monitor\n") {
		t.Fatalf("ungrouped create --label:\n%s", readTicketFile(t, spike))
	}

	listed, _, err := executeCollectingInput(t, options, nil,
		"tickets", "list", "--label", "change-monitor", "--all-projects", "--output", "json")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(listed, `"slug":"feature-work"`) || !strings.Contains(listed, `"slug":"spike-the-monitor"`) {
		t.Fatalf("cross-project label list: %s", listed)
	}
	if !strings.Contains(listed, `"labels":["change-monitor","origin-ui"]`) {
		t.Fatalf("list JSON missed labels: %s", listed)
	}

	textList, _, err := executeCollectingInput(t, options, nil,
		"tickets", "list", "--label", "change-monitor", "--all-projects")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(textList, "LABELS") {
		t.Fatalf("text list missed LABELS:\n%s", textList)
	}
	if !strings.Contains(textList, "change-monitor,origin-ui") {
		t.Fatalf("text list missed comma-separated labels:\n%s", textList)
	}

	both, _, err := executeCollectingInput(t, options, nil,
		"tickets", "list", "--label", "change-monitor", "--label", "origin-ui", "--all-projects", "--output", "json")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(both, `"slug":"feature-work"`) || strings.Contains(both, `"slug":"spike-the-monitor"`) {
		t.Fatalf("AND label list: %s", both)
	}

	if _, _, err := executeCollectingInput(t, options, nil,
		"tickets", "set", "spike-the-monitor", "--add-label", "dev-env"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(readTicketFile(t, spike), "  - change-monitor\n  - dev-env\n") {
		t.Fatalf("set --add-label:\n%s", readTicketFile(t, spike))
	}
	if _, _, err := executeCollectingInput(t, options, nil,
		"tickets", "set", "spike-the-monitor", "--remove-label", "change-monitor"); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(readTicketFile(t, spike), "change-monitor") {
		t.Fatalf("set --remove-label kept the label:\n%s", readTicketFile(t, spike))
	}
	if _, _, err := executeCollectingInput(t, options, nil,
		"tickets", "set", "spike-the-monitor", "--label", ""); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(readTicketFile(t, spike), "labels: []\n") {
		t.Fatalf("set --label empty:\n%s", readTicketFile(t, spike))
	}

	_, _, err = executeCollectingInput(t, options, nil,
		"tickets", "set", "spike-the-monitor", "--label", "change-monitor", "--add-label", "dev-env")
	if err == nil || clierr.CodeOf(err) != clierr.InvalidUsage {
		t.Fatalf("mixed --label and --add-label = %v (code %q)", err, clierr.CodeOf(err))
	}

	stdout, _, err := executeCollectingInput(t, options,
		strings.NewReader(`{"operation":"tickets.create","ticket":{"title":"apply labeled","labels":["change-monitor"]}}`),
		"apply", "-", "--output", "json")
	if err != nil {
		t.Fatal(err)
	}
	if created := decodeTicketMutation(t, stdout); created.ID != "apply-labeled" {
		t.Fatalf("apply create = %+v", created)
	}
	if !strings.Contains(readTicketFile(t, filepath.Join(home, "apply-labeled.md")), "change-monitor") {
		t.Fatalf("apply create labels:\n%s", readTicketFile(t, filepath.Join(home, "apply-labeled.md")))
	}
	if _, _, err := executeCollectingInput(t, options,
		strings.NewReader(`{"operation":"tickets.set","ticket":{"reference":"apply-labeled","addLabels":["dev-env"]}}`),
		"apply", "-", "--output", "json"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := executeCollectingInput(t, options,
		strings.NewReader(`{"operation":"tickets.set","ticket":{"reference":"apply-labeled","removeLabels":["change-monitor"]}}`),
		"apply", "-", "--output", "json"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := executeCollectingInput(t, options,
		strings.NewReader(`{"operation":"tickets.set","ticket":{"reference":"apply-labeled","labels":[]}}`),
		"apply", "-", "--output", "json"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(readTicketFile(t, filepath.Join(home, "apply-labeled.md")), "labels: []\n") {
		t.Fatalf("apply set cleared labels:\n%s", readTicketFile(t, filepath.Join(home, "apply-labeled.md")))
	}
}

func TestTicketsSetEmptyProjectUngroups(t *testing.T) {
	options, home := ticketTestOptions(t)
	if _, _, err := executeCollectingInput(t, options, nil, "tickets", "init"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := executeCollectingInput(t, options, nil, "projects", "create", "origin-pr-ux"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := executeCollectingInput(t, options, nil,
		"tickets", "create", "move me", "--project", "origin-pr-ux", "--label", "change-monitor"); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(home, "origin-pr-ux", "move-me.md")
	if _, _, err := executeCollectingInput(t, options, nil,
		"tickets", "set", "move-me", "--project", "", "--dry-run", "--output", "json"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(source); err != nil {
		t.Fatalf("dry-run ungroup removed the source: %v", err)
	}
	if _, _, err := executeCollectingInput(t, options, nil,
		"tickets", "set", "move-me", "--project", ""); err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(home, "move-me.md")
	if _, err := os.Stat(source); !os.IsNotExist(err) {
		t.Fatal("ungroup kept the Project file")
	}
	content := readTicketFile(t, destination)
	if !strings.Contains(content, "labels:\n  - change-monitor\n") {
		t.Fatalf("ungroup dropped labels:\n%s", content)
	}

	if _, _, err := executeCollectingInput(t, options,
		strings.NewReader(`{"operation":"tickets.set","ticket":{"reference":"move-me","project":"origin-pr-ux"}}`),
		"apply", "-", "--output", "json"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := executeCollectingInput(t, options,
		strings.NewReader(`{"operation":"tickets.set","ticket":{"reference":"move-me","project":""}}`),
		"apply", "-", "--output", "json"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(destination); err != nil {
		t.Fatalf("apply ungroup: %v", err)
	}
}

func TestLabelsListDerivesCounts(t *testing.T) {
	options, _ := ticketTestOptions(t)
	if _, _, err := executeCollectingInput(t, options, nil, "tickets", "init"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := executeCollectingInput(t, options, nil,
		"tickets", "create", "one", "--label", "change-monitor", "--label", "dev-env"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := executeCollectingInput(t, options, nil,
		"tickets", "create", "two", "--label", "change-monitor"); err != nil {
		t.Fatal(err)
	}

	stdout, _, err := executeCollectingInput(t, options, nil, "labels", "list", "--output", "json")
	if err != nil {
		t.Fatal(err)
	}
	var result struct {
		SchemaVersion int `json:"schemaVersion"`
		Labels        []struct {
			Name    string `json:"name"`
			Tickets int    `json:"tickets"`
		} `json:"labels"`
		TotalCount int  `json:"totalCount"`
		Truncated  bool `json:"truncated"`
	}
	if err := json.Unmarshal([]byte(stdout), &result); err != nil {
		t.Fatalf("decode labels list: %v\n%s", err, stdout)
	}
	if result.SchemaVersion != 2 || result.TotalCount != 2 || result.Truncated || len(result.Labels) != 2 {
		t.Fatalf("labels list = %s", stdout)
	}
	if result.Labels[0].Name != "change-monitor" || result.Labels[0].Tickets != 2 {
		t.Fatalf("first label = %+v", result.Labels[0])
	}
	if result.Labels[1].Name != "dev-env" || result.Labels[1].Tickets != 1 {
		t.Fatalf("second label = %+v", result.Labels[1])
	}
}

func TestLabelsAddRemoveRename(t *testing.T) {
	options, home := ticketTestOptions(t)
	if _, _, err := executeCollectingInput(t, options, nil, "tickets", "init"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := executeCollectingInput(t, options, nil, "tickets", "create", "one"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := executeCollectingInput(t, options, nil, "tickets", "create", "two"); err != nil {
		t.Fatal(err)
	}

	_, _, err := executeCollectingInput(t, options, nil, "labels", "add", "change-monitor")
	if err == nil || !strings.Contains(err.Error(), "ticket") {
		t.Fatalf("labels add without --ticket = %v", err)
	}
	if _, _, err := executeCollectingInput(t, options, nil,
		"labels", "add", "change-monitor", "--ticket", "one", "--ticket", "two", "--dry-run", "--output", "json"); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(readTicketFile(t, filepath.Join(home, "one.md")), "change-monitor") {
		t.Fatal("dry-run labels add wrote a file")
	}
	if _, _, err := executeCollectingInput(t, options, nil,
		"labels", "add", "change-monitor", "--ticket", "one", "--ticket", "two"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(readTicketFile(t, filepath.Join(home, "one.md")), "change-monitor") {
		t.Fatalf("labels add missed one:\n%s", readTicketFile(t, filepath.Join(home, "one.md")))
	}

	if _, _, err := executeCollectingInput(t, options, nil,
		"labels", "rename", "change-monitor", "monitor-theme"); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(readTicketFile(t, filepath.Join(home, "two.md")), "change-monitor") {
		t.Fatalf("labels rename left old name:\n%s", readTicketFile(t, filepath.Join(home, "two.md")))
	}
	if !strings.Contains(readTicketFile(t, filepath.Join(home, "two.md")), "monitor-theme") {
		t.Fatalf("labels rename missed new name:\n%s", readTicketFile(t, filepath.Join(home, "two.md")))
	}

	if _, _, err := executeCollectingInput(t, options, nil, "labels", "remove", "monitor-theme"); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(readTicketFile(t, filepath.Join(home, "one.md")), "monitor-theme") {
		t.Fatalf("labels remove left one:\n%s", readTicketFile(t, filepath.Join(home, "one.md")))
	}

	stdout, _, err := executeCollectingInput(t, options,
		strings.NewReader(`{"operation":"labels.add","label":{"name":"dev-env","tickets":["one"]}}`),
		"apply", "-", "--output", "json")
	if err != nil {
		t.Fatal(err)
	}
	if applied := decodeTicketMutation(t, stdout); applied.Operation != "labels.add" || applied.ID != "dev-env" {
		t.Fatalf("apply labels.add = %+v", applied)
	}
	if _, _, err := executeCollectingInput(t, options,
		strings.NewReader(`{"operation":"labels.rename","label":{"name":"dev-env","newName":"dev-setup"}}`),
		"apply", "-", "--output", "json"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := executeCollectingInput(t, options,
		strings.NewReader(`{"operation":"labels.remove","label":{"name":"dev-setup"}}`),
		"apply", "-", "--output", "json"); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(readTicketFile(t, filepath.Join(home, "one.md")), "dev-setup") {
		t.Fatalf("apply labels.remove left one:\n%s", readTicketFile(t, filepath.Join(home, "one.md")))
	}
}

func TestLabelFlagCompletionReadsExistingLabels(t *testing.T) {
	options, _ := ticketTestOptions(t)
	if _, _, err := executeCollectingInput(t, options, nil, "tickets", "init"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := executeCollectingInput(t, options, nil,
		"tickets", "create", "labeled", "--label", "change-monitor"); err != nil {
		t.Fatal(err)
	}
	command := cli.New(options)
	create := findCommand(command, "tickets", "create")
	complete, found := create.GetFlagCompletionFunc("label")
	if !found {
		t.Fatal("create --label has no completion function")
	}
	names, _ := complete(create, nil, "")
	if strings.Join(names, ",") != "change-monitor" {
		t.Fatalf("create --label completion = %v", names)
	}
}
