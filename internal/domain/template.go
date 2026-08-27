package domain

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/jpugliesi/tmux-worktree/internal/agentprovider"
)

const TemplateVersion = 1

const (
	DispatchEffortSmall  = "small"
	DispatchEffortMedium = "medium"
	DispatchEffortLarge  = "large"
	DispatchEffortXLarge = "xlarge"
)

var templateResourceName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

type Template struct {
	Version      int              `yaml:"version" json:"version"`
	Name         string           `yaml:"name" json:"name"`
	Repositories []RepositorySpec `yaml:"repositories" json:"repositories"`
	Initialize   *InitializeSpec  `yaml:"initialize,omitempty" json:"initialize,omitempty"`
	// Session is the command that twt runs each time it creates the tmux
	// session of a Workspace. Use it to lay out the windows and the panes.
	Session *SessionSpec `yaml:"session,omitempty" json:"session,omitempty"`
	// PoolDepth is the number of ready Prepared Environments to keep for this
	// Workspace Template. A value of 0 uses the default depth of 1.
	PoolDepth int `yaml:"pool_depth,omitempty" json:"poolDepth,omitempty"`
	// BranchPattern is the default Workspace branch name of this Workspace
	// Template. The tokens {prefix}, {name}, and {id8} expand to the user
	// branch prefix, the Workspace name, and the first 8 characters of the
	// Workspace ID. An empty value uses DefaultBranchPattern. The pattern is
	// presentation only: it does not change the Prepared Environment digest.
	BranchPattern string `yaml:"branch_pattern,omitempty" json:"branchPattern,omitempty"`
	// Agents are the Agent Sessions that each new Workspace gets.
	Agents []TemplateAgent `yaml:"agents,omitempty" json:"agents,omitempty"`
	// LocalDispatch configures local implementation dispatch for Tickets that
	// use this Workspace Template. It does not change Workspace preparation
	// or the Prepared Environment digest.
	LocalDispatch *LocalDispatchSpec `yaml:"local_dispatch,omitempty" json:"localDispatch,omitempty"`
}

// LocalDispatchSpec declares the defaults for local implementation dispatch.
// Empty fields fall back to the machine ticketAgent config, so a shared
// Template normally leaves provider unset and each machine selects an
// installed provider.
type LocalDispatchSpec struct {
	Provider       string `yaml:"provider,omitempty" json:"provider,omitempty"`
	Effort         string `yaml:"effort,omitempty" json:"effort,omitempty"`
	Instructions   string `yaml:"instructions,omitempty" json:"instructions,omitempty"`
	MaxConcurrency int    `yaml:"max_concurrency,omitempty" json:"maxConcurrency,omitempty"`
	// Stacking lets dispatch start a stack-ready Ticket (every open blocker
	// in review with a pull request) from the blocker's branch. v1 targets
	// origin-host repositories, where 'origin pr create --stack-on' makes
	// the dependent pull request a first-class stack member.
	Stacking bool `yaml:"stacking,omitempty" json:"stacking,omitempty"`
}

// EffectiveProvider returns the Template provider or the fallback. The
// receiver may be nil.
func (c *LocalDispatchSpec) EffectiveProvider(fallback string) string {
	if c == nil || strings.TrimSpace(c.Provider) == "" {
		return fallback
	}
	return c.Provider
}

// EffectiveEffort returns the Template effort or the fallback. The receiver
// may be nil.
func (c *LocalDispatchSpec) EffectiveEffort(fallback string) string {
	if c == nil || strings.TrimSpace(c.Effort) == "" {
		return fallback
	}
	return c.Effort
}

// EffectiveInstructions returns the Template instructions or the fallback.
// The receiver may be nil.
func (c *LocalDispatchSpec) EffectiveInstructions(fallback string) string {
	if c == nil || strings.TrimSpace(c.Instructions) == "" {
		return fallback
	}
	return c.Instructions
}

// EffectiveMaxConcurrency returns the Project budget for concurrent local
// dispatch Sessions. The receiver may be nil.
func (c *LocalDispatchSpec) EffectiveMaxConcurrency() int {
	if c == nil || c.MaxConcurrency == 0 {
		return DefaultLocalDispatchMaxConcurrency
	}
	return c.MaxConcurrency
}

// TemplateAgent declares one Agent Session that twt registers and starts
// during Workspace setup.
type TemplateAgent struct {
	Label    string `yaml:"label" json:"label"`
	Provider string `yaml:"provider" json:"provider"`
	// Start is the command that twt runs in a new Workspace window.
	Start []string `yaml:"start" json:"start"`
	// Resume is the prompt-free fallback command for a stopped Agent Session.
	// An empty value keeps the legacy behavior and uses Start.
	Resume []string `yaml:"resume,omitempty" json:"resume,omitempty"`
	// PreferProviderResume makes a verified linked provider session take
	// precedence over Resume.
	PreferProviderResume bool `yaml:"prefer_provider_resume,omitempty" json:"preferProviderResume,omitempty"`
	// Env sets KEY=VALUE pairs in the Agent Session window, so the agent's
	// commands see them regardless of the tmux server environment.
	Env []string `yaml:"env,omitempty" json:"env,omitempty"`
}

// AgentProviders are the supported Agent Session provider names.
var AgentProviders = agentprovider.Names()

// ValidAgentProvider reports whether the provider name is supported.
func ValidAgentProvider(provider string) bool {
	_, valid := agentprovider.Lookup(provider)
	return valid
}

// EffectivePoolDepth returns the number of ready Prepared Environments to keep.
func (t Template) EffectivePoolDepth() int {
	if t.PoolDepth < 1 {
		return 1
	}
	return t.PoolDepth
}

// Warnings returns the advisory messages for a valid Workspace Template. A
// warning does not stop a mutation.
func (t Template) Warnings() []string {
	if len(t.Repositories) == 0 {
		return []string{"The Workspace Template has no repositories."}
	}
	return nil
}

type RepositorySpec struct {
	Name          string            `yaml:"name" json:"name"`
	Clone         CloneSpec         `yaml:"clone" json:"clone"`
	Remotes       map[string]string `yaml:"remotes,omitempty" json:"remotes,omitempty"`
	DefaultBranch string            `yaml:"default_branch,omitempty" json:"defaultBranch,omitempty"`
	WindowName    string            `yaml:"window_name,omitempty" json:"windowName,omitempty"`
	Initialize    *InitializeSpec   `yaml:"initialize,omitempty" json:"initialize,omitempty"`
	Recycle       *RecycleSpec      `yaml:"recycle,omitempty" json:"recycle,omitempty"`
}

// RecycleSpec declares one command that cleans reusable ignored state before
// a Workspace releases its physical worktree.
type RecycleSpec struct {
	Command []string `yaml:"command" json:"command"`
}

type CloneSpec struct {
	URL   string `yaml:"url" json:"url"`
	Depth int    `yaml:"depth,omitempty" json:"depth,omitempty"`
}

// SessionSpec declares one command that twt runs each time it creates the
// tmux session of a Workspace. twt runs the command after it makes the session
// and one window for each repository. twt never runs it against a session
// that is already live, so the command cannot disturb panes that the user
// arranged.
type SessionSpec struct {
	Command []string `yaml:"command" json:"command"`
	// CWD is the working directory of the command. It is relative to the
	// Workspace root. An empty value uses the Workspace root.
	CWD string `yaml:"cwd,omitempty" json:"cwd,omitempty"`
}

type InitializeSpec struct {
	Command          []string `yaml:"command" json:"command"`
	WorkingDirectory string   `yaml:"working_directory,omitempty" json:"workingDirectory,omitempty"`
}

func NewTemplate(name string) Template {
	return Template{
		Version:      TemplateVersion,
		Name:         name,
		Repositories: []RepositorySpec{},
	}
}

func (t Template) Validate() error {
	if t.Version != TemplateVersion {
		return fmt.Errorf("unsupported template version %d: expected %d", t.Version, TemplateVersion)
	}
	if t.PoolDepth < 0 {
		return fmt.Errorf("pool_depth %d is negative", t.PoolDepth)
	}
	if err := validateBranchPattern(t.BranchPattern); err != nil {
		return err
	}
	seen := make(map[string]struct{}, len(t.Repositories))
	environmentNames := make(map[string]string, len(t.Repositories))
	for _, repository := range t.Repositories {
		if !templateResourceName.MatchString(repository.Name) || repository.Name == "." || repository.Name == ".." {
			return fmt.Errorf("repository name %q is invalid", repository.Name)
		}
		if _, exists := seen[repository.Name]; exists {
			return fmt.Errorf("repository %q is declared more than once", repository.Name)
		}
		seen[repository.Name] = struct{}{}
		environmentName := normalizeEnvironmentName(repository.Name)
		if earlier, exists := environmentNames[environmentName]; exists {
			return fmt.Errorf("repository names %q and %q use the same initialization environment name", earlier, repository.Name)
		}
		environmentNames[environmentName] = repository.Name
		if strings.TrimSpace(repository.Clone.URL) == "" {
			return fmt.Errorf("repository %q has no clone URL", repository.Name)
		}
		if repository.Clone.Depth < 0 {
			return fmt.Errorf("repository %q has a negative clone depth", repository.Name)
		}
		if _, exists := repository.Remotes["origin"]; exists {
			return fmt.Errorf("repository %q cannot declare origin as an extra remote", repository.Name)
		}
		for name, url := range repository.Remotes {
			if !templateResourceName.MatchString(name) || name == "." || name == ".." {
				return fmt.Errorf("repository %q has invalid remote name %q", repository.Name, name)
			}
			if strings.TrimSpace(url) == "" {
				return fmt.Errorf("repository %q has no URL for remote %q", repository.Name, name)
			}
		}
		if repository.WindowName != "" && (!templateResourceName.MatchString(repository.WindowName) || repository.WindowName == "." || repository.WindowName == "..") {
			return fmt.Errorf("repository %q has invalid window name %q", repository.Name, repository.WindowName)
		}
		if err := validateInitialize(repository.Initialize, false); err != nil {
			return fmt.Errorf("repository %q initialization: %w", repository.Name, err)
		}
		if repository.Recycle != nil && len(repository.Recycle.Command) == 0 {
			return fmt.Errorf("repository %q recycle command is empty", repository.Name)
		}
	}
	if err := validateInitialize(t.Initialize, true); err != nil {
		return fmt.Errorf("template initialization: %w", err)
	}
	if err := validateSession(t.Session); err != nil {
		return fmt.Errorf("template session command: %w", err)
	}
	if err := validateTemplateAgents(t.Agents); err != nil {
		return err
	}
	if err := validateLocalDispatch(t.LocalDispatch); err != nil {
		return err
	}
	return nil
}

func validateLocalDispatch(config *LocalDispatchSpec) error {
	if config == nil {
		return nil
	}
	switch config.Provider {
	case "", "codex", "claude", "cursor", "grok":
	default:
		return fmt.Errorf("local_dispatch provider %q is invalid: use codex, claude, cursor, or grok", config.Provider)
	}
	switch config.Effort {
	case "", DispatchEffortSmall, DispatchEffortMedium, DispatchEffortLarge, DispatchEffortXLarge:
	default:
		return fmt.Errorf("local_dispatch effort %q is invalid: use small, medium, large, or xlarge", config.Effort)
	}
	if config.MaxConcurrency < 0 {
		return fmt.Errorf("local_dispatch max_concurrency %d is negative", config.MaxConcurrency)
	}
	return nil
}

func validateTemplateAgents(agents []TemplateAgent) error {
	labels := make(map[string]struct{}, len(agents))
	for _, agent := range agents {
		label := strings.TrimSpace(agent.Label)
		if label == "" {
			return fmt.Errorf("each declared Agent Session must have a label")
		}
		if !templateResourceName.MatchString(label) || label == "." || label == ".." {
			return fmt.Errorf("Agent Session label %q is invalid", label)
		}
		if _, exists := labels[label]; exists {
			return fmt.Errorf("Agent Session label %q is declared more than once", label)
		}
		labels[label] = struct{}{}
		if strings.TrimSpace(agent.Provider) == "" {
			return fmt.Errorf("Agent Session %q has no provider", label)
		}
		if !ValidAgentProvider(agent.Provider) {
			return fmt.Errorf("Agent Session %q has unsupported provider %q", label, agent.Provider)
		}
		if len(agent.Start) == 0 || strings.TrimSpace(agent.Start[0]) == "" {
			return fmt.Errorf("Agent Session %q has no start command", label)
		}
		if len(agent.Resume) > 0 && strings.TrimSpace(agent.Resume[0]) == "" {
			return fmt.Errorf("Agent Session %q has an invalid resume command", label)
		}
		for _, entry := range agent.Env {
			key, _, found := strings.Cut(entry, "=")
			if !found || strings.TrimSpace(key) == "" {
				return fmt.Errorf("Agent Session %q env entry %q is not KEY=VALUE", label, entry)
			}
		}
	}
	return nil
}

// validateBranchPattern checks one declared branch_pattern. The pattern must
// contain {name}, and a render with sample values and an empty prefix must
// give a valid Git branch name.
func validateBranchPattern(pattern string) error {
	if pattern == "" {
		return nil
	}
	if !strings.Contains(pattern, "{name}") {
		return fmt.Errorf("branch_pattern %q does not contain the {name} token", pattern)
	}
	sample := RenderBranchPattern(pattern, "", "sample", "0123abcd")
	if err := ValidateBranchName(sample); err != nil {
		return fmt.Errorf("branch_pattern %q renders the invalid branch name %q: %w", pattern, sample, err)
	}
	return nil
}

func normalizeEnvironmentName(name string) string {
	return strings.ToUpper(strings.NewReplacer("-", "_", ".", "_").Replace(name))
}

func validateInitialize(initialize *InitializeSpec, requireWorkingDirectory bool) error {
	if initialize == nil {
		return nil
	}
	if len(initialize.Command) == 0 {
		return fmt.Errorf("command must not be empty")
	}
	if strings.TrimSpace(initialize.Command[0]) == "" {
		return fmt.Errorf("command must not be empty")
	}
	if requireWorkingDirectory && strings.TrimSpace(initialize.WorkingDirectory) == "" {
		return fmt.Errorf("working_directory must be set")
	}
	if requireWorkingDirectory && !insideWorkspaceRoot(initialize.WorkingDirectory) {
		return fmt.Errorf("working_directory must stay inside the Workspace root")
	}
	return nil
}

func validateSession(session *SessionSpec) error {
	if session == nil {
		return nil
	}
	if len(session.Command) == 0 || strings.TrimSpace(session.Command[0]) == "" {
		return fmt.Errorf("command must not be empty")
	}
	if session.CWD != "" && !insideWorkspaceRoot(session.CWD) {
		return fmt.Errorf("cwd must stay inside the Workspace root")
	}
	return nil
}

// insideWorkspaceRoot reports whether a declared relative directory stays inside
// the Workspace root.
func insideWorkspaceRoot(directory string) bool {
	if filepath.IsAbs(directory) {
		return false
	}
	clean := filepath.Clean(directory)
	return clean != ".." && !strings.HasPrefix(clean, ".."+string(filepath.Separator))
}
