package cli

import "testing"

func TestResolveProjectPickPrefersExactNames(t *testing.T) {
	lines := []string{"(none)", "change-monitor", "1"}
	tests := []struct {
		input string
		want  string
	}{
		{input: "(none)", want: "(none)"},
		{input: "change-monitor", want: "change-monitor"},
		{input: "0", want: "(none)"},
		{input: "1", want: "1"},
		{input: "new-work", want: "new-work"},
	}
	for _, test := range tests {
		got, err := resolveProjectPick(lines, test.input)
		if err != nil {
			t.Fatalf("resolveProjectPick(%q) = %v", test.input, err)
		}
		if got != test.want {
			t.Fatalf("resolveProjectPick(%q) = %q, want %q", test.input, got, test.want)
		}
	}
	if _, err := resolveProjectPick(lines, "9"); err == nil {
		t.Fatal("out-of-range number succeeded")
	}
	if _, err := resolveProjectPick(lines, ""); err == nil {
		t.Fatal("empty pick succeeded")
	}
}

func TestResolveFzfProjectChoicePrefersSelection(t *testing.T) {
	lines := []string{"(none)", "change-monitor"}
	got, err := resolveFzfProjectChoice(lines, "chan", "change-monitor")
	if err != nil || got != "change-monitor" {
		t.Fatalf("matched selection = %q, %v", got, err)
	}
	got, err = resolveFzfProjectChoice(lines, "new-work", "")
	if err != nil || got != "new-work" {
		t.Fatalf("unmatched query = %q, %v", got, err)
	}
	got, err = resolveFzfProjectChoice(lines, "(none)", "(none)")
	if err != nil || got != "(none)" {
		t.Fatalf("none selection = %q, %v", got, err)
	}
}

func TestParseYesDefault(t *testing.T) {
	tests := []struct {
		input string
		yes   bool
		ok    bool
	}{
		{input: "", yes: true, ok: true},
		{input: "Y", yes: true, ok: true},
		{input: "yes", yes: true, ok: true},
		{input: "n", yes: false, ok: true},
		{input: "No", yes: false, ok: true},
		{input: "yeah", ok: false},
		{input: "yep", ok: false},
	}
	for _, test := range tests {
		yes, ok := parseYesDefault(test.input)
		if yes != test.yes || ok != test.ok {
			t.Fatalf("parseYesDefault(%q) = %v, %v; want %v, %v", test.input, yes, ok, test.yes, test.ok)
		}
	}
}
