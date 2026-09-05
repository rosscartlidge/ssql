# `generate go -optimise` and `generate go -run` Proposal

Reference: DFC087
Created: 2026-04-28
Last modified: 2026-09-05

[Back to Index](./README.md)

**Status:** `-run` shipped 2026-04-28. `-build` shipped 2026-04-28. `-optimise` shipped 2026-04-29. **2026-09-05: `-optimise` is the DEFAULT; `+O` / `+optimise` turns it off** (Ross: a compiled program is where speed matters most — an unpruned parquet read was 6× slower and 12× the memory of the pruned one). With it: a fast path when no rule applies (no re-execution, byte-identical to `+O`), the re-execution names THIS binary rather than `ssql` on PATH, the generated header records the pipeline as typed and the one implemented plus the rules, and the browser build skips the optimiser (no bash to re-execute under). Tests: `optimise_default_test.go`; scale gate `generate-go-run-optimised`.

`-run` was implemented as designed but with one refinement: the
implementation does **build + exec** (two steps) rather than `go run`
(one step), because `go run` executes the compiled binary in the
same directory it built from — that would break relative file paths
in the user's pipeline (e.g. `ssql from data.csv`). The two-step
approach builds in a temp module dir, then exec's the binary from
the user's cwd, so module resolution and relative paths both work.
See `runGoSource` in `cmd/ssql/commands/generate_go.go` for
implementation.

`-mod=mod` is passed to `go build` so the temp dir's `go.sum` is
populated from Go's module cache on first run, avoiding a separate
`go mod tidy` step.

This proposal adds two flags to `ssql generate go`, mirroring the
shape of `generate sql -run` and the existing `generate ssql`
optimiser:

- **`-optimise`** (alias `-O`): apply the same pipeline rewrites
  that `generate ssql` applies, then generate Go code from the
  optimised pipeline. The headline win is automatic
  `-columns` / `ParquetColumns(...)` injection on Parquet reads —
  10× speedup with zero user effort.
- **`-run`** (alias `-r`): compile and execute the generated
  program, streaming its stdout/stderr through. Mirrors
  `generate sql -run` (which pipes the SQL to `duckdb -c`).

Combined: `ssql generate go -optimise -run` becomes the
"shortest path from natural ssql pipeline to running typed-Go
binary, with optimisations applied".

## 1. Motivation: the current four-step incantation

Today, getting from a natural pipeline to optimised typed-parallel
output requires the user to manually chain four ssql invocations
plus a shell loop:

```bash
$ pipeline=$(
    (export SSQLGO=1
     ssql from parquet shuffled.parquet \
       | ssql group-by relationship -count number \
       | ssql to table) \
    | ssql generate ssql)

$ (export SSQLGO=parallel; bash -c "$pipeline") \
    | ssql generate go > pipeline.go

$ go run pipeline.go
```

Steps 1+2 are the optimiser pass. Steps 2+3 are the
codegen+compile+run cycle. The user has to understand the
SSQLGO env-var convention, process substitution, and the fact
that `generate ssql` outputs a *string* that needs to be fed
back through `bash`.

The proposed surface is two short forms:

```bash
# Just compile (with optimisation)
$ (export SSQLGO=parallel
   ssql from parquet shuffled.parquet \
     | ssql group-by relationship -count number \
     | ssql to table) | ssql generate go -optimise -O > pipeline.go
$ go run pipeline.go

# Compile and run, one-shot
$ (export SSQLGO=parallel
   ssql from parquet shuffled.parquet \
     | ssql group-by relationship -count number \
     | ssql to table) | ssql generate go -optimise -run
```

Same end result, no shell-substitution gymnastics, no need to
remember the SSQLGO chain.

## 2. Data flow

### 2a. `-run` (without `-optimise`)

```
fragment stream (stdin)
    │
    ▼
AssembleCodeFragments → Go source string
    │
    ▼
write to temp file: /tmp/ssql-gen-XXXX.go
    │
    ▼
exec "go run /tmp/ssql-gen-XXXX.go"
    │
    ▼
stdout/stderr forwarded to the user
    │
    ▼
delete temp file (defer)
```

Identical control flow to `generate sql -run`'s
`exec "duckdb -c <sql>"`, just with `go run` as the executor.

Pre-conditions for `-run`:
- A Go toolchain on `$PATH` (`go run` requires it)
- The `ssql` library accessible from the user's `GOPATH`/module
  cache — automatic when ssql was installed via `go install`,
  but worth surfacing in the error message if `go run` fails

### 2b. `-optimise` (without `-run`)

```
fragment stream (stdin)
    │
    ▼
optimizePipeline (existing — returns rewritten pipeline string + rules)
    │
    ▼
fork a subprocess: bash -c "<rewritten-pipeline> | ssql generate go"
    │      ^                                       ^
    │      └── inherits SSQLGO=parallel from env   └── plain generate go,
    │                                                  no -optimise (avoids
    │                                                  recursion)
    ▼
fragment stream from the rewritten pipeline
    │
    ▼
forward to stdout (or write to OUTPUT file)
```

The key insight: **the existing `optimizePipeline` returns the
pipeline as a bash-runnable string**. So the implementation is
"run that string, then pipe back into `generate go` (without
`-optimise` to avoid loops), forward output".

Why re-execute rather than rewrite fragments in place? Because:
1. The optimiser's text-rewrite path (`pipelineCmd.RawArgs`,
   etc.) is already correct, well-tested, and used by
   `generate ssql`. Refactoring it to operate on
   `lib.CodeFragment` would duplicate the logic.
2. Fragment-level rewrites would still need to invoke the
   command codegen (`from_parquet_typed.go` etc.) to regenerate
   the fragments — i.e. effectively re-execute the pipeline.
   Doing it via bash is just being explicit about that.
3. Each command's codegen path runs in its own process today;
   reusing that contract avoids inventing a new in-process
   fragment-rebuild API.

Cost: one extra fork+exec per `-optimise` call. Negligible
compared to the actual `go build` and runtime work that
follows.

#### Shell equivalent (interim workaround until shipped)

A user independently discovered the exact same data flow as a
pure-shell pipeline, which validates the design and serves as a
stop-gap for anyone who needs the behaviour today:

```bash
(export SSQLGO=parallel;
  eval $(
    (export SSQLGO=1
     ssql from parquet shuffle.parquet \
       | ssql group-by relationship -count number \
       | ssql to table) | ssql generate ssql
  )
) | ssql generate go > parallel-par.go
```

The two SSQLGO scopes are the key trick:

- **Inner subshell `SSQLGO=1`** — runs the original pipeline in
  Record mode so `generate ssql` has Record-mode fragments to
  read and rewrite (column-pruning, predicate pushdown, etc.).
  The output is the rewritten pipeline as a bash string.
- **Outer subshell `SSQLGO=parallel`** — `eval`s the rewritten
  string. Each command in the optimised pipeline now emits
  parallel-typed fragments because of the outer env. The final
  `| ssql generate go` collects them and assembles the program.

This is exactly what the proposed `-optimise` implementation
above does, just hidden behind a flag: `optimizePipeline()`
returns the rewritten string, then `exec.Command("bash", "-c",
string + " | ssql generate go")` with the right `SSQLGO`
inherited on the env.

The shell version exposes one thing the implementation will
need to handle gracefully: **the user has to know which
SSQLGO each scope wants**. Inner is always `=1` (Record mode is
what `generate ssql` rewrites against); outer is whichever
mode the user wants the optimised pipeline to compile *to*.
The Go implementation can either pick the outer mode from
`os.Getenv("SSQLGO")` (matching whatever's set when
`generate go -optimise` runs) or infer it from incoming
fragment metadata (`OutputTypedSchema` set ⇒ typed/parallel,
unset ⇒ Record). Inferring from fragments is more robust
because it handles the user's own subshell-export pattern
without surprises.

### 2c. `-optimise -run` (combined)

The two flags compose naturally — the rewritten pipeline is fed
into `generate go -run` rather than `generate go`:

```
optimizePipeline → bash -c "<rewritten> | ssql generate go -run"
```

Inner `generate go -run` does the same as §2a. We avoid running
the pipeline twice.

## 3. Surface

### `generate go` flag additions

```
USAGE:
    ssql generate go [OPTIONS] [OUTPUT]

OPTIONS:
    -optimise, -O    Run the generate-ssql optimiser first;
                     compile from the rewritten pipeline.
                     Implies re-executing the pipeline once
                     to produce optimised fragments.

    -run, -r         Compile and run the generated Go code
                     instead of writing it to a file/stdout.
                     Output (stdout/stderr) of the compiled
                     program is forwarded.

    -explain, -e     (only meaningful with -optimise) Print
                     the applied optimiser rules to stderr,
                     same format as `generate ssql -explain`:
                     `[rule] before → after`.

    OUTPUT           Output Go file. Ignored when -run is set.
                     If unset (and -run is unset), the code is
                     written to stdout.
```

Both flags default to `false`. Both can be combined with `OUTPUT`,
but `OUTPUT` + `-run` is a small UX trap — we'd write the file
*and* run, or treat it as conflicting? Prefer **error out** with
"OUTPUT and -run are mutually exclusive — use -run to execute, or
omit -run to write the file".

### Examples to add

```
ssql generate go -optimise            # auto-prune Parquet columns,
                                      # write Go to stdout
ssql generate go -O -r                # same, then compile+run
ssql generate go -O -e -r             # show the applied rules
ssql generate go -run output.go       # ERROR: mutually exclusive
```

## 4. Failure modes

| Path | Failure | Behaviour |
|---|---|---|
| `-optimise` | Optimiser hits an unknown command | Pass through unchanged; same as `generate ssql` today |
| `-optimise` | Optimised pipeline fails to re-execute (e.g. SSQLGO env not set) | Error to stderr with the bash command that was attempted; non-zero exit |
| `-run` | No Go toolchain | Error: "ssql generate go -run: go binary not found in PATH" |
| `-run` | Generated code fails to compile | `go run` prints compile errors; non-zero exit |
| `-run` | Generated program crashes at runtime | Exit code from `go run`, stderr forwarded |
| `-run` | Temp file write fails | Standard I/O error |
| `-optimise -run` | Combined errors | Optimiser errors surface first, then re-execution errors, then compile, then run |

## 5. Edge cases

### 5a. SSQLGO env propagation

The user's pipeline runs with `SSQLGO=parallel` (or `=typed`,
or `=1`). When `-optimise` re-executes the rewritten pipeline,
the new bash subprocess **must** inherit that env — otherwise
the inner ssql calls go back to record-mode runtime instead
of producing fragments.

`exec.Command` preserves `os.Environ()` by default; we just
need to not zero it out. Document this in the implementation.

### 5b. Recursive `-optimise`

We must not call `generate go -optimise` from inside the
re-executed pipeline (would loop forever). Solution: spawn the
subprocess with explicit `ssql generate go` (no `-optimise`).
A defensive `SSQL_GENERATE_GO_OPTIMISE_GUARD=1` env var marker
can detect re-entry and error out — cheap insurance.

### 5c. `-run` and `OUTPUT`

Treat as mutually exclusive. Error message should be
specific enough that a confused user knows which flag to drop:

```
Error: ssql generate go: -run and OUTPUT are mutually exclusive
       (use -run to compile+execute, omit -run to write the
       generated source to a file)
```

### 5d. Codegen-time errors

If a command in the pipeline emits an error fragment (e.g. a
typed-mode-unsupported command in `SSQLGO=parallel`), the
optimised re-execution will produce the same error fragment.
`-optimise` doesn't recover from that — the user has to fix
their pipeline. Same as today.

### 5e. Working directory and tempfile cleanup

`go run` requires a `.go` file on disk. We write to
`os.CreateTemp("", "ssql-gen-*.go")` and `defer os.Remove(...)`.
On `SIGINT`/`SIGTERM` mid-`go run`, defer cleanup may not fire —
acceptable cost; tmp files get garbage-collected eventually.

The temp file's working directory matters for **relative file
paths in the pipeline**: `ssql from data.csv` would look for
`data.csv` relative to wherever `go run` is invoked. The
generated Go program's `*flagInput` defaults to the original
filename, which is captured at codegen time and may be relative
to the user's cwd. We should:

1. `chdir` to the user's cwd before `go run` — done by default;
   `exec.Command` inherits cwd
2. Make sure `flagInput` is rendered as the original (possibly
   relative) string — it already is

So this works correctly out of the box; document it as a guarantee.

### 5f. Multi-fragment pipelines (joins via process sub)

Pipelines that use `<( … )` for join right-sides currently
produce *multiple* fragment streams (one per process sub).
`generate go` already handles this. `-optimise` re-executes
the parent pipeline; the inner process subs run normally
during that re-execution. Should Just Work but worth a test.

## 6. Implementation sketch

In `cmd/ssql/commands/generate_go.go`, extend the handler:

```go
Flag("-optimise", "-O").
    Bool().
    Global().
    Default(false).
    Help("Apply pipeline optimiser before generating code").
    Done().

Flag("-run", "-r").
    Bool().
    Global().
    Default(false).
    Help("Compile and run the generated Go code").
    Done().

Flag("-explain", "-e").
    Bool().
    Global().
    Default(false).
    Help("(with -optimise) print applied rules to stderr").
    Done().

Handler(func(ctx *cf.Context) error {
    optimise := boolFlag(ctx, "-optimise")
    run      := boolFlag(ctx, "-run")
    explain  := boolFlag(ctx, "-explain")
    output   := stringFlag(ctx, "OUTPUT")

    if run && output != "" {
        return fmt.Errorf("ssql generate go: -run and OUTPUT are mutually exclusive")
    }

    // Path A: -optimise. Read fragments, optimise, re-execute,
    // delegate to a child `ssql generate go` (with -run if requested,
    // without -optimise to avoid recursion).
    if optimise {
        return runOptimiseThenGo(os.Stdin, run, explain, output)
    }

    // Path B: plain generate go, with optional -run.
    code, err := lib.AssembleCodeFragments(os.Stdin)
    if err != nil { return err }

    if run {
        return runGoSource(code)
    }
    return writeGoSource(code, output)
})
```

with helpers:

```go
func runOptimiseThenGo(in io.Reader, run, explain bool, output string) error {
    pipeline, rules, err := optimizePipeline(in)
    if err != nil { return fmt.Errorf("optimising: %w", err) }
    if explain {
        for _, r := range rules {
            fmt.Fprintf(os.Stderr, "[%s] %s → %s\n", r.Rule, r.Before, r.After)
        }
    }
    // Build the inner command string.
    inner := pipeline + " | ssql generate go"
    if run {
        inner += " -run"
    } else if output != "" {
        inner += " " + shellQuote(output)
    }
    cmd := exec.Command("bash", "-c", inner)
    cmd.Env = os.Environ() // critical: SSQLGO=parallel, GOMAXPROCS, etc.
    cmd.Stdout = os.Stdout
    cmd.Stderr = os.Stderr
    return cmd.Run()
}

func runGoSource(code string) error {
    f, err := os.CreateTemp("", "ssql-gen-*.go")
    if err != nil { return err }
    defer os.Remove(f.Name())
    if _, err := f.WriteString(code); err != nil { return err }
    f.Close()
    cmd := exec.Command("go", "run", f.Name())
    cmd.Stdout = os.Stdout
    cmd.Stderr = os.Stderr
    return cmd.Run()
}

func writeGoSource(code, output string) error {
    if output == "" {
        fmt.Print(code)
        return nil
    }
    if err := os.WriteFile(output, []byte(code), 0644); err != nil {
        return fmt.Errorf("writing output file: %w", err)
    }
    fmt.Fprintf(os.Stderr, "Generated Go code written to %s\n", output)
    return nil
}
```

Roughly 100 lines of new code in `generate_go.go`. No changes
to `generate_ssql.go` (we call `optimizePipeline` from across
the package — already exported in same package).

## 7. Testing

Targeted tests in `cmd/ssql/`:

1. **`-run` over a tiny pipeline** — e.g.
   `(SSQLGO=typed; ssql from /tmp/3rows.csv | ssql to table) |
   ssql generate go -run`. Assert stdout has the expected
   table.
2. **`-optimise` adds `-columns`** — pipe a parquet pipeline
   without `-columns`, run `generate go -optimise -e`, capture
   the explain output, assert `parquet-column-pruning` rule
   fired. Then capture the generated code (via `OUTPUT` flag)
   and assert `typed.ParquetColumns(...)` is in it.
3. **`-optimise -run` round-trip** — same parquet pipeline,
   `generate go -optimise -run`, assert stdout has the
   expected aggregated rows.
4. **`-run + OUTPUT` mutual exclusion** — assert error.
5. **Recursive guard** — set
   `SSQL_GENERATE_GO_OPTIMISE_GUARD=1` and pipe through
   `generate go -optimise`, assert error.
6. **`-run` without Go toolchain** — mocked PATH (`PATH=/dev/null`),
   assert clean error message.

Tests reuse the build-binary-and-pipe pattern in
`cmd/ssql/generation_test.go`.

## 8. Out of scope

- **Persistent compile cache.** Each `-run` invocation
  recompiles via `go run`, which uses Go's build cache. We
  don't add an ssql-level cache. Saving to a binary is what
  `OUTPUT` + manual `go build` is for.
- **Sandbox / chroot for `-run`.** The compiled program runs
  with the user's privileges and full disk access. Same trust
  model as `generate sql -run` shelling out to duckdb.
- **`-O` with non-typed pipelines.** Should still work — the
  optimiser applies to all pipelines, not just typed ones, and
  `generate go` produces Record-mode output if SSQLGO=1. Not
  the headline use case but should compose cleanly.
- **Auto-detection of "run vs print".** No magic — if you want
  to run, say `-run`. Less surprising than guessing.

## 9. Future follow-ups

- ~~**Persistent binary mode.** `-build OUT` flag that compiles
  to a binary file, with the schema baked in (so the binary
  doesn't re-sample a CSV at startup). Could be a separate
  proposal.~~ ✅ Shipped 2026-04-28. `compileGoSource(code, outPath)`
  is the shared helper between `-run` (outPath="" → binary
  inside temp dir, exec then nuke) and `-build` (outPath set →
  binary kept, temp source nuked). Mutual exclusion enforced
  on `{-run, -build, OUTPUT}` — pick one of three output forms.
- **Auto-`ParquetColumns` without `generate ssql`.** Have the
  typed-mode codegen perform downstream-field analysis directly
  during fragment processing. More work, but the user wouldn't
  even need `-optimise` to get column projection. Worth it
  long-term — once we add this, the only reason to keep
  `-optimise` is for non-Parquet rules (predicate pushdown,
  sort+limit→top, etc.).
- **Pipeline caching.** If the same pipeline is run repeatedly
  on the same data (test loop, dashboard refresh), we could
  cache the generated binary keyed on pipeline hash. Probably
  not worth the complexity until someone actually wants it.

## See also

- [`cmd/ssql/commands/generate_sql.go`](../../cmd/ssql/commands/generate_sql.go) §`-run` flag — the model for `-run`
- [`cmd/ssql/commands/generate_ssql.go`](../../cmd/ssql/commands/generate_ssql.go) §`optimizePipeline` — the rewriter we'd reuse
- [`cmd/ssql/commands/generate_go.go`](../../cmd/ssql/commands/generate_go.go) — the file we'd extend
- [`typed-codegen-proposal.md`](typed-codegen-proposal.md) §5d — parallel-mode codegen and the auto-`ParquetColumns` follow-up that this proposal sets up
