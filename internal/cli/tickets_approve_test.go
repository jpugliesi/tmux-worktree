package cli_test

import (
	"strings"
	"testing"
)

func TestTicketsApproveGatesAndAnswers(t *testing.T) {
	options, _ := ticketTestOptions(t)
	run := func(stdin string, args ...string) string {
		t.Helper()
		var input *strings.Reader
		if stdin != "" {
			input = strings.NewReader(stdin)
		}
		out, errOut, err := executeCollectingInput(t, options, input, args...)
		if err != nil {
			t.Fatalf("%v: %v\n%s%s", args, err, out, errOut)
		}
		return out
	}
	run("", "tickets", "init")
	run("", "tickets", "create", "Fix auth", "--status", "ready-for-agent")

	// Approve refuses a ticket without a ## Plan section.
	if out, _, err := executeCollectingInput(t, options, nil,
		"tickets", "approve", "fix-auth", "--as", "john", "--output", "json"); err == nil {
		t.Fatalf("approve without a plan succeeded: %s", out)
	}

	run("", "tickets", "claim", "fix-auth", "--as", "twt-local-01234567")
	run("1. Add the OAuth flow.", "tickets", "plan", "fix-auth", "-", "--as", "twt-local-01234567")
	run("Plan ready for your approval.", "tickets", "ask", "fix-auth", "-", "--as", "twt-local-01234567")

	approveJSON := run("Ship it.", "tickets", "approve", "fix-auth", "-", "--as", "john", "--output", "json")
	if !strings.Contains(approveJSON, `"operation":"tickets.approve"`) ||
		!strings.Contains(approveJSON, `"status":"applied"`) ||
		!strings.Contains(approveJSON, `"relay"`) {
		t.Fatalf("approve JSON = %s", approveJSON)
	}
	showJSON := run("", "tickets", "show", "fix-auth", "--output", "json")
	for _, want := range []string{
		`"planApprovedBy":"john"`, `"status":"ready-for-agent"`, "Plan approved.", "Ship it.",
	} {
		if !strings.Contains(showJSON, want) {
			t.Fatalf("show after approve lacks %s:\n%s", want, showJSON)
		}
	}

	// A plan rewrite clears the approval; the apply op re-approves.
	run("1. Use SAML instead.", "tickets", "plan", "fix-auth", "-", "--as", "twt-local-01234567")
	showJSON = run("", "tickets", "show", "fix-auth", "--output", "json")
	if strings.Contains(showJSON, "planApprovedBy") {
		t.Fatalf("plan rewrite kept the approval:\n%s", showJSON)
	}
	applyJSON := run(`{"operation":"tickets.approve","ticket":{"reference":"fix-auth","as":"john","note":"Round two."}}`,
		"apply", "-", "--output", "json")
	if !strings.Contains(applyJSON, `"status":"applied"`) {
		t.Fatalf("apply approve = %s", applyJSON)
	}
	showJSON = run("", "tickets", "show", "fix-auth", "--output", "json")
	if !strings.Contains(showJSON, `"planApprovedBy":"john"`) {
		t.Fatalf("apply approve did not stamp:\n%s", showJSON)
	}
}
