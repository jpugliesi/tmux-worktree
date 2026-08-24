package workspace

import "testing"

func TestParseWorkspaceSessionRowsKeepsAnEmptyFinalOwner(t *testing.T) {
	rows := parseWorkspaceSessionRows("$1\texample", true)
	if len(rows) != 1 || rows[0].id != "$1" || rows[0].name != "example" || rows[0].ownerID != "" {
		t.Fatalf("parseWorkspaceSessionRows() = %+v", rows)
	}
}
