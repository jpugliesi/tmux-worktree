package cli_test

import (
	"context"
	"os/exec"
	"slices"
	"strings"
	"testing"
	"time"
)

// runCommand runs a bounded test subprocess. Manual tmux commands always use
// an empty config, so a private test server cannot load the user's plug-ins.
func runCommand(t *testing.T, dir, name string, args ...string) string {
	t.Helper()
	const timeout = 30 * time.Second
	if name == "tmux" && !slices.Contains(args, "-f") {
		args = append([]string{"-f", "/dev/null"}, args...)
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	command := exec.CommandContext(ctx, name, args...)
	command.WaitDelay = time.Second
	command.Dir = dir
	output, err := command.CombinedOutput()
	if ctx.Err() == context.DeadlineExceeded {
		t.Fatalf("%s %s: timed out after %s\n%s", name, strings.Join(args, " "), timeout, output)
	}
	if err != nil {
		t.Fatalf("%s %s: %v\n%s", name, strings.Join(args, " "), err, output)
	}
	return strings.TrimSpace(string(output))
}
