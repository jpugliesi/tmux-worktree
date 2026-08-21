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

// ungroupedBoardSentinel is the Board picker row for a Ticket with no Board.
// Parentheses make it an invalid resource name, so it cannot collide with a
// Board.
const ungroupedBoardSentinel = "(none)"

// createTicketWizard collects title, Board, and description for a person at a
// terminal. DESCRIPTION and --stdin never enter this path. --title skips the
// title prompt. --board skips the picker and never creates a missing Board.
func createTicketWizard(command *cobra.Command, options Options, service *ticketservice.Service, request ticketservice.CreateRequest) (ticketservice.CreateRequest, error) {
	if command.Flags().Changed("board") && strings.TrimSpace(request.Board) != "" {
		if _, err := service.Board(request.Board); err != nil {
			return request, err
		}
	}
	if strings.TrimSpace(request.Title) == "" {
		title, err := promptTicketLine(command, "Title: ")
		if err != nil {
			return request, err
		}
		if title == "" {
			return request, invalidUsageWithHint(command, "Pass DESCRIPTION, --title, or --stdin.",
				"Ticket creation was canceled; no title was given")
		}
		request.Title = title
	}
	if !command.Flags().Changed("board") {
		choice, err := pickTicketBoard(command, options, service)
		if err != nil {
			return request, err
		}
		board, ensure, err := resolveWizardBoard(command, service, choice)
		if err != nil {
			return request, err
		}
		request.Board = board
		request.EnsureBoard = ensure
	}
	body, err := readTicketDescriptionInEditor(command, options)
	if err != nil {
		return request, err
	}
	request.Body = body
	return request, nil
}

// resolveWizardBoard maps one picker result to a Board name. "(none)" leaves
// the Ticket ungrouped. An existing Board is used as-is. A new name must
// pass resource-name rules, then the person confirms create.
func resolveWizardBoard(command *cobra.Command, service *ticketservice.Service, choice string) (string, bool, error) {
	if choice == "" || choice == ungroupedBoardSentinel {
		return "", false, nil
	}
	if choice == "templates" {
		return "", false, invalidUsageWithHint(command, "Pick an existing Board, or type a different name.",
			"the Board name %q is reserved", choice)
	}
	if err := store.ValidateResourceName(choice); err != nil {
		return "", false, invalidUsageWithHint(command, "Use letters, numbers, dots, hyphens, or underscores.",
			"invalid Board name %q", choice)
	}
	if _, err := service.Board(choice); err == nil {
		return choice, false, nil
	}
	ok, err := confirmNewBoard(command, choice)
	if err != nil {
		return "", false, err
	}
	if !ok {
		return "", false, invalidUsageWithHint(command, "Pick an existing Board, or confirm the new name.",
			"Ticket creation was canceled; Board %q was not created", choice)
	}
	return choice, true, nil
}

// pickTicketBoard shows the Board picker: (none), then every Board name.
func pickTicketBoard(command *cobra.Command, options Options, service *ticketservice.Service) (string, error) {
	boards, err := service.Boards()
	if err != nil {
		return "", err
	}
	lines := make([]string, 0, len(boards)+1)
	lines = append(lines, ungroupedBoardSentinel)
	for _, board := range boards {
		lines = append(lines, board.Name)
	}
	choice, err := options.PickTicketBoard(command, lines)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(choice), nil
}

// realPickTicketBoard selects one line with fzf when it is installed, or with
// a numbered list that also accepts a typed name.
func realPickTicketBoard(command *cobra.Command, lines []string) (string, error) {
	if _, err := exec.LookPath("fzf"); err == nil {
		return fzfPickOrQuery(lines)
	}
	return numberedBoardPick(command, lines)
}

// fzfPickOrQuery lists Boards in fzf. Enter accepts a match, or the typed
// query when nothing matches. This helper is separate from fzfPick, which
// requires an exact listed line.
func fzfPickOrQuery(lines []string) (string, error) {
	fzf := exec.Command("fzf",
		"--print-query",
		"--bind", "enter:accept-or-print-query",
		"--header", "Select a Board, or type a new name.")
	fzf.Stdin = strings.NewReader(strings.Join(lines, "\n") + "\n")
	fzf.Stderr = os.Stderr
	selected, err := fzf.Output()
	output := strings.TrimRight(string(selected), "\n")
	if output == "" {
		if err != nil {
			return "", fmt.Errorf("no Board was selected")
		}
		return "", fmt.Errorf("no Board was selected")
	}
	parts := strings.Split(output, "\n")
	query := strings.TrimSpace(parts[0])
	selection := ""
	if len(parts) > 1 {
		selection = strings.TrimSpace(parts[len(parts)-1])
	}
	return resolveFzfBoardChoice(lines, query, selection)
}

// resolveFzfBoardChoice prefers a listed selection. Otherwise it uses the
// typed query as a new Board name.
func resolveFzfBoardChoice(lines []string, query, selection string) (string, error) {
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
	return "", fmt.Errorf("no Board was selected")
}

// numberedBoardPick prints a numbered Board list and reads a number or a name.
func numberedBoardPick(command *cobra.Command, lines []string) (string, error) {
	if !interactiveInput(command.InOrStdin()) {
		return "", invalidUsage(command, "missing Board; pass --board in a script")
	}
	errOut := command.ErrOrStderr()
	for index, line := range lines {
		if _, err := fmt.Fprintf(errOut, "%d) %s\n", index, line); err != nil {
			return "", err
		}
	}
	name, err := promptTicketLine(command, "Board name or number: ")
	if err != nil {
		return "", err
	}
	choice, err := resolveBoardPick(lines, name)
	if err != nil {
		return "", invalidUsage(command, "%s", err.Error())
	}
	return choice, nil
}

// resolveBoardPick maps typed picker input to a line. Exact listed names win
// over numbers, so a Board named "1" is not taken as index 1.
func resolveBoardPick(lines []string, input string) (string, error) {
	input = strings.TrimSpace(input)
	if input == "" {
		return "", fmt.Errorf("no Board was selected")
	}
	for _, line := range lines {
		if line == input {
			return line, nil
		}
	}
	number, err := strconv.Atoi(input)
	if err == nil {
		if number < 0 || number >= len(lines) {
			return "", fmt.Errorf("give a Board number between 0 and %d", len(lines)-1)
		}
		return lines[number], nil
	}
	return input, nil
}

// confirmNewBoard asks whether to create a missing Board. Enter means yes.
func confirmNewBoard(command *cobra.Command, name string) (bool, error) {
	if _, err := fmt.Fprintf(command.ErrOrStderr(), "Board %q does not exist. Create it? [Y/n] ", name); err != nil {
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
