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

	// No cursor_cloud block: the default backend is local.
	dryJSON, _, err := executeCollectingInput(t, options, nil,
		"tickets", "dispatch", "fix-auth", "--dry-run", "--output", "json")
	if err != nil {
		t.Fatalf("local dry-run dispatch: %v\n%s", err, dryJSON)
	}
	for _, want := range []string{`"backend":"local"`, `"status":"valid"`, `"provider":"grok"`, `"agentLabel":"ticket-impl"`} {
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

	// An explicit cloud backend without cursor_cloud settings fails.
	_, _, err = executeCollectingInput(t, options, nil,
		"tickets", "dispatch", "fix-auth", "--backend", "cursor-cloud", "--dry-run", "--output", "json")
	if err == nil || !strings.Contains(err.Error(), "cursor_cloud") {
		t.Fatalf("cloud backend on a local-only Template error = %v", err)
	}

	// An unknown backend is invalid usage.
	_, _, err = executeCollectingInput(t, options, nil,
		"tickets", "dispatch", "fix-auth", "--backend", "codespaces", "--dry-run", "--output", "json")
	if err == nil || !strings.Contains(err.Error(), "unsupported dispatch backend") {
		t.Fatalf("unknown backend error = %v", err)
	}
}

func TestApplyTicketsDispatchAcceptsTheBackendField(t *testing.T) {
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
		strings.NewReader(`{"operation":"tickets.dispatch","ticket":{"reference":"fix-auth","backend":"local"}}`),
		"apply", "--stdin", "--dry-run", "--output", "json")
	if err != nil {
		t.Fatalf("apply local dispatch dry run: %v\n%s", err, applyJSON)
	}
	if !strings.Contains(applyJSON, `"backend":"local"`) || !strings.Contains(applyJSON, `"status":"valid"`) {
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
	if strings.Contains(syncJSON, `"cursor-cloud"`) {
		t.Fatalf("local-only sync reports a cloud backend:\n%s", syncJSON)
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

func TestTicketsSyncReportsCloudCapacityWithoutPendingSessions(t *testing.T) {
	options, _ := ticketTestOptions(t)
	writeTemplateFile(t, options.ConfigDir, domain.Template{
		Version: domain.TemplateVersion,
		Name:    "product",
		Repositories: []domain.RepositorySpec{{
			Name: "api", Clone: domain.CloneSpec{URL: "https://github.com/acme/api.git"}, DefaultBranch: "main",
		}},
		CursorCloud: &domain.CursorCloudSpec{MaxConcurrency: 3},
	})
	if _, _, err := executeCollectingInput(t, options, nil, "tickets", "init"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := executeCollectingInput(t, options, nil, "projects", "create", "core", "--template", "product"); err != nil {
		t.Fatal(err)
	}
	// No pending cloud sessions and no harness installed: sync still
	// reports the configured cloud capacity.
	syncJSON, _, err := executeCollectingInput(t, options, nil,
		"tickets", "sync", "--project", "core", "--output", "json")
	if err != nil {
		t.Fatalf("sync with cloud config: %v\n%s", err, syncJSON)
	}
	if !strings.Contains(syncJSON, `"cursor-cloud":{"capacity":{"maximum":3,"active":0,"available":3,"known":true}`) {
		t.Fatalf("sync JSON lacks the cloud capacity block:\n%s", syncJSON)
	}
}
