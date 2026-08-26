package store

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jpugliesi/tmux-worktree/internal/domain"
)

func TestEnvironmentDigestIsStableForMapInsertionOrder(t *testing.T) {
	first := digestTemplate()
	first.Repositories[0].Remotes = map[string]string{
		"github": "https://github.com/example/app.git",
		"backup": "https://backup.example.com/app.git",
	}
	second := digestTemplate()
	second.Repositories[0].Remotes = make(map[string]string)
	second.Repositories[0].Remotes["backup"] = "https://backup.example.com/app.git"
	second.Repositories[0].Remotes["github"] = "https://github.com/example/app.git"

	firstDigest, err := EnvironmentDigest(first)
	if err != nil {
		t.Fatal(err)
	}
	secondDigest, err := EnvironmentDigest(second)
	if err != nil {
		t.Fatal(err)
	}
	if firstDigest != secondDigest {
		t.Fatalf("digests differ: %q != %q", firstDigest, secondDigest)
	}
}

func TestEnvironmentDigestIgnoresChangesThatKeepTheWorktreeSet(t *testing.T) {
	tests := []struct {
		name   string
		change func(*domain.Template)
	}{
		{"template name", func(template *domain.Template) { template.Name = "other" }},
		{"window name", func(template *domain.Template) { template.Repositories[0].WindowName = "changed" }},
		{"template initialization", func(template *domain.Template) {
			template.Initialize = &domain.InitializeSpec{Command: []string{"./init.sh"}, WorkingDirectory: "app"}
		}},
		{"pool depth", func(template *domain.Template) { template.PoolDepth = 3 }},
		{"session command", func(template *domain.Template) {
			template.Session = &domain.SessionSpec{Command: []string{"./scripts/layout.sh"}, CWD: "app"}
		}},
		{"branch pattern", func(template *domain.Template) { template.BranchPattern = "dev/{name}" }},
		{"Agent Session", func(template *domain.Template) {
			template.Agents = []domain.TemplateAgent{{Label: "ticket-plan", Provider: "codex", Start: []string{"codex", "prompt"}, Resume: []string{"codex"}}}
		}},
		{"local dispatch", func(template *domain.Template) {
			template.LocalDispatch = &domain.LocalDispatchSpec{Provider: "grok", Effort: "large", MaxConcurrency: 3}
		}},
	}
	base, err := EnvironmentDigest(digestTemplate())
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			template := digestTemplate()
			test.change(&template)
			digest, err := EnvironmentDigest(template)
			if err != nil {
				t.Fatal(err)
			}
			if digest != base {
				t.Fatalf("digest changed: %q != %q", digest, base)
			}
		})
	}
}

func TestEnvironmentDigestChangesWithTheWorktreeSet(t *testing.T) {
	tests := []struct {
		name   string
		change func(*domain.Template)
	}{
		{"repository name", func(template *domain.Template) { template.Repositories[0].Name = "other" }},
		{"clone URL", func(template *domain.Template) {
			template.Repositories[0].Clone.URL = "https://example.com/other.git"
		}},
		{"clone depth", func(template *domain.Template) { template.Repositories[0].Clone.Depth = 1 }},
		{"default branch", func(template *domain.Template) { template.Repositories[0].DefaultBranch = "main" }},
		{"remotes", func(template *domain.Template) {
			template.Repositories[0].Remotes = map[string]string{"github": "https://github.com/example/app.git"}
		}},
		{"repository initialization", func(template *domain.Template) {
			template.Repositories[0].Initialize = &domain.InitializeSpec{Command: []string{"./init.sh"}}
		}},
		{"extra repository", func(template *domain.Template) {
			template.Repositories = append(template.Repositories, domain.RepositorySpec{
				Name: "docs", Clone: domain.CloneSpec{URL: "https://example.com/docs.git"},
			})
		}},
	}
	base, err := EnvironmentDigest(digestTemplate())
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			template := digestTemplate()
			test.change(&template)
			digest, err := EnvironmentDigest(template)
			if err != nil {
				t.Fatal(err)
			}
			if digest == base {
				t.Fatalf("digest did not change: %q", digest)
			}
		})
	}
}

func TestLegacyTemplateDigestChangesWithTheWholeTemplate(t *testing.T) {
	first := digestTemplate()
	second := digestTemplate()
	second.Repositories[0].WindowName = "changed"

	firstDigest, err := LegacyTemplateDigest(first)
	if err != nil {
		t.Fatal(err)
	}
	secondDigest, err := LegacyTemplateDigest(second)
	if err != nil {
		t.Fatal(err)
	}
	if firstDigest == secondDigest {
		t.Fatalf("legacy digest did not change: %q", firstDigest)
	}
}

func TestDigestSetMatchesBothDigests(t *testing.T) {
	template := digestTemplate()
	digests, err := Digests(template)
	if err != nil {
		t.Fatal(err)
	}
	if digests.Environment == digests.Legacy {
		t.Fatal("environment and legacy digests are equal")
	}
	if !digests.Matches(digests.Environment) || !digests.Matches(digests.Legacy) {
		t.Fatalf("DigestSet does not match its own digests: %+v", digests)
	}
	if digests.Matches("") || digests.Matches("other") {
		t.Fatalf("DigestSet matches an unknown digest: %+v", digests)
	}
}

func TestTemplateCatalogDisposition(t *testing.T) {
	digests, err := Digests(digestTemplate())
	if err != nil {
		t.Fatal(err)
	}
	catalog := TemplateCatalog{
		"readable":   TemplateStatus{Digests: digests},
		"unreadable": TemplateStatus{Unreadable: true},
	}
	tests := []struct {
		name     string
		template string
		digest   string
		want     TemplateDisposition
	}{
		{"matching digest is current", "readable", digests.Environment, TemplateCurrent},
		{"legacy digest is current", "readable", digests.Legacy, TemplateCurrent},
		{"stale digest is obsolete", "readable", "stale", TemplateObsolete},
		{"missing template is obsolete", "missing", digests.Environment, TemplateObsolete},
		{"unreadable template keeps its environments", "unreadable", "stale", TemplateKeep},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := catalog.Disposition(test.template, test.digest); got != test.want {
				t.Fatalf("Disposition(%q, %q) = %v, want %v", test.template, test.digest, got, test.want)
			}
		})
	}
}

func TestLoadTemplateCatalogWarnsAboutUnreadableTemplates(t *testing.T) {
	configDir := t.TempDir()
	directory := filepath.Join(configDir, "templates")
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	valid := "version: 1\nname: good\nrepositories:\n  - name: app\n    clone: {url: https://example.com/app.git}\n"
	if err := os.WriteFile(filepath.Join(directory, "good.yaml"), []byte(valid), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "bad.yaml"), []byte("{not yaml"), 0o644); err != nil {
		t.Fatal(err)
	}

	catalog, warnings, err := LoadTemplateCatalog(configDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(catalog) != 2 {
		t.Fatalf("catalog = %+v", catalog)
	}
	if catalog["good"].Unreadable || catalog["good"].Digests.Environment == "" {
		t.Fatalf("good template status = %+v", catalog["good"])
	}
	if !catalog["bad"].Unreadable {
		t.Fatalf("bad template status = %+v", catalog["bad"])
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], `"bad"`) {
		t.Fatalf("warnings = %v", warnings)
	}
}

// A session command is presentation. An edit to it must keep each ready
// Prepared Environment of the Workspace Template usable.
func TestSessionCommandEditKeepsPreparedEnvironments(t *testing.T) {
	prepared, err := Digests(digestTemplate())
	if err != nil {
		t.Fatal(err)
	}
	edited := digestTemplate()
	edited.Session = &domain.SessionSpec{Command: []string{"./scripts/layout.sh"}}
	editedDigests, err := Digests(edited)
	if err != nil {
		t.Fatal(err)
	}
	if editedDigests.Environment != prepared.Environment {
		t.Fatalf("environment digest changed: %q != %q", editedDigests.Environment, prepared.Environment)
	}
	catalog := TemplateCatalog{"example": TemplateStatus{Digests: editedDigests}}
	if got := catalog.Disposition("example", prepared.Environment); got != TemplateCurrent {
		t.Fatalf("Disposition after a session command edit = %v, want %v", got, TemplateCurrent)
	}
}

// A branch pattern is presentation. An edit to it must keep each ready
// Prepared Environment of the Workspace Template claimable.
func TestBranchPatternEditKeepsPreparedEnvironments(t *testing.T) {
	prepared, err := Digests(digestTemplate())
	if err != nil {
		t.Fatal(err)
	}
	edited := digestTemplate()
	edited.BranchPattern = "{prefix}dev/{name}"
	editedDigests, err := Digests(edited)
	if err != nil {
		t.Fatal(err)
	}
	if editedDigests.Environment != prepared.Environment {
		t.Fatalf("environment digest changed: %q != %q", editedDigests.Environment, prepared.Environment)
	}
	catalog := TemplateCatalog{"example": TemplateStatus{Digests: editedDigests}}
	if got := catalog.Disposition("example", prepared.Environment); got != TemplateCurrent {
		t.Fatalf("Disposition after a branch pattern edit = %v, want %v", got, TemplateCurrent)
	}
}

func digestTemplate() domain.Template {
	return domain.Template{
		Version: domain.TemplateVersion,
		Name:    "example",
		Repositories: []domain.RepositorySpec{{
			Name:  "app",
			Clone: domain.CloneSpec{URL: "https://example.com/app.git"},
		}},
	}
}
