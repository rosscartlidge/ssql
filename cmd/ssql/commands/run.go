package commands

import (
	"errors"
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	cf "github.com/rosscartlidge/autocli/v4"
)

// RegisterRun registers `ssql run`: run a pipeline document with no shell
// (DFC134 §5.1). Every stage is validated against the real command grammar
// before any starts; stages are exec'd directly and wired with pipes, so
// the only grammar a value ever meets is the command's own argument list.
func RegisterRun(cmd *cf.CommandBuilder) *cf.CommandBuilder {
	cmd.Subcommand("run").
		Description("Run a pipeline document (JSON: a list of stages, each a list of arguments) with no shell").
		ClauseDescription("The document is the pipeline as argv: [[\"from\",\"csv\",\"-arg\",\"orders.csv\"],[\"where\",\"-if\",\"customer\",\"eq\",\"anything\"],[\"to\",\"csv\"]]. A nested list in an argument's place is a nested pipeline whose output the stage reads as a file (the shell's <(…)). Every stage is checked before any runs. Write positionals as -arg VALUE so no value can be read as a flag.").
		Example("ssql run pipeline.json", "Run the document; the first stage reads this process's stdin, the last writes its stdout").
		Example("ssql run -check pipeline.json", "Validate every stage against the command grammar and run nothing").
		Example("ssql run -print pipeline.json", "Print the pipeline as a shell command line").
		Example("ssql run -explain pipeline.json", "Run it, naming the optimiser's rewrites and the pipeline that ran").
		Example("echo '[[\"from\",\"csv\",\"-arg\",\"a.csv\"],[\"to\",\"table\"]]' | ssql run -stdin", "Read the document itself from stdin (the first stage then has no stdin)").

		Flag("FILE").
			String().
			Global().
			Completer(&cf.FileCompleter{Pattern: "*.json"}).
			Help("The pipeline document").
			Done().

		Flag("-stdin").
			Bool().
			Global().
			Help("Read the document from stdin instead of FILE; the first stage then gets no stdin").
			Done().

		Flag("-check").
			Bool().
			Global().
			Help("Validate the document and exit; nothing runs. Checks the grammar (commands, flags, arity) and then the FIELD references: each stage is run on its input's schema alone, so a field a stage reads that no earlier stage produced is reported with the stage number and the fields available there").
			Done().

		Flag("-print").
			Bool().
			Global().
			Help("Print the document in shell form, as written, and exit; nothing runs (-explain shows what the optimiser makes of it)").
			Done().

		Flag("-optimise", "-O").
			Bool().
			Global().
			Default(true).
			Help("Apply the pipeline optimiser (the rewrites 'generate ssql' prints: a where on a catalog or hive column into -if on the from stage, sort+limit as top, dead sorts dropped, …) before running — the same answer, less work. ON by default; +O / +optimise runs the document exactly as written. Skipped when SSQL_MODE is set, since the stages are then describing themselves for a generate").
			Done().

		Flag("-explain", "-e").
			Bool().
			Global().
			Help("Print the optimiser's applied rules and the pipeline that runs to stderr, or why the document ran as written").
			Done().

		Handler(func(ctx *cf.Context) error {
			file := ctx.GetString("FILE", "")
			fromStdin := ctx.GetBool("-stdin", false)
			var data []byte
			var err error
			switch {
			case fromStdin && file != "":
				return fmt.Errorf("run: give FILE or -stdin, not both")
			case fromStdin:
				data, err = io.ReadAll(io.LimitReader(ctx.Stdin(), 16<<20))
			case file == "":
				return fmt.Errorf("run: a pipeline document is required (FILE, or -stdin)")
			default:
				data, err = os.ReadFile(file)
			}
			if err != nil {
				return fmt.Errorf("run: %w", err)
			}
			stages, err := parsePipelineDoc(data)
			if err != nil {
				return err
			}
			if err := checkPipelineDoc(ctx.Command, stages, ""); err != nil {
				return fmt.Errorf("run: %w", err)
			}
			if ctx.GetBool("-print", false) {
				fmt.Fprintln(ctx.Stdout(), renderPipelineDoc(stages))
				return nil
			}
			self, err := os.Executable()
			if err != nil {
				return fmt.Errorf("run: %w", err)
			}
			if ctx.GetBool("-check", false) {
				if err := checkPipelineFields(context.Background(), ctx.Command, self, stages, ""); err != nil {
					return fmt.Errorf("run -check: %w", err)
				}
				return nil
			}
			// Ctrl-C reaches the children through the context; they are
			// in this process group anyway, this makes the exit orderly.
			runCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			explain := ctx.GetBool("-explain", false)
			switch {
			case !ctx.GetBool("-optimise", true):
				// as written
			case modeEnv() != "":
				if explain {
					fmt.Fprintf(ctx.Stderr(), "run: not optimised: SSQL_MODE=%s is set (the stages are describing themselves)\n", modeEnv())
				}
			default:
				var rules []ruleApplication
				var reason string
				stages, rules, reason = optimisePipelineDoc(runCtx, ctx.Command, self, stages)
				if explain {
					switch {
					case reason != "":
						fmt.Fprintf(ctx.Stderr(), "run: not optimised: %s\n", reason)
					case len(rules) == 0:
						fmt.Fprintln(ctx.Stderr(), "(no optimisation rules applied)")
					default:
						for _, r := range rules {
							fmt.Fprintf(ctx.Stderr(), "[%s] %s → %s\n", r.Rule, r.Before, r.After)
						}
						fmt.Fprintf(ctx.Stderr(), "run: %s\n", renderPipelineDoc(stages))
					}
				}
			}
			var stdin io.Reader
			if !fromStdin {
				stdin = ctx.Stdin()
			}
			if err := runPipelineDoc(runCtx, ctx.Command, self, stages, stdin, ctx.Stdout(), ctx.Stderr()); err != nil {
				if errors.Is(err, errStdoutClosed) {
					exitBrokenPipe()
				}
				return fmt.Errorf("run: %w", err)
			}
			return nil
		}).
		Done()
	return cmd
}
