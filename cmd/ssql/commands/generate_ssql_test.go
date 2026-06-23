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
