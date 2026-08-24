package cli

import (
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/jpugliesi/tmux-worktree/internal/clierr"
	"github.com/jpugliesi/tmux-worktree/internal/domain"
	"github.com/jpugliesi/tmux-worktree/internal/maintenance"
	"github.com/spf13/cobra"
)

type environmentOutput struct {
	ID          string                      `json:"id"`
	Template    string                      `json:"template"`
	Status      string                      `json:"status"`
	ReadyAt     string                      `json:"readyAt,omitempty"`
	CreatedAt   string                      `json:"createdAt"`
	Bytes       int64                       `json:"bytes"`
	BaseCommits map[string]string           `json:"baseCommits"`
	Failure     string                      `json:"failure,omitempty"`
	Log         string                      `json:"log,omitempty"`
	Workspace   *environmentWorkspaceOutput `json:"workspace,omitempty"`
	Steps       []environmentStepOutput     `json:"steps,omitempty"`
}

type environmentWorkspaceOutput struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Status string `json:"status"`
}

type environmentStepOutput struct {
	ID     string `json:"id"`
	Status string `json:"status"`
	Error  string `json:"error,omitempty"`
}

type environmentsListOutput struct {
	SchemaVersion int                 `json:"schemaVersion"`
	Environments  []environmentOutput `json:"environments"`
	TotalCount    int                 `json:"totalCount"`
	Truncated     bool                `json:"truncated,omitempty"`
}

type environmentShowOutput struct {
	SchemaVersion int               `json:"schemaVersion"`
	Environment   environmentOutput `json:"environment"`
}

func newEnvironmentsCommand(options Options) *cobra.Command {
	service := options.maintenanceService()
	environments := groupCommand(&cobra.Command{
		Use:     "environments",
		Aliases: []string{"envs"},
		Short:   "Inspect Prepared Environments",
	})
	environments.AddCommand(newEnvironmentsListCommand(service), newEnvironmentsShowCommand(service))
	return environments
}

func newEnvironmentsListCommand(service *maintenance.Service) *cobra.Command {
	var limit, offset int
	command := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List Prepared Environments",
		Args:    noArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			report, err := service.EnvironmentReport()
			if err != nil {
				return err
			}
			sortEnvironmentReport(report)
			report, total, truncated, err := applyWindow(report, offset, limit)
			if err != nil {
				return err
			}
			if format := resolvedOutputFormat(command); format != outputText {
				values := make([]environmentOutput, 0, len(report))
				for _, info := range report {
					values = append(values, toEnvironmentOutput(info, false))
				}
				if format == outputNDJSON {
					return writeNDJSONList(command, values, total, truncated)
				}
				return writeReadJSON(command, environmentsListOutput{SchemaVersion: jsonSchemaVersion, Environments: values, TotalCount: total, Truncated: truncated}, "environments")
			}
			return writeEnvironmentTree(command.OutOrStdout(), time.Now(), report)
		},
	}
	addListReadFlags(command, &limit, &offset, environmentOutput{})
	return command
}

func newEnvironmentsShowCommand(service *maintenance.Service) *cobra.Command {
	command := &cobra.Command{
		Use:   "show ENVIRONMENT_ID",
		Short: "Show a Prepared Environment",
		Args:  exactArgs("ENVIRONMENT_ID"),
		RunE: func(command *cobra.Command, args []string) error {
			report, err := service.EnvironmentReport()
			if err != nil {
				return err
			}
			info, err := findEnvironment(report, args[0])
			if err != nil {
				return err
			}
			if WantsJSON(command) {
				return writeReadJSON(command, environmentShowOutput{SchemaVersion: jsonSchemaVersion, Environment: toEnvironmentOutput(info, true)}, "environment")
			}
			return writeEnvironmentDetail(command.OutOrStdout(), time.Now(), info)
		},
	}
	setArguments(command, requiredArgument("environment_id"))
	addFieldsFlag(command, environmentOutput{})
	command.ValidArgsFunction = environmentIDCompletion(service)
	return command
}

// findEnvironment accepts a complete Prepared Environment ID or a unique
// prefix of one.
func findEnvironment(report []maintenance.EnvironmentInfo, reference string) (maintenance.EnvironmentInfo, error) {
	var matches []maintenance.EnvironmentInfo
	for _, info := range report {
		if info.ID == reference {
			return info, nil
		}
		if reference != "" && strings.HasPrefix(info.ID, reference) {
			matches = append(matches, info)
		}
	}
	if len(matches) == 1 {
		return matches[0], nil
	}
	if len(matches) > 1 {
		return maintenance.EnvironmentInfo{}, fmt.Errorf("Prepared Environment ID prefix %q is ambiguous", reference)
	}
	return maintenance.EnvironmentInfo{}, clierr.New(clierr.NotFound, "Prepared Environment %q does not exist", reference)
}

// sortEnvironmentReport puts the newest Prepared Environment first.
func sortEnvironmentReport(report []maintenance.EnvironmentInfo) {
	sort.SliceStable(report, func(i, j int) bool {
		if report[i].CreatedAt.Equal(report[j].CreatedAt) {
			return report[i].ID < report[j].ID
		}
		return report[i].CreatedAt.After(report[j].CreatedAt)
	})
}

// writeEnvironmentTree groups the Prepared Environments by Workspace Template.
func writeEnvironmentTree(out io.Writer, now time.Time, report []maintenance.EnvironmentInfo) error {
	if len(report) == 0 {
		_, err := fmt.Fprintln(out, "No Prepared Environments.")
		return err
	}
	grouped := map[string][]maintenance.EnvironmentInfo{}
	names := make([]string, 0, len(report))
	for _, info := range report {
		if _, found := grouped[info.TemplateName]; !found {
			names = append(names, info.TemplateName)
		}
		grouped[info.TemplateName] = append(grouped[info.TemplateName], info)
	}
	sort.Strings(names)
	for _, name := range names {
		group := grouped[name]
		var bytes int64
		for _, info := range group {
			bytes += info.Bytes
		}
		if _, err := fmt.Fprintf(out, "%s (%d environments, %s)\n", name, len(group), formatBytes(bytes)); err != nil {
			return err
		}
		for index, info := range group {
			branch := "├─"
			if index == len(group)-1 {
				branch = "└─"
			}
			line := fmt.Sprintf("%s %s  %-8s %-4s %10s", branch, shortEnvironmentID(info.ID), info.Status, environmentAge(now, info), formatBytes(info.Bytes))
			if detail := environmentDetail(info); detail != "" {
				line += "  " + detail
			}
			if _, err := fmt.Fprintln(out, line); err != nil {
				return err
			}
		}
	}
	return nil
}

func writeEnvironmentDetail(out io.Writer, now time.Time, info maintenance.EnvironmentInfo) error {
	fields := [][2]string{
		{"Prepared Environment", info.ID},
		{"Template", info.TemplateName},
		{"Status", info.Status},
		{"Age", environmentAge(now, info)},
		{"Size", formatBytes(info.Bytes)},
		{"Created", info.CreatedAt.UTC().Format(time.RFC3339)},
	}
	if info.ReadyAt != nil {
		fields = append(fields, [2]string{"Ready", info.ReadyAt.UTC().Format(time.RFC3339)})
	}
	if info.Failure != "" {
		fields = append(fields, [2]string{"Failure", info.Failure})
	}
	if info.LogPath != "" {
		fields = append(fields, [2]string{"Log", info.LogPath})
	}
	if info.Workspace != nil {
		fields = append(fields,
			[2]string{"Workspace", info.Workspace.Name + " (" + string(info.Workspace.Status) + ")"},
			[2]string{"Workspace ID", info.Workspace.ID},
		)
	}
	if err := writeFields(out, fields); err != nil {
		return err
	}
	if len(info.BaseCommits) > 0 {
		if _, err := fmt.Fprintln(out); err != nil {
			return err
		}
		rows := make([][]string, 0, len(info.BaseCommits))
		for _, name := range sortedKeys(info.BaseCommits) {
			rows = append(rows, []string{name, info.BaseCommits[name]})
		}
		if err := writeTable(out, []string{"REPOSITORY", "BASE"}, rows); err != nil {
			return err
		}
	}
	if len(info.Steps) == 0 {
		return nil
	}
	succeeded := 0
	incomplete := make([][]string, 0, len(info.Steps))
	for _, step := range info.Steps {
		if step.Status == domain.StepSucceeded {
			succeeded++
			continue
		}
		incomplete = append(incomplete, []string{step.ID, string(step.Status), step.Error})
	}
	if _, err := fmt.Fprintf(out, "\nSteps: %d of %d are complete\n", succeeded, len(info.Steps)); err != nil {
		return err
	}
	if len(incomplete) == 0 {
		return nil
	}
	return writeTable(out, []string{"STEP", "STATUS", "ERROR"}, incomplete)
}

// environmentDetail writes the value that a person needs most for one status.
func environmentDetail(info maintenance.EnvironmentInfo) string {
	if info.Workspace != nil {
		return fmt.Sprintf("Workspace %s (%s)", info.Workspace.Name, info.Workspace.Status)
	}
	if info.LogPath != "" && (info.Failure != "" || info.Status == "failed") {
		return "log: " + info.LogPath
	}
	if info.Failure != "" {
		return info.Failure
	}
	if len(info.BaseCommits) == 0 {
		return ""
	}
	names := sortedKeys(info.BaseCommits)
	if len(names) == 1 {
		return "base " + info.BaseCommits[names[0]]
	}
	values := make([]string, 0, len(names))
	for _, name := range names {
		values = append(values, name+"="+info.BaseCommits[name])
	}
	return "base " + strings.Join(values, " ")
}

// environmentAge uses the ready time of a Prepared Environment, or its create
// time when it is not ready.
func environmentAge(now time.Time, info maintenance.EnvironmentInfo) string {
	if info.ReadyAt != nil {
		return formatAge(now.Sub(*info.ReadyAt))
	}
	return formatAge(now.Sub(info.CreatedAt))
}

func shortEnvironmentID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

func toEnvironmentOutput(info maintenance.EnvironmentInfo, withSteps bool) environmentOutput {
	result := environmentOutput{
		ID: info.ID, Template: info.TemplateName, Status: info.Status,
		CreatedAt: info.CreatedAt.UTC().Format(time.RFC3339), Bytes: info.Bytes,
		BaseCommits: info.BaseCommits, Failure: info.Failure, Log: info.LogPath,
	}
	if result.BaseCommits == nil {
		result.BaseCommits = map[string]string{}
	}
	if info.ReadyAt != nil {
		result.ReadyAt = info.ReadyAt.UTC().Format(time.RFC3339)
	}
	if info.Workspace != nil {
		result.Workspace = &environmentWorkspaceOutput{ID: info.Workspace.ID, Name: info.Workspace.Name, Status: info.Workspace.Status}
	}
	if withSteps {
		for _, step := range info.Steps {
			result.Steps = append(result.Steps, environmentStepOutput{ID: step.ID, Status: string(step.Status), Error: step.Error})
		}
	}
	return result
}

func sortedKeys(values map[string]string) []string {
	names := make([]string, 0, len(values))
	for name := range values {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
