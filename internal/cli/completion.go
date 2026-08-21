package cli

import (
	"strings"
	"time"

	agentservice "github.com/jpugliesi/tmux-worktree/internal/agent"
	"github.com/jpugliesi/tmux-worktree/internal/domain"
	"github.com/jpugliesi/tmux-worktree/internal/maintenance"
	projectservice "github.com/jpugliesi/tmux-worktree/internal/project"
	"github.com/jpugliesi/tmux-worktree/internal/store"
	"github.com/spf13/cobra"
)

// completionFunc has the signature of a cobra argument or flag completion
// function. Every twt completion reads the stores and returns no value when
// a read fails.
type completionFunc func(command *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective)

// noFileCompletion stops the shell from adding file names to twt names.
const noFileCompletion = cobra.ShellCompDirectiveNoFileComp

// agentProviderNames are the Agent Session provider values that twt
// accepts for --provider and in the request schema.
var agentProviderNames = []string{"codex", "claude", "cursor", "grok", "command"}

// outputFormatNames are the values that --output accepts. Only list commands
// accept ndjson.
var outputFormatNames = []string{"text", "json", "ndjson"}

// matching keeps the candidate values that start with the typed prefix.
func matching(values []string, toComplete string) []string {
	kept := make([]string, 0, len(values))
	for _, value := range values {
		if strings.HasPrefix(value, toComplete) {
			kept = append(kept, value)
		}
	}
	return kept
}

// fixedCompletion completes one closed set of values, such as an enum flag.
func fixedCompletion(values ...string) completionFunc {
	return func(_ *cobra.Command, _ []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		return matching(values, toComplete), noFileCompletion
	}
}

// templateFlagCompletion completes a --template flag value.
func templateFlagCompletion(templateStore store.TemplateStore) completionFunc {
	return func(_ *cobra.Command, _ []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		names, err := templateStore.List()
		if err != nil {
			return nil, noFileCompletion
		}
		return matching(names, toComplete), noFileCompletion
	}
}

// templateNameCompletion completes the first positional Project Template name.
func templateNameCompletion(templateStore store.TemplateStore) completionFunc {
	return func(command *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) > 0 {
			return nil, noFileCompletion
		}
		return templateFlagCompletion(templateStore)(command, args, toComplete)
	}
}

// templateRepositoryCompletion completes TEMPLATE, then the repositories of
// that Project Template.
func templateRepositoryCompletion(templateStore store.TemplateStore) completionFunc {
	return func(command *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return templateNameCompletion(templateStore)(command, args, toComplete)
		}
		if len(args) > 1 {
			return nil, noFileCompletion
		}
		return repositoryNames(templateStore, args[0], toComplete), noFileCompletion
	}
}

// repositoryNames lists the repository names of one Project Template.
func repositoryNames(templateStore store.TemplateStore, templateName, toComplete string) []string {
	template, err := templateStore.Load(templateName)
	if err != nil {
		return nil
	}
	names := make([]string, 0, len(template.Repositories))
	for _, repository := range template.Repositories {
		names = append(names, repository.Name)
	}
	return matching(names, toComplete)
}

// projectFlagCompletion completes a --project flag value.
func projectFlagCompletion(projects *projectservice.Service) completionFunc {
	return func(_ *cobra.Command, _ []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		return matching(projectReferences(projects), toComplete), noFileCompletion
	}
}

// projectNameCompletion completes the first positional Project name and the
// current sentinel.
func projectNameCompletion(projects *projectservice.Service) completionFunc {
	return func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) > 0 {
			return nil, noFileCompletion
		}
		return matching(projectReferences(projects), toComplete), noFileCompletion
	}
}

// projectRepositoryCompletion completes PROJECT, then the repositories of
// that Project.
func projectRepositoryCompletion(projects *projectservice.Service) completionFunc {
	return func(command *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return projectNameCompletion(projects)(command, args, toComplete)
		}
		if len(args) > 1 {
			return nil, noFileCompletion
		}
		project, err := resolveProject(projects, args[0])
		if err != nil {
			return nil, noFileCompletion
		}
		names := make([]string, 0, len(project.Repositories))
		for _, repository := range project.Repositories {
			names = append(names, repository.Name)
		}
		return matching(names, toComplete), noFileCompletion
	}
}

// projectReferences lists every Project name and the current sentinel.
func projectReferences(projects *projectservice.Service) []string {
	list, err := projects.List()
	if err != nil {
		return nil
	}
	names := make([]string, 0, len(list)+1)
	names = append(names, currentProjectReference)
	for _, project := range list {
		names = append(names, project.Name)
	}
	return names
}

// completionDiscoveryWindow bounds the provider scan of an AGENT reference
// completion. A key press must stay fast, so the completion offers the
// provider sessions of the last two weeks. An older session ID stays a valid
// typed value, and agents list shows it.
const completionDiscoveryWindow = 14 * 24 * time.Hour

// agentReferenceCompletion completes an AGENT reference. The candidates are
// the registered Agent Sessions of the Project, with the label as the
// description, and the provider sessions that the Project discovers, with the
// provider as the description. Every twt command that takes an AGENT
// reference adopts a discovered session on first touch, so a discovered
// session ID is a valid value.
func agentReferenceCompletion(agents *agentservice.Service, projects *projectservice.Service, stateDir string) completionFunc {
	return func(command *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) > 0 {
			return nil, noFileCompletion
		}
		scope, ok := completionScopeOf(command, projects)
		if !ok {
			return nil, noFileCompletion
		}
		candidates := []string{}
		for _, project := range scope.projects {
			registered, err := agents.List(project.ID)
			if err != nil {
				continue
			}
			for _, session := range registered {
				candidates = append(candidates, withDescription(session.ID, session.Label))
			}
			if !scope.discover {
				continue
			}
			found, err := discoverProjectSessionsSince(project, stateDir, registered, time.Now().Add(-completionDiscoveryWindow))
			if err != nil {
				continue
			}
			for _, session := range found {
				candidates = append(candidates, withDescription(session.SessionID, "discovered "+session.Provider))
			}
		}
		return matching(candidates, toComplete), noFileCompletion
	}
}

// completionScope is the Project set that one Agent Session completion reads.
type completionScope struct {
	projects []domain.Project
	// discover permits the provider scan. It is off for the fallback that has
	// no single Project, because one scan for each Project is too slow for a
	// key press.
	discover bool
}

// completionScopeOf resolves the Projects of one Agent Session completion the
// same way the command resolves its Project: a set --project flag selects that
// Project, and every other case uses the current Project. A command without a
// --project flag, such as agents focus, falls back to the registered Agent
// Sessions of every Project, so a reference still completes outside a Project
// directory. A resolution failure gives no candidates, because a completion
// must never report an error.
func completionScopeOf(command *cobra.Command, projects *projectservice.Service) (completionScope, bool) {
	flag := command.Flags().Lookup("project")
	if flag != nil && flag.Changed {
		project, err := resolveProject(projects, flag.Value.String())
		if err != nil {
			return completionScope{}, false
		}
		return completionScope{projects: []domain.Project{project}, discover: true}, true
	}
	if project, err := resolveProject(projects, currentProjectReference); err == nil {
		return completionScope{projects: []domain.Project{project}, discover: true}, true
	}
	if flag != nil {
		return completionScope{}, false
	}
	list, err := projects.List()
	if err != nil {
		return completionScope{}, false
	}
	return completionScope{projects: list}, true
}

// withDescription builds one completion candidate with its shell description.
// cobra splits the value from the description at the tab.
func withDescription(value, description string) string {
	if description == "" {
		return value
	}
	return value + "\t" + description
}

// adoptSessionCompletion completes the SESSION argument of projects adopt with
// the tmux sessions that no Project owns.
func adoptSessionCompletion(projects *projectservice.Service) completionFunc {
	return func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) > 0 {
			return nil, noFileCompletion
		}
		return matching(projects.AdoptableSessions(), toComplete), noFileCompletion
	}
}

// environmentIDCompletion completes Prepared Environment IDs.
func environmentIDCompletion(service *maintenance.Service) completionFunc {
	return func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) > 0 {
			return nil, noFileCompletion
		}
		report, err := service.EnvironmentReport()
		if err != nil {
			return nil, noFileCompletion
		}
		ids := make([]string, 0, len(report))
		for _, info := range report {
			ids = append(ids, info.ID)
		}
		return matching(ids, toComplete), noFileCompletion
	}
}
