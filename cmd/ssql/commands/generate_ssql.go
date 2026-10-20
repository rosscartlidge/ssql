package commands

import (
	"github.com/rosscartlidge/ssql/v4"
	"regexp"
	"slices"
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
	sub := cmd.Subcommand("ssql").
		Description("Optimize an ssql pipeline by rewriting it with fewer commands").
		Example("(export SSQL_MODE=record; ssql from ssh host /data.csv | ssql where -if status ge 500 | ssql to table) | ssql generate ssql", "Push filter into SSH").
		Example("(export SSQL_MODE=record; ssql from csv data.csv | ssql sort -desc revenue | ssql limit 10 | ssql to table) | ssql generate ssql", "Rewrite sort+limit as top").
		Example("(export SSQL_MODE=record; ssql from csv data.csv | ssql where -if age gt 25 | ssql where -if dept eq sales | ssql to table) | ssql generate ssql", "Merge adjacent where commands").
		Example("ssql generate ssql -pipeline 'ssql from csv data.csv | ssql sort -desc revenue | ssql limit 10 | ssql to table'", "One-shot: optimize the quoted pipeline (rewrites sort+limit as top)").
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
		Flag("-as-written").
		Bool().
		Global().
		Default(false).
		Help("Render the result as a rewrite of the pipeline as written: a zone the source resolved itself (TZ, the machine, a header) is left off the from stage, so a preview that compares with what was typed sees only the rules' changes; a -tz you wrote stays").
		Done()
	pipelineSourceFlags(sub, "optimize", "record").
		Handler(func(ctx *cf.Context) error {
			var run bool
			var explain bool
			var asWritten bool
			if v, ok := ctx.GlobalFlags["-run"]; ok {
				run = v.(bool)
			}
			if v, ok := ctx.GlobalFlags["-explain"]; ok {
				explain = v.(bool)
			}
			if v, ok := ctx.GlobalFlags["-as-written"]; ok {
				asWritten = v.(bool)
			}

			fragSrc, err := generateFragmentSource(ctx, "record", "ssql")
			if err != nil {
				return err
			}
			pipeline, rules, err := optimizePipelineWith(fragSrc, optimiseOptions{AsWritten: asWritten})
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
	Order   string   // the command's DECLARED order behavior (lib.Order*), from Op; "" = undeclared

	// Parsed fields for "from ssh"
	IsSSH       bool
	SSHHost     string
	SSHPath     string
	SSHGPU      bool
	SSHPushDown []string

	// Parsed fields for "from catalog"
	IsCatalog         bool
	CatalogHive       bool // -hive: columns derived from the shards' key=value directories
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
	JoinFile         string           // right-side file path
	JoinArgs         []string         // args after FILE (-using, -on, -type, etc.)
	JoinRightWhere   []string         // where args pushed into right side (rendered as process substitution)
	JoinIsProcessSub bool             // true if right side is already a process substitution (/dev/fd/N)
	JoinProcSubCmds  []*pipelineCmd   // parsed inner pipeline commands (for process substitution optimization)
	UnionProcSubs    [][]*pipelineCmd // union/except/intersect: inner pipelines for each -file /dev/fd/N, in order

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
	LimitN     string
	LimitLast  bool   // limit -last N (tail): not a head — sort+limit→top must not fire
	LimitLastN string // the N of limit -last (LimitN is cleared so the top rule can't see it)
}

// whereCondition represents a single -if / +if field op value condition.
type whereCondition struct {
	Field    string
	Operator string
	Value    string
	Negated  bool // +if form (negate the match)
}

// whereExpr is one -if-expr / +if-expr expression in a where clause.
type whereExpr struct {
	Expr    string
	Negated bool // +if-expr form (negate the result)
}

// whereClause represents one OR-group in a where command.
// Multiple conditions within a clause are ANDed.
type whereClause struct {
	Conditions []whereCondition
	Exprs      []whereExpr
}

// ruleApplication tracks an optimization rule that was applied.
type ruleApplication struct {
	Rule   string
	Before string
	After  string
}

func optimizePipeline(input io.Reader) (string, []ruleApplication, error) {
	return optimizePipelineWith(input, optimiseOptions{})
}

// optimiseOptions selects the rules for the consumer of the rewritten
// pipeline. The default (every rule) is what `generate ssql`, `generate
// go` and `run` want.
type optimiseOptions struct {
	// NoShardPushdown skips the rules that move a stage onto each shard
	// (`from catalog -- …`, `from ssh -- …`, the multi-file and `merge
	// -catalog` forms). `generate sql` translates one pipeline over the
	// files it names and refuses `--` by design (translateCatalogSource),
	// so for it those rewrites would turn a translatable pipeline into a
	// refused one; the pruning rule (catalog-predicate-extraction) and
	// the rest still apply.
	NoShardPushdown bool

	// AsWritten drops the zone a source resolved for itself
	// (CodeFragment.ResolvedZone) from its argv before the rules run, so
	// the rendering is a rewrite of the pipeline as the user typed it —
	// what the explore and playground previews compare against. Every
	// other consumer keeps the pin (a pushed-down shard runs under the
	// same zone as the head, DFC147).
	AsWritten bool
}

// dropResolvedZone takes the `-tz NAME` fromInitFragment appended back
// off a source fragment (argv and Command), recursively through the
// side-input bodies of a func fragment. A `-tz` the user typed has no
// ResolvedZone and stays.
func dropResolvedZone(frag *lib.CodeFragment) {
	for _, body := range frag.FuncBody {
		dropResolvedZone(body)
	}
	name := frag.ResolvedZone
	if name == "" {
		return
	}
	if frag.Op != nil {
		argv := frag.Op.Argv
		if n := len(argv); n >= 2 && argv[n-2] == "-tz" && argv[n-1] == name {
			frag.Op.Argv = argv[:n-2]
		}
	}
	frag.Command = strings.TrimSuffix(frag.Command, " -tz "+ssql.ShellQuote(name))
	frag.ResolvedZone = ""
}

// optimizePipelineWith is optimizePipeline under the given options.
func optimizePipelineWith(input io.Reader, opts optimiseOptions) (string, []ruleApplication, error) {
	cmds, rules, err := optimizePipelineCmdsWith(input, opts)
	if err != nil {
		return "", rules, err
	}
	return renderPipeline(cmds), rules, nil
}

// optimizePipelineCmds reads a fragment stream and returns the commands
// after every rule has been applied (removed stages marked), for a
// consumer that renders them itself: generate ssql as shell text, run as
// a document.
func optimizePipelineCmds(input io.Reader) ([]*pipelineCmd, []ruleApplication, error) {
	return optimizePipelineCmdsWith(input, optimiseOptions{})
}

// optimizePipelineCmdsWith is optimizePipelineCmds under the given options.
func optimizePipelineCmdsWith(input io.Reader, opts optimiseOptions) ([]*pipelineCmd, []ruleApplication, error) {
	// Read fragments from stdin
	var fragments []*lib.CodeFragment
	decoder := json.NewDecoder(input)
	for {
		var frag lib.CodeFragment
		if err := decoder.Decode(&frag); err != nil {
			if err == io.EOF {
				break
			}
			return nil, nil, fmt.Errorf("decoding fragment: %w", err)
		}
		if frag.Type == "error" {
			return nil, nil, &lib.UpstreamFailure{Frag: &frag}
		}
		if opts.AsWritten {
			dropResolvedZone(&frag)
			restoreAsWritten(&frag)
		}
		fragments = append(fragments, &frag)
	}

	if len(fragments) == 0 {
		return nil, nil, fmt.Errorf("no code fragments received")
	}

	// Parse each fragment into a pipelineCmd
	var cmds []*pipelineCmd
	// Inner pipelines from func fragments, queued in emission order —
	// a join consumes one; union/except/intersect one per -file /dev/fd/N.
	var pendingFuncBodies [][]*pipelineCmd
	for _, frag := range fragments {
		cmd := pipelineCmdFor(frag)
		// func fragments are side-input sources (join/union), not pipeline
		// stages — and never CONSUMERS (a union func fragment's Command is
		// the outer union string, which would otherwise eat its own body).
		if frag.Type == "func" {
			cmd.Removed = true
			var innerCmds []*pipelineCmd
			for _, bodyFrag := range frag.FuncBody {
				innerCmds = append(innerCmds, pipelineCmdFor(bodyFrag))
			}
			pendingFuncBodies = append(pendingFuncBodies, innerCmds)
			cmds = append(cmds, cmd)
			continue
		}
		// Attach pending func bodies to their consumer.
		if cmd.IsJoin && cmd.JoinIsProcessSub && len(pendingFuncBodies) > 0 {
			cmd.JoinProcSubCmds = pendingFuncBodies[0]
			pendingFuncBodies = pendingFuncBodies[1:]
		}
		if consumesFileProcSubs(cmd.Kind) {
			for i := 0; i+1 < len(cmd.RawArgs); i++ {
				if (cmd.RawArgs[i] == "-file" || cmd.RawArgs[i] == "-f") &&
					strings.HasPrefix(cmd.RawArgs[i+1], "/dev/fd/") && len(pendingFuncBodies) > 0 {
					cmd.UnionProcSubs = append(cmd.UnionProcSubs, pendingFuncBodies[0])
					pendingFuncBodies = pendingFuncBodies[1:]
				}
			}
		}
		cmds = append(cmds, cmd)
	}

	// Apply optimization rules in order
	var rules []ruleApplication

	// Canonicalize trivial -if-expr predicates into structured -if
	// conditions FIRST, so every later rule (merge, tightening,
	// contradiction, reorder, catalog extraction, join pushdown) can
	// reason about them — opaque expressions defeat all of those.
	rules = append(rules, ruleExprCanonicalize(cmds)...)

	// Phase 1 rules
	rules = append(rules, ruleWhereMerge(cmds)...)
	if !opts.NoShardPushdown {
		rules = append(rules, ruleSSHPredicatePushdown(cmds)...)
		rules = append(rules, ruleSSHAggregationPushdown(cmds)...)
		rules = append(rules, ruleMultiFilePredicatePushdown(cmds)...)
		rules = append(rules, ruleMultiFileAggregationPushdown(cmds)...)
		rules = append(rules, ruleMergeCatalogPredicatePushdown(cmds)...)
		rules = append(rules, ruleMergeCatalogAggregationPushdown(cmds)...)
	}

	// Phase 2 rules (before sort rules, since they may modify where commands)
	rules = append(rules, ruleCatalogPredicateExtraction(cmds)...)
	if !opts.NoShardPushdown {
		rules = append(rules, ruleCatalogPredicatePushdown(cmds)...)
		rules = append(rules, ruleCatalogAggregationPushdown(cmds)...)
	}
	r, empty := rulePredicateSimplification(cmds)
	rules = append(rules, r...)
	if empty {
		return nil, rules, errEmptyResult
	}
	rules = append(rules, rulePredicateReorder(cmds)...)

	// Phase 1 rules (after predicate rules)
	rules = append(rules, ruleSortLimitToTop(cmds)...)
	rules = append(rules, ruleSortElimination(cmds)...)
	rules = append(rules, ruleLimitLastToSource(cmds)...)

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

	return cmds, rules, nil
}

// --- Command parsing ---

// parsePipelineCmd parses a Command string like "ssql from ssh host /data.csv" into a pipelineCmd.
// pipelineCmdFor builds the optimiser's view of a fragment, preferring
// the structured Op descriptor (DFC123 slice 2) over re-tokenizing the
// shell-quoted Command string. Op.Argv is the emitting process's own
// argv — lossless, where parseCommandArgs cannot even represent an
// embedded single quote. The Command-string path remains as the
// fallback for fragments without Op (older ssql across an SSH
// boundary): version skew degrades to the old behavior, never to a
// wrong parse.
func pipelineCmdFor(frag *lib.CodeFragment) *pipelineCmd {
	if frag.Command == "" {
		// A command's continuation fragment (e.g. group-by's second):
		// not a pipeline stage of its own.
		return &pipelineCmd{Removed: true}
	}
	if frag.Op == nil || frag.Op.Kind == "" {
		return parsePipelineCmd(frag.Command)
	}
	cmd := &pipelineCmd{
		Kind:    frag.Op.Kind,
		RawArgs: append([]string(nil), frag.Op.Argv...),
		Order:   frag.Op.Order,
	}
	parseCmdFields(cmd)
	return cmd
}

// parseCmdFields populates the per-command parsed fields from
// Kind+RawArgs — shared by the Op path and the Command-string path.
func parseCmdFields(cmd *pipelineCmd) {
	switch cmd.Kind {
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
}

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
	parseCmdFields(cmd)
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
		case "-if", "+if":
			if i+3 < len(cmd.RawArgs) {
				cmd.MergeCatalogFilters = append(cmd.MergeCatalogFilters, whereCondition{
					Field:    cmd.RawArgs[i+1],
					Operator: cmd.RawArgs[i+2],
					Value:    cmd.RawArgs[i+3],
					Negated:  arg[0] == '+',
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
		case "-if", "-i", "+if", "+i":
			if i+3 < len(cmd.RawArgs) {
				cmd.CatalogFilters = append(cmd.CatalogFilters, whereCondition{
					Field: cmd.RawArgs[i+1], Operator: cmd.RawArgs[i+2], Value: cmd.RawArgs[i+3],
					Negated: arg[0] == '+',
				})
				i += 4
				continue
			}
		case "-gpu":
			cmd.CatalogGPU = true
		case "-hive":
			cmd.CatalogHive = true
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

// parseSortCmd reads a sort stage for ruleSortLimitToTop, which may only
// fire on the one shape `top` can express: a single key, a single clause,
// descending. Anything else (a second key, a clause separator, an argument
// it does not recognise) leaves SortField empty so the rule cannot fire.
// Every flag of sort is boolean, so the only element that consumes the
// next one is autocli's -arg, whose VALUE is a field whatever it looks
// like (DFC134 §5.2): `sort -arg name -arg -desc` is an ASCENDING two-key
// sort, which the old reading turned into `top -field name`.
func parseSortCmd(cmd *pipelineCmd) {
	var fields []string
	simple := true
	for i := 0; i < len(cmd.RawArgs); i++ {
		switch arg := cmd.RawArgs[i]; {
		case arg == cf.ArgFlag && i+1 < len(cmd.RawArgs):
			i++
			fields = append(fields, cmd.RawArgs[i])
		case (arg == "-spill" || arg == "-memory") && i+1 < len(cmd.RawArgs):
			i++ // the out-of-core setting; not a field
		case arg == "-desc" || arg == "-d":
			cmd.SortDesc = true
		case arg == "-asc" || arg == "-a":
			// default
		case strings.HasPrefix(arg, "-") || strings.HasPrefix(arg, "+"):
			simple = false // a clause separator, or a flag this reader does not know
		default:
			fields = append(fields, arg)
		}
	}
	if simple && len(fields) == 1 {
		cmd.SortField = fields[0]
	}
}

func parseLimitCmd(cmd *pipelineCmd) {
	for _, a := range cmd.RawArgs {
		switch {
		case a == "-last":
			cmd.LimitLast = true
		case !strings.HasPrefix(a, "-") && cmd.LimitN == "":
			cmd.LimitN = a
		}
	}
	if cmd.LimitLast {
		// `sort -desc x | limit -last N` is the N SMALLEST in
		// descending order — not `top N -field x`. Hide N from the
		// sort-limit-to-top rule (it keys on LimitN != "").
		cmd.LimitLastN = cmd.LimitN
		cmd.LimitN = ""
	}
}

// --- Where condition parsing ---

// whereHasNegationFlags reports a where stage carrying -not (a negated
// clause) or -invert/-v (the negated whole). The rewrite rules below
// reason about clauses as plain AND-groups ORed together — merging,
// splitting conditions off to catalog pruning or a join side, deleting
// contradictory clauses — and every one of those is unsound under a
// negation, so such a stage is left exactly as written (pushdown rules
// that move the WHOLE stage verbatim are unaffected).
// whereHasNegationFlags reports a where stage the structured view
// (parseWhereArgs / buildWhereArgs: -if, -if-expr and + only) cannot
// represent: -not / -invert, and since DFC134/DFC135 -param, -param-field
// and -if-field, whose meaning a rewrite through that view would drop (the
// canonicaliser turned `-if-expr 'k >= "b"' -param-field k string 1st`
// into `-if k ge b`, found 2026-09-22). Every rule that rewrites where
// through the view skips such a stage; a rule that only READS it (predicate
// pushdown deciding which side a condition belongs to) is conservative too.
func whereHasNegationFlags(args []string) bool {
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch a {
		case "-if", "-i", "+if", "+i":
			i += 3
		case "-if-expr", "-x", "+if-expr", "+x":
			i++
		case "+":
		default:
			return true
		}
	}
	return false
}

// parseWhereArgs parses where RawArgs into structured clauses.
// Format: [-if field op value]... [-if-expr expr]... [+ [-if ...]...]
func parseWhereArgs(args []string) []whereClause {
	var clauses []whereClause
	var current whereClause

	i := 0
	for i < len(args) {
		switch args[i] {
		case "-if", "-i", "+if", "+i":
			if i+3 < len(args) {
				current.Conditions = append(current.Conditions, whereCondition{
					Field: args[i+1], Operator: args[i+2], Value: args[i+3],
					Negated: args[i][0] == '+',
				})
				i += 4
				continue
			}
		case "-if-expr", "-x", "+if-expr", "+x":
			if i+1 < len(args) {
				current.Exprs = append(current.Exprs, whereExpr{
					Expr: args[i+1], Negated: args[i][0] == '+',
				})
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
			flag := "-if"
			if cond.Negated {
				flag = "+if"
			}
			args = append(args, flag, cond.Field, cond.Operator, cond.Value)
		}
		for _, expr := range clause.Exprs {
			flag := "-if-expr"
			if expr.Negated {
				flag = "+if-expr"
			}
			args = append(args, flag, expr.Expr)
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
		if whereHasNegationFlags(cmds[i].RawArgs) || whereHasNegationFlags(cmds[j].RawArgs) {
			continue
		}
		// Concatenating argument lists is AND only when BOTH stages are a
		// single clause: `where A + B | where C` is (A OR B) AND C, but the
		// concatenation `-if A + -if B -if C` reads A OR (B AND C) — five
		// rows instead of four on the codelab data (found 2026-09-10 while
		// adding -not/-invert; every generated-Go lane ran the wrong filter).
		if len(parseWhereArgs(cmds[i].RawArgs)) != 1 || len(parseWhereArgs(cmds[j].RawArgs)) != 1 {
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
		if whereReferences(cmds[j].RawArgs, catalogMetaCols(cmds[i].MergeCatalogFile, false, cmds[i].MergeCatalogShardField)) {
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

// ruleMergeCatalogAggregationPushdown splits a group-by after "merge -catalog" (with pushdown) into a per-shard partial aggregate and the parent's re-aggregation (splitGroupByPushdown).
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
		shard, parent, ok := splitGroupByPushdown(cmds[j].RawArgs, catalogMetaCols(cmds[i].MergeCatalogFile, false, cmds[i].MergeCatalogShardField))
		if !ok {
			continue
		}
		before := renderCmd(cmds[i]) + " | " + renderCmd(cmds[j])
		cmds[i].MergeCatalogPushDown = append(cmds[i].MergeCatalogPushDown, "+")
		cmds[i].MergeCatalogPushDown = append(cmds[i].MergeCatalogPushDown, shard...)
		cmds[j].RawArgs = parent
		after := renderCmd(cmds[i]) + " | " + renderCmd(cmds[j])
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

// orderOf returns a stage's order behavior (DFC123 §7 / slice 4):
// the command's OWN declaration when the fragment carries one
// (lib.DeclareOrder in its Register function, stamped onto Op.Order),
// else the legacy by-kind table for fragments from an older ssql, else
// OrderConsumes — never assume a stage ignores order.
//
// Faithfulness note: ssql sort is documented UNSTABLE
// (slices.SortFunc), so tie order after the later sort is
// unspecified with or without the earlier one — dropping it is exact
// under ssql's own contract. If sort ever becomes stable, the
// faithful rewrite changes (merge the earlier keys); DFC123 §7 pins
// that as a deliberate decision.
func orderOf(cmd *pipelineCmd) string {
	if cmd.Order != "" {
		return cmd.Order
	}
	// Op-less fragment (older ssql across an SSH boundary): the command's
	// own flag-conditional declaration first, then the legacy table.
	if o := lib.OrderForArgv(cmd.Kind, cmd.RawArgs); o != "" {
		return o
	}
	if o, ok := legacyOrderByKind[cmd.Kind]; ok {
		return o
	}
	return lib.OrderConsumes
}

// legacyOrderByKind is the fallback for Op-less fragments (older ssql
// across an SSH boundary). Commands built from this source declare
// their behavior themselves — this table must never be the first place
// a new command's order behavior is written down.
var legacyOrderByKind = map[string]string{
	"where":    lib.OrderTransparent,
	"include":  lib.OrderTransparent,
	"exclude":  lib.OrderTransparent,
	"rename":   lib.OrderTransparent,
	"cast":     lib.OrderTransparent,
	"update":   lib.OrderTransparent,
	"sort":     lib.OrderReset,
	"top":      lib.OrderReset,
	"group-by": lib.OrderReset,
	"resample": lib.OrderReset,
}

// ruleSortElimination removes dead sorts: a sort whose order reaches
// an order-reset stage across only order-transparent stages (DFC123
// §7 dead-order elimination — grown from the original adjacent
// sort-before-group-by rule). `sort x | where p | sort y` drops
// sort x; `sort x | limit 5 | sort y` keeps it (limit consumes the
// order — it selects WHICH five rows).
func ruleSortElimination(cmds []*pipelineCmd) []ruleApplication {
	var rules []ruleApplication
	for i := 0; i < len(cmds); i++ {
		if cmds[i].Removed || cmds[i].Kind != "sort" {
			continue
		}
		for j := nextNonRemoved(cmds, i); j != -1; j = nextNonRemoved(cmds, j) {
			switch orderOf(cmds[j]) {
			case lib.OrderTransparent:
				continue
			case lib.OrderReset:
				before := renderCmd(cmds[i])
				cmds[i].Removed = true
				rules = append(rules, ruleApplication{
					Rule:   "sort-elimination",
					Before: before,
					After:  "(removed — order reset by " + renderCmd(cmds[j]) + ")",
				})
			}
			break // consumer (or the reset stage itself): stop scanning
		}
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

		// Read catalog CSV header to determine metadata columns —
		// distinguishing EXACT columns (the value holds for every row in
		// the shard, so extraction can REPLACE the row filter) from RANGE
		// columns (_from/_to: pruning is only conservative — a shard
		// straddling the boundary still contains non-matching rows, so
		// the row filter must be KEPT). Before this distinction, range
		// extraction deleted the row filter and straddling shards LEAKED
		// non-matching rows (caught on the LXD rig, 2026-08-11).
		catalogCols, rangeCols, err := readCatalogMetadataColumns(cmds[i].CatalogFile, cmds[i].CatalogHive)
		if err != nil {
			continue // can't read catalog, skip
		}

		if whereHasNegationFlags(cmds[j].RawArgs) {
			continue
		}
		clauses := parseWhereArgs(cmds[j].RawArgs)
		// Only extract from single-clause where (multi-clause OR is harder to split)
		if len(clauses) != 1 {
			continue
		}
		clause := clauses[0]

		// Split conditions: catalogConds become pruning filters (negated
		// conditions included — the exec matcher handles the +if form
		// exactly for exact columns and conservatively for ranges);
		// dataConds stay in the where. Range-backed conditions appear in
		// BOTH (prune shards AND filter rows).
		var catalogConds, dataConds []whereCondition
		for _, cond := range clause.Conditions {
			if catalogCols[cond.Field] {
				catalogConds = append(catalogConds, cond)
				if rangeCols[cond.Field] {
					dataConds = append(dataConds, cond)
				}
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
		if whereReferences(cmds[j].RawArgs, catalogMetaCols(cmds[i].CatalogFile, cmds[i].CatalogHive, cmds[i].CatalogShardField)) {
			continue // the shard has no such column; the parent's records do
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

// ruleCatalogAggregationPushdown splits a group-by after "from catalog"
// (with push-down) into a per-shard partial aggregate and the parent's
// re-aggregation (splitGroupByPushdown).
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
		shard, parent, ok := splitGroupByPushdown(cmds[j].RawArgs, catalogMetaCols(cmds[i].CatalogFile, cmds[i].CatalogHive, cmds[i].CatalogShardField))
		if !ok {
			continue
		}
		before := renderCmd(cmds[i]) + " | " + renderCmd(cmds[j])
		cmds[i].CatalogPushDown = append(cmds[i].CatalogPushDown, "+")
		cmds[i].CatalogPushDown = append(cmds[i].CatalogPushDown, shard...)
		cmds[j].RawArgs = parent
		after := renderCmd(cmds[i]) + " | " + renderCmd(cmds[j])
		rules = append(rules, ruleApplication{
			Rule:   "catalog-aggregation-pushdown",
			Before: before,
			After:  after,
		})
	}
	return rules
}

// catalogMetaCols is the set of columns a catalog's records carry that
// its shards do not: the catalog's own columns (range pairs excluded —
// their base name is a column of the file), the hive keys and the shard
// field. Empty when the catalog cannot be read; the rules that consult it
// then assume every column is the shard's, as they did before 2026-10-14.
func catalogMetaCols(file string, hive bool, shardField string) map[string]bool {
	meta := map[string]bool{}
	cols, ranges, err := readCatalogMetadataColumns(file, hive)
	if err == nil {
		for c := range cols {
			if !ranges[c] {
				meta[c] = true
			}
		}
	}
	if shardField != "" {
		meta[shardField] = true
	}
	return meta
}

// whereReferences reports whether a where stage may read one of the
// columns: a structured condition names it, or an expression (whose
// fields the parser does not enumerate) contains it as a word.
func whereReferences(args []string, cols map[string]bool) bool {
	if len(cols) == 0 {
		return false
	}
	fields, opaque := extractWhereFields(args)
	for _, f := range fields {
		if cols[f] {
			return true
		}
	}
	if !opaque {
		return false
	}
	for _, a := range args {
		for c := range cols {
			if regexp.MustCompile(`\b` + regexp.QuoteMeta(c) + `\b`).MatchString(a) {
				return true
			}
		}
	}
	return false
}

// splitGroupByPushdown rewrites a group-by for per-shard pushdown. Until
// 2026-10-14 the rule shipped the group-by to the shards and DELETED it
// from the parent, so a catalog with several shards emitted one partial
// row per shard and key with the shard's metadata appended — wrong
// whenever a key spans shards — and a key that was a metadata column (a
// hive key) grouped on a column the shard does not have. Now the shard
// aggregates over the keys it holds (none means one row per shard) and
// the parent re-aggregates over every key: -count/-sum partials are
// summed, -min/-max take the extreme (aggDef.merge). Keys that are
// metadata columns are grouped in the parent only, where the records
// carry them. An aggregate with no merge form (-avg, -count-distinct,
// -collect, …), an expression, rollup/cube or any other flag returns
// ok=false and the group-by stays where it is.
func splitGroupByPushdown(args []string, metaCols map[string]bool) (shard, parent []string, ok bool) {
	var keys []string
	i := 0
	for i < len(args) && !strings.HasPrefix(args[i], "-") && !strings.HasPrefix(args[i], "+") {
		keys = append(keys, args[i])
		i++
	}
	shard = append(shard, "group-by")
	for _, k := range keys {
		if !metaCols[k] {
			shard = append(shard, k)
		}
	}
	parent = append(parent, keys...) // RawArgs: the stage name is not among them
	for i < len(args) {
		d, found := aggDefByFlag(args[i])
		if !found || d.merge == "" || i+d.arity() >= len(args) {
			return nil, nil, false
		}
		n := d.arity()
		result := args[i+n]
		shard = append(shard, args[i:i+n+1]...)
		parent = append(parent, d.merge, result, result)
		i += n + 1
	}
	return shard, parent, true
}

// ruleExprCanonicalize rewrites trivial -if-expr predicates (conjunctions
// of `field OP literal` — see exprToFlagConds for the conservative
// recognition rules) into structured -if conditions, in place. Negation is
// preserved: a canonicalized +if-expr becomes +if conditions... with one
// caveat: negating a CONJUNCTION distributes only when it has a single
// term (¬(a ∧ b) ≠ ¬a ∧ ¬b), so negated expressions canonicalize only
// when they reduce to exactly one condition.
func ruleExprCanonicalize(cmds []*pipelineCmd) []ruleApplication {
	var rules []ruleApplication
	for i := 0; i < len(cmds); i++ {
		if cmds[i].Removed || cmds[i].Kind != "where" || whereHasNegationFlags(cmds[i].RawArgs) {
			continue
		}
		clauses := parseWhereArgs(cmds[i].RawArgs)
		changed := false
		for ci, clause := range clauses {
			var kept []whereExpr
			for _, ex := range clause.Exprs {
				conds, ok := exprToFlagConds(ex.Expr)
				if !ok || (ex.Negated && len(conds) != 1) {
					kept = append(kept, ex)
					continue
				}
				for _, c := range conds {
					c.Negated = ex.Negated
					clauses[ci].Conditions = append(clauses[ci].Conditions, c)
				}
				changed = true
			}
			clauses[ci].Exprs = kept
		}
		if !changed {
			continue
		}
		before := renderCmd(cmds[i])
		cmds[i].RawArgs = buildWhereArgs(clauses)
		rules = append(rules, ruleApplication{
			Rule:   "expr-canonicalization",
			Before: before,
			After:  renderCmd(cmds[i]),
		})
	}
	return rules
}

// rulePredicateSimplification simplifies redundant predicates within where commands.
// Returns true for the second value if a contradiction was detected (empty result).
func rulePredicateSimplification(cmds []*pipelineCmd) ([]ruleApplication, bool) {
	var rules []ruleApplication
	for i := 0; i < len(cmds); i++ {
		if cmds[i].Removed || cmds[i].Kind != "where" || whereHasNegationFlags(cmds[i].RawArgs) {
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

	// Check for eq/eq contradictions and eq/ne contradictions.
	// Negated (+if) conditions are opaque to this reasoning — they are
	// never removed and never contribute to contradiction detection.
	eqValues := make(map[string]string) // field → value for eq conditions
	for _, c := range conds {
		if c.Operator == "eq" && !c.Negated {
			if prev, exists := eqValues[c.Field]; exists {
				if prev != c.Value {
					return whereClause{}, true // eq X AND eq Y where X != Y
				}
			}
			eqValues[c.Field] = c.Value
		}
	}
	for _, c := range conds {
		if c.Operator == "ne" && !c.Negated {
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
		if c.Negated {
			continue // +if bounds are inverted — exclude from range tightening
		}
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
		if cmds[i].Removed || cmds[i].Kind != "where" || whereHasNegationFlags(cmds[i].RawArgs) {
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
func readCatalogMetadataColumns(filename string, hive bool) (cols, rangeCols map[string]bool, err error) {
	var names []string
	var ranges map[string]bool
	if hive {
		// The derived columns exist only once the paths are known: load
		// the catalog as exec would (local globs, or ssh echo). Without
		// this a where on a derived column stayed a row filter — right,
		// but every file read.
		entries, err := ssql.LoadCatalog(filename, true, nil)
		if err != nil {
			return nil, nil, err
		}
		names, ranges, err = catalogMetadataColumns(filename, true, entries)
		if err != nil {
			return nil, nil, err
		}
	} else if names, ranges, err = ssql.CatalogColumns(filename); err != nil {
		return nil, nil, err
	}
	cols = make(map[string]bool, len(names))
	for _, n := range names {
		cols[n] = true
	}
	return cols, ranges, nil
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
		// A predicate on the RIGHT side is only movable into the source
		// for an inner equi-join: an ASOF join's match depends on which
		// right rows exist (filtering them changes the nearest), and a
		// left/right/full join keeps unmatched rows the filter would drop.
		// A left-only predicate stays movable for inner and left joins.
		rightMovable, leftMovable := true, true
		for k, a := range cmds[i].JoinArgs {
			switch a {
			case "-asof", "-asof-on":
				rightMovable = false
			case "-type", "-t":
				if k+1 < len(cmds[i].JoinArgs) {
					switch cmds[i].JoinArgs[k+1] {
					case "left":
						rightMovable = false
					case "right", "full":
						rightMovable, leftMovable = false, false
					}
				}
			}
		}
		if !leftMovable {
			continue
		}

		// Only handle simple -if predicates (not -if-expr which can reference any field)
		if whereHasNegationFlags(cmds[j].RawArgs) {
			continue
		}
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

		if !rightMovable {
			both = append(both, rightOnly...)
			rightOnly = nil
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
	// A name a stage between the source and the reference CREATES (an
	// update target, a window result, a rename's new name) is satisfied by
	// that stage, not the source: before 2026-10-20 `from parquet F |
	// update -set x 1 | include x` was pruned to `-columns x` and failed on
	// the column the file never had. A conditional update's target is
	// different: on the rows its condition misses the source's own value
	// survives, if the source has one, and the walk cannot know, so a later
	// reference to it leaves the stage opaque.
	made := map[string]bool{}
	maybe := map[string]bool{}
	for j := startIdx + 1; j < len(cmds); j++ {
		if cmds[j].Removed {
			continue
		}
		fields, allNeeded := extractReferencedFields(cmds[j])
		if allNeeded {
			return nil, true
		}
		for _, f := range fields {
			if maybe[f] {
				return nil, true
			}
			if !made[f] {
				needed[f] = true
			}
		}
		created, conditional := stageCreatedFields(cmds[j])
		for _, f := range created {
			if conditional {
				maybe[f] = true
			} else {
				made[f] = true
			}
		}
		// A schema-redefining command (group-by) replaces the stream
		// schema with its own output — the group keys plus the aggregation
		// output names. Fields referenced further downstream are satisfied
		// by that output, NOT by the source, so the source only needs the
		// fields up to and including this command's own input references
		// (already added above). Without this stop, downstream references
		// to aggregation outputs (e.g. a -count result named "count") leak
		// back as phantom source columns the file does not contain.
		if redefinesSourceSchema(cmds[j]) || cmds[j].Kind == "include" {
			return needed, false
		}
	}
	// No stage narrowed the record, so every source column reaches the
	// output: `from parquet F | sort id | to csv` was pruned to
	// `-columns id` and printed one column (found 2026-10-11 by the
	// parquet_hive_dir_single equivalence case; exec was right, every
	// compiled lane and the optimiser wrong).
	return nil, true
}

// stageCreatedFields names the fields a stage adds to the stream (the
// update and window schema rules of schema_ops.go run over an empty input
// schema, so only what the stage itself produces comes out; rename's new
// names from its -as pairs), and whether an update makes them under a
// condition, so that the source's own value may still reach the output.
func stageCreatedFields(cmd *pipelineCmd) (created []string, conditional bool) {
	switch cmd.Kind {
	case "update", "window":
		created, _ = lookupSchemaOp(cmd.Kind)(nil, []string{}, cmd.RawArgs)
		if cmd.Kind == "update" {
			for _, a := range cmd.RawArgs {
				if strings.HasPrefix(normFlag(a), "-if") {
					conditional = true
				}
			}
		}
	case "rename":
		for i := 0; i+2 < len(cmd.RawArgs); i++ {
			if a := cmd.RawArgs[i]; a == "-as" || a == "-a" {
				created = append(created, cmd.RawArgs[i+2])
			}
		}
	}
	return created, conditional
}

// redefinesSourceSchema reports whether a command fully replaces the stream
// schema with one of its own making, so that downstream field references no
// longer map back to source columns. group-by is the canonical case (output
// = group keys + aggregation outputs). distinct/exclude already short-circuit
// collectDownstreamFields via extractReferencedFields returning allNeeded.
func redefinesSourceSchema(cmd *pipelineCmd) bool {
	return cmd.Kind == "group-by"
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
	if whereHasNegationFlags(args) {
		return nil, true // fields still read; the negated shape is opaque to pruning
	}
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
		// Built-in aggregates: step over the flag's args (aggDefs, DFC129
		// §6); a FIELD RESULT flag also names an input field.
		if d, ok := aggDefByFlag(args[i]); ok {
			n := d.arity()
			if i+n < len(args) {
				if d.hasField {
					fields = append(fields, args[i+1])
				}
				if d.extraIsField {
					fields = append(fields, args[i+2]) // -arg-*'s BY is read too (projection pruning must keep it)
				}
				i += n + 1
			} else {
				i++
			}
			continue
		}
		switch args[i] {
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

// extractUpdateFields lists the fields an update stage reads, for column
// pruning: every flag is stepped over by its arity, `+` spellings included,
// and the argument positions that name an input field are collected. An
// expression flag (the fields are inside the text) or a macro call (its
// arity is in the macros file, and it is an expression; seen only under
// -as-written) makes the stage opaque: every field is needed. Before
// 2026-10-20 the walk knew -if, -set and -set-bucket only, so `from parquet
// F | update -set-field b a | include b` was pruned to `-columns b` and
// failed on the missing a (the DFC135 flags and the + forms all missed).
func extractUpdateFields(args []string) ([]string, bool) {
	var fields []string
	for i := 0; i < len(args); {
		var n int       // tokens the flag takes
		var reads []int // which of them (1-based) name a field the stage reads
		switch normFlag(args[i]) {
		case "-if", "-i":
			n, reads = 3, []int{1}
		case "-if-field":
			n, reads = 3, []int{1, 3}
		case "-set-field":
			n, reads = 2, []int{2}
		case "-set-bucket", "-b":
			n, reads = 3, []int{2} // reads the source timestamp
		case "-param-field":
			n, reads = 3, []int{3}
		case "-param":
			n = 3
		case "-set", "-s":
			n = 2 // -set field value: doesn't read existing field
		case "-if-expr", "-x", "-set-expr", "-e", "-set-macro", "-if-macro":
			return nil, true
		}
		if i+n >= len(args) {
			break // a flag short of its args (mid-typing): nothing more to read
		}
		for _, k := range reads {
			fields = append(fields, args[i+k])
		}
		i += 1 + n
	}
	return fields, false
}

func extractWindowFields(args []string) ([]string, bool) {
	var fields []string
	i := 0
	for i < len(args) {
		if d, ok := isWindowRegistryFlag(args[i]); ok {
			n := d.arity()
			if i+n < len(args) {
				fields = append(fields, args[i+1])
				if d.extraIsField {
					fields = append(fields, args[i+2])
				}
				i += n + 1
				continue
			}
		}
		switch args[i] {
		case "-partition", "-order":
			if i+1 < len(args) {
				fields = append(fields, args[i+1])
				i += 2
				continue
			}
		case "-lag", "-lead", "-nth-value":
			if i+2 < len(args) {
				fields = append(fields, args[i+1])
				i += 3 // -lag field N
				continue
			}
		case "-lag-default", "-lead-default":
			if i+3 < len(args) {
				fields = append(fields, args[i+1])
				i += 4 // field N default
				continue
			}
		case "-first", "-last", "-sum", "-avg", "-min", "-max", "-count-field":
			if i+1 < len(args) {
				fields = append(fields, args[i+1])
				i += 2
				continue
			}
		case "-range-preceding", "-range-following":
			if i+1 < len(args) {
				i += 2 // a bound, not a field
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
// stageWord is one element of a rendered stage: an argument string, or
// an inner pipeline standing where a file argument goes — the shell's
// <(…), a document's nested list.
type stageWord struct {
	Text  string
	Inner []*pipelineCmd
}

// cmdWords is a command as the arguments it runs with: "ssql", the
// command, its arguments, an inner pipeline as one word. renderCmd
// writes them as shell text; docFromCmds (run) as a pipeline document,
// so the two forms cannot drift.
func cmdWords(cmd *pipelineCmd) []stageWord {
	parts := []string{"ssql", cmd.Kind}
	text := func(ss []string) []stageWord {
		out := make([]stageWord, 0, len(ss))
		for _, s := range ss {
			out = append(out, stageWord{Text: s})
		}
		return out
	}

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
		return text(parts)
	}

	if cmd.IsJoin {
		if len(cmd.JoinRightWhere) > 0 {
			// The right side filtered: an inner pipeline over the plain
			// file (the shell's <(ssql from FILE | ssql where …)).
			inner := []*pipelineCmd{
				{Kind: "from", RawArgs: []string{cmd.JoinFile}},
				{Kind: "where", RawArgs: cmd.JoinRightWhere},
			}
			return append(append(text(parts), stageWord{Inner: inner}), text(cmd.JoinArgs)...)
		}
		if cmd.JoinIsProcessSub && len(cmd.JoinProcSubCmds) > 0 {
			return append(append(text(parts), stageWord{Inner: cmd.JoinProcSubCmds}), text(cmd.JoinArgs)...)
		}
		parts = append(parts, cmd.JoinFile)
		parts = append(parts, cmd.JoinArgs...)
		return text(parts)
	}

	if consumesFileProcSubs(cmd.Kind) && len(cmd.UnionProcSubs) > 0 {
		// Reconstruct -file <(inner pipeline) — the fragment's command
		// string holds the DEAD /dev/fd/N the generation run happened
		// to get; rendering it verbatim made replays read a stale fd.
		out := text(parts)
		idx := 0
		for i := 0; i < len(cmd.RawArgs); {
			a := cmd.RawArgs[i]
			if (a == "-file" || a == "-f") && i+1 < len(cmd.RawArgs) &&
				strings.HasPrefix(cmd.RawArgs[i+1], "/dev/fd/") && idx < len(cmd.UnionProcSubs) {
				out = append(out, stageWord{Text: "-file"}, stageWord{Inner: cmd.UnionProcSubs[idx]})
				idx++
				i += 2
				continue
			}
			out = append(out, stageWord{Text: a})
			i++
		}
		return out
	}

	if cmd.IsMergeCatalog {
		parts = append(parts, "-catalog", cmd.MergeCatalogFile)
		for _, f := range cmd.MergeCatalogFilters {
			ifFlag := "-if"
			if f.Negated {
				ifFlag = "+if"
			}
			parts = append(parts, ifFlag, f.Field, f.Operator, f.Value)
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
		return text(parts)
	}

	if cmd.IsMultiFile {
		parts = append(parts, cmd.MultiFileFormat)
		parts = append(parts, cmd.MultiFileFiles...)
		parts = append(parts, cmd.MultiFileFlags...)
		if len(cmd.MultiFilePushDown) > 0 {
			parts = append(parts, "--")
			parts = append(parts, cmd.MultiFilePushDown...)
		}
		return text(parts)
	}

	if cmd.IsCatalog {
		parts = append(parts, "catalog")
		if cmd.CatalogFile != "" {
			parts = append(parts, cmd.CatalogFile)
		}
		for _, f := range cmd.CatalogFilters {
			ifFlag := "-if"
			if f.Negated {
				ifFlag = "+if"
			}
			parts = append(parts, ifFlag, f.Field, f.Operator, f.Value)
		}
		if cmd.CatalogGPU {
			parts = append(parts, "-gpu")
		}
		if cmd.CatalogHive {
			parts = append(parts, "-hive")
		}
		if cmd.CatalogShardField != "" {
			parts = append(parts, "-shard-field", cmd.CatalogShardField)
		}
		if len(cmd.CatalogPushDown) > 0 {
			parts = append(parts, "--")
			parts = append(parts, cmd.CatalogPushDown...)
		}
		return text(parts)
	}

	parts = append(parts, cmd.RawArgs...)
	return text(parts)
}

// renderCmd writes a command as shell text, every argument quoted, an
// inner pipeline as a process substitution.
func renderCmd(cmd *pipelineCmd) string {
	var sb strings.Builder
	for i, w := range cmdWords(cmd) {
		if i > 0 {
			sb.WriteByte(' ')
		}
		if w.Inner != nil {
			sb.WriteString("<(" + renderProcSubPipeline(w.Inner) + ")")
			continue
		}
		sb.WriteString(shellJoin([]string{w.Text}))
	}
	return sb.String()
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


// ruleLimitLastToSource: `from csv|tsv|jsonl FILE | limit -last N` →
// `from … FILE -last N` — the seek-based tail at the source reads O(N)
// lines instead of the whole file (the tail twin of sort-limit-to-top).
// Only when the source is one local file with no -sample/-last already
// and no pushdown, and the limit is IMMEDIATELY next (anything between
// them would see different rows).
func ruleLimitLastToSource(cmds []*pipelineCmd) []ruleApplication {
	var rules []ruleApplication
	for i := 0; i < len(cmds); i++ {
		c := cmds[i]
		// Single-file sources are parsed by parseFromMultiFileCmd but
		// IsMultiFile is only set for >1 file — and they render from
		// RawArgs — so key on the parsed format/files and append to BOTH.
		if c.Removed || c.Kind != "from" || len(c.MultiFileFiles) != 1 || len(c.MultiFilePushDown) > 0 {
			continue
		}
		switch c.MultiFileFormat {
		case "csv", "tsv", "jsonl":
		default:
			continue
		}
		if slices.Contains(c.MultiFileFlags, "-sample") || slices.Contains(c.MultiFileFlags, "-last") {
			continue
		}
		j := nextNonRemoved(cmds, i)
		if j == -1 || cmds[j].Kind != "limit" || !cmds[j].LimitLast || cmds[j].LimitLastN == "" {
			continue
		}
		before := renderCmd(c) + " | " + renderCmd(cmds[j])
		c.MultiFileFlags = append(c.MultiFileFlags, "-last", cmds[j].LimitLastN)
		c.RawArgs = append(c.RawArgs, "-last", cmds[j].LimitLastN)
		cmds[j].Removed = true
		rules = append(rules, ruleApplication{Rule: "limit-last-to-source", Before: before, After: renderCmd(c)})
	}
	return rules
}

// consumesFileProcSubs reports the commands whose side inputs are
// `-file <(pipeline)` arguments, one func fragment each, in order.
func consumesFileProcSubs(kind string) bool {
	return kind == "union" || kind == "except" || kind == "intersect"
}
