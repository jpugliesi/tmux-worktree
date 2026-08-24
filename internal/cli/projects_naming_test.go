package cli_test

import (
	"encoding/json"
	"testing"
)

func TestProjectsCommandOwnsDurableTicketProjects(t *testing.T) {
	options, _ := ticketTestOptions(t)
	executeWithOptions(t, options, nil, "tickets", "init")

	created := executeWithOptions(t, options, nil, "projects", "create", "core", "--output", "json")
	var mutation ticketMutation
	if err := json.Unmarshal([]byte(created), &mutation); err != nil {
		t.Fatalf("decode Project create: %v\n%s", err, created)
	}
	if mutation.SchemaVersion != 2 || mutation.Operation != "projects.create" || mutation.ID != "core" {
		t.Fatalf("Project create = %+v", mutation)
	}

	executeWithOptions(t, options, nil, "tickets", "create", "Fix auth", "--project", "core")
	shown := executeWithOptions(t, options, nil, "tickets", "show", "fix-auth", "--output", "json")
	var result struct {
		SchemaVersion int `json:"schemaVersion"`
		Ticket        struct {
			Project string `json:"project"`
		} `json:"ticket"`
	}
	if err := json.Unmarshal([]byte(shown), &result); err != nil {
		t.Fatalf("decode Ticket show: %v\n%s", err, shown)
	}
	if result.SchemaVersion != 2 || result.Ticket.Project != "core" {
		t.Fatalf("Ticket Project = %#v", result)
	}

	project := executeWithOptions(t, options, nil, "projects", "show", "core", "--output", "json")
	var projectResult struct {
		SchemaVersion int `json:"schemaVersion"`
		Project       struct {
			Name    string `json:"name"`
			Tickets int    `json:"tickets"`
		} `json:"project"`
	}
	if err := json.Unmarshal([]byte(project), &projectResult); err != nil {
		t.Fatalf("decode Project show: %v\n%s", err, project)
	}
	if projectResult.SchemaVersion != 2 || projectResult.Project.Name != "core" || projectResult.Project.Tickets != 1 {
		t.Fatalf("Project show = %#v", projectResult)
	}
}
