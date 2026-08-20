package cli

import (
	"fmt"
	"strings"

	"github.com/jpugliesi/tmux-worktree/internal/domain"
	projectservice "github.com/jpugliesi/tmux-worktree/internal/project"
	"github.com/jpugliesi/tmux-worktree/internal/store"
	"github.com/spf13/cobra"
)

type templatesListOutput struct {
	SchemaVersion int      `json:"schemaVersion"`
	Templates     []string `json:"templates"`
}

type templateShowOutput struct {
	SchemaVersion int             `json:"schemaVersion"`
	Template      domain.Template `json:"template"`
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
	templates.AddCommand(newTemplatesValidateCommand(templateStore))
	templates.AddCommand(newTemplatePrepareCommand(options, templateStore))
	templates.AddCommand(newTemplateRepositoriesCommand(templateStore, options.StateDir))
	templates.AddCommand(newTemplateInitializeCommand(templateStore, options.StateDir))
	return templates
}

func newTemplatePrepareCommand(options Options, templateStore store.TemplateStore) *cobra.Command {
	service := projectservice.NewService(projectservice.Options{StateDir: options.StateDir, DataDir: options.DataDir, TmuxSocket: options.TmuxSocket})
	return &cobra.Command{
		Use:   "prepare TEMPLATE",
		Short: "Prepare the next initialized environment",
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
			environment, err := service.Prepare(args[0], template)
			if err != nil {
				return err
			}
			if WantsJSON(command) {
				return writeMutation(command, "templates.prepare", "applied", environment.ID, environment.TemplateName)
			}
			_, err = fmt.Fprintf(command.OutOrStdout(), "Prepared Environment %q for Project Template %q\n", environment.ID, environment.TemplateName)
			return err
		},
	}
}

func newTemplatesCreateCommand(templateStore store.TemplateStore, stateDir string) *cobra.Command {
	return &cobra.Command{
		Use:   "create NAME",
		Short: "Create an empty Project Template",
		Long:  "Create an empty Project Template.\n\nNAME is the reusable template name. After creation, add one or more repository specifications.",
		Example: `  twt2 templates create everysphere
  twt2 templates repos add everysphere app git@github.com:acme/app.git`,
		Args: exactArgs("NAME"),
		RunE: func(command *cobra.Command, args []string) error {
			lock, err := store.AcquireMutationLock(stateDir)
			if err != nil {
				return err
			}
			defer lock.Release()
			template := domain.NewTemplate(args[0])
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
			names, err = applyLimit(names, limit)
			if err != nil {
				return err
			}
			if WantsJSON(command) {
				return writeJSONOutput(command, templatesListOutput{SchemaVersion: jsonSchemaVersion, Templates: names})
			}
			for _, name := range names {
				if _, err := fmt.Fprintln(command.OutOrStdout(), name); err != nil {
					return err
				}
			}
			return nil
		},
	}
	command.Flags().IntVar(&limit, "limit", 0, "Limit the number of results; zero returns all results")
	return command
}

func newTemplatesShowCommand(templateStore store.TemplateStore) *cobra.Command {
	return &cobra.Command{
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
}

func newTemplatesValidateCommand(templateStore store.TemplateStore) *cobra.Command {
	return &cobra.Command{
		Use:   "validate NAME",
		Short: "Validate a Project Template",
		Args:  exactArgs("NAME"),
		RunE: func(command *cobra.Command, args []string) error {
			if _, err := templateStore.Load(args[0]); err != nil {
				return err
			}
			if WantsJSON(command) {
				return writeMutation(command, "templates.validate", "valid", "", args[0])
			}
			_, err := fmt.Fprintf(command.OutOrStdout(), "Project Template %q is valid\n", args[0])
			return err
		},
	}
}

func newTemplateRepositoriesCommand(templateStore store.TemplateStore, stateDir string) *cobra.Command {
	repositories := groupCommand(&cobra.Command{
		Use:     "repos",
		Aliases: []string{"repositories"},
		Short:   "Manage repository specifications",
	})
	repositories.AddCommand(newTemplateRepositoriesAddCommand(templateStore, stateDir))
	repositories.AddCommand(newTemplateRepositoryInitializeCommand(templateStore, stateDir))
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
			for _, repository := range template.Repositories {
				if repository.Name == args[1] {
					return fmt.Errorf("repository %q already exists in Project Template %q", args[1], args[0])
				}
			}
			template.Repositories = append(template.Repositories, domain.RepositorySpec{
				Name: args[1],
				Clone: domain.CloneSpec{
					URL:   args[2],
					Depth: depth,
				},
				Remotes:       extraRemotes,
				DefaultBranch: defaultBranch,
				WindowName:    windowName,
			})
			if err := template.Validate(); err != nil {
				return err
			}
			if isDryRun(command) {
				return writeMutation(command, "templates.repos.add", "valid", "", args[1])
			}
			if err := templateStore.Save(template); err != nil {
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
	return command
}

func newTemplateRepositoryInitializeCommand(templateStore store.TemplateStore, stateDir string) *cobra.Command {
	initialize := groupCommand(&cobra.Command{
		Use:   "init",
		Short: "Manage repository initialization",
	})
	initialize.AddCommand(&cobra.Command{
		Use:   "set TEMPLATE REPO -- COMMAND...",
		Short: "Set the repository initialization command",
		Args: func(command *cobra.Command, args []string) error {
			if command.ArgsLenAtDash() != 2 || len(args) < 3 {
				return invalidUsage(command, "expected TEMPLATE REPO -- COMMAND...")
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
			for index := range template.Repositories {
				if template.Repositories[index].Name != args[1] {
					continue
				}
				template.Repositories[index].Initialize = &domain.InitializeSpec{
					Command: append([]string(nil), args[2:]...),
				}
				if err := template.Validate(); err != nil {
					return err
				}
				if isDryRun(command) {
					return writeMutation(command, "templates.repos.init.set", "valid", "", args[1])
				}
				if err := templateStore.Save(template); err != nil {
					return err
				}
				if WantsJSON(command) {
					return writeMutation(command, "templates.repos.init.set", "applied", "", args[1])
				}
				_, err = fmt.Fprintf(command.OutOrStdout(), "Set initialization for repository %q in Project Template %q\n", args[1], args[0])
				return err
			}
			return fmt.Errorf("repository %q does not exist in Project Template %q", args[1], args[0])
		},
	})
	return initialize
}

func newTemplateInitializeCommand(templateStore store.TemplateStore, stateDir string) *cobra.Command {
	initialize := groupCommand(&cobra.Command{
		Use:   "init",
		Short: "Manage Project Template initialization",
	})
	var workingDirectory string
	set := &cobra.Command{
		Use:   "set TEMPLATE --cwd PATH -- COMMAND...",
		Short: "Set the Project initialization command",
		Args: func(command *cobra.Command, args []string) error {
			if !command.Flags().Changed("cwd") || strings.TrimSpace(workingDirectory) == "" {
				return invalidUsage(command, "missing required flag --cwd PATH")
			}
			if command.ArgsLenAtDash() != 1 || len(args) < 2 {
				return invalidUsage(command, "expected TEMPLATE --cwd PATH -- COMMAND...")
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
			template.Initialize = &domain.InitializeSpec{
				Command:          append([]string(nil), args[1:]...),
				WorkingDirectory: workingDirectory,
			}
			if err := template.Validate(); err != nil {
				return err
			}
			if isDryRun(command) {
				return writeMutation(command, "templates.init.set", "valid", "", args[0])
			}
			if err := templateStore.Save(template); err != nil {
				return err
			}
			if WantsJSON(command) {
				return writeMutation(command, "templates.init.set", "applied", "", args[0])
			}
			_, err = fmt.Fprintf(command.OutOrStdout(), "Set initialization for Project Template %q\n", args[0])
			return err
		},
	}
	set.Flags().StringVar(&workingDirectory, "cwd", "", "Set the initialization working directory")
	_ = set.MarkFlagRequired("cwd")
	initialize.AddCommand(set)
	return initialize
}
