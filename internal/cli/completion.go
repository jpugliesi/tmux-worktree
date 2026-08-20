package cli

import (
	"strings"

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
var agentProviderNames = []string{"codex", "claude", "cursor", "command"}

// outputFormatNames are the values that --output accepts.
var outputFormatNames = []string{"text", "json"}

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

// agentIDCompletion completes Agent Session IDs. With a --project flag it
// uses the Agent Sessions of that Project. Without one it uses every
// Project.
func agentIDCompletion(agents *agentservice.Service, projects *projectservice.Service) completionFunc {
	return func(command *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) > 0 {
			return nil, noFileCompletion
		}
		candidates := []domain.Project{}
		if reference, err := command.Flags().GetString("project"); err == nil && reference != "" {
			project, err := resolveProject(projects, reference)
			if err != nil {
				return nil, noFileCompletion
			}
			candidates = append(candidates, project)
		} else {
			list, err := projects.List()
			if err != nil {
				return nil, noFileCompletion
			}
			candidates = list
		}
		ids := []string{}
		for _, project := range candidates {
			sessions, err := agents.List(project.ID)
			if err != nil {
				continue
			}
			for _, session := range sessions {
				ids = append(ids, session.ID)
			}
		}
		return matching(ids, toComplete), noFileCompletion
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
