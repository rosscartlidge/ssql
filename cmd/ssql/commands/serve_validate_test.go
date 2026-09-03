//go:build !slim

package commands

import (
	"strings"
	"testing"
)

func TestServeValidatePipeline(t *testing.T) {
	ok := [][][]string{
		{{"from-loaded"}, {"where", "-if", "a", "gt", "1"}, {"to", "table"}},
		{{"status"}},
		{{"schema"}},
		{{"-help"}},
		{{"where", "-help"}},
		{{"describe", "-man"}},
	}
	for _, s := range ok {
		if err := serveValidatePipeline(s); err != nil {
			t.Errorf("%v: unexpected refusal: %v", s, err)
		}
	}
	bad := [][][]string{
		{{"where", "-if", "a", "gt", "1"}, {"to", "table"}},
		{{"to", "table"}},
		{{"describe"}},
	}
	for _, s := range bad {
		err := serveValidatePipeline(s)
		if err == nil || !strings.Contains(err.Error(), "from-loaded") {
			t.Errorf("%v: want loud hint naming from-loaded, got %v", s, err)
		}
	}
}
