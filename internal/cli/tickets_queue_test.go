package cli_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/jpugliesi/tmux-worktree/internal/cli"
	ticketservice "github.com/jpugliesi/tmux-worktree/internal/ticket"
)

func TestTicketsQueueDoesNotRequireAWorkspaceTemplate(t *testing.T) {
	options, _ := ticketTestOptions(t)
	if _, _, err := executeCollectingInput(t, options, nil, "tickets", "init"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := executeCollectingInput(t, options, nil, "projects", "create", "core"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := executeCollectingInput(t, options, nil,
		"tickets", "create", "Ready work", "--project", "core", "--status", "ready-for-agent"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := executeCollectingInput(t, options, nil,
		"tickets", "create", "Blocked work", "--project", "core", "--status", "ready-for-agent", "--blocked-by", "missing-dep"); err != nil {
		t.Fatal(err)
	}

	stdout, _, err := executeCollectingInput(t, options, nil,
		"tickets", "queue", "--project", "core", "--output", "json")
	if err != nil {
		t.Fatalf("queue: %v", err)
	}
	var queue struct {
		SchemaVersion int `json:"schemaVersion"`
		Queue         struct {
			Project string `json:"project"`
			Graph   []struct {
				Slug         string                          `json:"slug"`
				Dependencies []ticketservice.QueueDependency `json:"dependencies"`
				Ready        bool                            `json:"ready"`
			} `json:"graph"`
			Ready []struct {
				Slug string `json:"slug"`
			} `json:"ready"`
			ReadyTotalCount int  `json:"readyTotalCount"`
			ReadyTruncated  bool `json:"readyTruncated"`
		} `json:"queue"`
	}
	if err := json.Unmarshal([]byte(stdout), &queue); err != nil {
		t.Fatalf("decode queue: %v\n%s", err, stdout)
	}
	if queue.SchemaVersion != 2 || queue.Queue.Project != "core" || len(queue.Queue.Graph) != 2 ||
		queue.Queue.Graph[0].Slug != "blocked-work" || queue.Queue.Graph[0].Ready ||
		len(queue.Queue.Graph[0].Dependencies) != 1 || queue.Queue.Graph[0].Dependencies[0].State != ticketservice.QueueDependencyMissing ||
		queue.Queue.ReadyTotalCount != 1 || queue.Queue.ReadyTruncated ||
		len(queue.Queue.Ready) != 1 || queue.Queue.Ready[0].Slug != "ready-work" {
		t.Fatalf("queue = %s", stdout)
	}

	limited, _, err := executeCollectingInput(t, options, nil,
		"tickets", "queue", "--project", "core", "--limit", "1", "--fields", "ready,readyTotalCount", "--output", "json")
	if err != nil || !strings.Contains(limited, `"queue":{"ready":[`) || strings.Contains(limited, `"graph"`) {
		t.Fatalf("limited queue = %s, error = %v", limited, err)
	}
}

func TestTicketsQueueAppearsInSchemaAndCompletesProjects(t *testing.T) {
	options, _ := ticketTestOptions(t)
	if _, _, err := executeCollectingInput(t, options, nil, "tickets", "init"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := executeCollectingInput(t, options, nil, "projects", "create", "core"); err != nil {
		t.Fatal(err)
	}

	schema, _, err := executeCollectingInput(t, options, nil, "schema")
	if err != nil || !strings.Contains(schema, `"path":"twt tickets queue"`) ||
		!strings.Contains(schema, `"name":"limit"`) {
		t.Fatalf("schema = %s, error = %v", schema, err)
	}
	command := findCommand(cli.New(options), "tickets", "queue")
	completion, found := command.GetFlagCompletionFunc("project")
	if !found {
		t.Fatal("queue --project has no completion function")
	}
	names, _ := completion(command, nil, "")
	if strings.Join(names, ",") != "core" {
		t.Fatalf("Project completion = %v", names)
	}
}
