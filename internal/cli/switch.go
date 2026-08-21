package cli

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/jpugliesi/tmux-worktree/internal/clierr"
	"github.com/jpugliesi/tmux-worktree/internal/domain"
	projectservice "github.com/jpugliesi/tmux-worktree/internal/project"
	"github.com/spf13/cobra"
)

func newSwitchCommand(options Options) *cobra.Command {
	service := options.projectService()
	command := &cobra.Command{
		Use:   "switch [PROJECT]",
		Short: "Switch the tmux client to a Project session",
		Args:  optionalArg("PROJECT"),
		PreRunE: func(command *cobra.Command, _ []string) error {
			if WantsJSON(command) {
				return invalidUsage(command, "switch is an interactive command and does not support JSON output")
			}
			return nil
		},
		RunE: func(command *cobra.Command, args []string) error {
			var project domain.Project
			var err error
			if len(args) == 1 {
				project, err = resolveProject(service, args[0])
			} else {
				project, err = pickSwitchProject(command, options, service)
			}
			if err != nil {
				return err
			}
			if isDryRun(command) {
				return writeSwitchPlan(command.OutOrStdout(), project)
			}
			if project.Status == domain.ProjectArchived {
				project, err = service.Open(project.ID)
				if err != nil {
					return err
				}
			}
			return openTmux(options, project.TmuxSession)
		},
	}
	setArguments(command, optionalArgument("project", "the interactive picker asks for it when absent"))
	command.ValidArgsFunction = projectNameCompletion(service)
	return command
}

// writeSwitchPlan tells the user what switch would do, without a change.
func writeSwitchPlan(out io.Writer, project domain.Project) error {
	if project.Status == domain.ProjectArchived {
		_, err := fmt.Fprintf(out, "Dry run: open archived Project %q, then switch the client to session %q.\n", project.Name, project.TmuxSession)
		return err
	}
	_, err := fmt.Fprintf(out, "Dry run: switch the client to session %q of Project %q.\n", project.TmuxSession, project.Name)
	return err
}

// pickSwitchProject shows the interactive Project picker and returns the
// selected Project. Active Projects come first, most recent first.
func pickSwitchProject(command *cobra.Command, options Options, service *projectservice.Service) (domain.Project, error) {
	projects, err := service.List()
	if err != nil {
		return domain.Project{}, err
	}
	if len(projects) == 0 {
		return domain.Project{}, clierr.New(clierr.NotFound, "no Projects exist; run 'twt start NAME' first")
	}
	sortProjectsForDisplay(projects)
	now := time.Now().UTC()
	lines := make([]string, 0, len(projects))
	for _, project := range projects {
		age := formatAge(now.Sub(projectAgeReference(project)))
		lines = append(lines, fmt.Sprintf("%s\t%s\t%s\t%s", project.Name, project.TemplateName, project.Status, age))
	}
	index, err := options.SwitchPick(command, lines)
	if err != nil {
		return domain.Project{}, err
	}
	if index < 0 || index >= len(projects) {
		return domain.Project{}, fmt.Errorf("the Project picker returned an invalid selection")
	}
	return projects[index], nil
}

// realSwitchPick selects one picker line with fzf when it is installed, or
// with a numbered list on the terminal.
func realSwitchPick(command *cobra.Command, lines []string) (int, error) {
	if _, err := exec.LookPath("fzf"); err == nil {
		return fzfPick(lines)
	}
	return numberedPick(command, lines)
}

// fzfPick pipes the picker lines to fzf and returns the index of the
// selected line. fzf draws its finder on the terminal.
func fzfPick(lines []string) (int, error) {
	fzf := exec.Command("fzf")
	fzf.Stdin = strings.NewReader(strings.Join(lines, "\n") + "\n")
	fzf.Stderr = os.Stderr
	selected, err := fzf.Output()
	if err != nil {
		return 0, fmt.Errorf("no Project was selected")
	}
	choice := strings.TrimSpace(string(selected))
	for index, line := range lines {
		if line == choice {
			return index, nil
		}
	}
	return 0, fmt.Errorf("the Project picker returned an unknown line %q", choice)
}

// numberedPick prints a numbered Project list and reads the selected number
// from standard input.
func numberedPick(command *cobra.Command, lines []string) (int, error) {
	if !interactiveInput(command.InOrStdin()) {
		return 0, invalidUsage(command, "missing PROJECT; use 'twt switch PROJECT' in a script")
	}
	errOut := command.ErrOrStderr()
	for index, line := range lines {
		if _, err := fmt.Fprintf(errOut, "%d) %s\n", index+1, line); err != nil {
			return 0, err
		}
	}
	if _, err := fmt.Fprint(errOut, "Project number: "); err != nil {
		return 0, err
	}
	line, err := bufio.NewReader(command.InOrStdin()).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return 0, fmt.Errorf("read the Project number: %w", err)
	}
	number, err := strconv.Atoi(strings.TrimSpace(line))
	if err != nil || number < 1 || number > len(lines) {
		return 0, invalidUsage(command, "give a Project number between 1 and %d", len(lines))
	}
	return number - 1, nil
}
