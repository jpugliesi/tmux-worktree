package cli_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jpugliesi/tmux-worktree/internal/domain"
)

// fakeProviderOnPath puts an executable fake provider CLI on PATH so
// dispatch validation can resolve it without a real install.
func fakeProviderOnPath(t *testing.T, name string) {
	t.Helper()
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, name), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func TestTicketsDispatchRoutesToTheLocalBackend(t *testing.T) {
	options, _ := ticketTestOptions(t)
	fakeProviderOnPath(t, "grok")
	writeTwtConfigFile(t, options.ConfigDir, "ticketAgent:\n  provider: grok\n")
	writeTemplateFile(t, options.ConfigDir, domain.Template{
		Version: domain.TemplateVersion,
		Name:    "product",
		Repositories: []domain.RepositorySpec{{
			Name: "api", Clone: domain.CloneSpec{URL: "https://github.com/acme/api.git"}, DefaultBranch: "main",
		}},
	})
	if _, _, err := executeCollectingInput(t, options, nil, "tickets", "init"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := executeCollectingInput(t, options, nil, "projects", "create", "core", "--template", "product"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := executeCollectingInput(t, options, nil,
		"tickets", "create", "Fix auth", "--project", "core", "--status", "ready-for-agent"); err != nil {
		t.Fatal(err)
	}

	dryJSON, _, err := executeCollectingInput(t, options, nil,
		"tickets", "dispatch", "fix-auth", "--dry-run", "--output", "json")
	if err != nil {
		t.Fatalf("local dry-run dispatch: %v\n%s", err, dryJSON)
	}
	for _, want := range []string{`"status":"valid"`, `"provider":"grok"`, `"agentLabel":"ticket-impl"`} {
		if !strings.Contains(dryJSON, want) {
			t.Fatalf("dry-run JSON lacks %s:\n%s", want, dryJSON)
		}
	}
	claimedJSON, _, err := executeCollectingInput(t, options, nil,
		"tickets", "list", "--claimed", "--output", "json")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(claimedJSON, "fix-auth") {
		t.Fatalf("dry-run dispatch claimed the ticket:\n%s", claimedJSON)
	}

	// An unknown flag is an error: the backend flag is gone.
	_, _, err = executeCollectingInput(t, options, nil,
		"tickets", "dispatch", "fix-auth", "--backend", "local", "--dry-run", "--output", "json")
	if err == nil || !strings.Contains(err.Error(), "unknown flag") {
		t.Fatalf("removed --backend flag error = %v", err)
	}
}

func TestApplyTicketsDispatchRunsLocally(t *testing.T) {
	options, _ := ticketTestOptions(t)
	fakeProviderOnPath(t, "grok")
	writeTwtConfigFile(t, options.ConfigDir, "ticketAgent:\n  provider: grok\n")
	writeTemplateFile(t, options.ConfigDir, domain.Template{
		Version: domain.TemplateVersion,
		Name:    "product",
		Repositories: []domain.RepositorySpec{{
			Name: "api", Clone: domain.CloneSpec{URL: "https://github.com/acme/api.git"}, DefaultBranch: "main",
		}},
	})
	if _, _, err := executeCollectingInput(t, options, nil, "tickets", "init"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := executeCollectingInput(t, options, nil, "projects", "create", "core", "--template", "product"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := executeCollectingInput(t, options, nil,
		"tickets", "create", "Fix auth", "--project", "core", "--status", "ready-for-agent"); err != nil {
		t.Fatal(err)
	}
	applyJSON, _, err := executeCollectingInput(t, options,
		strings.NewReader(`{"operation":"tickets.dispatch","ticket":{"reference":"fix-auth"}}`),
		"apply", "--stdin", "--dry-run", "--output", "json")
	if err != nil {
		t.Fatalf("apply local dispatch dry run: %v\n%s", err, applyJSON)
	}
	if !strings.Contains(applyJSON, `"status":"valid"`) {
		t.Fatalf("apply JSON = %s", applyJSON)
	}
}

func TestTicketsCompleteRecordsPullRequestsAndReleasesTheClaim(t *testing.T) {
	options, _ := ticketTestOptions(t)
	if _, _, err := executeCollectingInput(t, options, nil, "tickets", "init"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := executeCollectingInput(t, options, nil,
		"tickets", "create", "Fix auth", "--status", "ready-for-agent"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := executeCollectingInput(t, options, nil,
		"tickets", "claim", "fix-auth", "--as", "twt-local-01234567"); err != nil {
		t.Fatal(err)
	}
	completeJSON, _, err := executeCollectingInput(t, options, nil,
		"tickets", "complete", "fix-auth", "--as", "twt-local-01234567",
		"--pr", "https://origin.cursor.com/acme/api/pull/7", "--output", "json")
	if err != nil {
		t.Fatalf("complete: %v\n%s", err, completeJSON)
	}
	if !strings.Contains(completeJSON, `"operation":"tickets.complete"`) || !strings.Contains(completeJSON, `"status":"applied"`) {
		t.Fatalf("complete JSON = %s", completeJSON)
	}
	showJSON, _, err := executeCollectingInput(t, options, nil, "tickets", "show", "fix-auth", "--output", "json")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(showJSON, `"status":"ready-for-human"`) ||
		!strings.Contains(showJSON, `"pullRequests":["https://origin.cursor.com/acme/api/pull/7"]`) ||
		!strings.Contains(showJSON, `"claimedBy":""`) {
		t.Fatalf("show after complete = %s", showJSON)
	}
	applyJSON, _, err := executeCollectingInput(t, options,
		strings.NewReader(`{"operation":"tickets.complete","ticket":{"reference":"fix-auth","as":"twt-local-01234567","pullRequests":["https://origin.cursor.com/acme/api/pull/7"]}}`),
		"apply", "--stdin", "--dry-run", "--output", "json")
	if err != nil {
		t.Fatalf("apply complete dry run: %v\n%s", err, applyJSON)
	}
	if !strings.Contains(applyJSON, `"status":"valid"`) {
		t.Fatalf("apply complete JSON = %s", applyJSON)
	}
}

func TestTicketsSyncWorksWithoutTheCursorHarnessOnALocalOnlyProject(t *testing.T) {
	options, _ := ticketTestOptions(t)
	writeTemplateFile(t, options.ConfigDir, domain.Template{
		Version: domain.TemplateVersion,
		Name:    "product",
		Repositories: []domain.RepositorySpec{{
			Name: "api", Clone: domain.CloneSpec{URL: "https://github.com/acme/api.git"}, DefaultBranch: "main",
		}},
	})
	if _, _, err := executeCollectingInput(t, options, nil, "tickets", "init"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := executeCollectingInput(t, options, nil, "projects", "create", "core", "--template", "product"); err != nil {
		t.Fatal(err)
	}
	syncJSON, _, err := executeCollectingInput(t, options, nil,
		"tickets", "sync", "--project", "core", "--output", "json")
	if err != nil {
		t.Fatalf("local-only sync: %v\n%s", err, syncJSON)
	}
	for _, want := range []string{`"operation":"tickets.sync"`, `"status":"applied"`, `"local":{`, `"known":true`} {
		if !strings.Contains(syncJSON, want) {
			t.Fatalf("sync JSON lacks %s:\n%s", want, syncJSON)
		}
	}
	applyJSON, _, err := executeCollectingInput(t, options,
		strings.NewReader(`{"operation":"tickets.sync","ticket":{"project":"core"}}`),
		"apply", "--stdin", "--dry-run", "--output", "json")
	if err != nil {
		t.Fatalf("apply sync dry run: %v\n%s", err, applyJSON)
	}
	if !strings.Contains(applyJSON, `"status":"valid"`) {
		t.Fatalf("apply sync JSON = %s", applyJSON)
	}
}

func TestTicketsPlanReplacesThePlanSection(t *testing.T) {
	options, _ := ticketTestOptions(t)
	if _, _, err := executeCollectingInput(t, options, nil, "tickets", "init"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := executeCollectingInput(t, options, nil,
		"tickets", "create", "Fix auth", "--status", "ready-for-agent"); err != nil {
		t.Fatal(err)
	}
	planJSON, _, err := executeCollectingInput(t, options,
		strings.NewReader("1. Do the thing.\n2. Test it.\n"),
		"tickets", "plan", "fix-auth", "--stdin", "--output", "json")
	if err != nil {
		t.Fatalf("tickets plan: %v\n%s", err, planJSON)
	}
	if !strings.Contains(planJSON, `"operation":"tickets.plan"`) || !strings.Contains(planJSON, `"status":"applied"`) {
		t.Fatalf("plan JSON = %s", planJSON)
	}
	showJSON, _, err := executeCollectingInput(t, options, nil, "tickets", "show", "fix-auth", "--output", "json")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(showJSON, `## Plan`) || !strings.Contains(showJSON, "Do the thing.") {
		t.Fatalf("show after plan = %s", showJSON)
	}
	applyJSON, _, err := executeCollectingInput(t, options,
		strings.NewReader(`{"operation":"tickets.plan","ticket":{"reference":"fix-auth","plan":"Revised."}}`),
		"apply", "--stdin", "--dry-run", "--output", "json")
	if err != nil {
		t.Fatalf("apply plan dry run: %v\n%s", err, applyJSON)
	}
	if !strings.Contains(applyJSON, `"status":"valid"`) {
		t.Fatalf("apply plan JSON = %s", applyJSON)
	}
}

func TestProjectsPlanCommands(t *testing.T) {
	options, _ := ticketTestOptions(t)
	if _, _, err := executeCollectingInput(t, options, nil, "tickets", "init"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := executeCollectingInput(t, options, nil, "projects", "create", "core"); err != nil {
		t.Fatal(err)
	}
	initJSON, _, err := executeCollectingInput(t, options, nil, "projects", "plan", "init", "core", "--output", "json")
	if err != nil {
		t.Fatalf("plan init: %v\n%s", err, initJSON)
	}
	if !strings.Contains(initJSON, `"operation":"projects.plan.init"`) || !strings.Contains(initJSON, `"status":"applied"`) {
		t.Fatalf("plan init JSON = %s", initJSON)
	}
	editJSON, _, err := executeCollectingInput(t, options,
		strings.NewReader("# core Plan\n\n## Goals\n\nShip it.\n"),
		"projects", "plan", "edit", "core", "--stdin", "--output", "json")
	if err != nil {
		t.Fatalf("plan edit: %v\n%s", err, editJSON)
	}
	showJSON, _, err := executeCollectingInput(t, options, nil, "projects", "plan", "show", "core", "--output", "json")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(showJSON, "Ship it.") {
		t.Fatalf("plan show JSON = %s", showJSON)
	}
	pathOut, _, err := executeCollectingInput(t, options, nil, "projects", "plan", "path", "core")
	if err != nil || !strings.Contains(pathOut, "core/plan.md") {
		t.Fatalf("plan path = %q err %v", pathOut, err)
	}
	projectJSON, _, err := executeCollectingInput(t, options, nil, "projects", "show", "core", "--output", "json")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(projectJSON, `"hasPlan":true`) || !strings.Contains(projectJSON, `"planTitle":"core Plan"`) {
		t.Fatalf("projects show lacks plan fields:\n%s", projectJSON)
	}
	applyJSON, _, err := executeCollectingInput(t, options,
		strings.NewReader(`{"operation":"projects.plan.edit","project":{"name":"core","plan":"# core Plan\n\nvia apply\n"}}`),
		"apply", "--stdin", "--output", "json")
	if err != nil || !strings.Contains(applyJSON, `"status":"applied"`) {
		t.Fatalf("apply plan edit = %s err %v", applyJSON, err)
	}
}

func TestTicketsAskAndAnswerRoundTrip(t *testing.T) {
	options, _ := ticketTestOptions(t)
	if _, _, err := executeCollectingInput(t, options, nil, "tickets", "init"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := executeCollectingInput(t, options, nil,
		"tickets", "create", "Fix auth", "--status", "ready-for-agent"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := executeCollectingInput(t, options, nil,
		"tickets", "claim", "fix-auth", "--as", "twt-local-01234567"); err != nil {
		t.Fatal(err)
	}
	askJSON, _, err := executeCollectingInput(t, options,
		strings.NewReader("Which OAuth provider should the login use?"),
		"tickets", "ask", "fix-auth", "--stdin", "--as", "twt-local-01234567", "--output", "json")
	if err != nil {
		t.Fatalf("ask: %v\n%s", err, askJSON)
	}
	if !strings.Contains(askJSON, `"operation":"tickets.ask"`) || !strings.Contains(askJSON, `"status":"applied"`) {
		t.Fatalf("ask JSON = %s", askJSON)
	}
	waitingJSON, _, err := executeCollectingInput(t, options, nil,
		"tickets", "list", "--needs-input", "--all-projects", "--output", "json")
	if err != nil {
		t.Fatalf("needs-input list: %v\n%s", err, waitingJSON)
	}
	if !strings.Contains(waitingJSON, "fix-auth") {
		t.Fatalf("waiting list lacks the ticket:\n%s", waitingJSON)
	}
	answerJSON, answerStderr, err := executeCollectingInput(t, options,
		strings.NewReader("Use OAuth with the corporate IdP."),
		"tickets", "answer", "fix-auth", "--stdin", "--output", "json")
	if err != nil {
		t.Fatalf("answer: %v\n%s", err, answerJSON)
	}
	if !strings.Contains(answerJSON, `"operation":"tickets.answer"`) || !strings.Contains(answerJSON, `"delivered":false`) {
		t.Fatalf("answer JSON = %s (stderr %s)", answerJSON, answerStderr)
	}
	showJSON, _, err := executeCollectingInput(t, options, nil, "tickets", "show", "fix-auth", "--output", "json")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(showJSON, `"status":"ready-for-agent"`) || !strings.Contains(showJSON, "### A ") ||
		!strings.Contains(showJSON, `"claimedBy":"twt-local-01234567"`) {
		t.Fatalf("show after answer = %s", showJSON)
	}
	applyJSON, _, err := executeCollectingInput(t, options,
		strings.NewReader(`{"operation":"tickets.ask","ticket":{"reference":"fix-auth","as":"twt-local-01234567","text":"Another question?"}}`),
		"apply", "--stdin", "--dry-run", "--output", "json")
	if err != nil || !strings.Contains(applyJSON, `"status":"valid"`) {
		t.Fatalf("apply ask dry run = %s err %v", applyJSON, err)
	}
}

func TestTicketsPRAddAndRemove(t *testing.T) {
	options, _ := ticketTestOptions(t)
	if _, _, err := executeCollectingInput(t, options, nil, "tickets", "init"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := executeCollectingInput(t, options, nil,
		"tickets", "create", "Fix auth", "--status", "ready-for-agent"); err != nil {
		t.Fatal(err)
	}
	addJSON, _, err := executeCollectingInput(t, options, nil,
		"tickets", "pr", "add", "fix-auth", "--pr", "https://origin.cursor.com/acme/api/pull/7", "--output", "json")
	if err != nil {
		t.Fatalf("pr add: %v\n%s", err, addJSON)
	}
	if !strings.Contains(addJSON, `"operation":"tickets.pr-add"`) || !strings.Contains(addJSON, `"status":"applied"`) {
		t.Fatalf("pr add JSON = %s", addJSON)
	}
	showJSON, _, err := executeCollectingInput(t, options, nil, "tickets", "show", "fix-auth", "--output", "json")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(showJSON, `"pullRequests":["https://origin.cursor.com/acme/api/pull/7"]`) ||
		!strings.Contains(showJSON, `"status":"ready-for-agent"`) {
		t.Fatalf("show after pr add = %s", showJSON)
	}
	rmJSON, _, err := executeCollectingInput(t, options,
		strings.NewReader(`{"operation":"tickets.pr-rm","ticket":{"reference":"fix-auth","pullRequests":["https://origin.cursor.com/acme/api/pull/7"]}}`),
		"apply", "--stdin", "--output", "json")
	if err != nil || !strings.Contains(rmJSON, `"status":"applied"`) {
		t.Fatalf("apply pr rm = %s err %v", rmJSON, err)
	}
}
