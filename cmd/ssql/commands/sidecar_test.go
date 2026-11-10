package commands

import (
	"strings"
	"testing"
)

// TestTypeOverrideColumnsBlameTheirSource: the type overrides a source
// checks against its header are merged from -type, -format and a schema
// sidecar, and a column missing from the header is blamed on the one
// that named it. It said "-type names column(s)" for all three until
// 2026-11-11, so a sidecar's stray column read as a flag nobody gave.
func TestTypeOverrideColumnsBlameTheirSource(t *testing.T) {
	defer func(c, f map[string]string) { sidecarColumns, fromTimeFormats = c, f }(sidecarColumns, fromTimeFormats)
	headers := []string{"code", "amt"}
	for _, tc := range []struct {
		name      string
		overrides map[string]string
		sidecar   map[string]string
		formats   map[string]string
		want      []string
		not       []string
	}{
		{name: "all present", overrides: map[string]string{"code": "string"}},
		{name: "-type", overrides: map[string]string{"nope": "int"},
			want: []string{"-type names column(s) not in the header: nope", "(available: code, amt)"}, not: []string{"sidecar"}},
		{name: "-format", overrides: map[string]string{"gone": "time"}, formats: map[string]string{"gone": "%Y"},
			want: []string{"-format names column(s) not in the header: gone"}, not: []string{"-type", "sidecar"}},
		{name: "sidecar", overrides: map[string]string{"ghost": "int", "code": "string"}, sidecar: map[string]string{"ghost": "pkg/datapackage.json", "code": "pkg/datapackage.json"},
			want: []string{"the schema sidecar pkg/datapackage.json names column(s) not in the header: ghost", "or THE WAY PAST"}, not: []string{"-type"}},
		{name: "both", overrides: map[string]string{"ghost": "int", "nope": "int"}, sidecar: map[string]string{"ghost": "datapackage.json"},
			want: []string{"-type names column(s) not in the header: nope; the schema sidecar datapackage.json names column(s) not in the header: ghost"}},
	} {
		sidecarColumns, fromTimeFormats = tc.sidecar, tc.formats
		err := checkTypeOverrideColumns(headers, tc.overrides, "from csv", "THE WAY PAST")
		if len(tc.want) == 0 {
			if err != nil {
				t.Errorf("%s: %v", tc.name, err)
			}
			continue
		}
		if err == nil {
			t.Errorf("%s: no error", tc.name)
			continue
		}
		for _, w := range tc.want {
			if !strings.Contains(err.Error(), w) {
				t.Errorf("%s: %q does not say %q", tc.name, err, w)
			}
		}
		for _, n := range tc.not {
			if strings.Contains(err.Error(), n) {
				t.Errorf("%s: %q says %q", tc.name, err, n)
			}
		}
	}
}
