package domain

import (
	"fmt"
	"strings"
)

// DefaultBranchPattern is the branch pattern that twt uses when the Workspace
// Template does not set branch_pattern. Without a configured branch prefix it
// renders to the plain Workspace name.
const DefaultBranchPattern = "{prefix}{name}"

// RenderBranchPattern expands the branch-pattern tokens. {prefix} becomes the
// user branch prefix, {name} becomes the Workspace name, and {id8} becomes the
// first 8 characters of the Workspace ID.
func RenderBranchPattern(pattern, prefix, name, id8 string) string {
	return strings.NewReplacer("{prefix}", prefix, "{name}", name, "{id8}", id8).Replace(pattern)
}

// ValidateBranchName checks one Git branch name. Every source of a Workspace
// branch name (the --branch flag, a rendered branch_pattern, and the Workspace
// name) uses this one validator.
func ValidateBranchName(branch string) error {
	if branch == "" {
		return fmt.Errorf("the branch name is empty")
	}
	if strings.HasPrefix(branch, "-") {
		return fmt.Errorf("the branch name must not start with '-'")
	}
	if strings.HasPrefix(branch, "/") || strings.HasSuffix(branch, "/") {
		return fmt.Errorf("the branch name must not start or end with '/'")
	}
	if strings.HasSuffix(branch, ".") || strings.HasSuffix(branch, ".lock") {
		return fmt.Errorf("the branch name must not end with '.' or '.lock'")
	}
	for _, forbidden := range []string{"..", "//", "@{", " ", "~", "^", ":", "?", "*", "[", "\\"} {
		if strings.Contains(branch, forbidden) {
			return fmt.Errorf("the branch name must not contain %q", forbidden)
		}
	}
	for _, character := range branch {
		if character < 0x20 || character == 0x7f {
			return fmt.Errorf("the branch name must not contain control characters")
		}
	}
	return nil
}
