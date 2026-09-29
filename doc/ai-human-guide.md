# Using an LLM to Write ssql Pipelines and Programs

ssql ships two prompts you paste into an LLM so that it writes correct
ssql for you. This page says which one to use, how to paste it, and how
to check what comes back.

[Back to Documentation](README.md)

## Which prompt

| You want | Paste | Get back |
|---|---|---|
| a shell pipeline: `ssql from … \| ssql … \| ssql to …` | [ai-cli-generation.md](ai-cli-generation.md) | a command line to run |
| a Go program that uses the `ssql` library | [ai-code-generation.md](ai-code-generation.md) | a complete `main.go` |

Rule of thumb: if the answer should be typed at a prompt, use the CLI
prompt; if it should be a `.go` file, use the Go prompt. Both cover
signal processing, joins and set operations. A pipeline can always
become a program later: `ssql generate go -pipeline '…'` compiles any
pipeline the LLM wrote.

## How to paste it

Paste the **entire file**, not just its opening code block, as the first
message of a fresh conversation (Claude, ChatGPT, Gemini, or a local
model), then describe what you want in plain language:

```
[the whole of ai-cli-generation.md]

Read sales.csv, keep the active rows, total revenue per region, show the top 5 as a table.
```

In a coding agent that reads files, point it at the prompt instead of
pasting: with the `claude` CLI (`npm install -g @anthropic-ai/claude-code`)
run `claude` in your project and say "read doc/ai-cli-generation.md, then
…", or keep a `CLAUDE.md` in the project that tells it to read the prompt
and to check `go doc github.com/rosscartlidge/ssql/v4` for any function
it is unsure of. The Gemini CLI (`npm install -g @google/gemini-cli`,
command `gemini`) and Aider (`pip install aider-chat`) work the same way.
Agents that can run commands are the productive setting: they run the
pipeline, read the error, and fix it themselves.

## Asking well

- **Name the file and the fields.** "Filter sales.csv where `amount` is over 500" beats "filter the data".
- **Say the steps in order.** Read, filter, group, aggregate, sort, output. The prompt maps each verb to a command.
- **Name the output.** A table on screen, a CSV file, JSON Lines, a chart file.
- **Build up.** Get the filter right, then add the group-by, then the chart. Each step is a short pipeline you can run.

## Checking the answer

For a pipeline, run it. ssql fails loudly on the mistakes an LLM makes
most (a misspelt field lists the fields that exist; a wrong flag names
the command's flags; a literal of the wrong type for a comparison stops
the run), so a pipeline that runs and prints what you asked for is
usually right. Look for:

- `ssql from` first, `|` between every command, a sink (`to table`, `to csv FILE`, …) last
- no data file after a transform (`ssql where data.csv …` is wrong)
- aggregate results referred to by the name given: after `-sum amount total` the field is `total`
- `-if-expr` (not `-expr`), `-using` / `-on L R` for joins, `-output` for chart files
- `ssql generate go -pipeline '…'` for a compiled version, with the sink inside the string

For a Go program, `go run` it. The first lines should look like this,
with the error from the reader checked before anything else:

```go
data, err := ssql.ReadCSV("sales.csv")
if err != nil {
    log.Fatalf("read sales.csv: %v", err)
}
```

Then check that it:

- reads with `ssql.ReadCSV` (or `ReadJSON`, `ReadParquet`, …) and checks the error
- reads fields with `ssql.GetOr(r, "age", int64(0))` — CSV numbers are `int64` or `float64`, never strings — and builds records with `ssql.MakeMutableRecord()…Freeze()`; there is no map access
- uses `ssql.Select`, `ssql.Where`, `ssql.Limit` (not `Map`, `Filter`, `Take`), `ssql.GroupByFields` then `ssql.Aggregate` with the same group name, and parameterless `ssql.Count()`
- imports `github.com/rosscartlidge/ssql/v4` and only the packages it uses
- uses `ssql.Chain(...)` for a multi-step pipeline of the same type

`go doc github.com/rosscartlidge/ssql/v4.FunctionName` is the authority
for any signature the program uses.

## When it goes wrong

| Symptom | Ask for |
|---|---|
| `Map`, `Filter`, `Take` | "use the ssql names: Select, Where, Limit" |
| `record["field"]` | "Record is not a map: use ssql.GetOr to read and MakeMutableRecord to build" |
| `data := ssql.ReadCSV(...)` with no error check | "ReadCSV returns (seq, err); check the error" |
| `GetOr(r, "age", "")` on a numeric column | "CSV numbers are int64 or float64; use int64(0)" |
| a flag that does not exist | paste the output of `ssql COMMAND -help` and ask it to use those flags |
| sorting on `amount_sum` after `-sum amount total` | "the result field is the name I gave: total" |
| a long tangle | "rewrite as short steps with named intermediate variables" |

If it keeps drifting, start a new conversation and paste the prompt
again; the prompt is the context it has lost.

## Next

- [CLI Codelab](cli-codelab.md) — what the pipelines it writes actually do
- [Getting Started Guide](codelab-intro.md) — the Go library the programs use
- [Expression Language](EXPRESSIONS.md) — `-if-expr` / `-set-expr`, which LLMs get wrong most often
