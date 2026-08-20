package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"time"

	"github.com/jpugliesi/tmux-worktree/internal/clierr"
	"github.com/jpugliesi/tmux-worktree/internal/domain"
	"github.com/spf13/cobra"
)

const jsonSchemaVersion = 1

// statusValid and statusApplied are the two mutation result states. A dry
// run reports valid; a real mutation reports applied.
const (
	statusValid   = "valid"
	statusApplied = "applied"
)

type mutationOutput struct {
	SchemaVersion int    `json:"schemaVersion"`
	Operation     string `json:"operation"`
	Status        string `json:"status"`
	ID            string `json:"id,omitempty"`
	Name          string `json:"name,omitempty"`
}

type commandErrorOutput struct {
	SchemaVersion int          `json:"schemaVersion"`
	Error         commandError `json:"error"`
}

type commandError struct {
	Code        string `json:"code"`
	Message     string `json:"message"`
	Hint        string `json:"hint,omitempty"`
	HelpCommand string `json:"helpCommand,omitempty"`
}

type projectOutput struct {
	ID           string               `json:"id"`
	Name         string               `json:"name"`
	Template     string               `json:"template"`
	Status       domain.ProjectStatus `json:"status"`
	CreatedAt    string               `json:"createdAt"`
	ArchivedAt   string               `json:"archivedAt,omitempty"`
	Root         string               `json:"root"`
	Repositories []repositoryOutput   `json:"repositories"`
}

type repositoryOutput struct {
	Name       string `json:"name"`
	WindowName string `json:"windowName"`
}

type projectsListOutput struct {
	SchemaVersion int             `json:"schemaVersion"`
	Projects      []projectOutput `json:"projects"`
	TotalCount    int             `json:"totalCount"`
	Truncated     bool            `json:"truncated,omitempty"`
}

type projectShowOutput struct {
	SchemaVersion int           `json:"schemaVersion"`
	Project       projectOutput `json:"project"`
}

func toProjectOutput(project domain.Project) projectOutput {
	repositories := make([]repositoryOutput, 0, len(project.Repositories))
	for _, repository := range project.Repositories {
		repositories = append(repositories, repositoryOutput{Name: repository.Name, WindowName: repository.WindowName})
	}
	result := projectOutput{
		ID:           project.ID,
		Name:         project.Name,
		Template:     project.TemplateName,
		Status:       project.Status,
		CreatedAt:    project.CreatedAt.Format(time.RFC3339),
		Root:         project.Root,
		Repositories: repositories,
	}
	if project.ArchivedAt != nil {
		result.ArchivedAt = project.ArchivedAt.Format(time.RFC3339)
	}
	return result
}

// sortProjectsForDisplay puts active Projects before archived Projects, and
// the most recent Projects first. The projects list and the switch picker
// use the same order.
func sortProjectsForDisplay(projects []domain.Project) {
	sort.SliceStable(projects, func(i, j int) bool {
		iArchived := projects[i].Status == domain.ProjectArchived
		jArchived := projects[j].Status == domain.ProjectArchived
		if iArchived != jArchived {
			return !iArchived
		}
		return projects[i].CreatedAt.After(projects[j].CreatedAt)
	})
}

// projectAgeReference returns the time that the age column reports: the
// archive time for an archived Project, else the create time.
func projectAgeReference(project domain.Project) time.Time {
	if project.Status == domain.ProjectArchived && project.ArchivedAt != nil {
		return *project.ArchivedAt
	}
	return project.CreatedAt
}

func isDryRun(command *cobra.Command) bool {
	value, _ := command.Flags().GetBool("dry-run")
	return value
}

// applyLimit cuts a result list to the requested size. It returns the kept
// values, the number of results before the cut, and whether the cut removed
// results. Every list output reports totalCount and truncated.
func applyLimit[T any](values []T, limit int) ([]T, int, bool, error) {
	total := len(values)
	if limit < 0 {
		return nil, total, false, clierr.New(clierr.InvalidUsage, "--limit must be zero or greater")
	}
	if limit > 0 && total > limit {
		return values[:limit], total, true, nil
	}
	return values, total, false, nil
}

func writeMutation(command *cobra.Command, operation, status, id, name string) error {
	if WantsJSON(command) {
		return writeJSONOutput(command, mutationOutput{SchemaVersion: jsonSchemaVersion, Operation: operation, Status: status, ID: id, Name: name})
	}
	_, err := fmt.Fprintf(command.OutOrStdout(), "%s: %s\n", operation, status)
	return err
}

// runMutation runs one mutation command. A dry run calls validate and
// reports the valid status with the id and name that validate returns.
// Otherwise apply changes state and returns the result id and name. In JSON
// mode the result is the mutation envelope; in text mode the text function
// writes the success line, or the mutation envelope line when text is nil.
func runMutation(command *cobra.Command, operation string, validate func() (string, string, error), apply func() (string, string, error), text func(out io.Writer, id, name string) error) error {
	if isDryRun(command) {
		id, name, err := validate()
		if err != nil {
			return err
		}
		return writeMutation(command, operation, statusValid, id, name)
	}
	id, name, err := apply()
	if err != nil {
		return err
	}
	if WantsJSON(command) || text == nil {
		return writeMutation(command, operation, statusApplied, id, name)
	}
	return text(command.OutOrStdout(), id, name)
}

func writeJSONOutput(command *cobra.Command, value any) error {
	encoder := json.NewEncoder(command.OutOrStdout())
	encoder.SetEscapeHTML(false)
	return encoder.Encode(value)
}

func WriteError(command *cobra.Command, writer io.Writer, err error) error {
	code := clierr.CodeOf(err)
	hint := clierr.HintOf(err)
	helpCommand := ""
	var usage usageError
	if errors.As(err, &usage) {
		helpCommand = usage.helpCommand
	}
	if !WantsJSON(command) {
		text := fmt.Sprintf("twt: %v\n", err)
		if hint != "" {
			text += hint + "\n"
		}
		if helpCommand != "" {
			text += fmt.Sprintf("Run '%s' for usage and examples.\n", helpCommand)
		}
		_, writeErr := io.WriteString(writer, text)
		return writeErr
	}
	return json.NewEncoder(writer).Encode(commandErrorOutput{SchemaVersion: jsonSchemaVersion, Error: commandError{Code: string(code), Message: err.Error(), Hint: hint, HelpCommand: helpCommand}})
}

// formatAge writes a duration as a short age such as "3d", "5h", or "42m".
func formatAge(age time.Duration) string {
	if age < 0 {
		age = 0
	}
	if age >= 24*time.Hour {
		return fmt.Sprintf("%dd", int(age/(24*time.Hour)))
	}
	if age >= time.Hour {
		return fmt.Sprintf("%dh", int(age/time.Hour))
	}
	return fmt.Sprintf("%dm", int(age/time.Minute))
}

func formatBytes(value int64) string {
	const unit = int64(1024)
	if value < unit {
		return fmt.Sprintf("%d B", value)
	}
	divisor := unit
	units := []string{"KiB", "MiB", "GiB", "TiB"}
	unitName := units[0]
	for _, candidate := range units[1:] {
		if value < divisor*unit {
			break
		}
		divisor *= unit
		unitName = candidate
	}
	return fmt.Sprintf("%.1f %s", float64(value)/float64(divisor), unitName)
}

// terminalWriter reports whether the writer is an interactive terminal.
func terminalWriter(writer io.Writer) bool {
	file, ok := writer.(*os.File)
	if !ok {
		return false
	}
	info, err := file.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

// interactiveInput reports whether the reader can serve an interactive
// prompt. A replaced test input always can; standard input must be a
// terminal.
func interactiveInput(input io.Reader) bool {
	if input != os.Stdin {
		return true
	}
	info, err := os.Stdin.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}
