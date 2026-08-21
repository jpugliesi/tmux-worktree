package cli

import (
	"bytes"
	"io"
	"strings"
	"testing"
)

func TestWriteTableAlignsColumnsUnderHeaders(t *testing.T) {
	var buf bytes.Buffer
	headers := []string{"KEY", "VALUE", "SOURCE"}
	rows := [][]string{
		{"configDir", "/Users/me/.config/twt", "env"},
		{"tmuxSocket", "", "default"},
	}
	if err := writeTable(&buf, headers, rows); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(buf.String(), "\t") {
		t.Fatalf("table still contains tabs:\n%s", buf.String())
	}
	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("lines = %#v", lines)
	}
	starts := headerStarts(t, lines[0], headers)
	for i, row := range rows {
		assertRowAligned(t, lines[i+1], starts, row)
	}
}

func TestWriteTableWritesNothingForNoRows(t *testing.T) {
	var buf bytes.Buffer
	if err := writeTable(&buf, []string{"KEY"}, nil); err != nil {
		t.Fatal(err)
	}
	if buf.Len() != 0 {
		t.Fatalf("wrote %q", buf.String())
	}
}

func TestWriteTableRejectsAMismatchedRow(t *testing.T) {
	err := writeTable(io.Discard, []string{"A", "B"}, [][]string{{"only"}})
	if err == nil {
		t.Fatal("expected an error")
	}
}

func TestWriteFieldsAlignsNames(t *testing.T) {
	var buf bytes.Buffer
	if err := writeFields(&buf, [][2]string{{"id", "abc"}, {"provider", "claude"}}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(buf.String(), "\t") {
		t.Fatalf("fields still contain tabs:\n%s", buf.String())
	}
	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("lines = %#v", lines)
	}
	idAt := strings.Index(lines[0], "abc")
	providerAt := strings.Index(lines[1], "claude")
	if idAt < 0 || idAt != providerAt {
		t.Fatalf("values are not aligned:\n%s", buf.String())
	}
}

func headerStarts(t *testing.T, header string, names []string) []int {
	t.Helper()
	starts := make([]int, len(names))
	for i, name := range names {
		starts[i] = strings.Index(header, name)
		if starts[i] < 0 {
			t.Fatalf("header %q missing from %q", name, header)
		}
	}
	return starts
}

func assertRowAligned(t *testing.T, line string, starts []int, values []string) {
	t.Helper()
	for i, value := range values {
		if value == "" {
			continue
		}
		start := starts[i]
		end := start + len(value)
		if end > len(line) || line[start:end] != value {
			t.Fatalf("column %d: line %q does not place %q at %d", i, line, value, start)
		}
	}
}
