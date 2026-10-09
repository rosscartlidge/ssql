package commands

import (
	"strings"
	"testing"

	"github.com/rosscartlidge/ssql/v4/cmd/ssql/lib"
)

// A dotted path is readable in any field position and writable in none
// (DFC144 Level 2 survey, 2026-10-09): the join validators accept it,
// the writers refuse it by name.

func TestFieldListHasOrPath(t *testing.T) {
	fields := []string{"id", "addr", "a.b"}
	for name, want := range map[string]bool{
		"id": true, "addr.city": true, "addr.city.x": true, "a.b": true, "a.b.c": true,
		"zip": false, "zip.code": false, "": false,
	} {
		if got := fieldListHasOrPath(fields, name); got != want {
			t.Errorf("fieldListHasOrPath(%q) = %v, want %v", name, got, want)
		}
	}
}

func TestValidateWriteTargets(t *testing.T) {
	schema := lib.NewSchema()
	schema.AddField("id", lib.TypeInt)
	schema.AddField("addr", lib.TypeJSON)
	schema.AddField("a.b", lib.TypeString)
	if err := validateWriteTargets(schema, []string{"id", "a.b", "newcol", "x.y"}, "cast", "hint"); err != nil {
		t.Errorf("literal fields and new names must pass: %v", err)
	}
	err := validateWriteTargets(schema, []string{"id", "addr.city"}, "cast", "flatten first")
	if err == nil {
		t.Fatal("a path into a json field must be refused as a write target")
	}
	for _, want := range []string{"cast:", `"addr.city"`, `"addr"`, "flatten first"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q lacks %q", err, want)
		}
	}
	if err := validateWriteTargets(nil, []string{"addr.city"}, "cast", ""); err != nil {
		t.Errorf("no schema, nothing to refuse: %v", err)
	}
}
