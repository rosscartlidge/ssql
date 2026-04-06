package commands

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"

	cf "github.com/rosscartlidge/autocli/v4"
	"github.com/rosscartlidge/ssql/v4/cmd/ssql/lib"
)

// registerGenerateSSQL registers the "generate ssql" subcommand
func registerGenerateSSQL(cmd *cf.SubcommandBuilder) {
	cmd.Subcommand("ssql").
		Description("Optimize an ssql pipeline by rewriting it with fewer commands").
		Example("(export SSQLGO=1; ssql from ssh host /data.csv | ssql where -if status ge 500 | ssql to table) | ssql generate ssql", "Push filter into SSH").
		Example("(export SSQLGO=1; ssql from csv data.csv | ssql sort -desc revenue | ssql limit 10 | ssql to table) | ssql generate ssql", "Rewrite sort+limit as top").
		Example("(export SSQLGO=1; ssql from csv data.csv | ssql where -if age gt 25 | ssql where -if dept eq sales | ssql to table) | ssql generate ssql", "Merge adjacent where commands").
		Flag("-run", "-r").
		Bool().
		Global().
		Default(false).
		Help("Execute the optimized pipeline").
		Done().
		Flag("-explain", "-e").
		Bool().
		Global().
		Default(false).
		Help("Print applied optimization rules to stderr").
		Done().
		Handler(func(ctx *cf.Context) error {
			var run bool
			var explain bool
			if v, ok := ctx.GlobalFlags["-run"]; ok {
				run = v.(bool)
			}
			if v, ok := ctx.GlobalFlags["-explain"]; ok {
				explain = v.(bool)
			}

			pipeline, rules, err := optimizePipeline(os.Stdin)
			if err != nil {
				if err == errEmptyResult {
					if explain {
						for _, r := range rules {
							fmt.Fprintf(os.Stderr, "[%s] %s → %s\n", r.Rule, r.Before, r.After)
						}
					}
					fmt.Fprintln(os.Stderr, "pipeline produces no results (contradictory predicates)")
					return nil
				}
				return fmt.Errorf("optimizing pipeline: %w", err)
			}

			if explain && len(rules) > 0 {
				for _, r := range rules {
					fmt.Fprintf(os.Stderr, "[%s] %s → %s\n", r.Rule, r.Before, r.After)
				}
			}

			if run {
				cmd := exec.Command("bash", "-c", pipeline)
				cmd.Stdout = os.Stdout
				cmd.Stderr = os.Stderr
				return cmd.Run()
			}

			fmt.Println(pipeline)
			return nil
		}).
		Done()
}

// errEmptyResult is returned when contradictory predicates are detected.
var errEmptyResult = fmt.Errorf("empty result")

// pipelineCmd is a parsed representation of one ssql command in the pipeline.
type pipelineCmd struct {
	Kind    string   // "from", "where", "sort", "limit", "group-by", "to", etc.
	RawArgs []string // all args after the command name (e.g., ["ssh", "host", "/data.csv"])
	Removed bool     // true if this command was eliminated by an optimization rule

	// Parsed fields for "from ssh"
	IsSSH       bool
	SSHHost     string
	SSHPath     string
	SSHGPU      bool
	SSHPushDown []string

	// Parsed fields for "from catalog"
	IsCatalog         bool
	CatalogFile       string
	CatalogFilters    []whereCondition // existing -if filters
	CatalogGPU        bool
	CatalogShardField string
	CatalogPushDown   []string

	// Parsed fields for multi-file "from csv/tsv/json/jsonl"
	IsMultiFile       bool
	MultiFileFormat   string   // "csv", "tsv", "json", "jsonl"
	MultiFileFiles    []string // file paths
	MultiFileFlags    []string // other flags (-merge-schemas, -source, -unordered, -type, etc.)
	MultiFilePushDown []string // args after --

	// Parsed fields for "join"
	IsJoin           bool
	JoinFile         string   // right-side file path
	JoinArgs         []string // args after FILE (-using, -on, -type, etc.)
	JoinRightWhere   []string // where args pushed into right side (rendered as process substitution)
	JoinIsProcessSub bool     // true if right side is already a process substitution (/dev/fd/N)
	JoinProcSubCmds  []*pipelineCmd // parsed inner pipeline commands (for process substitution optimization)

	// Parsed fields for "merge -catalog"
	IsMergeCatalog         bool
	MergeCatalogFile       string
	MergeCatalogFilters    []whereCondition
	MergeCatalogGPU        bool
	MergeCatalogShardField string
	MergeCatalogByFields   []string
	MergeCatalogDesc       bool
	MergeCatalogPushDown   []string
	MergeCatalogFlags      []string // other flags

	// Sort fields
	SortDesc  bool
	SortField string

	// Limit
	LimitN string
}

// whereCondition represents a single -if field op value condition.
type whereCondition struct {
	Field    string
	Operator string
	Value    string
}

// whereClause represents one OR-group in a where command.
// Multiple conditions within a clause are ANDed.
type whereClause struct {
	Conditions []whereCondition
	Exprs      []string // -if-expr expressions
}

// ruleApplication tracks an optimization rule that was applied.
type ruleApplication struct {
	Rule   string
	Before string
	After  string
}

func optimizePipeline(input io.Reader) (string, []ruleApplication, error) {
	// Read fragments from stdin
	var fragments []*lib.CodeFragment
	decoder := json.NewDecoder(input)
	for {
		var frag lib.CodeFragment
		if err := decoder.Decode(&frag); err != nil {
			if err == io.EOF {
				break
			}
			return "", nil, fmt.Errorf("decoding fragment: %w", err)
		}
		if frag.Type == "error" {
			return "", nil, fmt.Errorf("code generation failed in %s: %s", frag.Command, frag.Error)
		}
		fragments = append(fragments, &frag)
	}

	if len(fragments) == 0 {
		return "", nil, fmt.Errorf("no code fragments received")
	}

	// Parse each fragment into a pipelineCmd
	var cmds []*pipelineCmd
	var pendingFuncCmds []*pipelineCmd // inner pipeline cmds from func fragments awaiting their join
	for _, frag := range fragments {
		cmd := parsePipelineCmd(frag.Command)
		// func fragments are right-side sources for joins, not pipeline stages
		if frag.Type == "func" {
			cmd.Removed = true
			// Parse inner pipeline from FuncBody
			var innerCmds []*pipelineCmd
			for _, bodyFrag := range frag.FuncBody {
				innerCmds = append(innerCmds, parsePipelineCmd(bodyFrag.Command))
			}
			pendingFuncCmds = innerCmds
		}
		// Attach pending func body to its join
		if cmd.IsJoin && cmd.JoinIsProcessSub && len(pendingFuncCmds) > 0 {
			cmd.JoinProcSubCmds = pendingFuncCmds
			pendingFuncCmds = nil
		}
		cmds = append(cmds, cmd)
	}

	// Apply optimization rules in order
	var rules []ruleApplication

	// Phase 1 rules
	rules = append(rules, ruleWhereMerge(cmds)...)
	rules = append(rules, ruleSSHPredicatePushdown(cmds)...)
	rules = append(rules, ruleSSHAggregationPushdown(cmds)...)
	rules = append(rules, ruleMultiFilePredicatePushdown(cmds)...)
	rules = append(rules, ruleMultiFileAggregationPushdown(cmds)...)
	rules = append(rules, ruleMergeCatalogPredicatePushdown(cmds)...)
	rules = append(rules, ruleMergeCatalogAggregationPushdown(cmds)...)

	// Phase 2 rules (before sort rules, since they may modify where commands)
	rules = append(rules, ruleCatalogPredicateExtraction(cmds)...)
	rules = append(rules, ruleCatalogPredicatePushdown(cmds)...)
	rules = append(rules, ruleCatalogAggregationPushdown(cmds)...)
	r, empty := rulePredicateSimplification(cmds)
	rules = append(rules, r...)
	if empty {
		return "", rules, errEmptyResult
	}
	rules = append(rules, rulePredicateReorder(cmds)...)

	// Phase 1 rules (after predicate rules)
	rules = append(rules, ruleSortLimitToTop(cmds)...)
	rules = append(rules, ruleSortElimination(cmds)...)

	// Phase 4 rules
	var joinRules []ruleApplication
	cmds, joinRules = ruleJoinPredicatePushdown(cmds)
	rules = append(rules, joinRules...)

	// Phase 3 rules
	rules = append(rules, ruleParquetColumnPruning(cmds)...)
	// Note: mid-pipeline column dropping (inserting include after from csv) was benchmarked
	// and found to be counterproductive — the extra pipeline process overhead outweighs
	// serialization savings from fewer columns (even with 20→2 cols and 5+ stages).
	// Note: limit pushdown past row-preserving commands was benchmarked and found to be
	// a no-op — Unix pipe backpressure already provides early termination via SIGPIPE.

	// Render the optimized pipeline
	pipeline := renderPipeline(cmds)
	return pipeline, rules, nil
}

// --- Command parsing ---

// parsePipelineCmd parses a Command string like "ssql from ssh host /data.csv" into a pipelineCmd.
func parsePipelineCmd(command string) *pipelineCmd {
	if command == "" {
		return &pipelineCmd{Removed: true} // empty command (e.g., group-by second fragment)
	}

	args := parseCommandArgs(command)
	if len(args) < 2 {
		return &pipelineCmd{Kind: "unknown", RawArgs: args}
	}

	kind := args[1]
	rawArgs := args[2:]

	cmd := &pipelineCmd{
		Kind:    kind,
		RawArgs: rawArgs,
	}

	switch kind {
	case "from":
		parseFromCmd(cmd)
	case "merge":
		parseMergeCmd(cmd)
	case "join":
		parseJoinCmd(cmd)
	case "sort":
		parseSortCmd(cmd)
	case "limit":
		parseLimitCmd(cmd)
	}

	return cmd
}

func parseFromCmd(cmd *pipelineCmd) {
	if len(cmd.RawArgs) == 0 {
		return
	}
	switch cmd.RawArgs[0] {
	case "ssh":
		parseFromSSHCmd(cmd)
	case "catalog":
		parseFromCatalogCmd(cmd)
	case "csv", "tsv", "json", "jsonl":
		parseFromMultiFileCmd(cmd)
	}
}

func parseFromMultiFileCmd(cmd *pipelineCmd) {
	cmd.MultiFileFormat = cmd.RawArgs[0]
	i := 1
	for i < len(cmd.RawArgs) {
		arg := cmd.RawArgs[i]
		if arg == "--" {
			if i+1 < len(cmd.RawArgs) {
				cmd.MultiFilePushDown = cmd.RawArgs[i+1:]
			}
			break
		}
		if strings.HasPrefix(arg, "-") {
			// Flag — collect flag and its value(s)
			cmd.MultiFileFlags = append(cmd.MultiFileFlags, arg)
			i++
			// Consume flag values (non-flag args until next flag or --)
			for i < len(cmd.RawArgs) && !strings.HasPrefix(cmd.RawArgs[i], "-") && cmd.RawArgs[i] != "--" {
				cmd.MultiFileFlags = append(cmd.MultiFileFlags, cmd.RawArgs[i])
				i++
			}
			continue
		}
		// Positional — file path
		cmd.MultiFileFiles = append(cmd.MultiFileFiles, arg)
		i++
	}
	if len(cmd.MultiFileFiles) > 1 {
		cmd.IsMultiFile = true
	}
}

func parseMergeCmd(cmd *pipelineCmd) {
	i := 0
	for i < len(cmd.RawArgs) {
		arg := cmd.RawArgs[i]
		if arg == "--" {
			if i+1 < len(cmd.RawArgs) {
				cmd.MergeCatalogPushDown = cmd.RawArgs[i+1:]
			}
			break
		}
		switch arg {
		case "-catalog", "-c":
			if i+1 < len(cmd.RawArgs) {
				cmd.IsMergeCatalog = true
				cmd.MergeCatalogFile = cmd.RawArgs[i+1]
				i += 2
			} else {
				i++
			}
		case "-by", "-b":
			i++
			for i < len(cmd.RawArgs) && !strings.HasPrefix(cmd.RawArgs[i], "-") && cmd.RawArgs[i] != "--" {
				cmd.MergeCatalogByFields = append(cmd.MergeCatalogByFields, cmd.RawArgs[i])
				i++
			}
		case "-desc", "-d":
			cmd.MergeCatalogDesc = true
			i++
		case "-gpu":
			cmd.MergeCatalogGPU = true
			i++
		case "-shard-field":
			if i+1 < len(cmd.RawArgs) {
				cmd.MergeCatalogShardField = cmd.RawArgs[i+1]
				i += 2
			} else {
				i++
			}
		case "-if":
			if i+3 < len(cmd.RawArgs) {
				cmd.MergeCatalogFilters = append(cmd.MergeCatalogFilters, whereCondition{
					Field:    cmd.RawArgs[i+1],
					Operator: cmd.RawArgs[i+2],
					Value:    cmd.RawArgs[i+3],
				})
				i += 4
			} else {
				i++
			}
		default:
			i++
		}
	}
}

func parseFromSSHCmd(cmd *pipelineCmd) {
	cmd.IsSSH = true
	i := 1
	for i < len(cmd.RawArgs) {
		arg := cmd.RawArgs[i]
		if arg == "--" {
			if i+1 < len(cmd.RawArgs) {
				cmd.SSHPushDown = cmd.RawArgs[i+1:]
			}
			break
		}
		switch arg {
		case "-gpu":
			cmd.SSHGPU = true
		default:
			if cmd.SSHHost == "" {
				cmd.SSHHost = arg
			} else if cmd.SSHPath == "" {
				cmd.SSHPath = arg
			}
		}
		i++
	}
}

func parseFromCatalogCmd(cmd *pipelineCmd) {
	cmd.IsCatalog = true
	i := 1
	for i < len(cmd.RawArgs) {
		arg := cmd.RawArgs[i]
		if arg == "--" {
			if i+1 < len(cmd.RawArgs) {
				cmd.CatalogPushDown = cmd.RawArgs[i+1:]
			}
			break
		}
		switch arg {
		case "-if", "-i":
			if i+3 < len(cmd.RawArgs) {
				cmd.CatalogFilters = append(cmd.CatalogFilters, whereCondition{
					Field: cmd.RawArgs[i+1], Operator: cmd.RawArgs[i+2], Value: cmd.RawArgs[i+3],
				})
				i += 4
				continue
			}
		case "-gpu":
			cmd.CatalogGPU = true
		case "-shard-field":
			if i+1 < len(cmd.RawArgs) {
				cmd.CatalogShardField = cmd.RawArgs[i+1]
				i += 2
				continue
			}
		default:
			if cmd.CatalogFile == "" && !strings.HasPrefix(arg, "-") {
				cmd.CatalogFile = arg
			}
		}
		i++
	}
}

func parseJoinCmd(cmd *pipelineCmd) {
	cmd.IsJoin = true
	if len(cmd.RawArgs) == 0 {
		return
	}
	cmd.JoinFile = cmd.RawArgs[0]
	cmd.JoinArgs = cmd.RawArgs[1:]
	cmd.JoinIsProcessSub = strings.HasPrefix(cmd.JoinFile, "/dev/fd/")
}

func parseSortCmd(cmd *pipelineCmd) {
	for _, arg := range cmd.RawArgs {
		switch arg {
		case "-desc", "-d":
			cmd.SortDesc = true
		case "-asc", "-a":
			// default
		default:
			if !strings.HasPrefix(arg, "-") && cmd.SortField == "" {
				cmd.SortField = arg
			}
		}
	}
}

func parseLimitCmd(cmd *pipelineCmd) {
	if len(cmd.RawArgs) > 0 {
		cmd.LimitN = cmd.RawArgs[0]
	}
}

// --- Where condition parsing ---

// parseWhereArgs parses where RawArgs into structured clauses.
// Format: [-if field op value]... [-if-expr expr]... [+ [-if ...]...]
func parseWhereArgs(args []string) []whereClause {
	var clauses []whereClause
	var current whereClause

	i := 0
	for i < len(args) {
		switch args[i] {
		case "-if", "-i":
			if i+3 < len(args) {
				current.Conditions = append(current.Conditions, whereCondition{
					Field: args[i+1], Operator: args[i+2], Value: args[i+3],
				})
				i += 4
				continue
			}
		case "-if-expr", "-x":
			if i+1 < len(args) {
				current.Exprs = append(current.Exprs, args[i+1])
				i += 2
				continue
			}
		case "+":
			if len(current.Conditions) > 0 || len(current.Exprs) > 0 {
				clauses = append(clauses, current)
				current = whereClause{}
			}
		}
		i++
	}
	if len(current.Conditions) > 0 || len(current.Exprs) > 0 {
		clauses = append(clauses, current)
	}
	return clauses
}

// buildWhereArgs rebuilds where RawArgs from structured clauses.
func buildWhereArgs(clauses []whereClause) []string {
	var args []string
	for i, clause := range clauses {
		if i > 0 {
			args = append(args, "+")
		}
		for _, cond := range clause.Conditions {
			args = append(args, "-if", cond.Field, cond.Operator, cond.Value)
		}
		for _, expr := range clause.Exprs {
			args = append(args, "-if-expr", expr)
		}
	}
	return args
}

// nextNonRemoved returns the next non-removed command after index i, or -1.
func nextNonRemoved(cmds []*pipelineCmd, i int) int {
	for j := i + 1; j < len(cmds); j++ {
		if !cmds[j].Removed {
			return j
		}
	}
	return -1
}

// --- Phase 1 optimization rules ---

// ruleWhereMerge merges consecutive where commands into one.
func ruleWhereMerge(cmds []*pipelineCmd) []ruleApplication {
	var rules []ruleApplication
	for i := 0; i < len(cmds); i++ {
		if cmds[i].Removed || cmds[i].Kind != "where" {
			continue
		}
		j := nextNonRemoved(cmds, i)
		if j == -1 || cmds[j].Kind != "where" {
			continue
		}
		before := renderCmd(cmds[i]) + " | " + renderCmd(cmds[j])
		cmds[i].RawArgs = append(cmds[i].RawArgs, cmds[j].RawArgs...)
		cmds[j].Removed = true
		after := renderCmd(cmds[i])
		rules = append(rules, ruleApplication{
			Rule:   "where-merge",
			Before: before,
			After:  after,
		})
		i-- // re-check in case there are more consecutive wheres
	}
	return rules
}

// ruleSSHPredicatePushdown moves a where command after "from ssh" into the SSH push-down.
func ruleSSHPredicatePushdown(cmds []*pipelineCmd) []ruleApplication {
	var rules []ruleApplication
	for i := 0; i < len(cmds); i++ {
		if cmds[i].Removed || cmds[i].Kind != "from" || !cmds[i].IsSSH {
			continue
		}
		j := nextNonRemoved(cmds, i)
		if j == -1 || cmds[j].Kind != "where" {
			continue
		}
		before := renderCmd(cmds[i]) + " | " + renderCmd(cmds[j])
		pushArgs := append([]string{"where"}, cmds[j].RawArgs...)
		if len(cmds[i].SSHPushDown) > 0 {
			cmds[i].SSHPushDown = append(cmds[i].SSHPushDown, "+")
			cmds[i].SSHPushDown = append(cmds[i].SSHPushDown, pushArgs...)
		} else {
			cmds[i].SSHPushDown = pushArgs
		}
		cmds[j].Removed = true
		after := renderCmd(cmds[i])
		rules = append(rules, ruleApplication{
			Rule:   "ssh-predicate-pushdown",
			Before: before,
			After:  after,
		})
	}
	return rules
}

// ruleSSHAggregationPushdown moves a group-by command after "from ssh" (with push-down) into SSH push-down.
func ruleSSHAggregationPushdown(cmds []*pipelineCmd) []ruleApplication {
	var rules []ruleApplication
	for i := 0; i < len(cmds); i++ {
		if cmds[i].Removed || cmds[i].Kind != "from" || !cmds[i].IsSSH {
			continue
		}
		if len(cmds[i].SSHPushDown) == 0 {
			continue
		}
		j := nextNonRemoved(cmds, i)
		if j == -1 || cmds[j].Kind != "group-by" {
			continue
		}
		before := renderCmd(cmds[i]) + " | " + renderCmd(cmds[j])
		pushArgs := append([]string{"group-by"}, cmds[j].RawArgs...)
		cmds[i].SSHPushDown = append(cmds[i].SSHPushDown, "+")
		cmds[i].SSHPushDown = append(cmds[i].SSHPushDown, pushArgs...)
		cmds[j].Removed = true
		after := renderCmd(cmds[i])
		rules = append(rules, ruleApplication{
			Rule:   "ssh-aggregation-pushdown",
			Before: before,
			After:  after,
		})
	}
	return rules
}

// ruleMultiFilePredicatePushdown moves a where command after multi-file "from csv/tsv/json/jsonl" into pushdown.
func ruleMultiFilePredicatePushdown(cmds []*pipelineCmd) []ruleApplication {
	var rules []ruleApplication
	for i := 0; i < len(cmds); i++ {
		if cmds[i].Removed || cmds[i].Kind != "from" || !cmds[i].IsMultiFile {
			continue
		}
		j := nextNonRemoved(cmds, i)
		if j == -1 || cmds[j].Kind != "where" {
			continue
		}
		before := renderCmd(cmds[i]) + " | " + renderCmd(cmds[j])
		pushArgs := append([]string{"where"}, cmds[j].RawArgs...)
		if len(cmds[i].MultiFilePushDown) > 0 {
			cmds[i].MultiFilePushDown = append(cmds[i].MultiFilePushDown, "+")
			cmds[i].MultiFilePushDown = append(cmds[i].MultiFilePushDown, pushArgs...)
		} else {
			cmds[i].MultiFilePushDown = pushArgs
		}
		cmds[j].Removed = true
		after := renderCmd(cmds[i])
		rules = append(rules, ruleApplication{
			Rule:   "multifile-predicate-pushdown",
			Before: before,
			After:  after,
		})
	}
	return rules
}

// ruleMultiFileAggregationPushdown moves a group-by after multi-file "from" (with pushdown) into pushdown.
func ruleMultiFileAggregationPushdown(cmds []*pipelineCmd) []ruleApplication {
	var rules []ruleApplication
	for i := 0; i < len(cmds); i++ {
		if cmds[i].Removed || cmds[i].Kind != "from" || !cmds[i].IsMultiFile {
			continue
		}
		if len(cmds[i].MultiFilePushDown) == 0 {
			continue
		}
		j := nextNonRemoved(cmds, i)
		if j == -1 || cmds[j].Kind != "group-by" {
			continue
		}
		before := renderCmd(cmds[i]) + " | " + renderCmd(cmds[j])
		pushArgs := append([]string{"group-by"}, cmds[j].RawArgs...)
		cmds[i].MultiFilePushDown = append(cmds[i].MultiFilePushDown, "+")
		cmds[i].MultiFilePushDown = append(cmds[i].MultiFilePushDown, pushArgs...)
		cmds[j].Removed = true
		after := renderCmd(cmds[i])
		rules = append(rules, ruleApplication{
			Rule:   "multifile-aggregation-pushdown",
			Before: before,
			After:  after,
		})
	}
	return rules
}

// ruleMergeCatalogPredicatePushdown moves a where command after "merge -catalog" into pushdown.
func ruleMergeCatalogPredicatePushdown(cmds []*pipelineCmd) []ruleApplication {
	var rules []ruleApplication
	for i := 0; i < len(cmds); i++ {
		if cmds[i].Removed || cmds[i].Kind != "merge" || !cmds[i].IsMergeCatalog {
			continue
		}
		j := nextNonRemoved(cmds, i)
		if j == -1 || cmds[j].Kind != "where" {
			continue
		}
		before := renderCmd(cmds[i]) + " | " + renderCmd(cmds[j])
		pushArgs := append([]string{"where"}, cmds[j].RawArgs...)
		if len(cmds[i].MergeCatalogPushDown) > 0 {
			cmds[i].MergeCatalogPushDown = append(cmds[i].MergeCatalogPushDown, "+")
			cmds[i].MergeCatalogPushDown = append(cmds[i].MergeCatalogPushDown, pushArgs...)
		} else {
			cmds[i].MergeCatalogPushDown = pushArgs
		}
		cmds[j].Removed = true
		after := renderCmd(cmds[i])
		rules = append(rules, ruleApplication{
			Rule:   "merge-catalog-predicate-pushdown",
			Before: before,
			After:  after,
		})
	}
	return rules
}

// ruleMergeCatalogAggregationPushdown moves a group-by after "merge -catalog" (with pushdown) into pushdown.
func ruleMergeCatalogAggregationPushdown(cmds []*pipelineCmd) []ruleApplication {
	var rules []ruleApplication
	for i := 0; i < len(cmds); i++ {
		if cmds[i].Removed || cmds[i].Kind != "merge" || !cmds[i].IsMergeCatalog {
			continue
		}
		if len(cmds[i].MergeCatalogPushDown) == 0 {
			continue
		}
		j := nextNonRemoved(cmds, i)
		if j == -1 || cmds[j].Kind != "group-by" {
			continue
		}
		before := renderCmd(cmds[i]) + " | " + renderCmd(cmds[j])
		pushArgs := append([]string{"group-by"}, cmds[j].RawArgs...)
		cmds[i].MergeCatalogPushDown = append(cmds[i].MergeCatalogPushDown, "+")
		cmds[i].MergeCatalogPushDown = append(cmds[i].MergeCatalogPushDown, pushArgs...)
		cmds[j].Removed = true
		after := renderCmd(cmds[i])
		rules = append(rules, ruleApplication{
			Rule:   "merge-catalog-aggregation-pushdown",
			Before: before,
			After:  after,
		})
	}
	return rules
}

// ruleSortLimitToTop replaces sort -desc FIELD + limit N with top N -field FIELD.
func ruleSortLimitToTop(cmds []*pipelineCmd) []ruleApplication {
	var rules []ruleApplication
	for i := 0; i < len(cmds); i++ {
		if cmds[i].Removed || cmds[i].Kind != "sort" {
			continue
		}
		if !cmds[i].SortDesc || cmds[i].SortField == "" {
			continue
		}
		j := nextNonRemoved(cmds, i)
		if j == -1 || cmds[j].Kind != "limit" || cmds[j].LimitN == "" {
			continue
		}
		before := renderCmd(cmds[i]) + " | " + renderCmd(cmds[j])
		cmds[i].Kind = "top"
		cmds[i].RawArgs = []string{cmds[j].LimitN, "-field", cmds[i].SortField}
		cmds[i].SortDesc = false
		cmds[i].SortField = ""
		cmds[j].Removed = true
		after := renderCmd(cmds[i])
		rules = append(rules, ruleApplication{
			Rule:   "sort-limit-to-top",
			Before: before,
			After:  after,
		})
	}
	return rules
}

// ruleSortElimination removes a sort that immediately precedes a group-by.
func ruleSortElimination(cmds []*pipelineCmd) []ruleApplication {
	var rules []ruleApplication
	for i := 0; i < len(cmds); i++ {
		if cmds[i].Removed || cmds[i].Kind != "sort" {
			continue
		}
		j := nextNonRemoved(cmds, i)
		if j == -1 || cmds[j].Kind != "group-by" {
			continue
		}
		before := renderCmd(cmds[i])
		cmds[i].Removed = true
		rules = append(rules, ruleApplication{
			Rule:   "sort-elimination",
			Before: before,
			After:  "(removed)",
		})
	}
	return rules
}

// --- Phase 2 optimization rules ---

// ruleCatalogPredicateExtraction moves where conditions that match catalog metadata
// columns into the catalog's -if pruning filters.
func ruleCatalogPredicateExtraction(cmds []*pipelineCmd) []ruleApplication {
	var rules []ruleApplication
	for i := 0; i < len(cmds); i++ {
		if cmds[i].Removed || cmds[i].Kind != "from" || !cmds[i].IsCatalog {
			continue
		}
		j := nextNonRemoved(cmds, i)
		if j == -1 || cmds[j].Kind != "where" {
			continue
		}

		// Read catalog CSV header to determine metadata columns
		catalogCols, err := readCatalogMetadataColumns(cmds[i].CatalogFile)
		if err != nil {
			continue // can't read catalog, skip
		}

		clauses := parseWhereArgs(cmds[j].RawArgs)
		// Only extract from single-clause where (multi-clause OR is harder to split)
		if len(clauses) != 1 {
			continue
		}
		clause := clauses[0]

		// Split conditions into catalog-matchable and data-only
		var catalogConds, dataConds []whereCondition
		for _, cond := range clause.Conditions {
			if catalogCols[cond.Field] {
				catalogConds = append(catalogConds, cond)
			} else {
				dataConds = append(dataConds, cond)
			}
		}

		if len(catalogConds) == 0 {
			continue
		}

		before := renderCmd(cmds[i]) + " | " + renderCmd(cmds[j])

		// Add to catalog filters
		cmds[i].CatalogFilters = append(cmds[i].CatalogFilters, catalogConds...)

		// Update or remove where command
		if len(dataConds) == 0 && len(clause.Exprs) == 0 {
			cmds[j].Removed = true
		} else {
			remaining := whereClause{Conditions: dataConds, Exprs: clause.Exprs}
			cmds[j].RawArgs = buildWhereArgs([]whereClause{remaining})
		}

		after := renderCmd(cmds[i])
		if !cmds[j].Removed {
			after += " | " + renderCmd(cmds[j])
		}
		rules = append(rules, ruleApplication{
			Rule:   "catalog-predicate-extraction",
			Before: before,
			After:  after,
		})
	}
	return rules
}

// ruleCatalogPredicatePushdown moves a where command after "from catalog" into push-down.
func ruleCatalogPredicatePushdown(cmds []*pipelineCmd) []ruleApplication {
	var rules []ruleApplication
	for i := 0; i < len(cmds); i++ {
		if cmds[i].Removed || cmds[i].Kind != "from" || !cmds[i].IsCatalog {
			continue
		}
		j := nextNonRemoved(cmds, i)
		if j == -1 || cmds[j].Kind != "where" {
			continue
		}
		before := renderCmd(cmds[i]) + " | " + renderCmd(cmds[j])
		pushArgs := append([]string{"where"}, cmds[j].RawArgs...)
		if len(cmds[i].CatalogPushDown) > 0 {
			cmds[i].CatalogPushDown = append(cmds[i].CatalogPushDown, "+")
			cmds[i].CatalogPushDown = append(cmds[i].CatalogPushDown, pushArgs...)
		} else {
			cmds[i].CatalogPushDown = pushArgs
		}
		cmds[j].Removed = true
		after := renderCmd(cmds[i])
		rules = append(rules, ruleApplication{
			Rule:   "catalog-predicate-pushdown",
			Before: before,
			After:  after,
		})
	}
	return rules
}

// ruleCatalogAggregationPushdown moves a group-by after "from catalog" (with push-down) into push-down.
func ruleCatalogAggregationPushdown(cmds []*pipelineCmd) []ruleApplication {
	var rules []ruleApplication
	for i := 0; i < len(cmds); i++ {
		if cmds[i].Removed || cmds[i].Kind != "from" || !cmds[i].IsCatalog {
			continue
		}
		if len(cmds[i].CatalogPushDown) == 0 {
			continue
		}
		j := nextNonRemoved(cmds, i)
		if j == -1 || cmds[j].Kind != "group-by" {
			continue
		}
		before := renderCmd(cmds[i]) + " | " + renderCmd(cmds[j])
		pushArgs := append([]string{"group-by"}, cmds[j].RawArgs...)
		cmds[i].CatalogPushDown = append(cmds[i].CatalogPushDown, "+")
		cmds[i].CatalogPushDown = append(cmds[i].CatalogPushDown, pushArgs...)
		cmds[j].Removed = true
		after := renderCmd(cmds[i])
		rules = append(rules, ruleApplication{
			Rule:   "catalog-aggregation-pushdown",
			Before: before,
			After:  after,
		})
	}
	return rules
}

// rulePredicateSimplification simplifies redundant predicates within where commands.
// Returns true for the second value if a contradiction was detected (empty result).
func rulePredicateSimplification(cmds []*pipelineCmd) ([]ruleApplication, bool) {
	var rules []ruleApplication
	for i := 0; i < len(cmds); i++ {
		if cmds[i].Removed || cmds[i].Kind != "where" {
			continue
		}
		clauses := parseWhereArgs(cmds[i].RawArgs)
		changed := false
		allEmpty := true

		for ci, clause := range clauses {
			simplified, contradiction := simplifyClause(clause)
			if contradiction {
				// This AND-clause can never match — remove it
				clauses[ci] = whereClause{}
				changed = true
				continue
			}
			if len(simplified.Conditions) != len(clause.Conditions) {
				clauses[ci] = simplified
				changed = true
			}
			if len(clauses[ci].Conditions) > 0 || len(clauses[ci].Exprs) > 0 {
				allEmpty = false
			}
		}

		if !changed {
			continue
		}

		// If ALL OR-clauses are contradictory, the entire pipeline produces no results
		if allEmpty {
			before := renderCmd(cmds[i])
			rules = append(rules, ruleApplication{
				Rule:   "empty-result",
				Before: before,
				After:  "(no results — contradictory predicates)",
			})
			return rules, true
		}

		// Rebuild with non-empty clauses
		var kept []whereClause
		for _, c := range clauses {
			if len(c.Conditions) > 0 || len(c.Exprs) > 0 {
				kept = append(kept, c)
			}
		}

		before := renderCmd(cmds[i])
		cmds[i].RawArgs = buildWhereArgs(kept)
		after := renderCmd(cmds[i])
		if before != after {
			rules = append(rules, ruleApplication{
				Rule:   "predicate-simplification",
				Before: before,
				After:  after,
			})
		}
	}
	return rules, false
}

// simplifyClause simplifies conditions within a single AND-clause.
// Returns the simplified clause and whether a contradiction was found.
func simplifyClause(clause whereClause) (whereClause, bool) {
	conds := clause.Conditions
	if len(conds) <= 1 {
		return clause, false
	}

	// Check for eq/eq contradictions and eq/ne contradictions
	eqValues := make(map[string]string) // field → value for eq conditions
	for _, c := range conds {
		if c.Operator == "eq" {
			if prev, exists := eqValues[c.Field]; exists {
				if prev != c.Value {
					return whereClause{}, true // eq X AND eq Y where X != Y
				}
			}
			eqValues[c.Field] = c.Value
		}
	}
	for _, c := range conds {
		if c.Operator == "ne" {
			if eqVal, exists := eqValues[c.Field]; exists && eqVal == c.Value {
				return whereClause{}, true // eq X AND ne X
			}
		}
	}

	// Tighten same-field same-direction ranges
	// Group conditions by field
	type bound struct {
		idx      int
		operator string
		value    float64
		raw      string
		isNum    bool
	}
	fieldLower := make(map[string]bound) // field → tightest lower bound (gt/ge)
	fieldUpper := make(map[string]bound) // field → tightest upper bound (lt/le)
	remove := make(map[int]bool)

	for idx, c := range conds {
		val, err := strconv.ParseFloat(c.Value, 64)
		isNum := err == nil

		switch c.Operator {
		case "gt", "ge":
			if prev, exists := fieldLower[c.Field]; exists && isNum && prev.isNum {
				// Keep the tighter lower bound
				if val > prev.value || (val == prev.value && c.Operator == "gt" && prev.operator == "ge") {
					remove[prev.idx] = true
					fieldLower[c.Field] = bound{idx, c.Operator, val, c.Value, true}
				} else {
					remove[idx] = true
				}
			} else if isNum {
				fieldLower[c.Field] = bound{idx, c.Operator, val, c.Value, true}
			}
		case "lt", "le":
			if prev, exists := fieldUpper[c.Field]; exists && isNum && prev.isNum {
				// Keep the tighter upper bound
				if val < prev.value || (val == prev.value && c.Operator == "lt" && prev.operator == "le") {
					remove[prev.idx] = true
					fieldUpper[c.Field] = bound{idx, c.Operator, val, c.Value, true}
				} else {
					remove[idx] = true
				}
			} else if isNum {
				fieldUpper[c.Field] = bound{idx, c.Operator, val, c.Value, true}
			}
		}
	}

	// Check for lower > upper contradictions
	for field, lower := range fieldLower {
		if upper, exists := fieldUpper[field]; exists && lower.isNum && upper.isNum {
			if lower.value > upper.value {
				return whereClause{}, true
			}
			if lower.value == upper.value {
				// gt X AND lt X → contradiction; gt X AND le X → contradiction; ge X AND lt X → contradiction
				if lower.operator == "gt" || upper.operator == "lt" {
					return whereClause{}, true
				}
				// ge X AND le X is valid (exactly X)
			}
		}
	}

	if len(remove) == 0 {
		return clause, false
	}

	var kept []whereCondition
	for idx, c := range conds {
		if !remove[idx] {
			kept = append(kept, c)
		}
	}
	return whereClause{Conditions: kept, Exprs: clause.Exprs}, false
}

// rulePredicateReorder reorders conditions within where clauses so cheap ops run first.
func rulePredicateReorder(cmds []*pipelineCmd) []ruleApplication {
	var rules []ruleApplication
	for i := 0; i < len(cmds); i++ {
		if cmds[i].Removed || cmds[i].Kind != "where" {
			continue
		}
		clauses := parseWhereArgs(cmds[i].RawArgs)
		changed := false

		for ci, clause := range clauses {
			if len(clause.Conditions) <= 1 {
				continue
			}
			// Sort conditions by cost (cheap first)
			sorted := make([]whereCondition, len(clause.Conditions))
			copy(sorted, clause.Conditions)
			sort.SliceStable(sorted, func(a, b int) bool {
				return conditionCost(sorted[a].Operator) < conditionCost(sorted[b].Operator)
			})
			// Check if order changed
			for k := range sorted {
				if sorted[k] != clause.Conditions[k] {
					changed = true
					break
				}
			}
			if changed {
				clauses[ci].Conditions = sorted
			}
		}

		if !changed {
			continue
		}

		before := renderCmd(cmds[i])
		cmds[i].RawArgs = buildWhereArgs(clauses)
		after := renderCmd(cmds[i])
		if before != after {
			rules = append(rules, ruleApplication{
				Rule:   "predicate-reorder",
				Before: before,
				After:  after,
			})
		}
	}
	return rules
}

// conditionCost returns a cost estimate for a where operator (lower = cheaper).
func conditionCost(op string) int {
	switch op {
	case "eq", "ne":
		return 1
	case "gt", "ge", "lt", "le":
		return 2
	case "contains", "startswith", "endswith":
		return 3
	case "regex":
		return 4
	default:
		return 5
	}
}

// --- Catalog helpers ---

// readCatalogMetadataColumns reads the catalog CSV header and returns the set of
// metadata column names (collapsing field_from/field_to to field).
func readCatalogMetadataColumns(filename string) (map[string]bool, error) {
	f, err := os.Open(filename)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	reader := csv.NewReader(f)
	headers, err := reader.Read()
	if err != nil {
		return nil, err
	}

	cols := make(map[string]bool)
	for _, h := range headers {
		h = strings.TrimSpace(strings.ToLower(h))
		if h == "host" || h == "path" || h == "format" || h == "fields" {
			continue
		}
		base := h
		if strings.HasSuffix(h, "_from") {
			base = strings.TrimSuffix(h, "_from")
		} else if strings.HasSuffix(h, "_to") {
			base = strings.TrimSuffix(h, "_to")
		}
		cols[base] = true
	}
	return cols, nil
}

// --- Phase 4 optimization rules ---

// ruleJoinPredicatePushdown moves where conditions to the appropriate side of a join.
// Left-only predicates are inserted before the join. Right-only predicates are pushed
// into a process substitution on the right side.
func ruleJoinPredicatePushdown(cmds []*pipelineCmd) ([]*pipelineCmd, []ruleApplication) {
	var rules []ruleApplication
	for i := 0; i < len(cmds); i++ {
		if cmds[i].Removed || !cmds[i].IsJoin {
			continue
		}

		j := nextNonRemoved(cmds, i)
		if j == -1 || cmds[j].Kind != "where" {
			continue
		}

		// Only handle simple -if predicates (not -if-expr which can reference any field)
		clauses := parseWhereArgs(cmds[j].RawArgs)
		if len(clauses) != 1 {
			continue // multi-clause OR is harder to split across sides
		}
		clause := clauses[0]
		if len(clause.Exprs) > 0 {
			continue // expressions can reference fields from both sides
		}
		if len(clause.Conditions) == 0 {
			continue
		}

		// Find the left source to read its fields
		leftFrom := findLeftSource(cmds, i)
		if leftFrom == -1 {
			continue
		}
		leftFile, leftFormat := getFromFileAndFormat(cmds[leftFrom])
		if leftFile == "" {
			continue
		}

		leftFields, err := readFieldNames(leftFile, leftFormat)
		if err != nil {
			continue
		}

		// Read right-side fields — either from plain file or process substitution's from command
		var rightFields []string
		if cmds[i].JoinIsProcessSub {
			rightFile, rightFormat := findProcSubSource(cmds[i].JoinProcSubCmds)
			if rightFile == "" {
				continue
			}
			rightFields, err = readFieldNames(rightFile, rightFormat)
		} else {
			rightFields, err = readFieldNames(cmds[i].JoinFile, "")
		}
		if err != nil {
			continue
		}

		leftSet := make(map[string]bool)
		for _, f := range leftFields {
			leftSet[f] = true
		}
		rightSet := make(map[string]bool)
		for _, f := range rightFields {
			rightSet[f] = true
		}

		// Classify each predicate
		var leftOnly, rightOnly, both []whereCondition
		for _, cond := range clause.Conditions {
			inLeft := leftSet[cond.Field]
			inRight := rightSet[cond.Field]
			switch {
			case inLeft && !inRight:
				leftOnly = append(leftOnly, cond)
			case inRight && !inLeft:
				rightOnly = append(rightOnly, cond)
			default:
				both = append(both, cond) // in both sides or neither — leave after join
			}
		}

		if len(leftOnly) == 0 && len(rightOnly) == 0 {
			continue
		}

		before := renderCmd(cmds[i]) + " | " + renderCmd(cmds[j])

		// Push right-only predicates into join's right side
		if len(rightOnly) > 0 {
			whereArgs := buildWhereArgs([]whereClause{{Conditions: rightOnly}})
			if cmds[i].JoinIsProcessSub {
				// Append where to the existing inner pipeline
				cmds[i].JoinProcSubCmds = append(cmds[i].JoinProcSubCmds, &pipelineCmd{
					Kind:    "where",
					RawArgs: whereArgs,
				})
			} else {
				cmds[i].JoinRightWhere = whereArgs
			}
		}

		// Left-only predicates: insert a where command before the join
		if len(leftOnly) > 0 {
			leftWhere := &pipelineCmd{
				Kind:    "where",
				RawArgs: buildWhereArgs([]whereClause{{Conditions: leftOnly}}),
			}
			// Splice before position i
			newCmds := make([]*pipelineCmd, 0, len(cmds)+1)
			newCmds = append(newCmds, cmds[:i]...)
			newCmds = append(newCmds, leftWhere)
			newCmds = append(newCmds, cmds[i:]...)
			cmds = newCmds
			i++ // adjust for inserted command
			j++ // adjust for inserted command
		}

		// Update or remove the original where
		if len(both) == 0 {
			cmds[j].Removed = true
		} else {
			cmds[j].RawArgs = buildWhereArgs([]whereClause{{Conditions: both}})
		}

		// Build after description
		var afterParts []string
		if len(leftOnly) > 0 {
			afterParts = append(afterParts, renderCmd(cmds[i-1])) // the inserted where
		}
		afterParts = append(afterParts, renderCmd(cmds[i]))
		if !cmds[j].Removed {
			afterParts = append(afterParts, renderCmd(cmds[j]))
		}

		rules = append(rules, ruleApplication{
			Rule:   "join-predicate-pushdown",
			Before: before,
			After:  strings.Join(afterParts, " | "),
		})
	}

	return cmds, rules
}

// findProcSubSource finds the source file and format from a process substitution's inner pipeline.
// Returns ("", "") if the inner pipeline doesn't start with a simple "from" command.
func findProcSubSource(innerCmds []*pipelineCmd) (string, string) {
	for _, cmd := range innerCmds {
		if cmd.Kind == "from" && !cmd.IsSSH && !cmd.IsCatalog {
			return getFromFileAndFormat(cmd)
		}
	}
	return "", ""
}

// renderProcSubPipeline renders an inner pipeline as "ssql cmd1 | ssql cmd2 | ...".
func renderProcSubPipeline(innerCmds []*pipelineCmd) string {
	var parts []string
	for _, cmd := range innerCmds {
		if cmd.Removed {
			continue
		}
		parts = append(parts, renderCmd(cmd))
	}
	return strings.Join(parts, " | ")
}

// findLeftSource finds the from command that feeds into a join at position joinIdx.
// Walks backwards past non-removed commands to find the most recent from.
func findLeftSource(cmds []*pipelineCmd, joinIdx int) int {
	for j := joinIdx - 1; j >= 0; j-- {
		if cmds[j].Removed {
			continue
		}
		if cmds[j].Kind == "from" {
			return j
		}
		// If there's another command in between (where, sort, etc.), the left source
		// is still the from command further back
	}
	return -1
}

// readFieldNames reads column/field names from a file (CSV, TSV, or JSONL).
func readFieldNames(filename, format string) ([]string, error) {
	if format == "" {
		// Infer from extension
		switch {
		case strings.HasSuffix(filename, ".csv"):
			format = "csv"
		case strings.HasSuffix(filename, ".tsv"):
			format = "tsv"
		case strings.HasSuffix(filename, ".jsonl"), strings.HasSuffix(filename, ".json"):
			format = "jsonl"
		default:
			return nil, fmt.Errorf("unknown format for %s", filename)
		}
	}

	switch format {
	case "csv", "tsv":
		return readFileHeader(filename, format)
	case "jsonl", "json":
		return readJSONLFieldNames(filename)
	default:
		return nil, fmt.Errorf("unsupported format: %s", format)
	}
}

// readJSONLFieldNames reads the first line of a JSONL file and extracts field names.
func readJSONLFieldNames(filename string) ([]string, error) {
	f, err := os.Open(filename)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var line []byte
	buf := make([]byte, 1)
	for {
		_, err := f.Read(buf)
		if err != nil {
			break
		}
		if buf[0] == '\n' {
			break
		}
		line = append(line, buf[0])
	}
	if len(line) == 0 {
		return nil, fmt.Errorf("empty file")
	}

	// Parse JSON to extract keys
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(line, &obj); err != nil {
		return nil, err
	}
	var fields []string
	for k := range obj {
		fields = append(fields, k)
	}
	sort.Strings(fields)
	return fields, nil
}

// --- Phase 3 optimization rules ---

// ruleParquetColumnPruning adds -columns to "from parquet" based on downstream field usage.
func ruleParquetColumnPruning(cmds []*pipelineCmd) []ruleApplication {
	var rules []ruleApplication
	for i := 0; i < len(cmds); i++ {
		if cmds[i].Removed || cmds[i].Kind != "from" {
			continue
		}
		if !isParquetFrom(cmds[i]) || hasColumnsFlag(cmds[i]) {
			continue
		}

		needed, allNeeded := collectDownstreamFields(cmds, i)
		if allNeeded || len(needed) == 0 {
			continue
		}

		before := renderCmd(cmds[i])
		for _, f := range sortedStringSet(needed) {
			cmds[i].RawArgs = append(cmds[i].RawArgs, "-columns", f)
		}
		after := renderCmd(cmds[i])
		if before != after {
			rules = append(rules, ruleApplication{
				Rule:   "parquet-column-pruning",
				Before: before,
				After:  after,
			})
		}
	}
	return rules
}

// ruleMidPipelineColumnDrop inserts an include command after CSV/TSV from to drop
// unneeded columns early, reducing JSONL serialization through the pipeline.
// Returns the (possibly extended) cmds slice and any rule applications.
func ruleMidPipelineColumnDrop(cmds []*pipelineCmd) ([]*pipelineCmd, []ruleApplication) {
	var rules []ruleApplication
	for i := 0; i < len(cmds); i++ {
		if cmds[i].Removed || cmds[i].Kind != "from" {
			continue
		}
		if cmds[i].IsSSH || cmds[i].IsCatalog {
			continue
		}

		file, format := getFromFileAndFormat(cmds[i])
		if file == "" {
			continue
		}
		// Only for CSV/TSV (formats where we can read the header)
		isCSV := format == "csv" || format == "tsv" ||
			(format == "" && (strings.HasSuffix(file, ".csv") || strings.HasSuffix(file, ".tsv")))
		if !isCSV {
			continue
		}
		// Skip if parquet (handled by its own rule)
		if format == "parquet" {
			continue
		}

		// Don't insert if next command is already an include
		j := nextNonRemoved(cmds, i)
		if j != -1 && cmds[j].Kind == "include" {
			continue
		}

		// Read file header to get all columns
		allCols, err := readFileHeader(file, format)
		if err != nil || len(allCols) <= 1 {
			continue
		}

		needed, allNeeded := collectDownstreamFields(cmds, i)
		if allNeeded || len(needed) == 0 || len(needed) >= len(allCols) {
			continue
		}

		// Verify all needed fields exist in the source
		colSet := make(map[string]bool)
		for _, c := range allCols {
			colSet[c] = true
		}
		allExist := true
		for f := range needed {
			if !colSet[f] {
				allExist = false
				break
			}
		}
		if !allExist {
			continue
		}

		// Insert include command after from
		includeCmd := &pipelineCmd{
			Kind:    "include",
			RawArgs: sortedStringSet(needed),
		}
		// Splice into cmds after position i
		newCmds := make([]*pipelineCmd, 0, len(cmds)+1)
		newCmds = append(newCmds, cmds[:i+1]...)
		newCmds = append(newCmds, includeCmd)
		newCmds = append(newCmds, cmds[i+1:]...)
		cmds = newCmds

		rules = append(rules, ruleApplication{
			Rule:   "column-drop",
			Before: fmt.Sprintf("%d columns", len(allCols)),
			After:  fmt.Sprintf("include %s (%d of %d)", strings.Join(sortedStringSet(needed), " "), len(needed), len(allCols)),
		})
		i++ // skip past the inserted include
	}
	return cmds, rules
}

// ruleLimitPushdown moves limit before consecutive row-preserving commands
// (rename, cast, include, exclude, update) so fewer rows are transformed.
func ruleLimitPushdown(cmds []*pipelineCmd) []ruleApplication {
	var rules []ruleApplication
	for i := 0; i < len(cmds); i++ {
		if cmds[i].Removed || cmds[i].Kind != "limit" {
			continue
		}

		// Find the earliest position we can push the limit to
		insertPos := i
		for j := i - 1; j >= 0; j-- {
			if cmds[j].Removed {
				continue
			}
			if isRowPreserving(cmds[j]) {
				insertPos = j
			} else {
				break
			}
		}
		if insertPos >= i {
			continue
		}

		// Record before state
		before := renderPipelineRange(cmds, insertPos, i)

		// Shift commands: move limit from position i to insertPos
		saved := cmds[i]
		for j := i; j > insertPos; j-- {
			cmds[j] = cmds[j-1]
		}
		cmds[insertPos] = saved

		after := renderPipelineRange(cmds, insertPos, i)
		rules = append(rules, ruleApplication{
			Rule:   "limit-pushdown",
			Before: before,
			After:  after,
		})
	}
	return rules
}

// isRowPreserving returns true if a command preserves the number of rows.
func isRowPreserving(cmd *pipelineCmd) bool {
	switch cmd.Kind {
	case "rename", "cast", "include", "exclude", "update":
		return true
	default:
		return false
	}
}

// renderPipelineRange renders commands from index start to end (inclusive) as a pipeline.
func renderPipelineRange(cmds []*pipelineCmd, start, end int) string {
	var parts []string
	for i := start; i <= end && i < len(cmds); i++ {
		if !cmds[i].Removed {
			parts = append(parts, renderCmd(cmds[i]))
		}
	}
	return strings.Join(parts, " | ")
}

// --- Field analysis ---

// collectDownstreamFields collects all field names referenced by commands after index i.
// Returns the set of needed fields and whether all fields are needed (due to expressions etc).
func collectDownstreamFields(cmds []*pipelineCmd, startIdx int) (map[string]bool, bool) {
	needed := make(map[string]bool)
	for j := startIdx + 1; j < len(cmds); j++ {
		if cmds[j].Removed {
			continue
		}
		fields, allNeeded := extractReferencedFields(cmds[j])
		if allNeeded {
			return nil, true
		}
		for _, f := range fields {
			needed[f] = true
		}
	}
	return needed, false
}

// extractReferencedFields returns field names explicitly referenced by a command.
// Returns allNeeded=true if the command may reference arbitrary fields (expressions, distinct, etc).
func extractReferencedFields(cmd *pipelineCmd) ([]string, bool) {
	switch cmd.Kind {
	case "where":
		return extractWhereFields(cmd.RawArgs)
	case "group-by":
		return extractGroupByFields(cmd.RawArgs)
	case "sort":
		return extractPositionalFields(cmd.RawArgs), false
	case "top":
		return extractTopFields(cmd.RawArgs), false
	case "include":
		return extractPositionalFields(cmd.RawArgs), false
	case "exclude":
		// Exclude references specific fields to drop, but all other fields pass through.
		// We can't determine the complement, so assume all fields needed.
		return nil, true
	case "rename":
		return extractRenameFields(cmd.RawArgs), false
	case "cast":
		return extractCastFields(cmd.RawArgs), false
	case "join":
		return extractJoinFields(cmd.RawArgs)
	case "update":
		return extractUpdateFields(cmd.RawArgs)
	case "distinct":
		return nil, true
	case "window":
		return extractWindowFields(cmd.RawArgs)
	case "to":
		return nil, false // terminal, outputs whatever it receives
	case "limit", "offset":
		return nil, false // don't reference fields
	default:
		return nil, true // unknown command, assume all fields needed
	}
}

func extractWhereFields(args []string) ([]string, bool) {
	clauses := parseWhereArgs(args)
	var fields []string
	for _, clause := range clauses {
		for _, cond := range clause.Conditions {
			fields = append(fields, cond.Field)
		}
		if len(clause.Exprs) > 0 {
			return nil, true
		}
	}
	return fields, false
}

func extractGroupByFields(args []string) ([]string, bool) {
	var fields []string
	i := 0
	for i < len(args) && !strings.HasPrefix(args[i], "-") {
		fields = append(fields, args[i])
		i++
	}
	for i < len(args) {
		switch args[i] {
		case "-count":
			if i+1 < len(args) {
				i += 2
			} else {
				i++
			}
		case "-sum", "-avg", "-min", "-max", "-collect":
			if i+2 < len(args) {
				fields = append(fields, args[i+1])
				i += 3
			} else {
				i++
			}
		case "-expr", "-stream-expr":
			return nil, true
		default:
			i++
		}
	}
	return fields, false
}

func extractPositionalFields(args []string) []string {
	var fields []string
	for _, arg := range args {
		if !strings.HasPrefix(arg, "-") {
			fields = append(fields, arg)
		}
	}
	return fields
}

func extractTopFields(args []string) []string {
	for i, arg := range args {
		if (arg == "-field" || arg == "-f") && i+1 < len(args) {
			return []string{args[i+1]}
		}
	}
	return nil
}

func extractRenameFields(args []string) []string {
	var fields []string
	i := 0
	for i < len(args) {
		if (args[i] == "-as" || args[i] == "-a") && i+2 < len(args) {
			fields = append(fields, args[i+1]) // old field name
			i += 3
		} else {
			i++
		}
	}
	return fields
}

func extractCastFields(args []string) []string {
	var fields []string
	i := 0
	for i < len(args) {
		if (args[i] == "-type" || args[i] == "-t") && i+2 < len(args) {
			fields = append(fields, args[i+1])
			i += 3
		} else {
			i++
		}
	}
	return fields
}

func extractJoinFields(args []string) ([]string, bool) {
	var fields []string
	i := 0
	for i < len(args) {
		switch args[i] {
		case "-using", "-u":
			if i+1 < len(args) {
				fields = append(fields, args[i+1])
				i += 2
				continue
			}
		case "-on", "-o":
			if i+2 < len(args) {
				fields = append(fields, args[i+1]) // left field
				i += 3
				continue
			}
		}
		i++
	}
	return fields, false
}

func extractUpdateFields(args []string) ([]string, bool) {
	var fields []string
	i := 0
	for i < len(args) {
		switch args[i] {
		case "-if", "-i":
			if i+3 < len(args) {
				fields = append(fields, args[i+1])
				i += 4
				continue
			}
		case "-if-expr", "-x", "-set-expr":
			return nil, true
		case "-set":
			if i+2 < len(args) {
				i += 3 // -set field value: doesn't read existing field
				continue
			}
		}
		i++
	}
	return fields, false
}

func extractWindowFields(args []string) ([]string, bool) {
	var fields []string
	i := 0
	for i < len(args) {
		switch args[i] {
		case "-partition", "-order":
			if i+1 < len(args) {
				fields = append(fields, args[i+1])
				i += 2
				continue
			}
		case "-lag", "-lead":
			if i+2 < len(args) {
				fields = append(fields, args[i+1])
				i += 3 // -lag field N
				continue
			}
		case "-first", "-last", "-sum", "-avg", "-min", "-max":
			if i+1 < len(args) {
				fields = append(fields, args[i+1])
				i += 2
				continue
			}
		case "-expr":
			return nil, true
		}
		i++
	}
	return fields, false
}

// --- File helpers ---

func isParquetFrom(cmd *pipelineCmd) bool {
	return len(cmd.RawArgs) >= 2 && cmd.RawArgs[0] == "parquet"
}

func hasColumnsFlag(cmd *pipelineCmd) bool {
	for _, arg := range cmd.RawArgs {
		if arg == "-columns" || arg == "-c" {
			return true
		}
	}
	return false
}

func getFromFileAndFormat(cmd *pipelineCmd) (string, string) {
	if len(cmd.RawArgs) == 0 {
		return "", ""
	}
	switch cmd.RawArgs[0] {
	case "csv", "tsv", "json", "jsonl", "arrow", "parquet", "xlsx", "wav":
		if len(cmd.RawArgs) >= 2 {
			return cmd.RawArgs[1], cmd.RawArgs[0]
		}
		return "", cmd.RawArgs[0]
	default:
		return cmd.RawArgs[0], ""
	}
}

// readFileHeader reads the first line of a CSV/TSV file to get column names.
func readFileHeader(filename, format string) ([]string, error) {
	f, err := os.Open(filename)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	reader := csv.NewReader(f)
	if format == "tsv" || strings.HasSuffix(filename, ".tsv") {
		reader.Comma = '\t'
	}
	headers, err := reader.Read()
	if err != nil {
		return nil, err
	}
	for i := range headers {
		headers[i] = strings.TrimSpace(headers[i])
	}
	return headers, nil
}

// sortedStringSet returns sorted keys from a string set.
func sortedStringSet(m map[string]bool) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// --- Rendering ---

// renderCmd renders a single pipelineCmd as a shell command string.
func renderCmd(cmd *pipelineCmd) string {
	var parts []string
	parts = append(parts, "ssql", cmd.Kind)

	if cmd.IsSSH {
		parts = append(parts, "ssh")
		if cmd.SSHHost != "" {
			parts = append(parts, cmd.SSHHost)
		}
		if cmd.SSHPath != "" {
			parts = append(parts, cmd.SSHPath)
		}
		if cmd.SSHGPU {
			parts = append(parts, "-gpu")
		}
		if len(cmd.SSHPushDown) > 0 {
			parts = append(parts, "--")
			parts = append(parts, cmd.SSHPushDown...)
		}
		return shellJoin(parts)
	}

	if cmd.IsJoin {
		if len(cmd.JoinRightWhere) > 0 {
			// Render with process substitution for right-side filtering (plain file join).
			// The <(...) must NOT be quoted — it's bash syntax, not an argument.
			rightPipeline := "ssql from " + shellJoin([]string{cmd.JoinFile}) + " | ssql where " + shellJoin(cmd.JoinRightWhere)
			prefix := shellJoin(parts)
			suffix := shellJoin(cmd.JoinArgs)
			return prefix + " <(" + rightPipeline + ") " + suffix
		}
		if cmd.JoinIsProcessSub && len(cmd.JoinProcSubCmds) > 0 {
			// Render modified process substitution with inner pipeline
			rightPipeline := renderProcSubPipeline(cmd.JoinProcSubCmds)
			prefix := shellJoin(parts)
			suffix := shellJoin(cmd.JoinArgs)
			return prefix + " <(" + rightPipeline + ") " + suffix
		}
		parts = append(parts, cmd.JoinFile)
		parts = append(parts, cmd.JoinArgs...)
		return shellJoin(parts)
	}

	if cmd.IsMergeCatalog {
		parts = append(parts, "-catalog", cmd.MergeCatalogFile)
		for _, f := range cmd.MergeCatalogFilters {
			parts = append(parts, "-if", f.Field, f.Operator, f.Value)
		}
		if len(cmd.MergeCatalogByFields) > 0 {
			parts = append(parts, "-by")
			parts = append(parts, cmd.MergeCatalogByFields...)
		}
		if cmd.MergeCatalogDesc {
			parts = append(parts, "-desc")
		}
		if cmd.MergeCatalogGPU {
			parts = append(parts, "-gpu")
		}
		if cmd.MergeCatalogShardField != "" {
			parts = append(parts, "-shard-field", cmd.MergeCatalogShardField)
		}
		if len(cmd.MergeCatalogPushDown) > 0 {
			parts = append(parts, "--")
			parts = append(parts, cmd.MergeCatalogPushDown...)
		}
		return shellJoin(parts)
	}

	if cmd.IsMultiFile {
		parts = append(parts, cmd.MultiFileFormat)
		parts = append(parts, cmd.MultiFileFiles...)
		parts = append(parts, cmd.MultiFileFlags...)
		if len(cmd.MultiFilePushDown) > 0 {
			parts = append(parts, "--")
			parts = append(parts, cmd.MultiFilePushDown...)
		}
		return shellJoin(parts)
	}

	if cmd.IsCatalog {
		parts = append(parts, "catalog")
		if cmd.CatalogFile != "" {
			parts = append(parts, cmd.CatalogFile)
		}
		for _, f := range cmd.CatalogFilters {
			parts = append(parts, "-if", f.Field, f.Operator, f.Value)
		}
		if cmd.CatalogGPU {
			parts = append(parts, "-gpu")
		}
		if cmd.CatalogShardField != "" {
			parts = append(parts, "-shard-field", cmd.CatalogShardField)
		}
		if len(cmd.CatalogPushDown) > 0 {
			parts = append(parts, "--")
			parts = append(parts, cmd.CatalogPushDown...)
		}
		return shellJoin(parts)
	}

	parts = append(parts, cmd.RawArgs...)
	return shellJoin(parts)
}

// renderPipeline renders non-removed commands as a shell pipeline string.
func renderPipeline(cmds []*pipelineCmd) string {
	var parts []string
	for _, cmd := range cmds {
		if cmd.Removed {
			continue
		}
		parts = append(parts, renderCmd(cmd))
	}
	return strings.Join(parts, " | ")
}

// shellJoin joins args into a shell command, quoting args that need it.
func shellJoin(args []string) string {
	var parts []string
	for _, arg := range args {
		if needsQuoting(arg) {
			parts = append(parts, "'"+strings.ReplaceAll(arg, "'", "'\\''")+"'")
		} else {
			parts = append(parts, arg)
		}
	}
	return strings.Join(parts, " ")
}

// needsQuoting returns true if a shell argument needs quoting.
func needsQuoting(s string) bool {
	if s == "" {
		return true
	}
	for _, c := range s {
		switch c {
		case ' ', '\t', '\n', '|', '&', ';', '(', ')', '<', '>', '"', '\'', '`', '$', '!', '{', '}', '[', ']', '*', '?', '#', '~':
			return true
		}
	}
	return false
}
