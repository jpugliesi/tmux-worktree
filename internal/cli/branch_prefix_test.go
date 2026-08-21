package cli

import (
	"os"
	"path/filepath"
	"testing"
)

func writePrefixConfig(t *testing.T, prefix string) string {
	t.Helper()
	configDir := t.TempDir()
	content := "branchPrefix: " + prefix + "\n"
	if err := os.WriteFile(filepath.Join(configDir, "config.yaml"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return configDir
}

func TestResolveBranchPrefixPrefersTheInjectedValue(t *testing.T) {
	t.Setenv("TWT_BRANCH_PREFIX", "env/")
	options := Options{ConfigDir: writePrefixConfig(t, "config/"), BranchPrefix: "injected/"}
	prefix, err := options.resolveBranchPrefix()
	if err != nil {
		t.Fatal(err)
	}
	if prefix != "injected/" {
		t.Fatalf("resolveBranchPrefix() = %q, want %q", prefix, "injected/")
	}
}

func TestResolveBranchPrefixPrefersTheEnvironmentOverTheConfig(t *testing.T) {
	t.Setenv("TWT_BRANCH_PREFIX", "env/")
	options := Options{ConfigDir: writePrefixConfig(t, "config/")}
	prefix, err := options.resolveBranchPrefix()
	if err != nil {
		t.Fatal(err)
	}
	if prefix != "env/" {
		t.Fatalf("resolveBranchPrefix() = %q, want %q", prefix, "env/")
	}
}

func TestResolveBranchPrefixReadsTheConfigWhenTheEnvironmentIsUnset(t *testing.T) {
	t.Setenv("TWT_BRANCH_PREFIX", "")
	options := Options{ConfigDir: writePrefixConfig(t, "config/")}
	prefix, err := options.resolveBranchPrefix()
	if err != nil {
		t.Fatal(err)
	}
	if prefix != "config/" {
		t.Fatalf("resolveBranchPrefix() = %q, want %q", prefix, "config/")
	}
}

func TestResolveBranchPrefixIsEmptyWithoutASource(t *testing.T) {
	t.Setenv("TWT_BRANCH_PREFIX", "")
	options := Options{ConfigDir: t.TempDir()}
	prefix, err := options.resolveBranchPrefix()
	if err != nil {
		t.Fatal(err)
	}
	if prefix != "" {
		t.Fatalf("resolveBranchPrefix() = %q, want an empty prefix", prefix)
	}
}
