package ticket

import (
	"strings"
	"testing"

	"github.com/jpugliesi/tmux-worktree/internal/clierr"
	"github.com/jpugliesi/tmux-worktree/internal/domain"
)

func TestPlanSectionExtractsTheSectionBody(t *testing.T) {
	tests := []struct {
		name, body, want string
	}{
		{name: "missing section", body: "\n# T\n\n## What to build\n\nbody\n", want: ""},
		{name: "empty section", body: "\n# T\n\n## Plan\n", want: ""},
		{name: "plain body", body: "\n# T\n\n## Plan\n\nDo the thing.\n", want: "Do the thing."},
		{
			name: "keeps subsections",
			body: "\n# T\n\n## Plan\n\nold\n\n### detail\n\nx\n\n## Comments\n",
			want: "old\n\n### detail\n\nx",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := PlanSection(test.body); got != test.want {
				t.Fatalf("PlanSection = %q, want %q", got, test.want)
			}
		})
	}
}

func TestReplaceBodySection(t *testing.T) {
	tests := []struct {
		name, body, heading, content, want string
	}{
		{
			name:    "replaces an existing section and keeps subsections",
			body:    "\n# T\n\n## Plan\n\nold\n\n### detail\n\nx\n\n## Comments\n",
			heading: "Plan", content: "new plan",
			want: "\n# T\n\n## Plan\n\nnew plan\n\n## Comments\n",
		},
		{
			name:    "inserts a missing section before Comments",
			body:    "\n# T\n\n## What to build\n\nbody\n\n## Comments\n\nnote\n",
			heading: "Plan", content: "the plan",
			want: "\n# T\n\n## What to build\n\nbody\n\n## Plan\n\nthe plan\n\n## Comments\n\nnote\n",
		},
		{
			name:    "appends when Comments is missing",
			body:    "\n# T\n\n## What to build\n",
			heading: "Plan", content: "the plan",
			want: "\n# T\n\n## What to build\n\n## Plan\n\nthe plan\n",
		},
		{
			name:    "idempotent replace",
			body:    "\n# T\n\n## Plan\n\nsame\n",
			heading: "Plan", content: "same",
			want: "\n# T\n\n## Plan\n\nsame\n",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := replaceBodySection(test.body, test.heading, test.content)
			if got != test.want {
				t.Fatalf("replaceBodySection:\n---got---\n%q\n---want---\n%q", got, test.want)
			}
		})
	}
}

func TestAppendBodySectionKeepsFollowingSections(t *testing.T) {
	body := "\n# T\n\n## Comments\n\nfirst\n\n## Plan\n\nkeep\n"
	got := appendBodySection(body, "Comments", "second")
	if !strings.Contains(got, "## Comments\n\nfirst\n\nsecond\n") {
		t.Fatalf("comment not inside its section:\n%s", got)
	}
	if !strings.Contains(got, "## Plan\n\nkeep") {
		t.Fatalf("following section lost:\n%s", got)
	}
}

func TestCommentLandsInsideTheCommentsSection(t *testing.T) {
	service, _ := newTestService(t)
	if _, err := service.Init(false); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Create(CreateRequest{Title: "Fix auth", Status: domain.TicketReadyForAgent}, false); err != nil {
		t.Fatal(err)
	}
	if _, err := service.SetPlanSection("fix-auth", "", "Step one.", false); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Comment("fix-auth", "A note.", false); err != nil {
		t.Fatal(err)
	}
	shown, err := service.Show("fix-auth")
	if err != nil {
		t.Fatal(err)
	}
	comments := strings.Index(shown.Body, "## Comments")
	note := strings.Index(shown.Body, "A note.")
	plan := strings.Index(shown.Body, "## Plan")
	if comments == -1 || note < comments {
		t.Fatalf("comment not under Comments:\n%s", shown.Body)
	}
	if plan == -1 {
		t.Fatalf("plan section lost:\n%s", shown.Body)
	}
}

func TestSetPlanSectionGuardsTheClaim(t *testing.T) {
	service, _ := newTestService(t)
	if _, err := service.Init(false); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Create(CreateRequest{Title: "Fix auth", Status: domain.TicketReadyForAgent}, false); err != nil {
		t.Fatal(err)
	}
	if _, err := service.SetPlanSection("fix-auth", "", "Unclaimed plan.", false); err != nil {
		t.Fatalf("unclaimed plan write: %v", err)
	}
	if _, err := service.Claim("fix-auth", "worker-a", false); err != nil {
		t.Fatal(err)
	}
	if _, err := service.SetPlanSection("fix-auth", "worker-b", "steal", false); clierr.CodeOf(err) != clierr.Locked {
		t.Fatal("foreign plan write accepted")
	}
	if _, err := service.SetPlanSection("fix-auth", "worker-a", "Revised plan.", false); err != nil {
		t.Fatalf("claimant plan write: %v", err)
	}
	if _, err := service.SetPlanSection("fix-auth", "worker-a", "  ", false); clierr.CodeOf(err) != clierr.InvalidUsage {
		t.Fatal("empty plan accepted")
	}
	shown, err := service.Show("fix-auth")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(shown.Body, "## Plan\n\nRevised plan.") || strings.Contains(shown.Body, "Unclaimed plan.") {
		t.Fatalf("plan section not replaced:\n%s", shown.Body)
	}
}
