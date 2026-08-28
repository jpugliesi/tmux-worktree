package cli

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/jpugliesi/tmux-worktree/internal/clierr"
	"github.com/jpugliesi/tmux-worktree/internal/domain"
	"github.com/jpugliesi/tmux-worktree/internal/store"
	workspaceservice "github.com/jpugliesi/tmux-worktree/internal/workspace"
	"github.com/spf13/cobra"
)

type templateListEntry struct {
	Name string `json:"name"`
}

type templatesListOutput struct {
	SchemaVersion int                 `json:"schemaVersion"`
	Templates     []templateListEntry `json:"templates"`
	TotalCount    int                 `json:"totalCount"`
	Truncated     bool                `json:"truncated,omitempty"`
}

type templateShowOutput struct {
	SchemaVersion int             `json:"schemaVersion"`
	Template      domain.Template `json:"template"`
}

type templateValidateOutput struct {
	SchemaVersion int      `json:"schemaVersion"`
	Operation     string   `json:"operation"`
	Status        string   `json:"status"`
	Name          string   `json:"name"`
	Warnings      []string `json:"warnings"`
}

func newTemplatesCommand(options Options) *cobra.Command {
	templateStore := options.templateStore()
	templates := groupCommand(&cobra.Command{
		Use:   "templates",
		Short: "Manage Workspace Templates",
	})
	templates.AddCommand(newTemplatesCreateCommand(options))
	templates.AddCommand(newTemplatesListCommand(templateStore))
	templates.AddCommand(newTemplatesShowCommand(templateStore))
	templates.AddCommand(newTemplatesPathCommand(templateStore))
	templates.AddCommand(newTemplatesValidateCommand(templateStore))
	templates.AddCommand(newTemplatesEditCommand(templateStore, options))
	templates.AddCommand(newTemplatesRemoveCommand(templateStore, options))
	templates.AddCommand(newTemplatePrepareCommand(options, templateStore))
	templates.AddCommand(newTemplateRepositoriesCommand(options, templateStore))
	templates.AddCommand(newTemplateInitializeCommand(options, templateStore, options.StateDir))
	templates.AddCommand(newTemplateRecycleCommand(options, templateStore, options.StateDir))
	return templates
}

func newTemplateRecycleCommand(options Options, templateStore store.TemplateStore, stateDir string) *cobra.Command {
	recycle := groupCommand(&cobra.Command{Use: "recycle", Short: "Manage repository recycle commands"})
	var setRepository string
	set := &cobra.Command{
		Use:   "set TEMPLATE --repo REPO -- COMMAND...",
		Short: "Set a repository recycle command",
		Args: func(command *cobra.Command, args []string) error {
			if strings.TrimSpace(setRepository) == "" {
				return invalidUsage(command, "missing required flag --repo REPO")
			}
			if command.ArgsLenAtDash() != 1 || len(args) < 2 {
				return invalidUsage(command, "expected TEMPLATE --repo REPO -- COMMAND...")
			}
			return nil
		},
		RunE: func(command *cobra.Command, args []string) error {
			return setTemplateRecycle(command, options, templateStore, stateDir, args[0], setRepository, args[1:])
		},
	}
	set.Flags().StringVar(&setRepository, "repo", "", "Select the repository")
	_ = set.MarkFlagRequired("repo")
	setArguments(set, requiredArgument("template"), variadicArgument("command", true, ""))
	set.ValidArgsFunction = templateNameCompletion(templateStore)
	_ = set.RegisterFlagCompletionFunc("repo", templateRepositoryFlagCompletion(templateStore))

	var unsetRepository string
	unset := &cobra.Command{
		Use:   "unset TEMPLATE --repo REPO",
		Short: "Remove a repository recycle command",
		Args:  exactArgs("TEMPLATE"),
		RunE: func(command *cobra.Command, args []string) error {
			return setTemplateRecycle(command, options, templateStore, stateDir, args[0], unsetRepository, nil)
		},
	}
	unset.Flags().StringVar(&unsetRepository, "repo", "", "Select the repository")
	_ = unset.MarkFlagRequired("repo")
	setArguments(unset, requiredArgument("template"))
	unset.ValidArgsFunction = templateNameCompletion(templateStore)
	_ = unset.RegisterFlagCompletionFunc("repo", templateRepositoryFlagCompletion(templateStore))
	recycle.AddCommand(set, unset)
	return recycle
}

func templateRepositoryFlagCompletion(templateStore store.TemplateStore) completionFunc {
	return func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return nil, noFileCompletion
		}
		return repositoryNames(templateStore, args[0], toComplete), noFileCompletion
	}
}

func setTemplateRecycle(command *cobra.Command, options Options, templateStore store.TemplateStore, stateDir, templateName, repositoryName string, recycleCommand []string) error {
	defer syncSharedTemplates(command, options)
	lock, err := store.AcquireMutationLock(stateDir)
	if err != nil {
		return err
	}
	defer lock.Release()
	template, err := templateStore.Load(templateName)
	if err != nil {
		return err
	}
	found := false
	for index := range template.Repositories {
		if template.Repositories[index].Name != repositoryName {
			continue
		}
		found = true
		if len(recycleCommand) == 0 {
			template.Repositories[index].Recycle = nil
		} else {
			template.Repositories[index].Recycle = &domain.RecycleSpec{Command: append([]string(nil), recycleCommand...)}
		}
		break
	}
	if !found {
		return clierr.New(clierr.NotFound, "repository %q does not exist in Workspace Template %q", repositoryName, templateName)
	}
	if err := template.Validate(); err != nil {
		return err
	}
	operation := "templates.recycle.set"
	verb := "Set"
	if len(recycleCommand) == 0 {
		operation = "templates.recycle.unset"
		verb = "Removed"
	}
	return runMutation(command, operation,
		func() (string, string, error) { return "", repositoryName, nil },
		func() (string, string, error) { return "", repositoryName, templateStore.Save(template) },
		func(out io.Writer, _, _ string) error {
			_, err := fmt.Fprintf(out, "%s recycle command for repository %q in Workspace Template %q\n", verb, repositoryName, templateName)
			return err
		})
}

type templatePrepareOutput struct {
	SchemaVersion int      `json:"schemaVersion"`
	Template      string   `json:"template"`
	Environments  []string `json:"environments"`
}

func newTemplatePrepareCommand(options Options, templateStore store.TemplateStore) *cobra.Command {
	command := &cobra.Command{
		Use:   "prepare TEMPLATE",
		Short: "Prepare the next initialized environments",
		Args:  exactArgs("TEMPLATE"),
		RunE: func(command *cobra.Command, args []string) error {
			return prepareTemplateEnvironments(command, options, templateStore, args[0])
		},
	}
	setArguments(command, requiredArgument("template"))
	command.ValidArgsFunction = templateNameCompletion(templateStore)
	return command
}

// prepareTemplateEnvironments fills the Prepared Environment pool of one
// Workspace Template. Both the templates prepare command and apply use it.
func prepareTemplateEnvironments(command *cobra.Command, options Options, templateStore store.TemplateStore, name string) error {
	template, err := templateStore.Load(name)
	if err != nil {
		return err
	}
	if isDryRun(command) {
		if err := template.Validate(); err != nil {
			return err
		}
		return writeMutation(command, "templates.prepare", statusValid, "", name)
	}
	serviceOptions := options.workspaceServiceOptions()
	if !WantsJSON(command) {
		serviceOptions.Progress = func(message string) {
			_, _ = fmt.Fprintln(command.ErrOrStderr(), message)
		}
	}
	service := workspaceservice.NewService(serviceOptions)
	refreshed, err := service.RefreshReadyEnvironments(template)
	if err != nil {
		return err
	}
	queued, err := service.TopUpPool(name, template, template.EffectivePoolDepth())
	if err != nil {
		return err
	}
	prepared := make([]string, 0, len(refreshed)+len(queued))
	for _, environment := range refreshed {
		prepared = append(prepared, environment.ID)
	}
	for _, entry := range queued {
		environment, err := service.PrepareQueued(entry.ID, entry.QueueToken)
		if err != nil {
			return err
		}
		prepared = append(prepared, environment.ID)
	}
	if WantsJSON(command) {
		return writeJSONOutput(command, templatePrepareOutput{SchemaVersion: jsonSchemaVersion, Template: name, Environments: prepared})
	}
	if len(prepared) == 0 {
		_, err = fmt.Fprintf(command.OutOrStdout(), "The Prepared Environment pool for Workspace Template %q is full\n", name)
		return err
	}
	for _, id := range prepared {
		if _, err := fmt.Fprintf(command.OutOrStdout(), "Prepared Environment %q for Workspace Template %q\n", id, name); err != nil {
			return err
		}
	}
	return nil
}

func newTemplatesCreateCommand(options Options) *cobra.Command {
	var fromFile string
	command := &cobra.Command{
		Use:   "create NAME",
		Short: "Create an empty Workspace Template",
		Args:  exactArgs("NAME"),
		RunE: func(command *cobra.Command, args []string) error {
			template := domain.NewTemplate(args[0])
			if fromFile != "" {
				decoded, err := readTemplateDocument(command, fromFile)
				if err != nil {
					return err
				}
				if decoded.Name != "" && decoded.Name != args[0] {
					return invalidUsage(command, "the Workspace Template document contains name %q; the NAME argument is %q", decoded.Name, args[0])
				}
				decoded.Name = args[0]
				template = decoded
			}
			return createTemplate(command, options, template)
		},
	}
	command.Flags().StringVar(&fromFile, "from-file", "", "Read the Workspace Template YAML from this file, or - for standard input")
	setArguments(command, requiredArgument("name"))
	return command
}

// createTemplate validates and saves one Workspace Template under the mutation
// lock. Both the templates create command and apply use it.
func createTemplate(command *cobra.Command, options Options, template domain.Template) error {
	defer syncSharedTemplates(command, options)
	templateStore := options.templateStore()
	lock, err := store.AcquireMutationLock(options.StateDir)
	if err != nil {
		return err
	}
	defer lock.Release()
	if err := templateStore.ValidateCreate(template); err != nil {
		return err
	}
	return runMutation(command, "templates.create",
		func() (string, string, error) {
			return "", template.Name, nil
		},
		func() (string, string, error) {
			return "", template.Name, templateStore.Create(template)
		},
		func(out io.Writer, _, name string) error {
			_, err := fmt.Fprintf(out, "Created Workspace Template %q\n", name)
			return err
		})
}

// readTemplateDocument decodes one strict Workspace Template YAML document from
// a file or from standard input.
func readTemplateDocument(command *cobra.Command, path string) (domain.Template, error) {
	if isStdinToken(path) {
		return store.DecodeTemplate(io.LimitReader(command.InOrStdin(), 1024*1024), "standard input")
	}
	file, err := os.Open(path)
	if err != nil {
		return domain.Template{}, clierr.New(clierr.NotFound, "read Workspace Template file %q: %v", path, err)
	}
	defer file.Close()
	return store.DecodeTemplate(file, fmt.Sprintf("%q", path))
}

func newTemplatesListCommand(templateStore store.TemplateStore) *cobra.Command {
	var limit, offset int
	command := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List Workspace Templates",
		Args:    noArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			names, err := templateStore.List()
			if err != nil {
				return err
			}
			names, total, truncated, err := applyWindow(names, offset, limit)
			if err != nil {
				return err
			}
			if format := resolvedOutputFormat(command); format != outputText {
				entries := make([]templateListEntry, 0, len(names))
				for _, name := range names {
					entries = append(entries, templateListEntry{Name: name})
				}
				if format == outputNDJSON {
					return writeNDJSONList(command, entries, total, truncated)
				}
				return writeReadJSON(command, templatesListOutput{SchemaVersion: jsonSchemaVersion, Templates: entries, TotalCount: total, Truncated: truncated}, "templates")
			}
			for _, name := range names {
				if _, err := fmt.Fprintln(command.OutOrStdout(), name); err != nil {
					return err
				}
			}
			if total == 0 {
				_, err = fmt.Fprintln(command.ErrOrStderr(), "No Workspace Templates exist. Run 'twt templates create NAME'.")
				return err
			}
			return nil
		},
	}
	addListReadFlags(command, &limit, &offset, templateListEntry{})
	return command
}

func newTemplatesShowCommand(templateStore store.TemplateStore) *cobra.Command {
	command := &cobra.Command{
		Use:   "show NAME",
		Short: "Show a Workspace Template",
		Args:  exactArgs("NAME"),
		RunE: func(command *cobra.Command, args []string) error {
			template, err := templateStore.Load(args[0])
			if err != nil {
				return err
			}
			if WantsJSON(command) {
				return writeReadJSON(command, templateShowOutput{SchemaVersion: jsonSchemaVersion, Template: template}, "template")
			}
			data, err := store.EncodeTemplate(template)
			if err != nil {
				return err
			}
			_, err = command.OutOrStdout().Write(data)
			return err
		},
	}
	setArguments(command, requiredArgument("name"))
	addFieldsFlag(command, domain.Template{})
	command.ValidArgsFunction = templateNameCompletion(templateStore)
	return command
}

func newTemplatesPathCommand(templateStore store.TemplateStore) *cobra.Command {
	command := &cobra.Command{
		Use:   "path NAME",
		Short: "Print the Workspace Template YAML path",
		Args:  exactArgs("NAME"),
		RunE: func(command *cobra.Command, args []string) error {
			path, err := templateStore.Path(args[0])
			if err != nil {
				return err
			}
			_, err = fmt.Fprintln(command.OutOrStdout(), path)
			return err
		},
	}
	setArguments(command, requiredArgument("name"))
	command.ValidArgsFunction = templateNameCompletion(templateStore)
	return command
}

func newTemplatesValidateCommand(templateStore store.TemplateStore) *cobra.Command {
	command := &cobra.Command{
		Use:   "validate NAME",
		Short: "Validate a Workspace Template",
		Args:  exactArgs("NAME"),
		RunE: func(command *cobra.Command, args []string) error {
			template, err := templateStore.Load(args[0])
			if err != nil {
				return err
			}
			warnings := template.Warnings()
			if warnings == nil {
				warnings = []string{}
			}
			if WantsJSON(command) {
				return writeJSONOutput(command, templateValidateOutput{
					SchemaVersion: jsonSchemaVersion, Operation: "templates.validate",
					Status: statusValid, Name: args[0], Warnings: warnings,
				})
			}
			if _, err := fmt.Fprintf(command.OutOrStdout(), "Workspace Template %q is valid\n", args[0]); err != nil {
				return err
			}
			for _, warning := range warnings {
				if _, err := fmt.Fprintf(command.OutOrStdout(), "Warning: %s\n", warning); err != nil {
					return err
				}
			}
			return nil
		},
	}
	setArguments(command, requiredArgument("name"))
	command.ValidArgsFunction = templateNameCompletion(templateStore)
	return command
}

func newTemplatesEditCommand(templateStore store.TemplateStore, options Options) *cobra.Command {
	command := &cobra.Command{
		Use:   "edit NAME",
		Short: "Edit a Workspace Template YAML file in your editor",
		Args:  exactArgs("NAME"),
		RunE: func(command *cobra.Command, args []string) error {
			// The editor is an interactive escape: it opens only for a person
			// at a terminal, so a piped call never blocks in an editor.
			if !interactiveInput(command.InOrStdin()) {
				return invalidUsageWithHint(command,
					"Run 'twt templates create NAME --from-file -' or edit the file at 'twt templates path NAME'.",
					"twt templates edit has no terminal")
			}
			path, err := templateStore.Path(args[0])
			if err != nil {
				return err
			}
			defer syncSharedTemplates(command, options)
			return runMutation(command, "templates.edit",
				func() (string, string, error) {
					return "", args[0], nil
				},
				func() (string, string, error) {
					lock, err := store.AcquireMutationLock(options.StateDir)
					if err != nil {
						return "", "", err
					}
					defer lock.Release()
					if err := options.OpenEditor(path); err != nil {
						return "", "", err
					}
					if _, err := templateStore.Load(args[0]); err != nil {
						return "", "", clierr.WithHint(clierr.Wrap(clierr.UnsafeState, err),
							"Fix the file or run 'twt templates validate %s'.", args[0])
					}
					return "", args[0], nil
				},
				func(out io.Writer, _, name string) error {
					_, err := fmt.Fprintf(out, "Workspace Template %q is valid\n", name)
					return err
				})
		},
	}
	setArguments(command, requiredArgument("name"))
	command.ValidArgsFunction = templateNameCompletion(templateStore)
	return command
}

func newTemplatesRemoveCommand(templateStore store.TemplateStore, options Options) *cobra.Command {
	command := &cobra.Command{
		Use:     "remove NAME",
		Aliases: []string{"rm"},
		Short:   "Delete a Workspace Template YAML file",
		Args:    exactArgs("NAME"),
		RunE: func(command *cobra.Command, args []string) error {
			return removeTemplate(command, options, templateStore, args[0])
		},
	}
	setArguments(command, requiredArgument("name"))
	command.ValidArgsFunction = templateNameCompletion(templateStore)
	return command
}

// removeTemplate deletes one Workspace Template YAML file. No Workspace record
// can name the Workspace Template. Both the templates remove command and apply
// use it.
func removeTemplate(command *cobra.Command, options Options, templateStore store.TemplateStore, name string) error {
	defer syncSharedTemplates(command, options)
	if _, err := templateStore.Path(name); err != nil {
		return err
	}
	if err := checkTemplateIsUnused(options, name); err != nil {
		return err
	}
	return runMutation(command, "templates.remove",
		func() (string, string, error) {
			return "", name, nil
		},
		func() (string, string, error) {
			lock, err := store.AcquireMutationLock(options.StateDir)
			if err != nil {
				return "", "", err
			}
			defer lock.Release()
			if err := checkTemplateIsUnused(options, name); err != nil {
				return "", "", err
			}
			return "", name, templateStore.Delete(name)
		},
		func(out io.Writer, _, removed string) error {
			_, err := fmt.Fprintf(out, "Removed Workspace Template %q\n", removed)
			return err
		})
}

// checkTemplateIsUnused refuses Workspace Template removal while a Workspace
// or Ticket Project still names the Workspace Template.
func checkTemplateIsUnused(options Options, name string) error {
	workspaces, err := store.NewWorkspaceStore(options.StateDir).List()
	if err != nil {
		return err
	}
	workspaceUsers := make([]string, 0, len(workspaces))
	for _, workspace := range workspaces {
		if workspace.TemplateName == name {
			workspaceUsers = append(workspaceUsers, workspace.Name)
		}
	}
	projectUsers := []string{}
	tickets, ticketErr := options.ticketService()
	if ticketErr == nil {
		projects, listErr := tickets.Projects()
		if listErr != nil && clierr.CodeOf(listErr) != clierr.PreconditionFailed && clierr.CodeOf(listErr) != clierr.NotFound {
			return listErr
		}
		if listErr == nil {
			for _, project := range projects {
				if project.TemplateName == name {
					projectUsers = append(projectUsers, project.Name)
				}
			}
		}
	} else if clierr.CodeOf(ticketErr) != clierr.PreconditionFailed && clierr.CodeOf(ticketErr) != clierr.NotFound {
		return ticketErr
	}
	if len(workspaceUsers) > 0 {
		return clierr.WithHint(
			clierr.New(clierr.PreconditionFailed, "Workspace Template %q is used by %d Workspaces: %s", name, len(workspaceUsers), strings.Join(workspaceUsers, ", ")),
			"Remove these Workspaces first with 'twt workspaces remove WORKSPACE --apply'.")
	}
	if len(projectUsers) > 0 {
		return clierr.WithHint(
			clierr.New(clierr.PreconditionFailed, "Workspace Template %q is used by %d Projects: %s", name, len(projectUsers), strings.Join(projectUsers, ", ")),
			"Set a different Workspace Template on these Projects first.")
	}
	return nil
}

func newTemplateRepositoriesCommand(options Options, templateStore store.TemplateStore) *cobra.Command {
	repositories := groupCommand(&cobra.Command{
		Use:     "repos",
		Aliases: []string{"repositories"},
		Short:   "Manage repository specifications",
	})
	repositories.AddCommand(newTemplateRepositoriesAddCommand(options, templateStore))
	repositories.AddCommand(newTemplateRepositoriesRemoveCommand(options, templateStore, options.StateDir))
	return repositories
}

func newTemplateRepositoriesAddCommand(options Options, templateStore store.TemplateStore) *cobra.Command {
	var depth int
	var filter string
	var remotes []string
	var defaultBranch string
	var windowName string
	command := &cobra.Command{
		Use:   "add TEMPLATE REPO URL",
		Short: "Add a repository specification",
		Args:  exactArgs("TEMPLATE", "REPO", "URL"),
		RunE: func(command *cobra.Command, args []string) error {
			extraRemotes := make(map[string]string, len(remotes))
			for _, remote := range remotes {
				name, url, found := strings.Cut(remote, "=")
				if !found || strings.TrimSpace(url) == "" {
					return fmt.Errorf("invalid remote %q: use name=url", remote)
				}
				if err := store.ValidateResourceName(name); err != nil {
					return fmt.Errorf("invalid remote %q: %w", name, err)
				}
				if name == "origin" {
					return fmt.Errorf("origin is the clone remote and cannot be an extra remote")
				}
				extraRemotes[name] = url
			}
			return addRepositoryToTemplate(command, options, args[0], domain.RepositorySpec{
				Name: args[1],
				Clone: domain.CloneSpec{
					URL:    args[2],
					Depth:  depth,
					Filter: filter,
				},
				Remotes:       extraRemotes,
				DefaultBranch: defaultBranch,
				WindowName:    windowName,
			})
		},
	}
	command.Flags().IntVar(&depth, "depth", 0, "Deprecated; Repository Caches always keep full history")
	_ = command.Flags().MarkDeprecated("depth", "Repository Caches always keep full history")
	command.Flags().StringVar(&filter, "filter", "", "Set a partial-clone filter for the Repository Cache, for example blob:none")
	command.Flags().StringArrayVar(&remotes, "remote", nil, "Add an extra remote as name=url")
	command.Flags().StringVar(&defaultBranch, "default-branch", "", "Set the default branch")
	command.Flags().StringVar(&windowName, "window-name", "", "Set the tmux window name")
	setArguments(command, requiredArgument("template"), requiredArgument("repo"), requiredArgument("url"))
	command.ValidArgsFunction = templateNameCompletion(templateStore)
	return command
}

// addRepositoryToTemplate adds one repository specification under the
// mutation lock and validates the result. Both the templates repos add
// command and apply use it.
func addRepositoryToTemplate(command *cobra.Command, options Options, templateName string, repository domain.RepositorySpec) error {
	defer syncSharedTemplates(command, options)
	if err := store.ValidateResourceName(repository.Name); err != nil {
		return fmt.Errorf("invalid repository name: %w", err)
	}
	templateStore := options.templateStore()
	lock, err := store.AcquireMutationLock(options.StateDir)
	if err != nil {
		return err
	}
	defer lock.Release()
	template, err := templateStore.Load(templateName)
	if err != nil {
		return err
	}
	updated, err := addTemplateRepository(template, repository)
	if err != nil {
		return err
	}
	return runMutation(command, "templates.repos.add",
		func() (string, string, error) {
			return "", repository.Name, nil
		},
		func() (string, string, error) {
			return "", repository.Name, templateStore.Save(updated)
		},
		func(out io.Writer, _, name string) error {
			_, err := fmt.Fprintf(out, "Added repository %q to Workspace Template %q\n", name, templateName)
			return err
		})
}

// addTemplateRepository adds one repository specification to a Workspace
// Template and validates the result.
func addTemplateRepository(template domain.Template, repository domain.RepositorySpec) (domain.Template, error) {
	for _, existing := range template.Repositories {
		if existing.Name == repository.Name {
			return template, clierr.New(clierr.AlreadyExists, "repository %q already exists in Workspace Template %q", repository.Name, template.Name)
		}
	}
	template.Repositories = append(template.Repositories, repository)
	if err := template.Validate(); err != nil {
		return template, err
	}
	return template, nil
}

func newTemplateRepositoriesRemoveCommand(options Options, templateStore store.TemplateStore, stateDir string) *cobra.Command {
	command := &cobra.Command{
		Use:     "remove TEMPLATE REPO",
		Aliases: []string{"rm"},
		Short:   "Remove a repository specification",
		Args:    exactArgs("TEMPLATE", "REPO"),
		RunE: func(command *cobra.Command, args []string) error {
			return removeRepositoryFromTemplate(command, options, templateStore, stateDir, args[0], args[1])
		},
	}
	setArguments(command, requiredArgument("template"), requiredArgument("repo"))
	command.ValidArgsFunction = templateRepositoryCompletion(templateStore)
	return command
}

// removeRepositoryFromTemplate removes one repository specification under the
// mutation lock and validates the result. Both the templates repos remove
// command and apply use it.
func removeRepositoryFromTemplate(command *cobra.Command, options Options, templateStore store.TemplateStore, stateDir, templateName, repositoryName string) error {
	defer syncSharedTemplates(command, options)
	lock, err := store.AcquireMutationLock(stateDir)
	if err != nil {
		return err
	}
	defer lock.Release()
	template, err := templateStore.Load(templateName)
	if err != nil {
		return err
	}
	kept := make([]domain.RepositorySpec, 0, len(template.Repositories))
	for _, repository := range template.Repositories {
		if repository.Name != repositoryName {
			kept = append(kept, repository)
		}
	}
	if len(kept) == len(template.Repositories) {
		return clierr.New(clierr.NotFound, "repository %q does not exist in Workspace Template %q", repositoryName, templateName)
	}
	template.Repositories = kept
	if err := template.Validate(); err != nil {
		return err
	}
	return runMutation(command, "templates.repos.remove",
		func() (string, string, error) {
			return "", repositoryName, nil
		},
		func() (string, string, error) {
			return "", repositoryName, templateStore.Save(template)
		},
		func(out io.Writer, _, name string) error {
			_, err := fmt.Fprintf(out, "Removed repository %q from Workspace Template %q\n", name, templateName)
			return err
		})
}

func newTemplateInitializeCommand(options Options, templateStore store.TemplateStore, stateDir string) *cobra.Command {
	initialize := groupCommand(&cobra.Command{
		Use:   "init",
		Short: "Manage Workspace Template initialization",
	})
	var workingDirectory string
	var repository string
	set := &cobra.Command{
		Use:   "set TEMPLATE [--repo REPO] [--cwd PATH] -- COMMAND...",
		Short: "Set a Workspace or repository initialization command",
		Args: func(command *cobra.Command, args []string) error {
			if strings.TrimSpace(repository) != "" && command.Flags().Changed("cwd") {
				return invalidUsage(command, "do not use --cwd together with --repo; repository initialization runs in the repository worktree")
			}
			if strings.TrimSpace(repository) == "" && (!command.Flags().Changed("cwd") || strings.TrimSpace(workingDirectory) == "") {
				return invalidUsage(command, "missing required flag --cwd PATH; use --repo REPO for repository initialization")
			}
			if command.ArgsLenAtDash() != 1 || len(args) < 2 {
				return invalidUsage(command, "expected TEMPLATE [--repo REPO] [--cwd PATH] -- COMMAND...")
			}
			return nil
		},
		RunE: func(command *cobra.Command, args []string) error {
			return setTemplateInitialization(command, options, templateStore, stateDir, args[0], repository, workingDirectory, args[1:])
		},
	}
	set.Flags().StringVar(&workingDirectory, "cwd", "", "Set the Workspace initialization working directory, relative to the Workspace root")
	set.Flags().StringVar(&repository, "repo", "", "Set repository initialization for this repository instead of Workspace initialization")
	setArguments(set, requiredArgument("template"), variadicArgument("command", true, ""))
	set.ValidArgsFunction = templateNameCompletion(templateStore)
	_ = set.RegisterFlagCompletionFunc("repo", func(command *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		return repositoryNames(templateStore, args[0], toComplete), cobra.ShellCompDirectiveNoFileComp
	})
	initialize.AddCommand(set)
	return initialize
}

// setTemplateInitialization sets the initialization command of one Workspace
// Template under the mutation lock. A set repository name sets repository
// initialization, which runs in the repository worktree; an empty repository
// name sets Workspace initialization, which runs in workingDirectory. Both the
// templates init set command and apply use it.
func setTemplateInitialization(command *cobra.Command, options Options, templateStore store.TemplateStore, stateDir, templateName, repository, workingDirectory string, initializeCommand []string) error {
	defer syncSharedTemplates(command, options)
	lock, err := store.AcquireMutationLock(stateDir)
	if err != nil {
		return err
	}
	defer lock.Release()
	template, err := templateStore.Load(templateName)
	if err != nil {
		return err
	}
	operation := "templates.init.set"
	name := templateName
	if strings.TrimSpace(repository) != "" {
		operation = "templates.repos.init.set"
		name = repository
		found := false
		for index := range template.Repositories {
			if template.Repositories[index].Name != repository {
				continue
			}
			template.Repositories[index].Initialize = &domain.InitializeSpec{
				Command: append([]string(nil), initializeCommand...),
			}
			found = true
			break
		}
		if !found {
			return clierr.New(clierr.NotFound, "repository %q does not exist in Workspace Template %q", repository, templateName)
		}
	} else {
		template.Initialize = &domain.InitializeSpec{
			Command:          append([]string(nil), initializeCommand...),
			WorkingDirectory: workingDirectory,
		}
	}
	if err := template.Validate(); err != nil {
		return err
	}
	return runMutation(command, operation,
		func() (string, string, error) {
			return "", name, nil
		},
		func() (string, string, error) {
			return "", name, templateStore.Save(template)
		},
		func(out io.Writer, _, _ string) error {
			if operation == "templates.repos.init.set" {
				_, err := fmt.Fprintf(out, "Set initialization for repository %q in Workspace Template %q\n", repository, templateName)
				return err
			}
			_, err := fmt.Fprintf(out, "Set initialization for Workspace Template %q\n", templateName)
			return err
		})
}

// syncSharedTemplates pushes a shared-template write through the tickets
// git sync, best-effort, so other executor machines receive it on their
// next pull. Without a configured twt home it is a no-op; a failure leaves
// the change local for the next successful sync round.
func syncSharedTemplates(command *cobra.Command, options Options) {
	if isDryRun(command) || options.resolveTwtHome() == "" {
		return
	}
	service, err := options.ticketService()
	if err != nil {
		return
	}
	if _, err := service.Sync(false); err != nil {
		fmt.Fprintf(command.ErrOrStderr(),
			"Warning: the Workspace Template change is saved but not synced yet: %v\n", err)
	}
}
