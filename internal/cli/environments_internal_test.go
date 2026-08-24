package cli

import (
	"bytes"
	"reflect"
	"testing"
	"time"

	"github.com/jpugliesi/tmux-worktree/internal/maintenance"
	"github.com/spf13/cobra"
)

type countingEnvironmentService struct {
	report   []maintenance.EnvironmentInfo
	measured []string
}

func (s *countingEnvironmentService) EnvironmentReport() ([]maintenance.EnvironmentInfo, error) {
	return append([]maintenance.EnvironmentInfo(nil), s.report...), nil
}

func (s *countingEnvironmentService) MeasureEnvironmentSizes(report []maintenance.EnvironmentInfo) {
	for index := range report {
		s.measured = append(s.measured, report[index].ID)
		bytes := int64(1024)
		report[index].Bytes = &bytes
	}
}

func executeEnvironmentCommand(t *testing.T, command *cobra.Command, args ...string) (string, error) {
	t.Helper()
	var output bytes.Buffer
	root := &cobra.Command{Use: "twt", SilenceErrors: true, SilenceUsage: true}
	root.SetOut(&output)
	root.SetErr(&output)
	root.PersistentFlags().String("output", outputText, "")
	root.AddCommand(command)
	root.SetArgs(append([]string{command.Name()}, args...))
	return output.String(), root.Execute()
}

func environmentReportRows() []maintenance.EnvironmentInfo {
	created := time.Date(2026, time.August, 24, 12, 0, 0, 0, time.UTC)
	return []maintenance.EnvironmentInfo{
		{ID: "older", TemplateName: "example", Status: "ready", CreatedAt: created},
		{ID: "newer", TemplateName: "example", Status: "ready", CreatedAt: created.Add(time.Hour)},
	}
}

func TestEnvironmentsListMeasuresOnlyVisibleRowsWhenOutputNeedsBytes(t *testing.T) {
	tests := []struct {
		name         string
		args         []string
		wantMeasured []string
		wantError    bool
	}{
		{name: "default text", args: []string{"--output", "text"}},
		{name: "text size after limit", args: []string{"--output", "text", "--size", "--limit", "1"}, wantMeasured: []string{"newer"}},
		{name: "full json after limit", args: []string{"--output", "json", "--limit", "1"}, wantMeasured: []string{"newer"}},
		{name: "full ndjson after limit", args: []string{"--output", "ndjson", "--limit", "1"}, wantMeasured: []string{"newer"}},
		{name: "fields without bytes", args: []string{"--output", "json", "--fields", "id,status"}},
		{name: "fields with bytes after limit", args: []string{"--output", "json", "--fields", "id,bytes", "--limit", "1"}, wantMeasured: []string{"newer"}},
		{name: "unknown field", args: []string{"--output", "json", "--fields", "unknown"}, wantError: true},
		{name: "empty fields", args: []string{"--output", "json", "--fields", " "}, wantError: true},
		{name: "text size with json", args: []string{"--output", "json", "--size"}, wantError: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			service := &countingEnvironmentService{report: environmentReportRows()}
			_, err := executeEnvironmentCommand(t, newEnvironmentsListCommand(service), test.args...)
			if (err != nil) != test.wantError {
				t.Fatalf("error = %v, wantError %t", err, test.wantError)
			}
			if !reflect.DeepEqual(service.measured, test.wantMeasured) {
				t.Fatalf("measured Environment IDs = %v, want %v", service.measured, test.wantMeasured)
			}
		})
	}
}

func TestEnvironmentsShowMeasuresOnlyTheSelectedRowWhenOutputNeedsBytes(t *testing.T) {
	tests := []struct {
		name         string
		reference    string
		args         []string
		wantMeasured []string
		wantError    bool
	}{
		{name: "text", reference: "older", args: []string{"--output", "text"}, wantMeasured: []string{"older"}},
		{name: "full json", reference: "newer", args: []string{"--output", "json"}, wantMeasured: []string{"newer"}},
		{name: "fields without bytes", reference: "newer", args: []string{"--output", "json", "--fields", "id,status"}},
		{name: "unknown field", reference: "newer", args: []string{"--output", "json", "--fields", "unknown"}, wantError: true},
		{name: "unknown reference", reference: "missing", args: []string{"--output", "text"}, wantError: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			service := &countingEnvironmentService{report: environmentReportRows()}
			args := append([]string{test.reference}, test.args...)
			_, err := executeEnvironmentCommand(t, newEnvironmentsShowCommand(service), args...)
			if (err != nil) != test.wantError {
				t.Fatalf("error = %v, wantError %t", err, test.wantError)
			}
			if !reflect.DeepEqual(service.measured, test.wantMeasured) {
				t.Fatalf("measured Environment IDs = %v, want %v", service.measured, test.wantMeasured)
			}
		})
	}
}

func TestEnvironmentIDCompletionDoesNotMeasureSizes(t *testing.T) {
	service := &countingEnvironmentService{report: environmentReportRows()}
	values, _ := environmentIDCompletion(service)(nil, nil, "")
	if len(values) != 2 {
		t.Fatalf("Environment ID completion = %v", values)
	}
	if len(service.measured) != 0 {
		t.Fatalf("Environment ID completion measured %v", service.measured)
	}
}
