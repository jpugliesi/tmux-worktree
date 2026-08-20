package cli

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

	"github.com/jpugliesi/tmux-worktree/internal/clierr"
	"github.com/jpugliesi/tmux-worktree/internal/domain"
	projectservice "github.com/jpugliesi/tmux-worktree/internal/project"
	"github.com/jpugliesi/tmux-worktree/internal/store"
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
	templateStore := store.NewTemplateStore(options.ConfigDir)
	templates := groupCommand(&cobra.Command{
		Use:   "templates",
		Short: "Manage Project Templates",
	})
	templates.AddCommand(newTemplatesCreateCommand(templateStore, options.StateDir))
	templates.AddCommand(newTemplatesListCommand(templateStore))
	templates.AddCommand(newTemplatesShowCommand(templateStore))
	templates.AddCommand(newTemplatesPathCommand(templateStore))
	templates.AddCommand(newTemplatesValidateCommand(templateStore))
	templates.AddCommand(newTemplatesEditCommand(templateStore, options.StateDir))
	templates.AddCommand(newTemplatesRemoveCommand(templateStore, options))
	templates.AddCommand(newTemplatePrepareCommand(options, templateStore))
	templates.AddCommand(newTemplateRepositoriesCommand(templateStore, options.StateDir))
	templates.AddCommand(newTemplateInitializeCommand(templateStore, options.StateDir))
	return templates
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
			template, err := templateStore.Load(args[0])
			if err != nil {
				return err
			}
			if isDryRun(command) {
				if err := template.Validate(); err != nil {
					return err
				}
				return writeMutation(command, "templates.prepare", "valid", "", args[0])
			}
			serviceOptions := projectservice.Options{StateDir: options.StateDir, DataDir: options.DataDir, TmuxSocket: options.TmuxSocket}
			if !WantsJSON(command) {
				serviceOptions.Progress = func(message string) {
					_, _ = fmt.Fprintln(command.ErrOrStderr(), message)
				}
			}
			service := projectservice.NewService(serviceOptions)
			queued, err := service.TopUpPool(args[0], template, template.EffectivePoolDepth())
			if err != nil {
				return err
			}
			prepared := make([]string, 0, len(queued))
			for _, entry := range queued {
				environment, err := service.PrepareQueued(entry.ID, entry.QueueToken)
				if err != nil {
					return err
				}
				prepared = append(prepared, environment.ID)
			}
			if WantsJSON(command) {
				return writeJSONOutput(command, templatePrepareOutput{SchemaVersion: jsonSchemaVersion, Template: args[0], Environments: prepared})
			}
			if len(prepared) == 0 {
				_, err = fmt.Fprintf(command.OutOrStdout(), "The Prepared Environment pool for Project Template %q is full\n", args[0])
				return err
			}
			for _, id := range prepared {
				if _, err := fmt.Fprintf(command.OutOrStdout(), "Prepared Environment %q for Project Template %q\n", id, args[0]); err != nil {
					return err
				}
			}
			return nil
		},
	}
	setArguments(command, requiredArgument("template"))
	command.ValidArgsFunction = templateNameCompletion(templateStore)
	return command
}

func newTemplatesCreateCommand(templateStore store.TemplateStore, stateDir string) *cobra.Command {
	var fromFile string
	var fromStdin bool
	command := &cobra.Command{
		Use:   "create NAME",
		Short: "Create an empty Project Template",
		Long: "Create an empty Project Template.\n\nNAME is the reusable template name. After creation, add one or more " +
			"repository specifications.\n\nWith --from-file or --from-stdin, twt2 reads one strict Project Template " +
			"YAML document. NAME stays required: it sets the template name, and a different name in the document is an error.",
		Example: `  twt2 templates create everysphere
  twt2 templates repos add everysphere app git@github.com:acme/app.git
  twt2 templates create everysphere --from-file ./everysphere.yaml`,
		Args: exactArgs("NAME"),
		RunE: func(command *cobra.Command, args []string) error {
			if fromFile != "" && fromStdin {
				return invalidUsage(command, "do not use --from-file together with --from-stdin")
			}
			template := domain.NewTemplate(args[0])
			if fromFile != "" || fromStdin {
				decoded, err := readTemplateDocument(command, fromFile, fromStdin)
				if err != nil {
					return err
				}
				if decoded.Name != "" && decoded.Name != args[0] {
					return invalidUsage(command, "the Project Template document contains name %q; the NAME argument is %q", decoded.Name, args[0])
				}
				decoded.Name = args[0]
				template = decoded
			}
			lock, err := store.AcquireMutationLock(stateDir)
			if err != nil {
				return err
			}
			defer lock.Release()
			if err := templateStore.ValidateCreate(template); err != nil {
				return err
			}
			if isDryRun(command) {
				return writeMutation(command, "templates.create", "valid", "", args[0])
			}
			if err := templateStore.Create(template); err != nil {
				return err
			}
			if WantsJSON(command) {
				return writeMutation(command, "templates.create", "applied", "", args[0])
			}
			_, err = fmt.Fprintf(command.OutOrStdout(), "Created Project Template %q\n", args[0])
			return err
		},
	}
	command.Flags().StringVar(&fromFile, "from-file", "", "Read the Project Template YAML from this file")
	command.Flags().BoolVar(&fromStdin, "from-stdin", false, "Read the Project Template YAML from standard input")
	setArguments(command, requiredArgument("name"))
	return command
}

// readTemplateDocument decodes one strict Project Template YAML document from
// a file or from standard input.
func readTemplateDocument(command *cobra.Command, path string, useStdin bool) (domain.Template, error) {
	if useStdin {
		return store.DecodeTemplate(io.LimitReader(command.InOrStdin(), 1024*1024), "standard input")
	}
	file, err := os.Open(path)
	if err != nil {
		return domain.Template{}, clierr.New(clierr.NotFound, "read Project Template file %q: %v", path, err)
	}
	defer file.Close()
	return store.DecodeTemplate(file, fmt.Sprintf("%q", path))
}

func newTemplatesListCommand(templateStore store.TemplateStore) *cobra.Command {
	var limit int
	command := &cobra.Command{
		Use:   "list",
		Short: "List Project Templates",
		Args:  noArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			names, err := templateStore.List()
			if err != nil {
				return err
			}
			names, total, truncated, err := applyLimit(names, limit)
			if err != nil {
				return err
			}
			if WantsJSON(command) {
				entries := make([]templateListEntry, 0, len(names))
				for _, name := range names {
					entries = append(entries, templateListEntry{Name: name})
				}
				return writeJSONOutput(command, templatesListOutput{SchemaVersion: jsonSchemaVersion, Templates: entries, TotalCount: total, Truncated: truncated})
			}
			for _, name := range names {
				if _, err := fmt.Fprintln(command.OutOrStdout(), name); err != nil {
					return err
				}
			}
			if total == 0 {
				_, err = fmt.Fprintln(command.ErrOrStderr(), "No Project Templates exist. Run 'twt2 templates create NAME'.")
				return err
			}
			return nil
		},
	}
	command.Flags().IntVar(&limit, "limit", 0, "Limit the number of results; zero returns all results")
	return command
}

func newTemplatesShowCommand(templateStore store.TemplateStore) *cobra.Command {
	command := &cobra.Command{
		Use:   "show NAME",
		Short: "Show a Project Template",
		Args:  exactArgs("NAME"),
		RunE: func(command *cobra.Command, args []string) error {
			template, err := templateStore.Load(args[0])
			if err != nil {
				return err
			}
			if WantsJSON(command) {
				return writeJSONOutput(command, templateShowOutput{SchemaVersion: jsonSchemaVersion, Template: template})
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
	command.ValidArgsFunction = templateNameCompletion(templateStore)
	return command
}

func newTemplatesPathCommand(templateStore store.TemplateStore) *cobra.Command {
	command := &cobra.Command{
		Use:   "path NAME",
		Short: "Print the Project Template YAML path",
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
		Short: "Validate a Project Template",
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
					Status: "valid", Name: args[0], Warnings: warnings,
				})
			}
			if _, err := fmt.Fprintf(command.OutOrStdout(), "Project Template %q is valid\n", args[0]); err != nil {
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

func newTemplatesEditCommand(templateStore store.TemplateStore, stateDir string) *cobra.Command {
	command := &cobra.Command{
		Use:   "edit NAME",
		Short: "Edit a Project Template YAML file in your editor",
		Args:  exactArgs("NAME"),
		RunE: func(command *cobra.Command, args []string) error {
			editor := os.Getenv("VISUAL")
			if strings.TrimSpace(editor) == "" {
				editor = os.Getenv("EDITOR")
			}
			if strings.TrimSpace(editor) == "" {
				return invalidUsage(command, "set VISUAL or EDITOR to the editor command that twt2 must start")
			}
			path, err := templateStore.Path(args[0])
			if err != nil {
				return err
			}
			if isDryRun(command) {
				return writeMutation(command, "templates.edit", "valid", "", args[0])
			}
			lock, err := store.AcquireMutationLock(stateDir)
			if err != nil {
				return err
			}
			defer lock.Release()
			parts := strings.Fields(editor)
			process := exec.Command(parts[0], append(parts[1:], path)...)
			process.Stdin = os.Stdin
			process.Stdout = command.OutOrStdout()
			process.Stderr = command.ErrOrStderr()
			if err := process.Run(); err != nil {
				return fmt.Errorf("run the editor %q: %w", editor, err)
			}
			if _, err := templateStore.Load(args[0]); err != nil {
				return clierr.WithHint(clierr.Wrap(clierr.UnsafeState, err),
					"Fix the file or run 'twt2 templates validate %s'.", args[0])
			}
			if WantsJSON(command) {
				return writeMutation(command, "templates.edit", "applied", "", args[0])
			}
			_, err = fmt.Fprintf(command.OutOrStdout(), "Project Template %q is valid\n", args[0])
			return err
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
		Short:   "Delete a Project Template YAML file",
		Args:    exactArgs("NAME"),
		RunE: func(command *cobra.Command, args []string) error {
			if _, err := templateStore.Path(args[0]); err != nil {
				return err
			}
			if err := checkTemplateIsUnused(options, args[0]); err != nil {
				return err
			}
			if isDryRun(command) {
				return writeMutation(command, "templates.remove", "valid", "", args[0])
			}
			lock, err := store.AcquireMutationLock(options.StateDir)
			if err != nil {
				return err
			}
			defer lock.Release()
			if err := checkTemplateIsUnused(options, args[0]); err != nil {
				return err
			}
			if err := templateStore.Delete(args[0]); err != nil {
				return err
			}
			if WantsJSON(command) {
				return writeMutation(command, "templates.remove", "applied", "", args[0])
			}
			_, err = fmt.Fprintf(command.OutOrStdout(), "Removed Project Template %q\n", args[0])
			return err
		},
	}
	setArguments(command, requiredArgument("name"))
	command.ValidArgsFunction = templateNameCompletion(templateStore)
	return command
}

// checkTemplateIsUnused refuses Project Template removal while a Project
// record still names the Project Template.
func checkTemplateIsUnused(options Options, name string) error {
	projects, err := store.NewProjectStore(options.StateDir).List()
	if err != nil {
		return err
	}
	users := make([]string, 0, len(projects))
	for _, project := range projects {
		if project.TemplateName == name {
			users = append(users, project.Name)
		}
	}
	if len(users) == 0 {
		return nil
	}
	return clierr.WithHint(
		clierr.New(clierr.PreconditionFailed, "Project Template %q is used by %d Projects: %s", name, len(users), strings.Join(users, ", ")),
		"Remove these Projects first with 'twt2 projects remove PROJECT --apply'.")
}

func newTemplateRepositoriesCommand(templateStore store.TemplateStore, stateDir string) *cobra.Command {
	repositories := groupCommand(&cobra.Command{
		Use:     "repos",
		Aliases: []string{"repositories"},
		Short:   "Manage repository specifications",
	})
	repositories.AddCommand(newTemplateRepositoriesAddCommand(templateStore, stateDir))
	repositories.AddCommand(newTemplateRepositoriesRemoveCommand(templateStore, stateDir))
	return repositories
}

func newTemplateRepositoriesAddCommand(templateStore store.TemplateStore, stateDir string) *cobra.Command {
	var depth int
	var remotes []string
	var defaultBranch string
	var windowName string
	command := &cobra.Command{
		Use:   "add TEMPLATE REPO URL",
		Short: "Add a repository specification",
		Args:  exactArgs("TEMPLATE", "REPO", "URL"),
		RunE: func(command *cobra.Command, args []string) error {
			lock, err := store.AcquireMutationLock(stateDir)
			if err != nil {
				return err
			}
			defer lock.Release()
			if err := store.ValidateResourceName(args[1]); err != nil {
				return fmt.Errorf("invalid repository name: %w", err)
			}
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
			template, err := templateStore.Load(args[0])
			if err != nil {
				return err
			}
			updated, err := addTemplateRepository(template, domain.RepositorySpec{
				Name: args[1],
				Clone: domain.CloneSpec{
					URL:   args[2],
					Depth: depth,
				},
				Remotes:       extraRemotes,
				DefaultBranch: defaultBranch,
				WindowName:    windowName,
			})
			if err != nil {
				return err
			}
			if isDryRun(command) {
				return writeMutation(command, "templates.repos.add", "valid", "", args[1])
			}
			if err := templateStore.Save(updated); err != nil {
				return err
			}
			if WantsJSON(command) {
				return writeMutation(command, "templates.repos.add", "applied", "", args[1])
			}
			_, err = fmt.Fprintf(command.OutOrStdout(), "Added repository %q to Project Template %q\n", args[1], args[0])
			return err
		},
	}
	command.Flags().IntVar(&depth, "depth", 0, "Set the clone depth; zero gets all history")
	command.Flags().StringArrayVar(&remotes, "remote", nil, "Add an extra remote as name=url")
	command.Flags().StringVar(&defaultBranch, "default-branch", "", "Set the default branch")
	command.Flags().StringVar(&windowName, "window-name", "", "Set the tmux window name")
	setArguments(command, requiredArgument("template"), requiredArgument("repo"), requiredArgument("url"))
	command.ValidArgsFunction = templateNameCompletion(templateStore)
	return command
}

// addTemplateRepository adds one repository specification to a Project
// Template and validates the result.
func addTemplateRepository(template domain.Template, repository domain.RepositorySpec) (domain.Template, error) {
	for _, existing := range template.Repositories {
		if existing.Name == repository.Name {
			return template, clierr.New(clierr.AlreadyExists, "repository %q already exists in Project Template %q", repository.Name, template.Name)
		}
	}
	template.Repositories = append(template.Repositories, repository)
	if err := template.Validate(); err != nil {
		return template, err
	}
	return template, nil
}

func newTemplateRepositoriesRemoveCommand(templateStore store.TemplateStore, stateDir string) *cobra.Command {
	command := &cobra.Command{
		Use:     "remove TEMPLATE REPO",
		Aliases: []string{"rm"},
		Short:   "Remove a repository specification",
		Args:    exactArgs("TEMPLATE", "REPO"),
		RunE: func(command *cobra.Command, args []string) error {
			lock, err := store.AcquireMutationLock(stateDir)
			if err != nil {
				return err
			}
			defer lock.Release()
			template, err := templateStore.Load(args[0])
			if err != nil {
				return err
			}
			kept := make([]domain.RepositorySpec, 0, len(template.Repositories))
			for _, repository := range template.Repositories {
				if repository.Name != args[1] {
					kept = append(kept, repository)
				}
			}
			if len(kept) == len(template.Repositories) {
				return clierr.New(clierr.NotFound, "repository %q does not exist in Project Template %q", args[1], args[0])
			}
			template.Repositories = kept
			if err := template.Validate(); err != nil {
				return err
			}
			if isDryRun(command) {
				return writeMutation(command, "templates.repos.remove", "valid", "", args[1])
			}
			if err := templateStore.Save(template); err != nil {
				return err
			}
			if WantsJSON(command) {
				return writeMutation(command, "templates.repos.remove", "applied", "", args[1])
			}
			_, err = fmt.Fprintf(command.OutOrStdout(), "Removed repository %q from Project Template %q\n", args[1], args[0])
			return err
		},
	}
	setArguments(command, requiredArgument("template"), requiredArgument("repo"))
	command.ValidArgsFunction = templateRepositoryCompletion(templateStore)
	return command
}

func newTemplateInitializeCommand(templateStore store.TemplateStore, stateDir string) *cobra.Command {
	initialize := groupCommand(&cobra.Command{
		Use:   "init",
		Short: "Manage Project Template initialization",
	})
	var workingDirectory string
	var repository string
	set := &cobra.Command{
		Use:   "set TEMPLATE [--repo REPO] [--cwd PATH] -- COMMAND...",
		Short: "Set a Project or repository initialization command",
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
			lock, err := store.AcquireMutationLock(stateDir)
			if err != nil {
				return err
			}
			defer lock.Release()
			template, err := templateStore.Load(args[0])
			if err != nil {
				return err
			}
			operation := "templates.init.set"
			name := args[0]
			if strings.TrimSpace(repository) != "" {
				operation = "templates.repos.init.set"
				name = repository
				found := false
				for index := range template.Repositories {
					if template.Repositories[index].Name != repository {
						continue
					}
					template.Repositories[index].Initialize = &domain.InitializeSpec{
						Command: append([]string(nil), args[1:]...),
					}
					found = true
					break
				}
				if !found {
					return clierr.New(clierr.NotFound, "repository %q does not exist in Project Template %q", repository, args[0])
				}
			} else {
				template.Initialize = &domain.InitializeSpec{
					Command:          append([]string(nil), args[1:]...),
					WorkingDirectory: workingDirectory,
				}
			}
			if err := template.Validate(); err != nil {
				return err
			}
			if isDryRun(command) {
				return writeMutation(command, operation, "valid", "", name)
			}
			if err := templateStore.Save(template); err != nil {
				return err
			}
			if WantsJSON(command) {
				return writeMutation(command, operation, "applied", "", name)
			}
			if operation == "templates.repos.init.set" {
				_, err = fmt.Fprintf(command.OutOrStdout(), "Set initialization for repository %q in Project Template %q\n", repository, args[0])
				return err
			}
			_, err = fmt.Fprintf(command.OutOrStdout(), "Set initialization for Project Template %q\n", args[0])
			return err
		},
	}
	set.Flags().StringVar(&workingDirectory, "cwd", "", "Set the Project initialization working directory, relative to the Project root")
	set.Flags().StringVar(&repository, "repo", "", "Set repository initialization for this repository instead of Project initialization")
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
