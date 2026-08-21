package cli

import (
	"fmt"
	"io"
	"strings"
	"text/tabwriter"
)

// writeTable writes an aligned text table. The header row is omitted when
// headers is empty. A table with no data rows writes nothing.
func writeTable(out io.Writer, headers []string, rows [][]string) error {
	if len(rows) == 0 {
		return nil
	}
	width := len(headers)
	if width == 0 {
		width = len(rows[0])
	}
	if len(headers) > 0 && len(headers) != width {
		return fmt.Errorf("text table header has %d columns, want %d", len(headers), width)
	}
	for index, row := range rows {
		if len(row) != width {
			return fmt.Errorf("text table row %d has %d columns, want %d", index, len(row), width)
		}
	}
	writer := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
	if len(headers) > 0 {
		if err := writeTableRow(writer, headers); err != nil {
			return err
		}
	}
	for _, row := range rows {
		if err := writeTableRow(writer, row); err != nil {
			return err
		}
	}
	return writer.Flush()
}

func writeTableRow(writer *tabwriter.Writer, row []string) error {
	_, err := fmt.Fprintln(writer, strings.Join(row, "\t"))
	return err
}

// writeFields writes an aligned two-column name/value table with no header.
func writeFields(out io.Writer, fields [][2]string) error {
	rows := make([][]string, 0, len(fields))
	for _, field := range fields {
		rows = append(rows, []string{field[0], field[1]})
	}
	return writeTable(out, nil, rows)
}
