package domain

import (
	"strings"
	"testing"
)

func TestTemplateRejectsCollidingRepositoryEnvironmentNames(t *testing.T) {
	template := Template{
		Version: TemplateVersion,
		Name:    "collision",
		Repositories: []RepositorySpec{
			{Name: "foo-bar", Clone: CloneSpec{URL: "https://example.com/one.git"}},
			{Name: "foo.bar", Clone: CloneSpec{URL: "https://example.com/two.git"}},
		},
	}
	if err := template.Validate(); err == nil || !strings.Contains(err.Error(), "same initialization environment name") {
		t.Fatalf("Template.Validate error = %v", err)
	}
}

func TestTemplatePoolDepth(t *testing.T) {
	template := Template{Version: TemplateVersion, Name: "example"}
	if depth := template.EffectivePoolDepth(); depth != 1 {
		t.Fatalf("EffectivePoolDepth() with no value = %d, want 1", depth)
	}
	if err := template.Validate(); err != nil {
		t.Fatalf("Validate() with no pool depth error = %v", err)
	}

	template.PoolDepth = 3
	if depth := template.EffectivePoolDepth(); depth != 3 {
		t.Fatalf("EffectivePoolDepth() = %d, want 3", depth)
	}
	if err := template.Validate(); err != nil {
		t.Fatalf("Validate() with pool depth 3 error = %v", err)
	}

	template.PoolDepth = -1
	if err := template.Validate(); err == nil || !strings.Contains(err.Error(), "pool_depth") {
		t.Fatalf("Validate() with a negative pool depth error = %v", err)
	}
}
