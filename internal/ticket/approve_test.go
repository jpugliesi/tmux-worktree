package ticket

import (
	"strings"
	"testing"

	"github.com/jpugliesi/tmux-worktree/internal/clierr"
	"github.com/jpugliesi/tmux-worktree/internal/domain"
)

func TestApproveActsAsTheAnswerOnAWaitingTicket(t *testing.T) {
	service := askFixture(t) // claimed fix-auth by twt-local-01234567
	if _, err := service.SetPlanSection("fix-auth", "twt-local-01234567", "1. Do the thing.", false); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Ask("fix-auth", "twt-local-01234567", "Plan ready for your approval.", false); err != nil {
		t.Fatal(err)
	}
	approved, err := service.Approve("fix-auth", "john", "ship it", false)
	if err != nil {
		t.Fatalf("Approve: %v", err)
	}
	if approved.PlanApprovedBy != "john" || approved.PlanApprovedAt == "" {
		t.Fatalf("approval stamp = %+v", approved)
	}
	if approved.Status != domain.TicketReadyForAgent {
		t.Fatalf("status = %q, want the pre-ask status restored", approved.Status)
	}
	if approved.ClaimedBy != "twt-local-01234567" {
		t.Fatalf("approve dropped the claim: %+v", approved)
	}
	if approved.AskStatus != "" {
		t.Fatalf("twt_ask_status not cleared: %q", approved.AskStatus)
	}
	shown, err := service.Show("fix-auth")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(shown.Body, "Plan approved.") || !strings.Contains(shown.Body, "ship it") {
		t.Fatalf("answer entry missing:\n%s", shown.Body)
	}
}

func TestApproveStampsAPlainPlanAndRecordsTheNote(t *testing.T) {
	service := askFixture(t)
	if _, err := service.SetPlanSection("fix-auth", "twt-local-01234567", "1. Do the thing.", false); err != nil {
		t.Fatal(err)
	}
	approved, err := service.Approve("fix-auth", "john", "small scope note", false)
	if err != nil {
		t.Fatalf("Approve: %v", err)
	}
	if approved.Status != domain.TicketReadyForAgent {
		t.Fatalf("approve changed the status: %q", approved.Status)
	}
	shown, err := service.Show("fix-auth")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(shown.Body, "small scope note") {
		t.Fatalf("note not recorded:\n%s", shown.Body)
	}
	// Dry run validates without a write.
	if _, err := service.Approve("fix-auth", "john", "", true); err != nil {
		t.Fatalf("dry-run approve on approved plan: %v", err)
	}
}

func TestApproveRequiresAPlanSection(t *testing.T) {
	service := askFixture(t)
	if _, err := service.Approve("fix-auth", "john", "", false); clierr.CodeOf(err) != clierr.PreconditionFailed {
		t.Fatalf("approve without plan = %v, want precondition_failed", err)
	}
	if _, err := service.Approve("fix-auth", "", "", false); clierr.CodeOf(err) != clierr.InvalidUsage {
		t.Fatalf("approve without approver = %v, want invalid_usage", err)
	}
}

func TestRequireApprovedPlanGatesDispatch(t *testing.T) {
	service := askFixture(t)
	shown, err := service.Show("fix-auth")
	if err != nil {
		t.Fatal(err)
	}
	// The skeleton body has no ## Plan section: dispatch is free.
	if err := RequireApprovedPlan(shown); err != nil {
		t.Fatalf("no-plan ticket gated: %v", err)
	}
	if _, err := service.SetPlanSection("fix-auth", "twt-local-01234567", "1. Do the thing.", false); err != nil {
		t.Fatal(err)
	}
	shown, err = service.Show("fix-auth")
	if err != nil {
		t.Fatal(err)
	}
	if err := RequireApprovedPlan(shown); clierr.CodeOf(err) != clierr.PreconditionFailed {
		t.Fatalf("unapproved plan = %v, want precondition_failed", err)
	}
	if _, err := service.Approve("fix-auth", "john", "", false); err != nil {
		t.Fatal(err)
	}
	shown, err = service.Show("fix-auth")
	if err != nil {
		t.Fatal(err)
	}
	if err := RequireApprovedPlan(shown); err != nil {
		t.Fatalf("approved plan gated: %v", err)
	}
}
