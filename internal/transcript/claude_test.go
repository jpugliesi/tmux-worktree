package transcript

import "testing"

func TestEncodeClaudeProjectReplacesEveryNonAlphanumericCharacter(t *testing.T) {
	tests := []struct {
		path string
		want string
	}{
		{path: "/Users/john.pugliesi/code/tmux-worktree", want: "-Users-john-pugliesi-code-tmux-worktree"},
		{path: "/tmp/a.b/repo_1", want: "-tmp-a-b-repo-1"},
	}
	for _, test := range tests {
		if got := encodeClaudeProject(test.path); got != test.want {
			t.Fatalf("encodeClaudeProject(%q) = %q, want %q", test.path, got, test.want)
		}
	}
}
