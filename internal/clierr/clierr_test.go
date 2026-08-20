package clierr_test

import (
	"errors"
	"fmt"
	"testing"

	"github.com/jpugliesi/tmux-worktree/internal/clierr"
)

func TestCodeOfAndExitCode(t *testing.T) {
	tests := []struct {
		name string
		err  error
		code clierr.Code
		exit int
	}{
		{"nil", nil, clierr.Internal, 0},
		{"plain error", errors.New("boom"), clierr.Internal, 1},
		{"not found", clierr.New(clierr.NotFound, "Project %q does not exist", "x"), clierr.NotFound, 3},
		{"already exists", clierr.New(clierr.AlreadyExists, "exists"), clierr.AlreadyExists, 3},
		{"precondition failed", clierr.New(clierr.PreconditionFailed, "not ready"), clierr.PreconditionFailed, 3},
		{"locked", clierr.New(clierr.Locked, "busy"), clierr.Locked, 3},
		{"unsafe state", clierr.New(clierr.UnsafeState, "dirty"), clierr.UnsafeState, 3},
		{"invalid usage", clierr.New(clierr.InvalidUsage, "bad flag"), clierr.InvalidUsage, 2},
		{"internal", clierr.New(clierr.Internal, "broken"), clierr.Internal, 1},
		{"wrapped in fmt", fmt.Errorf("context: %w", clierr.New(clierr.NotFound, "missing")), clierr.NotFound, 3},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if test.err != nil && clierr.CodeOf(test.err) != test.code {
				t.Fatalf("CodeOf = %q, want %q", clierr.CodeOf(test.err), test.code)
			}
			if clierr.ExitCode(test.err) != test.exit {
				t.Fatalf("ExitCode = %d, want %d", clierr.ExitCode(test.err), test.exit)
			}
		})
	}
}

func TestWithHintPropagatesThroughWrapping(t *testing.T) {
	err := clierr.WithHint(clierr.New(clierr.PreconditionFailed, "Project %q is archived", "fix-auth"),
		"Run 'twt2 projects open %s' to open the Project.", "fix-auth")
	if err.Error() != `Project "fix-auth" is archived` {
		t.Fatalf("message = %q", err.Error())
	}
	wrapped := fmt.Errorf("open: %w", err)
	if clierr.HintOf(wrapped) != "Run 'twt2 projects open fix-auth' to open the Project." {
		t.Fatalf("hint = %q", clierr.HintOf(wrapped))
	}
	if clierr.HintOf(errors.New("plain")) != "" {
		t.Fatal("plain error returned a hint")
	}
}

func TestWrapKeepsSentinelErrorsVisible(t *testing.T) {
	sentinel := errors.New("lock is held")
	err := clierr.Wrap(clierr.Locked, fmt.Errorf("%w: environment %q", sentinel, "x"))
	if !errors.Is(err, sentinel) {
		t.Fatal("errors.Is lost the wrapped sentinel")
	}
	if clierr.CodeOf(err) != clierr.Locked {
		t.Fatalf("CodeOf = %q", clierr.CodeOf(err))
	}
	if err.Error() != `lock is held: environment "x"` {
		t.Fatalf("message = %q", err.Error())
	}
}
