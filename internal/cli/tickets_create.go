package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"github.com/jpugliesi/tmux-worktree/internal/store"
	ticketservice "github.com/jpugliesi/tmux-worktree/internal/ticket"
	"github.com/spf13/cobra"
)

// ungroupedProjectSentinel is the Project picker row for a Ticket with no Project.
// Parentheses make it an invalid resource name, so it cannot collide with a
// Project.
const ungroupedProjectSentinel = "(none)"

// createTicketWizard collects title, Project, and description for a person at a
// terminal. DESCRIPTION and - never enter this path. --title skips the
// title prompt. --project skips the picker and never creates a missing Project.
func createTicketWizard(command *cobra.Command, options Options, service ticketservice.Store, request ticketservice.CreateRequest) (ticketservice.CreateRequest, error) {
	if command.Flags().Changed("project") && strings.TrimSpace(request.Project) != "" {
		if _, err := service.Project(request.Project); err != nil {
			return request, err
		}
	}
	if strings.TrimSpace(request.Title) == "" {
		title, err := promptTicketLine(command, "Title: ")
		if err != nil {
			return request, err
		}
		if title == "" {
			return request, invalidUsageWithHint(command, "Pass DESCRIPTION, --title, or -.",
				"Ticket creation was canceled; no title was given")
		}
		request.Title = title
	}
	if !command.Flags().Changed("project") {
		choice, err := pickTicketProject(command, options, service)
		if err != nil {
			return request, err
		}
		project, ensure, err := resolveWizardProject(command, service, choice)
		if err != nil {
			return request, err
		}
		request.Project = project
		request.EnsureProject = ensure
	}
	body, err := readTicketDescriptionInEditor(command, options)
	if err != nil {
		return request, err
	}
	request.Body = body
	return request, nil
}

// resolveWizardProject maps one picker result to a Project name. "(none)" leaves
// the Ticket ungrouped. An existing Project is used as-is. A new name must
// pass resource-name rules, then the person confirms create.
func resolveWizardProject(command *cobra.Command, service ticketservice.Store, choice string) (string, bool, error) {
	if choice == "" || choice == ungroupedProjectSentinel {
		return "", false, nil
	}
	if choice == "templates" {
		return "", false, invalidUsageWithHint(command, "Pick an existing Project, or type a different name.",
			"the Project name %q is reserved", choice)
	}
	if err := store.ValidateResourceName(choice); err != nil {
		return "", false, invalidUsageWithHint(command, "Use letters, numbers, dots, hyphens, or underscores.",
			"invalid Project name %q", choice)
	}
	if _, err := service.Project(choice); err == nil {
		return choice, false, nil
	}
	ok, err := confirmNewProject(command, choice)
	if err != nil {
		return "", false, err
	}
	if !ok {
		return "", false, invalidUsageWithHint(command, "Pick an existing Project, or confirm the new name.",
			"Ticket creation was canceled; Project %q was not created", choice)
	}
	return choice, true, nil
}

// pickTicketProject shows the Project picker: (none), then every Project name.
func pickTicketProject(command *cobra.Command, options Options, service ticketservice.Store) (string, error) {
	projects, err := service.Projects()
	if err != nil {
		return "", err
	}
	lines := make([]string, 0, len(projects)+1)
	lines = append(lines, ungroupedProjectSentinel)
	for _, project := range projects {
		lines = append(lines, project.Name)
	}
	choice, err := options.PickTicketProject(command, lines)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(choice), nil
}

// realPickTicketProject selects one line with fzf when it is installed, or with
// a numbered list that also accepts a typed name.
func realPickTicketProject(command *cobra.Command, lines []string) (string, error) {
	if _, err := exec.LookPath("fzf"); err == nil {
		return fzfPickOrQuery(lines)
	}
	return numberedProjectPick(command, lines)
}

// fzfPickOrQuery lists Projects in fzf. Enter accepts a match, or the typed
// query when nothing matches. This helper is separate from fzfPick, which
// requires an exact listed line.
func fzfPickOrQuery(lines []string) (string, error) {
	fzf := exec.Command("fzf",
		"--print-query",
		"--bind", "enter:accept-or-print-query",
		"--header", "Select a Project, or type a new name.")
	fzf.Stdin = strings.NewReader(strings.Join(lines, "\n") + "\n")
	fzf.Stderr = os.Stderr
	selected, err := fzf.Output()
	output := strings.TrimRight(string(selected), "\n")
	if output == "" {
		if err != nil {
			return "", fmt.Errorf("no Project was selected")
		}
		return "", fmt.Errorf("no Project was selected")
	}
	parts := strings.Split(output, "\n")
	query := strings.TrimSpace(parts[0])
	selection := ""
	if len(parts) > 1 {
		selection = strings.TrimSpace(parts[len(parts)-1])
	}
	return resolveFzfProjectChoice(lines, query, selection)
}

// resolveFzfProjectChoice prefers a listed selection. Otherwise it uses the
// typed query as a new Project name.
func resolveFzfProjectChoice(lines []string, query, selection string) (string, error) {
	if selection != "" {
		for _, line := range lines {
			if line == selection {
				return selection, nil
			}
		}
	}
	if query != "" {
		return query, nil
	}
	return "", fmt.Errorf("no Project was selected")
}

// numberedProjectPick prints a numbered Project list and reads a number or a name.
func numberedProjectPick(command *cobra.Command, lines []string) (string, error) {
	if !interactiveInput(command.InOrStdin()) {
		return "", invalidUsage(command, "missing Project; pass --project in a script")
	}
	errOut := command.ErrOrStderr()
	for index, line := range lines {
		if _, err := fmt.Fprintf(errOut, "%d) %s\n", index, line); err != nil {
			return "", err
		}
	}
	name, err := promptTicketLine(command, "Project name or number: ")
	if err != nil {
		return "", err
	}
	choice, err := resolveProjectPick(lines, name)
	if err != nil {
		return "", invalidUsage(command, "%s", err.Error())
	}
	return choice, nil
}

// resolveProjectPick maps typed picker input to a line. Exact listed names win
// over numbers, so a Project named "1" is not taken as index 1.
func resolveProjectPick(lines []string, input string) (string, error) {
	input = strings.TrimSpace(input)
	if input == "" {
		return "", fmt.Errorf("no Project was selected")
	}
	for _, line := range lines {
		if line == input {
			return line, nil
		}
	}
	number, err := strconv.Atoi(input)
	if err == nil {
		if number < 0 || number >= len(lines) {
			return "", fmt.Errorf("give a Project number between 0 and %d", len(lines)-1)
		}
		return lines[number], nil
	}
	return input, nil
}

// confirmNewProject asks whether to create a missing Project. Enter means yes.
func confirmNewProject(command *cobra.Command, name string) (bool, error) {
	if _, err := fmt.Fprintf(command.ErrOrStderr(), "Project %q does not exist. Create it? [Y/n] ", name); err != nil {
		return false, err
	}
	line, err := readTicketPromptLine(command)
	if err != nil {
		return false, err
	}
	yes, ok := parseYesDefault(line)
	if !ok {
		return false, invalidUsageWithHint(command, "Answer y or n.",
			"unrecognized confirm answer %q", line)
	}
	return yes, nil
}

// parseYesDefault parses a [Y/n] answer. Empty, y, and yes mean yes. n and
// no mean no. Any other value is invalid.
func parseYesDefault(answer string) (bool, bool) {
	switch strings.ToLower(strings.TrimSpace(answer)) {
	case "", "y", "yes":
		return true, true
	case "n", "no":
		return false, true
	default:
		return false, false
	}
}

// promptTicketLine writes a prompt to stderr and reads one line from stdin.
func promptTicketLine(command *cobra.Command, prompt string) (string, error) {
	if _, err := fmt.Fprint(command.ErrOrStderr(), prompt); err != nil {
		return "", err
	}
	return readTicketPromptLine(command)
}

func readTicketPromptLine(command *cobra.Command) (string, error) {
	line, err := readLine(command.InOrStdin())
	if err != nil && !errors.Is(err, io.EOF) {
		return "", fmt.Errorf("read the prompt: %w", err)
	}
	return strings.TrimSpace(line), nil
}

// readLine reads one line from r without buffering the next line. Title and
// confirm share standard input, so a bufio.Reader would steal the second
// answer.
func readLine(r io.Reader) (string, error) {
	var buf []byte
	one := make([]byte, 1)
	for {
		n, err := r.Read(one)
		if n > 0 {
			if one[0] == '\n' {
				return string(buf), nil
			}
			if one[0] != '\r' {
				buf = append(buf, one[0])
			}
		}
		if err != nil {
			if errors.Is(err, io.EOF) && len(buf) > 0 {
				return string(buf), nil
			}
			return string(buf), err
		}
	}
}

// readTicketDescriptionInEditor opens VISUAL or EDITOR on an empty file and
// returns the saved text. The CLI writes YAML frontmatter itself.
func readTicketDescriptionInEditor(command *cobra.Command, options Options) (string, error) {
	temp, err := os.CreateTemp("", "twt-ticket-*.md")
	if err != nil {
		return "", fmt.Errorf("create the ticket draft file: %w", err)
	}
	path := temp.Name()
	defer os.Remove(path)
	if err := temp.Close(); err != nil {
		return "", fmt.Errorf("create the ticket draft file: %w", err)
	}
	if err := options.OpenEditor(path); err != nil {
		return "", err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read the ticket draft file: %w", err)
	}
	if strings.TrimSpace(string(data)) == "" {
		return "", invalidUsageWithHint(command, "Write the description and save the file, or pass DESCRIPTION.",
			"the editor saved an empty ticket")
	}
	return string(data), nil
}
