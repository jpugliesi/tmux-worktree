package domain_test

import (
	"sort"
	"strings"
	"testing"

	"github.com/jpugliesi/tmux-worktree/internal/domain"
)

func TestTicketStatusesSortedAndValid(t *testing.T) {
	statuses := domain.TicketStatuses()
	if len(statuses) != 6 {
		t.Fatalf("TicketStatuses returned %d statuses, want 6", len(statuses))
	}
	if !sort.StringsAreSorted(statuses) {
		t.Fatalf("TicketStatuses is not sorted: %v", statuses)
	}
	for _, status := range statuses {
		if !domain.ValidTicketStatus(domain.TicketStatus(status)) {
			t.Fatalf("listed status %q is not valid", status)
		}
	}
	for _, invalid := range []string{"", "open", "Done", "ready", "needs-triage "} {
		if domain.ValidTicketStatus(domain.TicketStatus(invalid)) {
			t.Fatalf("status %q must not be valid", invalid)
		}
	}
}

func TestSlugify(t *testing.T) {
	tests := []struct {
		title string
		want  string
	}{
		{"Reconnect Change Monitor VFS tools", "reconnect-change-monitor-vfs-tools"},
		{"fix the vfs tools", "fix-the-vfs-tools"},
		{"Fix: auth!! now", "fix-auth-now"},
		{"  --- hello ---  ", "hello"},
		{"already-a-slug", "already-a-slug"},
		{"MiXeD CaSe 42", "mixed-case-42"},
		{"café au lait", "caf-au-lait"},
		{"日本語のタイトル", ""},
		{"!!!", ""},
		{"", ""},
		{
			// 60-char cap cuts at the last hyphen before the limit.
			strings.Repeat("abcde ", 12), // slug would be 71 chars
			"abcde-abcde-abcde-abcde-abcde-abcde-abcde-abcde-abcde-abcde",
		},
		{
			// No hyphen before the limit: hard cut at 60.
			strings.Repeat("a", 70),
			strings.Repeat("a", 60),
		},
	}
	for _, test := range tests {
		if got := domain.Slugify(test.title); got != test.want {
			t.Errorf("Slugify(%q) = %q, want %q", test.title, got, test.want)
		}
	}
}

func TestSlugifyCapKeepsLimit(t *testing.T) {
	slug := domain.Slugify(strings.Repeat("word ", 40))
	if len(slug) > domain.TicketSlugMaxLength {
		t.Fatalf("slug %q is longer than %d", slug, domain.TicketSlugMaxLength)
	}
	if strings.HasSuffix(slug, "-") || strings.HasPrefix(slug, "-") {
		t.Fatalf("slug %q keeps a boundary hyphen", slug)
	}
}

func TestTicketValidate(t *testing.T) {
	valid := domain.Ticket{
		Slug:     "fix-auth",
		Title:    "Fix auth",
		Status:   domain.TicketNeedsTriage,
		Priority: 2,
	}
	valid.Labels = []string{"change-monitor", "dev-env"}
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid ticket rejected: %v", err)
	}
	tests := []struct {
		name   string
		mutate func(*domain.Ticket)
	}{
		{"empty title", func(t *domain.Ticket) { t.Title = " " }},
		{"unknown status", func(t *domain.Ticket) { t.Status = "open" }},
		{"empty status", func(t *domain.Ticket) { t.Status = "" }},
		{"priority low", func(t *domain.Ticket) { t.Priority = -1 }},
		{"priority high", func(t *domain.Ticket) { t.Priority = 5 }},
		{"slug uppercase", func(t *domain.Ticket) { t.Slug = "Fix-Auth" }},
		{"slug leading hyphen", func(t *domain.Ticket) { t.Slug = "-fix" }},
		{"slug empty", func(t *domain.Ticket) { t.Slug = "" }},
		{"slug too long", func(t *domain.Ticket) { t.Slug = strings.Repeat("a", 61) }},
		{"label uppercase", func(t *domain.Ticket) { t.Labels = []string{"Change-Monitor"} }},
		{"label space", func(t *domain.Ticket) { t.Labels = []string{"change monitor"} }},
		{"label empty", func(t *domain.Ticket) { t.Labels = []string{""} }},
		{"label too long", func(t *domain.Ticket) { t.Labels = []string{strings.Repeat("a", 61)} }},
		{"label repeated", func(t *domain.Ticket) { t.Labels = []string{"change-monitor", "change-monitor"} }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ticket := valid
			test.mutate(&ticket)
			if err := ticket.Validate(); err == nil {
				t.Fatal("Validate accepted an invalid ticket")
			}
		})
	}
}

func TestValidTicketLabel(t *testing.T) {
	for _, label := range []string{"change-monitor", "a", "dev-env", "origin-ui"} {
		if !domain.ValidTicketLabel(label) {
			t.Fatalf("label %q must be valid", label)
		}
	}
	for _, label := range []string{"", "Change-Monitor", "change monitor", "-lead", strings.Repeat("a", 61)} {
		if domain.ValidTicketLabel(label) {
			t.Fatalf("label %q must not be valid", label)
		}
	}
}
