package cli_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jpugliesi/tmux-worktree/internal/clierr"
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
		"apply", "-", "--dry-run", "--output", "json")
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
	showJSON, _, err := executeCollectingInput(t, options, nil, "tickets", "get", "fix-auth", "--output", "json")
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
		"apply", "-", "--dry-run", "--output", "json")
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
		"apply", "-", "--dry-run", "--output", "json")
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
		"tickets", "plan", "fix-auth", "-", "--output", "json")
	if err != nil {
		t.Fatalf("tickets plan: %v\n%s", err, planJSON)
	}
	if !strings.Contains(planJSON, `"operation":"tickets.plan"`) || !strings.Contains(planJSON, `"status":"applied"`) {
		t.Fatalf("plan JSON = %s", planJSON)
	}
	showJSON, _, err := executeCollectingInput(t, options, nil, "tickets", "get", "fix-auth", "--output", "json")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(showJSON, `## Plan`) || !strings.Contains(showJSON, "Do the thing.") {
		t.Fatalf("show after plan = %s", showJSON)
	}
	applyJSON, _, err := executeCollectingInput(t, options,
		strings.NewReader(`{"operation":"tickets.plan","ticket":{"reference":"fix-auth","plan":"Revised."}}`),
		"apply", "-", "--dry-run", "--output", "json")
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
	editJSON, _, err := executeCollectingInput(t, options,
		strings.NewReader("# core Plan\n\nShip it.\n"),
		"projects", "plan", "core", "-", "--output", "json")
	if err != nil {
		t.Fatalf("plan write: %v\n%s", err, editJSON)
	}
	if !strings.Contains(editJSON, `"operation":"projects.plan"`) || !strings.Contains(editJSON, `"status":"applied"`) {
		t.Fatalf("plan write JSON = %s", editJSON)
	}
	showJSON, _, err := executeCollectingInput(t, options, nil, "projects", "plan", "get", "core", "--output", "json")
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
	projectJSON, _, err := executeCollectingInput(t, options, nil, "projects", "get", "core", "--output", "json")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(projectJSON, `"hasPlan":true`) || !strings.Contains(projectJSON, `"planTitle":"core Plan"`) {
		t.Fatalf("projects show lacks plan fields:\n%s", projectJSON)
	}
	applyJSON, _, err := executeCollectingInput(t, options,
		strings.NewReader(`{"operation":"projects.plan","project":{"name":"core","plan":"# core Plan\n\nvia apply\n"}}`),
		"apply", "-", "--output", "json")
	if err != nil || !strings.Contains(applyJSON, `"status":"applied"`) {
		t.Fatalf("apply plan = %s err %v", applyJSON, err)
	}
}

func TestProjectsPlanEditOpensTheEditorInATerminal(t *testing.T) {
	options, _ := ticketTestOptions(t)
	if _, _, err := executeCollectingInput(t, options, nil, "tickets", "init"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := executeCollectingInput(t, options, nil, "projects", "create", "core"); err != nil {
		t.Fatal(err)
	}

	// A missing Project is not_found before any editor opens.
	options.OpenEditor = func(string) error {
		return fmt.Errorf("the editor must not open for a missing Project")
	}
	_, _, err := executeCollectingInput(t, options, strings.NewReader(""), "projects", "plan", "missing")
	if err == nil || clierr.CodeOf(err) != clierr.NotFound {
		t.Fatalf("plan of a missing Project = %v (code %q)", err, clierr.CodeOf(err))
	}

	// A missing plan.md opens a blank editor. The save creates the file.
	options.OpenEditor = func(path string) error {
		seed, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if strings.TrimSpace(string(seed)) != "" {
			return fmt.Errorf("the draft is not empty: %q", seed)
		}
		return os.WriteFile(path, []byte("# core Plan\n\nFrom a blank editor.\n"), 0o644)
	}
	stdout, _, err := executeCollectingInput(t, options, strings.NewReader(""), "projects", "plan", "core")
	if err != nil {
		t.Fatalf("blank plan editor: %v\n%s", err, stdout)
	}
	if !strings.Contains(stdout, `Wrote the plan of Project "core"`) {
		t.Fatalf("blank plan editor stdout = %q", stdout)
	}
	showJSON, _, err := executeCollectingInput(t, options, nil, "projects", "plan", "get", "core", "--output", "json")
	if err != nil || !strings.Contains(showJSON, "From a blank editor.") {
		t.Fatalf("plan show after blank editor = %s err %v", showJSON, err)
	}

	// Without a configured editor the command refuses with invalid_usage.
	t.Setenv("VISUAL", "")
	t.Setenv("EDITOR", "")
	options.OpenEditor = nil
	_, _, err = executeCollectingInput(t, options, strings.NewReader(""), "projects", "plan", "core")
	if err == nil || clierr.CodeOf(err) != clierr.InvalidUsage {
		t.Fatalf("plan without an editor = %v (code %q)", err, clierr.CodeOf(err))
	}

	// The editor gets a draft seeded with the current plan, and the saved
	// text lands through the normal write.
	options.OpenEditor = func(path string) error {
		seed, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if !strings.Contains(string(seed), "# core Plan") {
			return fmt.Errorf("the draft is not seeded with the plan: %q", seed)
		}
		return os.WriteFile(path, []byte("# core Plan\n\nEdited in the editor.\n"), 0o644)
	}
	stdout, _, err = executeCollectingInput(t, options, strings.NewReader(""), "projects", "plan", "core")
	if err != nil {
		t.Fatalf("editor write: %v\n%s", err, stdout)
	}
	if !strings.Contains(stdout, `Wrote the plan of Project "core"`) {
		t.Fatalf("editor edit stdout = %q", stdout)
	}
	showJSON, _, err = executeCollectingInput(t, options, nil, "projects", "plan", "get", "core", "--output", "json")
	if err != nil || !strings.Contains(showJSON, "Edited in the editor.") {
		t.Fatalf("plan show after editor edit = %s err %v", showJSON, err)
	}

	// An empty save refuses instead of erasing plan.md.
	options.OpenEditor = func(path string) error {
		return os.WriteFile(path, []byte(" \n"), 0o644)
	}
	_, _, err = executeCollectingInput(t, options, strings.NewReader(""), "projects", "plan", "core")
	if err == nil || clierr.CodeOf(err) != clierr.InvalidUsage {
		t.Fatalf("empty editor save = %v (code %q)", err, clierr.CodeOf(err))
	}
	showJSON, _, err = executeCollectingInput(t, options, nil, "projects", "plan", "get", "core", "--output", "json")
	if err != nil || !strings.Contains(showJSON, "Edited in the editor.") {
		t.Fatalf("plan content after the empty save = %s err %v", showJSON, err)
	}
}

func TestTicketsPlanOpensTheEditorInATerminal(t *testing.T) {
	options, _ := ticketTestOptions(t)
	if _, _, err := executeCollectingInput(t, options, nil, "tickets", "init"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := executeCollectingInput(t, options, nil,
		"tickets", "create", "Fix auth", "--status", "ready-for-agent"); err != nil {
		t.Fatal(err)
	}

	// Without a terminal and without - the command refuses.
	options.OpenEditor = func(string) error {
		return fmt.Errorf("the editor must not open without a terminal")
	}
	_, _, err := executeCollectingInput(t, options, nil, "tickets", "plan", "fix-auth")
	if err == nil || clierr.CodeOf(err) != clierr.InvalidUsage {
		t.Fatalf("plan without a terminal = %v (code %q)", err, clierr.CodeOf(err))
	}
	if hint := clierr.HintOf(err); !strings.Contains(hint, "Pass -") {
		t.Fatalf("plan without a terminal hint = %q", hint)
	}

	// A missing Ticket is not_found before any editor opens.
	options.OpenEditor = func(string) error {
		return fmt.Errorf("the editor must not open for a missing ticket")
	}
	_, _, err = executeCollectingInput(t, options, strings.NewReader(""), "tickets", "plan", "missing-ticket")
	if err == nil || clierr.CodeOf(err) != clierr.NotFound {
		t.Fatalf("plan of a missing ticket = %v (code %q)", err, clierr.CodeOf(err))
	}

	// With no existing ## Plan section the editor gets an empty draft.
	options.OpenEditor = func(path string) error {
		seed, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if strings.TrimSpace(string(seed)) != "" {
			return fmt.Errorf("the draft is not empty: %q", seed)
		}
		return os.WriteFile(path, []byte("1. Do the thing.\n"), 0o644)
	}
	stdout, _, err := executeCollectingInput(t, options, strings.NewReader(""), "tickets", "plan", "fix-auth")
	if err != nil {
		t.Fatalf("editor plan: %v\n%s", err, stdout)
	}
	if !strings.Contains(stdout, `Wrote the plan of ticket "fix-auth"`) {
		t.Fatalf("editor plan stdout = %q", stdout)
	}

	// A later edit seeds the draft with the current section body.
	options.OpenEditor = func(path string) error {
		seed, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if !strings.Contains(string(seed), "Do the thing.") || strings.Contains(string(seed), "## Plan") {
			return fmt.Errorf("the draft is not the plan section body: %q", seed)
		}
		return os.WriteFile(path, []byte("1. Do it better.\n"), 0o644)
	}
	if _, _, err := executeCollectingInput(t, options, strings.NewReader(""), "tickets", "plan", "fix-auth"); err != nil {
		t.Fatal(err)
	}
	showJSON, _, err := executeCollectingInput(t, options, nil, "tickets", "get", "fix-auth", "--output", "json")
	if err != nil || !strings.Contains(showJSON, "Do it better.") || strings.Contains(showJSON, "Do the thing.") {
		t.Fatalf("show after editor plan = %s err %v", showJSON, err)
	}
}

func TestProjectsPlanOpensTheEditorForTheCurrentProject(t *testing.T) {
	options, _ := ticketTestOptions(t)
	if _, _, err := executeCollectingInput(t, options, nil, "tickets", "init"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := executeCollectingInput(t, options, nil, "projects", "create", "core"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := executeCollectingInput(t, options,
		strings.NewReader("# core Plan\n\nSeed.\n"),
		"projects", "plan", "core", "-"); err != nil {
		t.Fatal(err)
	}

	_, _, err := executeCollectingInput(t, options, strings.NewReader(""), "projects", "plan")
	if err == nil || clierr.CodeOf(err) != clierr.InvalidUsage {
		t.Fatalf("plan with no Project in scope = %v (code %q)", err, clierr.CodeOf(err))
	}
	if hint := clierr.HintOf(err); !strings.Contains(hint, "TWT_PROJECT") {
		t.Fatalf("no Project hint = %q", hint)
	}

	t.Setenv("TWT_PROJECT", "core")
	options.OpenEditor = func(path string) error {
		seed, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if !strings.Contains(string(seed), "# core Plan") {
			return fmt.Errorf("the draft is not seeded with the plan: %q", seed)
		}
		return os.WriteFile(path, []byte("# core Plan\n\nEdited from projects plan.\n"), 0o644)
	}
	stdout, _, err := executeCollectingInput(t, options, strings.NewReader(""), "projects", "plan")
	if err != nil {
		t.Fatalf("projects plan editor: %v\n%s", err, stdout)
	}
	if !strings.Contains(stdout, `Wrote the plan of Project "core"`) {
		t.Fatalf("projects plan stdout = %q", stdout)
	}
	showJSON, _, err := executeCollectingInput(t, options, nil, "projects", "plan", "get", "core", "--output", "json")
	if err != nil || !strings.Contains(showJSON, "Edited from projects plan.") {
		t.Fatalf("plan show after projects plan = %s err %v", showJSON, err)
	}

	editJSON, _, err := executeCollectingInput(t, options,
		strings.NewReader("# core Plan\n\nvia parent stdin\n"),
		"projects", "plan", "-", "--output", "json")
	if err != nil {
		t.Fatalf("projects plan -: %v\n%s", err, editJSON)
	}
	if !strings.Contains(editJSON, `"operation":"projects.plan"`) || !strings.Contains(editJSON, `"status":"applied"`) {
		t.Fatalf("projects plan - JSON = %s", editJSON)
	}

	_, _, err = executeCollectingInput(t, options, nil, "projects", "plan", "gett")
	if err == nil || clierr.CodeOf(err) != clierr.InvalidUsage {
		t.Fatalf("plan subcommand typo = %v (code %q)", err, clierr.CodeOf(err))
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
		"tickets", "ask", "fix-auth", "-", "--as", "twt-local-01234567", "--output", "json")
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
		"tickets", "answer", "fix-auth", "-", "--output", "json")
	if err != nil {
		t.Fatalf("answer: %v\n%s", err, answerJSON)
	}
	if !strings.Contains(answerJSON, `"operation":"tickets.answer"`) || !strings.Contains(answerJSON, `"delivered":false`) {
		t.Fatalf("answer JSON = %s (stderr %s)", answerJSON, answerStderr)
	}
	showJSON, _, err := executeCollectingInput(t, options, nil, "tickets", "get", "fix-auth", "--output", "json")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(showJSON, `"status":"ready-for-agent"`) || !strings.Contains(showJSON, "### A ") ||
		!strings.Contains(showJSON, `"claimedBy":"twt-local-01234567"`) {
		t.Fatalf("show after answer = %s", showJSON)
	}
	applyJSON, _, err := executeCollectingInput(t, options,
		strings.NewReader(`{"operation":"tickets.ask","ticket":{"reference":"fix-auth","as":"twt-local-01234567","text":"Another question?"}}`),
		"apply", "-", "--dry-run", "--output", "json")
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
	if !strings.Contains(addJSON, `"operation":"tickets.pr.add"`) || !strings.Contains(addJSON, `"status":"applied"`) {
		t.Fatalf("pr add JSON = %s", addJSON)
	}
	showJSON, _, err := executeCollectingInput(t, options, nil, "tickets", "get", "fix-auth", "--output", "json")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(showJSON, `"pullRequests":["https://origin.cursor.com/acme/api/pull/7"]`) ||
		!strings.Contains(showJSON, `"status":"ready-for-agent"`) {
		t.Fatalf("show after pr add = %s", showJSON)
	}
	rmJSON, _, err := executeCollectingInput(t, options,
		strings.NewReader(`{"operation":"tickets.pr.rm","ticket":{"reference":"fix-auth","pullRequests":["https://origin.cursor.com/acme/api/pull/7"]}}`),
		"apply", "-", "--output", "json")
	if err != nil || !strings.Contains(rmJSON, `"status":"applied"`) {
		t.Fatalf("apply pr rm = %s err %v", rmJSON, err)
	}
}
