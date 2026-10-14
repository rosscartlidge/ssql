package ssql

import (
	"slices"
	"strings"
	"testing"
)

// TestRequireFields: the first record's schema decides; a missing field
// stops the pipeline through ssql.Run with exec's wording; a dotted path
// is checked by its head; an empty stream passes.
func TestRequireFields(t *testing.T) {
	rows := []Record{
		MakeMutableRecord().Int("id", 1).String("name", "a").Freeze(),
		MakeMutableRecord().Int("id", 2).String("name", "b").Freeze(),
	}
	got := slices.Collect(RequireFields("sort", "name", "id.x")(slices.Values(rows)))
	if len(got) != 2 {
		t.Fatalf("present fields: got %d records, want 2", len(got))
	}
	err := Run(func() error {
		for range RequireFields("group-by", "nosuch", "name", "other.x")(slices.Values(rows)) {
		}
		return nil
	})
	want := "group-by references unknown field(s): nosuch, other.x (available: id, name)"
	if err == nil || err.Error() != want {
		t.Fatalf("missing field: got %v, want %s", err, want)
	}
	// A flattened column named with a dot is a column; a key that only a
	// later record (within the sample) carries is known, as exec's
	// sampled schema would know it.
	flat := []Record{MakeMutableRecord().Int("items.sku", 1).Freeze()}
	if got := slices.Collect(RequireFields("sort", "items.sku")(slices.Values(flat))); len(got) != 1 {
		t.Fatalf("flattened column: got %d records", len(got))
	}
	late := []Record{MakeMutableRecord().Int("id", 1).Freeze(), MakeMutableRecord().Int("id", 2).String("hit", "y").Freeze()}
	if got := slices.Collect(RequireFields("include", "hit")(slices.Values(late))); len(got) != 2 {
		t.Fatalf("late key: got %d records", len(got))
	}
	if n := len(slices.Collect(RequireFields("where", "nosuch")(slices.Values([]Record{})))); n != 0 {
		t.Fatalf("empty stream: got %d records", n)
	}
	if err := Run(func() error {
		for range RequireFields("where", "nosuch")(slices.Values([]Record{})) {
		}
		return nil
	}); err != nil {
		t.Fatalf("empty stream must pass: %v", err)
	}
	if !strings.Contains(UnknownFieldsError("x", []string{"b"}, []string{"z", "a"}).Error(), "(available: a, z)") {
		t.Error("available fields must be sorted")
	}
}
