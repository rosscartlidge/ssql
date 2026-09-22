package commands

import (
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
			Help("Validate the document and exit; nothing runs").
			Done().

		Flag("-print").
			Bool().
			Global().
			Help("Print the pipeline in shell form and exit; nothing runs").
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
			if ctx.GetBool("-check", false) {
				return nil
			}
			self, err := os.Executable()
			if err != nil {
				return fmt.Errorf("run: %w", err)
			}
			// Ctrl-C reaches the children through the context; they are
			// in this process group anyway, this makes the exit orderly.
			runCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			var stdin io.Reader
			if !fromStdin {
				stdin = ctx.Stdin()
			}
			if err := runPipelineDoc(runCtx, ctx.Command, self, stages, stdin, ctx.Stdout(), ctx.Stderr()); err != nil {
				return fmt.Errorf("run: %w", err)
			}
			return nil
		}).
		Done()
	return cmd
}
