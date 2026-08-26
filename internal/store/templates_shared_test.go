package store

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jpugliesi/tmux-worktree/internal/clierr"
	"github.com/jpugliesi/tmux-worktree/internal/domain"
)

func sharedStores(t *testing.T) (TemplateStore, string, string) {
	t.Helper()
	configDir := t.TempDir()
	sharedDir := filepath.Join(t.TempDir(), "templates")
	templates := NewTemplateStore(configDir).WithSharedDir(sharedDir)
	return templates, filepath.Join(configDir, "templates"), sharedDir
}

func TestSharedTemplateStoreWritesNewTemplatesToTheSharedRoot(t *testing.T) {
	templates, configTemplates, sharedDir := sharedStores(t)
	if err := templates.Create(domain.NewTemplate("product")); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := os.Stat(filepath.Join(sharedDir, "product.yaml")); err != nil {
		t.Fatalf("new template is not in the shared root: %v", err)
	}
	if _, err := os.Stat(filepath.Join(configTemplates, "product.yaml")); !os.IsNotExist(err) {
		t.Fatal("new template landed in the config dir")
	}
	// Create refuses a name that exists in any root.
	if err := templates.Create(domain.NewTemplate("product")); clierr.CodeOf(err) != clierr.AlreadyExists {
		t.Fatalf("duplicate create = %v, want already_exists", err)
	}
	names, err := templates.List()
	if err != nil || len(names) != 1 || names[0] != "product" {
		t.Fatalf("List = %v, %v", names, err)
	}
}

func TestSharedTemplateStoreConfigDirOverridesByName(t *testing.T) {
	templates, configTemplates, sharedDir := sharedStores(t)
	shared := domain.NewTemplate("product")
	shared.PoolDepth = 1
	override := domain.NewTemplate("product")
	override.PoolDepth = 4
	writeTemplateTo(t, sharedDir, shared)
	writeTemplateTo(t, configTemplates, override)

	loaded, err := templates.Load("product")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded.PoolDepth != 4 {
		t.Fatalf("Load did not prefer the config-dir override: %+v", loaded)
	}
	path, err := templates.Path("product")
	if err != nil || !strings.HasPrefix(path, configTemplates) {
		t.Fatalf("Path = %q, %v", path, err)
	}
	shadowed := templates.Shadowed()
	if len(shadowed) != 1 || shadowed[0] != "product" {
		t.Fatalf("Shadowed = %v", shadowed)
	}
	// Save edits the winning copy in place.
	loaded.PoolDepth = 2
	if err := templates.Save(loaded); err != nil {
		t.Fatalf("Save: %v", err)
	}
	reloaded, err := templates.Load("product")
	if err != nil || reloaded.PoolDepth != 2 {
		t.Fatalf("Save target wrong: %+v, %v", reloaded, err)
	}
	sharedCopy, err := NewTemplateStore(filepath.Dir(sharedDir)).Load("product")
	if err != nil || sharedCopy.PoolDepth != 1 {
		t.Fatalf("Save touched the shared copy: %+v, %v", sharedCopy, err)
	}
	// Delete refuses a shadowed name.
	if err := templates.Delete("product"); clierr.CodeOf(err) != clierr.PreconditionFailed {
		t.Fatalf("shadowed delete = %v, want precondition_failed", err)
	}
}

func writeTemplateTo(t *testing.T, dir string, template domain.Template) {
	t.Helper()
	data, err := EncodeTemplate(template)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, template.Name+".yaml"), data, 0o644); err != nil {
		t.Fatal(err)
	}
}
