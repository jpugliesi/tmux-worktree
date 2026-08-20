package store

import (
	"testing"

	"github.com/jpugliesi/tmux-worktree/internal/domain"
)

func TestTemplateDigestIsStableForMapInsertionOrder(t *testing.T) {
	first := digestTemplate()
	first.Repositories[0].Remotes = map[string]string{
		"github": "https://github.com/example/app.git",
		"backup": "https://backup.example.com/app.git",
	}
	second := digestTemplate()
	second.Repositories[0].Remotes = make(map[string]string)
	second.Repositories[0].Remotes["backup"] = "https://backup.example.com/app.git"
	second.Repositories[0].Remotes["github"] = "https://github.com/example/app.git"

	firstDigest, err := TemplateDigest(first)
	if err != nil {
		t.Fatal(err)
	}
	secondDigest, err := TemplateDigest(second)
	if err != nil {
		t.Fatal(err)
	}
	if firstDigest != secondDigest {
		t.Fatalf("digests differ: %q != %q", firstDigest, secondDigest)
	}
}

func TestTemplateDigestChangesWithTheTemplate(t *testing.T) {
	first := digestTemplate()
	second := digestTemplate()
	second.Repositories[0].Clone.Depth = 1

	firstDigest, err := TemplateDigest(first)
	if err != nil {
		t.Fatal(err)
	}
	secondDigest, err := TemplateDigest(second)
	if err != nil {
		t.Fatal(err)
	}
	if firstDigest == secondDigest {
		t.Fatalf("digest did not change: %q", firstDigest)
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
