package cli_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jpugliesi/tmux-worktree/internal/cli"
)

// twtHomeOptions builds Options with a shared twt home and no explicit
// tickets home, so the home layout resolves everything.
func twtHomeOptions(t *testing.T) (cli.Options, string) {
	t.Helper()
	// The developer shell may export the real vault; the empty values keep
	// resolution inside the test home.
	t.Setenv("TWT_TICKETS_HOME", "")
	t.Setenv("TWT_HOME", "")
	root := t.TempDir()
	home := filepath.Join(root, "twt-home")
	return cli.Options{
		ConfigDir: filepath.Join(root, "config"),
		StateDir:  filepath.Join(root, "state"),
		DataDir:   filepath.Join(root, "data"),
		Home:      home,
	}, home
}

func TestTwtHomeResolvesTicketsAndSharedTemplates(t *testing.T) {
	options, home := twtHomeOptions(t)

	// Tickets land under <home>/tickets.
	if _, _, err := executeCollectingInput(t, options, nil, "tickets", "init"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(home, "tickets", "index.md")); err != nil {
		t.Fatalf("tickets did not land under the home: %v", err)
	}

	// New templates land under <home>/templates.
	if _, _, err := executeCollectingInput(t, options, nil, "templates", "create", "product"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(home, "templates", "product.yaml")); err != nil {
		t.Fatalf("template did not land in the shared root: %v", err)
	}
	list, _, err := executeCollectingInput(t, options, nil, "templates", "list", "--output", "json")
	if err != nil || !strings.Contains(list, `"product"`) {
		t.Fatalf("templates list = %s, %v", list, err)
	}

	// The resolved config reports the home.
	config, _, err := executeCollectingInput(t, options, nil, "config", "--output", "json")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"key":"home"`, filepath.ToSlash(home)} {
		if !strings.Contains(config, want) {
			t.Fatalf("config lacks %s:\n%s", want, config)
		}
	}
}

func TestTwtHomeDoctorReportsShadowedTemplates(t *testing.T) {
	options, home := twtHomeOptions(t)
	if _, _, err := executeCollectingInput(t, options, nil, "templates", "create", "product"); err != nil {
		t.Fatal(err)
	}
	// A config-dir copy of the same name shadows the shared one.
	sharedCopy, err := os.ReadFile(filepath.Join(home, "templates", "product.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	configTemplates := filepath.Join(options.ConfigDir, "templates")
	if err := os.MkdirAll(configTemplates, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configTemplates, "product.yaml"), sharedCopy, 0o644); err != nil {
		t.Fatal(err)
	}
	doctor, _, err := executeCollectingInput(t, options, nil, "doctor", "--output", "json")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(doctor, "template-shadow:product") {
		t.Fatalf("doctor does not report the shadowed template:\n%s", doctor)
	}
}

func TestExplicitTicketsHomeWinsOverTheTwtHome(t *testing.T) {
	options, _ := twtHomeOptions(t)
	explicit := filepath.Join(t.TempDir(), "vault-tickets")
	options.TicketsHome = explicit
	if _, _, err := executeCollectingInput(t, options, nil, "tickets", "init"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(explicit, "index.md")); err != nil {
		t.Fatalf("explicit tickets home was not used: %v", err)
	}
}
