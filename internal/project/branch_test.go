package project

import (
	"strings"
	"testing"

	"github.com/jpugliesi/tmux-worktree/internal/clierr"
	"github.com/jpugliesi/tmux-worktree/internal/domain"
)

func branchTestService(t *testing.T) (*Service, *[]string) {
	t.Helper()
	messages := &[]string{}
	service := NewService(Options{
		StateDir: t.TempDir(),
		DataDir:  t.TempDir(),
		Progress: func(message string) { *messages = append(*messages, message) },
	})
	return service, messages
}

func branchTestTemplate() domain.Template {
	return domain.Template{
		Version: domain.TemplateVersion,
		Name:    "example",
		Repositories: []domain.RepositorySpec{{
			Name:          "app",
			Clone:         domain.CloneSpec{URL: "https://example.com/app.git"},
			DefaultBranch: "main",
		}},
	}
}

const branchTestProjectID = "0123abcd-project-id"

func TestResolveProjectBranchOrder(t *testing.T) {
	tests := []struct {
		name    string
		pattern string
		opts    CreateOptions
		want    string
	}{
		{"the default is the Project name", "", CreateOptions{}, "fix-auth"},
		{"the branch prefix joins the default", "", CreateOptions{BranchPrefix: "jpugliesi/"}, "jpugliesi/fix-auth"},
		{"the template pattern renders", "dev/{name}", CreateOptions{}, "dev/fix-auth"},
		{"the template pattern uses the prefix and the id", "{prefix}dev/{name}-{id8}", CreateOptions{BranchPrefix: "jpugliesi/"}, "jpugliesi/dev/fix-auth-0123abcd"},
		{"the --branch flag wins and ignores the prefix", "dev/{name}", CreateOptions{Branch: "feature/custom", BranchPrefix: "jpugliesi/"}, "feature/custom"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			service, _ := branchTestService(t)
			template := branchTestTemplate()
			template.BranchPattern = test.pattern
			got, err := service.resolveProjectBranch("fix-auth", branchTestProjectID, template, test.opts)
			if err != nil {
				t.Fatalf("resolveProjectBranch() error = %v", err)
			}
			if got != test.want {
				t.Fatalf("resolveProjectBranch() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestResolveProjectBranchRejectsTheRepositoryDefaultBranch(t *testing.T) {
	tests := []struct {
		name string
		opts CreateOptions
	}{
		{"a Project named like the default branch", CreateOptions{}},
		{"the --branch flag names the default branch", CreateOptions{Branch: "main"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			service, _ := branchTestService(t)
			_, err := service.resolveProjectBranch("main", branchTestProjectID, branchTestTemplate(), test.opts)
			if err == nil || !strings.Contains(err.Error(), "default branch") {
				t.Fatalf("resolveProjectBranch() error = %v, want a default-branch refusal", err)
			}
			if clierr.CodeOf(err) != clierr.InvalidUsage {
				t.Fatalf("resolveProjectBranch() code = %q, want %q", clierr.CodeOf(err), clierr.InvalidUsage)
			}
		})
	}
}

func TestResolveProjectBranchRejectsAnInvalidBranchName(t *testing.T) {
	service, _ := branchTestService(t)
	_, err := service.resolveProjectBranch("fix-auth", branchTestProjectID, branchTestTemplate(), CreateOptions{Branch: "bad name"})
	if err == nil || clierr.CodeOf(err) != clierr.InvalidUsage {
		t.Fatalf("resolveProjectBranch() error = %v, want invalid_usage", err)
	}
}

func TestResolveProjectBranchNamesTheBranchPrefixSource(t *testing.T) {
	service, _ := branchTestService(t)
	_, err := service.resolveProjectBranch("fix-auth", branchTestProjectID, branchTestTemplate(), CreateOptions{BranchPrefix: "bad prefix "})
	if err == nil || !strings.Contains(err.Error(), `branch prefix "bad prefix "`) {
		t.Fatalf("resolveProjectBranch() error = %v, want the branch-prefix cause", err)
	}
	if clierr.CodeOf(err) != clierr.InvalidUsage {
		t.Fatalf("resolveProjectBranch() code = %q, want %q", clierr.CodeOf(err), clierr.InvalidUsage)
	}
	if hint := clierr.HintOf(err); !strings.Contains(hint, "TWT_BRANCH_PREFIX") || !strings.Contains(hint, "branchPrefix") {
		t.Fatalf("resolveProjectBranch() hint = %q, want the prefix sources", hint)
	}
}

func TestResolveProjectBranchFallsBackOnACollision(t *testing.T) {
	service, messages := branchTestService(t)
	template := branchTestTemplate()
	cachePath := service.cachePath("app", template.Repositories[0].Clone.URL)
	testGit(t, "", "init", "-q", "-b", "main", cachePath)
	testGit(t, cachePath, "config", "user.name", "twt test")
	testGit(t, cachePath, "config", "user.email", "test@example.com")
	testGit(t, cachePath, "commit", "-q", "--allow-empty", "-m", "first")
	testGit(t, cachePath, "branch", "fix-auth")

	got, err := service.resolveProjectBranch("fix-auth", branchTestProjectID, template, CreateOptions{})
	if err != nil {
		t.Fatalf("resolveProjectBranch() error = %v", err)
	}
	if got != "twt/fix-auth-0123abcd" {
		t.Fatalf("collision fallback = %q, want %q", got, "twt/fix-auth-0123abcd")
	}
	want := `Branch "fix-auth" exists. twt uses "twt/fix-auth-0123abcd".`
	if len(*messages) != 1 || (*messages)[0] != want {
		t.Fatalf("progress = %v, want %q", *messages, want)
	}
}
