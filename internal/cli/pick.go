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

	"github.com/spf13/cobra"
)

// pickOptions configures one fzf or numbered-list picker.
type pickOptions struct {
	Noun        string
	MissingHint string
	FzfArgs     []string
}

// pickLine selects one picker line with fzf when fzf is installed, or with a
// numbered list on the terminal.
func pickLine(command *cobra.Command, lines []string, options pickOptions) (int, error) {
	if _, err := exec.LookPath("fzf"); err == nil {
		return fzfPick(lines, options.Noun, options.FzfArgs...)
	}
	return numberedPick(command, lines, options.Noun, options.MissingHint)
}

// fzfPick pipes the picker lines to fzf and returns the index of the
// selected line. fzf draws its finder on the terminal.
func fzfPick(lines []string, noun string, extraArgs ...string) (int, error) {
	fzf := exec.Command("fzf", extraArgs...)
	fzf.Stdin = strings.NewReader(strings.Join(lines, "\n") + "\n")
	fzf.Stderr = os.Stderr
	selected, err := fzf.Output()
	if err != nil {
		return 0, fmt.Errorf("no %s was selected", noun)
	}
	choice := strings.TrimSpace(string(selected))
	for index, line := range lines {
		if line == choice {
			return index, nil
		}
	}
	return 0, fmt.Errorf("the %s picker returned an unknown line %q", noun, choice)
}

// numberedPick prints a numbered list and reads the selected number from
// standard input.
func numberedPick(command *cobra.Command, lines []string, noun, missingHint string) (int, error) {
	if !interactiveInput(command.InOrStdin()) {
		return 0, invalidUsage(command, missingHint)
	}
	errOut := command.ErrOrStderr()
	for index, line := range lines {
		if _, err := fmt.Fprintf(errOut, "%d) %s\n", index+1, line); err != nil {
			return 0, err
		}
	}
	if _, err := fmt.Fprintf(errOut, "%s number: ", noun); err != nil {
		return 0, err
	}
	line, err := bufio.NewReader(command.InOrStdin()).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return 0, fmt.Errorf("read the %s number: %w", noun, err)
	}
	number, err := strconv.Atoi(strings.TrimSpace(line))
	if err != nil || number < 1 || number > len(lines) {
		return 0, invalidUsage(command, "give a number between 1 and %d", len(lines))
	}
	return number - 1, nil
}

// shellQuote wraps one value in single quotes for a POSIX shell.
func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'"'"'`) + "'"
}
