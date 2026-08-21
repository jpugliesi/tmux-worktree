package domain

import (
	"strings"
	"testing"
)

func TestRenderBranchPattern(t *testing.T) {
	tests := []struct {
		name    string
		pattern string
		prefix  string
		want    string
	}{
		{"default pattern without a prefix", DefaultBranchPattern, "", "fix-auth"},
		{"default pattern with a prefix", DefaultBranchPattern, "jpugliesi/", "jpugliesi/fix-auth"},
		{"custom pattern", "dev/{name}", "", "dev/fix-auth"},
		{"custom pattern with the prefix and the id", "{prefix}dev/{name}-{id8}", "jpugliesi/", "jpugliesi/dev/fix-auth-0123abcd"},
		{"pattern without tokens for the prefix", "dev/{name}", "jpugliesi/", "dev/fix-auth"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := RenderBranchPattern(test.pattern, test.prefix, "fix-auth", "0123abcd")
			if got != test.want {
				t.Fatalf("RenderBranchPattern(%q, %q) = %q, want %q", test.pattern, test.prefix, got, test.want)
			}
		})
	}
}

func TestValidateBranchName(t *testing.T) {
	valid := []string{"fix-auth", "feature/custom", "twt/fix-auth-0123abcd", "a.b_c-1"}
	for _, branch := range valid {
		if err := ValidateBranchName(branch); err != nil {
			t.Fatalf("ValidateBranchName(%q) = %v, want nil", branch, err)
		}
	}
	invalid := []string{
		"",
		"-leading-dash",
		"/leading-slash",
		"trailing-slash/",
		"double//slash",
		"double..dot",
		"at@{brace",
		"a space",
		"trailing-dot.",
		"name.lock",
		"tab\tcharacter",
		"tilde~1",
		"caret^",
		"colon:name",
		"quest?ion",
		"aster*isk",
		"brack[et",
		"back\\slash",
	}
	for _, branch := range invalid {
		if err := ValidateBranchName(branch); err == nil {
			t.Fatalf("ValidateBranchName(%q) = nil, want an error", branch)
		}
	}
}

func TestTemplateValidatesTheBranchPattern(t *testing.T) {
	tests := []struct {
		name    string
		pattern string
		wantErr string
	}{
		{"empty pattern is valid", "", ""},
		{"name token only", "{name}", ""},
		{"directory pattern", "dev/{name}", ""},
		{"prefix and id tokens", "{prefix}dev/{name}-{id8}", ""},
		{"missing name token", "dev/{id8}", "does not contain the {name} token"},
		{"double dot", "dev..{name}", "invalid branch name"},
		{"space", "dev {name}", "invalid branch name"},
		{"leading dash", "-{name}", "invalid branch name"},
		{"at brace", "@{{name}", "invalid branch name"},
		{"trailing slash after an empty prefix render", "{prefix}/{name}", "invalid branch name"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			template := Template{
				Version: TemplateVersion,
				Name:    "example",
				Repositories: []RepositorySpec{{
					Name:  "app",
					Clone: CloneSpec{URL: "https://example.com/app.git"},
				}},
				BranchPattern: test.pattern,
			}
			err := template.Validate()
			if test.wantErr == "" {
				if err != nil {
					t.Fatalf("Template.Validate() = %v, want nil", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), test.wantErr) {
				t.Fatalf("Template.Validate() = %v, want %q", err, test.wantErr)
			}
		})
	}
}
