package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/jpugliesi/tmux-worktree/internal/clierr"
	"github.com/jpugliesi/tmux-worktree/internal/domain"
	"github.com/spf13/cobra"
)

const jsonSchemaVersion = 1

// The --output values. When --output is not set and standard output is not a
// terminal, twt uses json.
const (
	outputText   = "text"
	outputJSON   = "json"
	outputNDJSON = "ndjson"
)

// ndjsonAnnotation marks a list command that accepts --output ndjson.
const ndjsonAnnotation = "twt.ndjson"

// fieldsAnnotation stores the valid --fields names of one read command on its
// --fields flag.
const fieldsAnnotation = "twt.fields"

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

// applyWindow cuts a sorted result list to one window: it skips the first
// offset values, then keeps at most limit values. It returns the kept values,
// the number of results before the cut, and whether results remain after the
// window. Every list output reports totalCount and truncated.
func applyWindow[T any](values []T, offset, limit int) ([]T, int, bool, error) {
	total := len(values)
	if limit < 0 {
		return nil, total, false, clierr.New(clierr.InvalidUsage, "--limit must be zero or greater")
	}
	if offset < 0 {
		return nil, total, false, clierr.New(clierr.InvalidUsage, "--offset must be zero or greater")
	}
	if offset > total {
		offset = total
	}
	values = values[offset:]
	if limit > 0 && len(values) > limit {
		return values[:limit], total, true, nil
	}
	return values, total, false, nil
}

// addListReadFlags registers the shared read flags of one list command:
// --limit, --offset, and --fields. It also permits --output ndjson. The
// element value gives the valid --fields names.
func addListReadFlags(command *cobra.Command, limit, offset *int, element any) {
	command.Flags().IntVar(limit, "limit", 0, "Limit the number of results; zero returns all results")
	command.Flags().IntVar(offset, "offset", 0, "Skip this number of sorted results before the limit applies")
	addFieldsFlag(command, element)
	if command.Annotations == nil {
		command.Annotations = map[string]string{}
	}
	command.Annotations[ndjsonAnnotation] = "true"
}

// addFieldsFlag registers the --fields flag on one read command. The valid
// field names come from the json tags of the element struct.
func addFieldsFlag(command *cobra.Command, element any) {
	command.Flags().String("fields", "", "Show only these comma-separated fields in json or ndjson output")
	flag := command.Flags().Lookup("fields")
	if flag.Annotations == nil {
		flag.Annotations = map[string][]string{}
	}
	flag.Annotations[fieldsAnnotation] = jsonFieldNames(element)
}

// jsonFieldNames reads the top-level JSON field names of one output struct
// from its json tags. It walks embedded structs. It skips schemaVersion,
// because schemaVersion always stays in the output.
func jsonFieldNames(element any) []string {
	seen := map[string]bool{}
	names := []string{}
	var walk func(structType reflect.Type)
	walk = func(structType reflect.Type) {
		for structType.Kind() == reflect.Pointer {
			structType = structType.Elem()
		}
		if structType.Kind() != reflect.Struct {
			return
		}
		for index := 0; index < structType.NumField(); index++ {
			field := structType.Field(index)
			tag := strings.Split(field.Tag.Get("json"), ",")[0]
			if field.Anonymous && tag == "" {
				walk(field.Type)
				continue
			}
			if !field.IsExported() || tag == "-" {
				continue
			}
			name := tag
			if name == "" {
				name = field.Name
			}
			if name == "schemaVersion" || seen[name] {
				continue
			}
			seen[name] = true
			names = append(names, name)
		}
	}
	walk(reflect.TypeOf(element))
	sort.Strings(names)
	return names
}

// fieldMask reads the validated --fields selection of one read command. It
// returns nil when the command does not set --fields. An unknown field name
// is an invalid_usage error that lists the valid names.
func fieldMask(command *cobra.Command) (map[string]bool, error) {
	flag := command.Flags().Lookup("fields")
	if flag == nil || !flag.Changed {
		return nil, nil
	}
	valid := map[string]bool{}
	for _, name := range flag.Annotations[fieldsAnnotation] {
		valid[name] = true
	}
	mask := map[string]bool{}
	for _, name := range strings.Split(flag.Value.String(), ",") {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		if !valid[name] {
			return nil, invalidUsage(command, "unknown field %q; valid fields: %s", name, strings.Join(flag.Annotations[fieldsAnnotation], ", "))
		}
		mask[name] = true
	}
	if len(mask) == 0 {
		return nil, invalidUsage(command, "give --fields one or more field names; valid fields: %s", strings.Join(flag.Annotations[fieldsAnnotation], ", "))
	}
	return mask, nil
}

// maskObjectKeys keeps only the masked keys of one decoded JSON object. The
// schemaVersion key always stays.
func maskObjectKeys(object map[string]json.RawMessage, mask map[string]bool) map[string]json.RawMessage {
	for key := range object {
		if key != "schemaVersion" && !mask[key] {
			delete(object, key)
		}
	}
	return object
}

// maskMarshaled marshals one value and keeps only the masked top-level keys.
func maskMarshaled(value any, mask map[string]bool) (map[string]json.RawMessage, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(data, &object); err != nil {
		return nil, err
	}
	return maskObjectKeys(object, mask), nil
}

// writeReadJSON writes one read envelope. When the command sets --fields, it
// filters the value under key: each element of a list, or the one shown
// object. An empty key filters the top-level keys of the envelope itself.
// schemaVersion always stays.
func writeReadJSON(command *cobra.Command, envelope any, key string) error {
	mask, err := fieldMask(command)
	if err != nil {
		return err
	}
	if mask == nil {
		return writeJSONOutput(command, envelope)
	}
	object, err := maskEnvelope(envelope, key, mask)
	if err != nil {
		return err
	}
	return writeJSONOutput(command, object)
}

// maskEnvelope applies one field mask inside a marshaled envelope.
func maskEnvelope(envelope any, key string, mask map[string]bool) (map[string]json.RawMessage, error) {
	data, err := json.Marshal(envelope)
	if err != nil {
		return nil, err
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(data, &object); err != nil {
		return nil, err
	}
	if key == "" {
		return maskObjectKeys(object, mask), nil
	}
	raw, found := object[key]
	if !found {
		return object, nil
	}
	if trimmed := strings.TrimSpace(string(raw)); strings.HasPrefix(trimmed, "[") {
		var elements []map[string]json.RawMessage
		if err := json.Unmarshal(raw, &elements); err != nil {
			return nil, err
		}
		for index := range elements {
			elements[index] = maskObjectKeys(elements[index], mask)
		}
		masked, err := json.Marshal(elements)
		if err != nil {
			return nil, err
		}
		object[key] = masked
		return object, nil
	}
	var element map[string]json.RawMessage
	if err := json.Unmarshal(raw, &element); err != nil {
		return nil, err
	}
	masked, err := json.Marshal(maskObjectKeys(element, mask))
	if err != nil {
		return nil, err
	}
	object[key] = masked
	return object, nil
}

// ndjsonSummary is the final line of one ndjson list result.
type ndjsonSummary struct {
	TotalCount int  `json:"totalCount"`
	Truncated  bool `json:"truncated"`
}

// writeNDJSONList writes one list as newline-delimited JSON: one element for
// each line without an envelope, then one summary line with totalCount and
// truncated. The --fields mask applies to each element line.
func writeNDJSONList[T any](command *cobra.Command, elements []T, total int, truncated bool) error {
	mask, err := fieldMask(command)
	if err != nil {
		return err
	}
	encoder := json.NewEncoder(command.OutOrStdout())
	encoder.SetEscapeHTML(false)
	for _, element := range elements {
		var value any = element
		if mask != nil {
			if value, err = maskMarshaled(element, mask); err != nil {
				return err
			}
		}
		if err := encoder.Encode(value); err != nil {
			return err
		}
	}
	return encoder.Encode(ndjsonSummary{TotalCount: total, Truncated: truncated})
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
	if resolvedOutputFormat(command) == outputText {
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
