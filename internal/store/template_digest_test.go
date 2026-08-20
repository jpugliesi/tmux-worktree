package store

import (
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
