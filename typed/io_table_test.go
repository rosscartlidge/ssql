package typed

import (
	"bytes"
	"slices"
	"strings"
	"testing"
)

type tblRow struct {
	ID    int64
	Label string
}

func tblRows() []tblRow {
	return []tblRow{
		{1, "short"},
		{2, "this-is-a-very-long-label-that-exceeds-the-cap-for-sure"},
	}
}

// TestWriteTableMaxWidth checks that the optional maxWidth truncates long
// cells with a trailing "..." (matching ssql.DisplayTable), and that omitting
// it (or 0) leaves values intact — the back-compatible default.
func TestWriteTableMaxWidth(t *testing.T) {
	long := "this-is-a-very-long-label-that-exceeds-the-cap-for-sure"

	// No width → no truncation (old behaviour preserved).
	var full bytes.Buffer
	if err := WriteTableToWriter(slices.Values(tblRows()), &full); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(full.String(), long) {
		t.Errorf("no-width table should keep the full value:\n%s", full.String())
	}

	// maxWidth 20 → truncate to exactly 20 chars ending in "...".
	var capped bytes.Buffer
	if err := WriteTableToWriter(slices.Values(tblRows()), &capped, 20); err != nil {
		t.Fatal(err)
	}
	want := long[:17] + "..." // 20 chars
	if !strings.Contains(capped.String(), want) {
		t.Errorf("maxWidth=20 should truncate to %q:\n%s", want, capped.String())
	}
	if strings.Contains(capped.String(), long) {
		t.Errorf("maxWidth=20 should not contain the full value:\n%s", capped.String())
	}
}

// TestWriteTableSelectedMaxWidth covers the column-list variant the codegen
// uses when the user selects/reorders fields.
func TestWriteTableSelectedMaxWidth(t *testing.T) {
	cols := []TableColumn[tblRow]{
		{Header: "label", Format: func(r *tblRow) string { return r.Label }, RightAlign: false},
	}
	var buf bytes.Buffer
	if err := WriteTableSelectedToWriter(slices.Values(tblRows()), &buf, cols, 12); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "this-is-a...") { // 12 chars
		t.Errorf("selected-column table should truncate at 12:\n%s", buf.String())
	}
}
