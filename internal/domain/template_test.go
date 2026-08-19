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
