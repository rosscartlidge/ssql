package commands

import (
	"slices"
	"testing"
)

// TestCollectDownstreamFields_GroupByBarrier guards the schema-barrier fix:
// group-by replaces the stream schema with {group keys, aggregation outputs},
// so fields referenced AFTER a group-by (e.g. a -count result named "count")
// must NOT leak back as source columns. Before the fix, this caused
// `from parquet … -columns count` to project a column the file never had.
func TestCollectDownstreamFields_GroupByBarrier(t *testing.T) {
	mkcmds := func(commands ...string) []*pipelineCmd {
		var cmds []*pipelineCmd
		for _, c := range commands {
			cmds = append(cmds, parsePipelineCmd(c))
		}
		return cmds
	}

	cases := []struct {
		name  string
		cmds  []*pipelineCmd
		want  []string // sorted source fields needed (startIdx = 0, the "from")
		allOK bool     // expected allNeeded flag
	}{
		{
			name: "count output name does not leak to source",
			cmds: mkcmds(
				"ssql from parquet shuffled.parquet",
				"ssql group-by relationship -count count",
				"ssql sort -desc count",
				"ssql limit 10",
			),
			want: []string{"relationship"},
		},
		{
			name: "aggregation source field kept, output name dropped",
			cmds: mkcmds(
				"ssql from parquet shuffled.parquet",
				"ssql group-by relationship -sum a_sequence total",
				"ssql sort -desc total",
			),
			want: []string{"a_sequence", "relationship"},
		},
		{
			name: "pre-group-by filter field is collected",
			cmds: mkcmds(
				"ssql from parquet shuffled.parquet",
				"ssql where -if z_sequence gt 5",
				"ssql group-by relationship -count count",
				"ssql sort -desc count",
			),
			want: []string{"relationship", "z_sequence"},
		},
		{
			// No stage narrows the record: every source column reaches the
			// output, so nothing may be pruned (2026-10-11: this pipeline
			// was rewritten to `-columns relationship` and printed one).
			name: "pass-through stages keep every column",
			cmds: mkcmds(
				"ssql from parquet shuffled.parquet",
				"ssql where -if z_sequence gt 5",
				"ssql sort relationship",
				"ssql to csv",
			),
			allOK: true,
		},
		{
			// update reads fields through every flag, + spellings too; before
			// 2026-10-20 the walk knew -if, -set and -set-bucket only and the
			// others were pruned away (found fixing the DFC152 schema rule).
			name: "update's field-reading flags are collected",
			cmds: mkcmds(
				"ssql from parquet shuffled.parquet",
				"ssql update -set-field b a_kind -if-field z_kind eq a_name -set k 1 +if relationship eq x -set-bucket m z_sequence 1m -param-field p int a_sequence -param q int 1",
				"ssql include a_kind",
			),
			want: []string{"a_kind", "a_name", "a_sequence", "relationship", "z_kind", "z_sequence"},
		},
		{
			name: "a negated expression in update is opaque",
			cmds: mkcmds(
				"ssql from parquet shuffled.parquet",
				"ssql update +if-expr 'a_kind == \"x\"' -set b 1",
				"ssql include b",
			),
			allOK: true,
		},
		{
			name: "a macro call in update is opaque (-as-written argv)",
			cmds: mkcmds(
				"ssql from parquet shuffled.parquet",
				"ssql update -set-macro b add3 a_sequence z_sequence a_sequence",
				"ssql include b",
			),
			allOK: true,
		},
		{
			// A name update makes is not a source column: `-columns x` was
			// emitted for it before 2026-10-20 and the parquet read failed.
			name: "update's unconditional targets are not source columns",
			cmds: mkcmds(
				"ssql from parquet shuffled.parquet",
				"ssql update -set x 1 -set-field y a_kind",
				"ssql include x y",
			),
			want: []string{"a_kind"},
		},
		{
			name: "window results and rename's new names are not source columns",
			cmds: mkcmds(
				"ssql from parquet shuffled.parquet",
				"ssql window -partition relationship -sum a_sequence total",
				"ssql rename -as z_kind kind",
				"ssql include kind total",
			),
			want: []string{"a_sequence", "relationship", "z_kind"},
		},
		{
			// On rows the condition misses the source's own b survives, if
			// the source has one: the walk cannot tell, so no pruning.
			name: "a conditional update's target referenced later is opaque",
			cmds: mkcmds(
				"ssql from parquet shuffled.parquet",
				"ssql update -if a_kind eq x -set b 9",
				"ssql include b",
			),
			allOK: true,
		},
		{
			name: "include narrows",
			cmds: mkcmds(
				"ssql from parquet shuffled.parquet",
				"ssql sort z_sequence",
				"ssql include relationship z_sequence",
				"ssql update -set-expr x 'z_sequence * 2'",
			),
			want: []string{"relationship", "z_sequence"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, allNeeded := collectDownstreamFields(tc.cmds, 0)
			if allNeeded != tc.allOK {
				t.Fatalf("allNeeded = %v, want %v", allNeeded, tc.allOK)
			}
			gotSorted := sortedStringSet(got)
			if !slices.Equal(gotSorted, tc.want) {
				t.Errorf("needed source fields = %v, want %v", gotSorted, tc.want)
			}
		})
	}
}
