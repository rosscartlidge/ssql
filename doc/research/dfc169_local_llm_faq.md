# A Dynamic FAQ on a Local Model: Teaching Gemma to Answer for ssql

Reference: DFC169
Created: 2026-11-15
Last modified: 2026-11-21

[Back to Index](./README.md)

Status: phase 1 (the harness and eval) built 2026-11-15; §10 has the results.

## 1. The ask

Ross (2026-11-15): people should be able to ask questions about ssql and get
example pipelines for their own problems without him answering. The FAQ
should be built with an LLM, not written by hand. A hosted frontier model is
not affordable as an open service. The workstation is enough to run one,
though: Ollama, an RTX 5090 Laptop GPU (24 GB), and open models up to
`gemma4:31b`. The challenge: can one of these models be taught to be a good
enough dynamic FAQ?

**Short answer: yes, if we stop asking the model to know ssql and make it
consult ssql.** A 17–19 GB model already writes correct CLI pipelines 15
times out of 15 when given the prompt (DFC043 Appendix E). What it cannot
do is know things it has not been shown, such as the column names in the
user's file, a flag added last week, or whether a feature exists at all.
Those facts have authorities in this repo:
- the commands, through `-spec-json`, `-help-at`, `SSQL_MODE=schema`,
  `conventions` and `functions`;
- the docs, through `scripts/docsearch.sh`;
- the engine, which runs the pipeline and fails loudly.

The design wires the model to those authorities and grades it on what the
authorities say. This is DFC115 ("commands are the authority") applied to a
chatbot.

## 2. What we already have

| Asset | What it gives the FAQ |
|---|---|
| `doc/ai-cli-generation.md` (31 KB, ~9.2 k tokens) | The tuned CLI prompt. Gemma 4 31B scored 15/15 on CLI with it (DFC043 E.2) |
| `doc/ai-code-generation.md` (42 KB) | The Go prompt. Gemma 4 31B scored 13/15 on Go; Go is harder for small models |
| `scripts/testdata/ai-test-cases.md` + `scripts/test-ai-prompts.sh --llm` | A 30-case harness that already runs against Ollama through a wrapper |
| `scripts/docsearch.sh` | Hybrid BM25 plus `nomic-embed-text` retrieval over `doc/`, `claude/` and the journals, already local |
| `ssql -spec-json` (289 KB) | Every command, flag, argument and help text, generated from the grammar |
| `ssql conventions`, `ssql functions` | System-wide semantics and the expression catalog |
| `SSQL_MODE=schema`, `-help-at`, `-complete` | Column names and types of a pipeline's output; help for the word under the cursor |
| The playground (slim wasm) and its `#p=` share links | A sandbox in the visitor's browser that runs a pipeline against `cmd/ssql-playground/data/` with no server-side execution |
| `ssql serve` and its validator (`serve_validate.go`) | An allow-listed server-side runner, if one is ever wanted |

## 3. Measurements on this workstation (2026-11-15)

Ollama with `num_ctx=32768`, `think:false` and temperature 0. The prompt was
the full `ai-cli-generation.md` plus one question.

| Model | VRAM | Prompt of 9,228 tokens | Generation | Cold load |
|---|---|---|---|---|
| `gemma4:26b` (mixture of experts) | 17 GB | 2.6 s | **107 tok/s** | 0.7–20 s |
| `gemma4:31b` (dense) | 19 GB | 9.9 s | 24 tok/s | 8–17 s |

The question was "a pipeline that counts rows per dept in employees.csv":
- 26b: `ssql from employees.csv | ssql group-by department -count n | ssql to table`
- 31b: `ssql from employees.csv | ssql group-by dept -count n | ssql to table`

The column is `dept`. The 26b answer is fluent and wrong, and only running it
shows that. Neither model saw the file. The 31b model was right because the
prompt's examples happen to use `dept`. This one exchange is the design
argument in miniature: **ground the model on the data's schema and verify by
running, never trust fluency.**

**Throughput.** A 400-token answer takes about 4 s on 26b and about 17 s on
31b, after a 3–10 s prompt read. One GPU serves one request at a time, with
queueing. That is fine for an FAQ, but not a free-for-all, so rate limits
are part of the design (§7).

## 4. The design: retrieve, ground, draft, verify, answer

```
question ──► router ──► retrieve (docsearch: top k doc chunks, with file:line)
                   │
                   ├──► ground  (spec-json slice for the commands named;
                   │             conventions/functions if relevant;
                   │             the dataset's schema: playground data, or a
                   │             header the visitor pasted)
                   ▼
                draft  (gemma: answer + pipeline in a fenced block)
                   ▼
                verify (run the pipeline: slim ssql in a jail against the
                   │    playground data, exit code + stderr + first rows;
                   │    on failure, feed ssql's own error back: at most 2 retries)
                   ▼
                answer (prose + verified pipeline + its real output rows
                        + "open in playground" #p= link + doc citations)
```

**Retrieval** reuses docsearch unchanged. The FAQ gets a curated corpus:
- user docs (`doc/*.md`, the codelabs, EXPRESSIONS, troubleshooting);
- the CHANGELOG's recent sections.

Not research DFCs or journals by default. They are full of superseded
plans, and a small model will happily quote a deprecated design as current.
Use DFCs only with the `Deprecated-by` filter, if at all.

**Grounding** is where the 26b error dies:
- the router names the commands the question touches, and the answer's
  context gets those commands' `-spec-json` entries, not all 289 KB;
- the dataset's real header and types come from `SSQL_MODE=schema`.

**Verification** is the step that makes a small model trustworthy. ssql
fails loudly by design. An unknown field lists the available ones, and a
join without a schema header says how to fix it. So the error text is a
better teacher than another paragraph of prompt. A pipeline that does not
run after two corrections is not shown as an answer. The visitor gets the
honest version: "I couldn't make this work; here is the closest I got and
the error."

**Answers carry evidence:**
- the pipeline's actual output rows;
- a playground link that runs it;
- the doc chunks it relied on, with `file:line`.

A reader can check any claim in one click. That is the substitute for the
model being as good as a frontier one.

### 4.0 Two authorities that make answers tight: the help and schema mode

Ross (2026-11-15): "the extensive online help of ssql and the schema
generation might help make tight answers too." They are the two cheapest
authorities, and each fits a different step.

**The help is the grounding.** `ssql CMD -help` is generated from the same
grammar the parser uses. It gives:
- the usage line;
- every flag with its arity and argument names (`-sum field result-name`);
- the clause separators;
- the command's 2–3 `.Example()` lines, which CLAUDE.md requires of every
  command.

Retrieval picks the commands; their `-help` text goes into the context
verbatim. `-spec-json` stays the machine-readable form for the verifier.

A help page is a few hundred tokens, about 1–2 % of the context per
command, against 9 k tokens for the whole tuned prompt. It also can't drift
from the binary, because it comes from the binary. The tuned prompt keeps
the cross-command patterns and the anti-patterns, which per-command help
cannot express. When a draft uses one flag in a way the help doesn't show,
`-help-at` on that word gives exactly that flag's help, so a retry can be
pointed at it.

**Schema mode is the pre-flight check.** `SSQL_MODE=schema … | ssql
generate schema` runs the pipeline's grammar and field logic without
reading the data:
- it answers from a CSV header or a Parquet footer;
- it returns each stage's output columns and types, or the stage's
  unknown-field error;
- it takes milliseconds;
- it needs only the header, so it works against a header the visitor
  pasted. Their data never leaves their machine, and the check is still
  real.

The order is draft → schema pre-flight (a bad field is caught here with
the available list) → run on sample data → answer. The output columns go
into the answer ("this produces `dept, n`") so the reader knows the shape
before running it.

**Found while checking this (2026-11-15):**
- `where -if department eq x` in schema mode errors with the
  available-fields list, as exec does.
- `group-by department -count n` in schema mode answered `n` with exit 0,
  silently dropping the unknown group field.
- Exec of the same pipeline fails loudly.

The pre-flight can only be as strict as schema mode, so this is a
prerequisite for the FAQ. It is also a fail-loudly bug on its own, and it
is in TODO. Phase 1 should sweep every command's schema mode for the same
gap: an unknown field in each field-taking flag, schema lane against exec
lane. The flag list comes from `-spec-json`, the way the type matrix walks
it.

### 4.1 Where the pipeline runs

| Option | Pros | Cons |
|---|---|---|
| **A. Server-side slim ssql in a jail** (no network, read-only playground data, CPU/time/memory limits, a fresh tmpdir) | The model sees real errors and real output in the loop | One more process to sandbox |
| **B. The visitor's browser** (the playground wasm runs it, and the page posts the result back for a retry) | Zero server execution | The verify loop crosses the network twice; harder to make deterministic for the eval |

**Recommendation: A for the loop, B for the user.** The server verifies
against the playground data with the slim build: no `net/http`, no
`serve`, no URL sources (DFC151), and nothing but `ssql` on the jail's
PATH. The answer's link reruns the same pipeline in the visitor's own
browser. Pipelines that need the visitor's real data stay in their hands.
The model shapes the pipeline against a header they paste, and the page
says "verified against the sample data, not yours".

Shell syntax matters here. A model's pipeline is shell text. The verifier
must parse it into ssql invocations and refuse anything else (`;`, `$( )`,
redirections to paths, commands other than `ssql`). `serve_validate.go`
already does this job for serve. Reuse it, never `bash -c` the model's
text.

## 5. "Teaching": a ladder, cheapest first, each rung measured

1. **Prompt + RAG + grounding + verify loop** (§4). No training. Expected to
   carry most of the value. The DFC043 numbers are the floor.
2. **Few-shot from a curated bank.** The question/answer pairs that passed
   the eval become retrieved examples. The FAQ improves by adding good
   answers, as a static FAQ would, but the bank is searched rather than
   read.
3. **Distillation (one-off, offline).** Use a frontier model once, paid,
   bounded, never in the serving path, to write a few thousand
   question→pipeline pairs. Then keep only the pairs whose pipelines run and
   whose outputs match an independent check (§6). QLoRA-tune
   `gemma4:26b` or `e4b` on them.

   The 24 GB card fits QLoRA of a ~12B model comfortably. A 26–31B model is
   tight, so plan on rented hours or a smaller student. This is the "teach"
   in the literal sense. It is only worth doing if rung 1–2's eval
   plateaus, and the data has to be regenerated as the CLI grows, so it
   carries a maintenance cost the other rungs do not.
4. **Not recommended: fine-tuning on the docs as text.** It bakes in a
   snapshot of a CLI that changes weekly. Retrieval and grounding stay
   current for free.

## 6. The eval is the product (a gate we watch fail)

DFC043 graded with `Contains` patterns. CLAUDE.md is blunt about that
oracle: power is oracle strength × input discrimination. The FAQ's eval
should use the equivalence suite's oracle instead.

- **Pipeline questions** (target ~100, phrased the way people ask, with
  typos), each with a hand-checked golden output over the playground data.
  - Pass = the verified pipeline's normalised output equals the golden. The
    pipeline text doesn't matter, because many pipelines are right.
  - Shuffled data with distinct values, so a wrong group or filter
    diverges.
- **Doc questions** ("does join support left outer?", "why is my Parquet
  read slow over HTTP?"). Pass = the required facts are present and the
  cited `file:line` contains them. A second model judges only the "facts
  present" part, against a rubric written by hand.
- **Must-refuse questions.** Features that don't exist, pipelines that
  need network access, Go questions the Go prompt can't cover. Pass =
  "ssql doesn't do that" plus the nearest real alternative, never a made-up
  flag. **This class is the one that protects Ross's reputation.**
- **Drift questions, regenerated from `-spec-json`.** One question per
  command, so a new command with no answerable question fails the eval the
  way `TestFieldCompletionConfiguration` fails a new command without
  completion.

Every eval runs per model (26b, 31b, gpt-oss:20b, e4b) and reports
accuracy, p50/p95 latency and retries used. Before anything ships, the
gate is watched to fail: point it at a grounded pipeline with the schema
step removed, and it must catch the `department` error.

## 7. Serving it from a laptop to the public

- **Front end.** A chat panel beside the existing playground on GitHub
  Pages. The static page calls a small Go service (`ssql faq serve`, or a
  separate binary) through Tailscale Funnel or a Cloudflare Tunnel. No
  open ports, and the laptop's address is never public.
- **The service holds:**
  - the Ollama client;
  - the docsearch index;
  - the jail;
  - a per-IP token bucket and a global queue of one or two at a time,
    with "busy, try again" when it is full;
  - a hard cap on question length and on retries;
  - a log of questions, without IPs, to feed the eval.
- **When the laptop is off,** the page says so and falls back to search
  over the docs in the browser: the docsearch index exported as JSON with
  BM25 only, no model. That is the static FAQ as a floor.
- **Prompt injection.** The retrieved docs are ours. The visitor's text
  and pasted headers are untrusted. The model has no tools beyond the
  jailed ssql, so the worst case is a wrong answer, which verification
  catches, or a rude one. Rate limits bound the cost.
- **Model.** Default to `gemma4:26b`: four times the generation speed of
  31b. Switch to 31b if the eval says it is meaningfully more accurate
  with grounding. Keep one model warm (`keep_alive`), because a cold load
  is 10–20 s.

## 8. Phases

1. **Harness first, no UI.** A `scripts/faq/` Go program (or `go run`
   tool) does retrieve → ground → draft → verify for one question on the
   CLI. Build the eval set and run it across four models. Deliverable: a
   table like DFC043 E.2, but with an execution oracle.
2. **Service + page.** The chat panel, the queue, the tunnel. Show it to
   two or three people, and log their questions.
3. **Grow the bank from the logs.** Real questions that failed become eval
   cases. Fixes go to the docs, the prompt or the error messages: the
   Ralph Wiggum loop of DFC043, now fed by users.
4. **Distil only if 3 plateaus.**

A side effect worth having: every question the FAQ can't answer is a
documentation bug or a missing feature, so it goes to `TODO.md`. The FAQ's
failure log is a usability study that runs itself.

## 9. Open questions for Ross (answered 2026-11-16)

Ross opened phase 2 to **the local network and his tailnet only, with no
Tailscale Funnel yet**. The answers:

1. **Where it lives:** its own page, served by the FAQ service. The
   playground is https on GitHub Pages and can't call a plain-http LAN
   address. Answers link into the public playground instead, with its
   `#p=` share link.
2. **Go questions:** CLI only. A Go question gets a doc answer, never a
   verified program.
3. **Visitors' own data:** pasted, never uploaded. They paste column names
   (or a CSV header with a few rows). They are checked in schema mode, and
   any rows are run.
4. **Availability:** "when the laptop is on", which is enough on a LAN.
5. **Logging:** yes, with no addresses and no pasted data, and the page
   says so.

## 10. Phase 1 built (2026-11-15)

### 10.1 The harness

`scripts/faq` is a Go module of its own, like `scripts/docsearch`, and needs
no UI:

```
go -C scripts/faq run . -ask 'how many employees per department?'
go -C scripts/faq run . -eval cases.json -models gemma4:26b,gemma4:31b -v
go -C scripts/faq run . -eval cases.json -goldens     # every golden, no model
```

It does what §4 describes, in this order:
1. **Retrieve.** docsearch, keeping user docs and the CHANGELOG only (no
   research DFCs or journals, and not the `doc/ai-*` prompts).
2. **Ground.** The system prompt is the tuned CLI prompt
   (`doc/ai-cli-generation.md`), the answer format, and each sample file's
   columns and types as schema mode reports them.
3. **Draft.** Ollama chat, with temperature 0, a fixed seed and no
   thinking.
4. **Validate.** Only `ssql` stages joined by `|`, with `<( … )` for join's
   right side. It refuses `; & $` backticks and redirection, `$` or
   backticks inside double quotes (they expand), absolute paths and `..`.
5. **Schema pre-flight.** The pipeline runs under `SSQL_MODE=schema`. This
   relies on DFC170, which had to land first.
6. **Run.** On a scratch copy of the playground data, with ssql first on
   `PATH` and a 20 s limit.
7. **Correct.** On failure, the model gets ssql's own error and the `-help`
   of each command it used, at most 2 times.

How an answer is labelled:
- A pipeline that never runs is labelled so, never shown as an answer.
- One that names a file the sample data lacks is an illustration, shown but
  not run.
- A reply starting "UNSUPPORTED" is a refusal, in any decoration the model
  gives it.

### 10.2 The eval

`scripts/faq/cases.json` has 33 cases: 22 pipeline, 5 doc and 6 refuse.

- **Pipeline cases.** Each is graded by the rows of the model's pipeline
  against a golden pipeline, written and run by hand. Several were also
  cross-checked with awk. The comparison is values as a multiset per row
  and rows as a multiset, so column names are free (`n` or `count`) but an
  extra column differs. An ordered case compares order too.
  - A final stdout sink is replaced by `to jsonl`. One that formats times is
    replaced by `to csv -time-format …`, read back, so a month displayed as
    2024-01 compares as 2024-01.
- **Doc cases.** Each needs its key facts in the reply.
- **Refuse cases.** Each needs "UNSUPPORTED", and no pipeline it offers may
  use the invented spelling. Prose may name the spelling to say it doesn't
  exist.
- **Goldens.** Every golden is run before any model is graded, and a golden
  that fails or gives no rows stops the eval.
- **The grader's own tests** (`go test` in `scripts/faq`) were each watched
  failing.

### 10.3 Results (third run, the harness fixed)

Over the released v4.148.0, on the RTX 5090 Laptop GPU:

| Model | Pass | pipeline | doc | refuse | corrections | p50 | p95 |
|---|---|---|---|---|---|---|---|
| gemma4:26b | 31/33 (94%) | 20/22 | 5/5 | 6/6 | 6 | 1.9 s | 8.6 s |
| gemma4:31b | 32/33 (97%) | 22/22 | 5/5 | 5/6 | 5 | 22.3 s | 44.9 s |
| gpt-oss:20b | 31/33 (94%) | 22/22 | 4/5 | 5/6 | 10 | 4.7 s | 18.9 s |
| gemma4:e4b | 26/33 (79%) | 18/22 | 4/5 | 4/6 | 8 | 1.9 s | 10.0 s |

Every remaining failure is a model's own error:
- an extra `customer_id` column where the question asked for name and
  count;
- `-as` with its arguments reversed;
- a "translation" built by joining the English names to themselves;
- an export to a local file offered instead of a refusal;
- a timestamp where the question asked for 2024-01.

**Recommendation:** `gemma4:26b` as the default. It is within one case of
31b at a tenth of its latency (p50 1.9 s against 22 s), and it is the only
model that refused all six impossible requests.

### 10.4 What phase 1 found outside the FAQ

- **Schema mode was not fit to be a pre-flight.** This became DFC170
  (v4.148.0).
- **`join -as` with a right field the right side lacks copied nothing,
  exit 0, in every lane.** A model wrote `-as src_name name` and got an
  error from the next stage instead. It is fixed in `resolveJoin`, the check
  every lane shares, and `TestJoinAsUnknownRightFieldIsLoud` covers five
  lanes. Rerun with the fix, the same model's correction turn moves to the
  right stage, though 26b still doesn't settle on the right arguments.
- **A join after an undetermined right side** refuses with misleading
  advice in schema mode. It is in TODO.

### 10.5 Harness faults the first two runs exposed

Each is fixed:
- failing pipelines weren't in the report;
- the sandbox lacked bash, which ssql's own stages use;
- an example over an imagined file was retried instead of being shown;
- the refusal grader failed a correct "ssql has no `from pdf`";
- `**UNSUPPORTED:**` in bold wasn't read as a refusal;
- a formatted sink (`to table -time-format`) couldn't be graded.

Run-to-run differences at temperature 0 are real. The same question passed
alone and failed in the eval run. The eval should be run more than once
before a model decision rests on one case.

### 10.6 Next

Phase 2, built in §11.

## 11. Phase 2 built (2026-11-16): the service on the LAN and the tailnet

```
cd scripts/faq && go build -o ~/.local/bin/ssql-faq .
cd ~/src/ssql && ssql-faq -serve -listen :8484 -models gemma4:26b
```

`ssql-faq.service` is the same command as a systemd user unit; its header
says how to install it.

### 11.1 What it is

- **`-serve` serves:**
  - one embedded page (`page.html`: no external scripts; the model's text
    is escaped before rendering);
  - `POST /api/ask` (`{question, header?}`);
  - `GET /api/health`.
- **Reach:** `privateOnly` answers loopback, RFC 1918, link-local and
  Tailscale's ranges (100.64.0.0/10, fd7a:115c:a1e0::/48), and returns 403
  to anything else. It also refuses any request carrying
  `Tailscale-Funnel-Request`. Funnel traffic arrives over loopback, so that
  header is what tells it apart. Running `tailscale funnel` by mistake gets
  403s; going public is a code change, made together with the per-address
  rate limits a public service needs. `-listen tailscale:PORT` binds the
  tailnet address alone.
- **One question at a time** (one GPU). At most four wait, and the rest get
  503 "busy". The caps are a 2 KB question, 20 KB of pasted data, 200
  columns and 200 rows. A model turn times out after 120 s. The model is
  warmed at start-up and kept loaded for 30 minutes.
- **The jail.** `sandbox.run` with `jail` set runs:
  - `systemd-run --user --scope`, with TasksMax=64, MemoryMax=1G and no
    swap;
  - then `prlimit`, with a 64 MB file and 30 s of CPU;
  - then `bwrap --unshare-all`.

  Inside, there is no network. The host's `/usr` is read-only, the scratch
  copy of the data is `/work`, and ssql is at `/run/ssql/ssql`. Everything
  but `/work` and `/tmp` is read-only (`--remount-ro /`; the first test
  run found bwrap's root writable). `RLIMIT_NPROC` can't cap a run,
  because it counts every process the user owns. The scope can, and costs
  about 15 ms.

  The jail runs the **slim** ssql (DFC151), built from the checkout into
  `~/.cache/ssql-faq/ssql` at start-up. `-serve` refuses to start without
  the jail. The CLI's `-ask` and `-eval` use it when it is present.
- **Pasted data** (`visitor.go`) becomes `data.csv` in a directory of its
  own, and the prompt names its columns. With names only, verification
  stops at schema mode: an unknown column still comes back with the real
  ones, and the answer is labelled "checked against your columns". With
  rows, the pipeline runs on them. Neither kind gets a playground link,
  since the playground lacks data.csv.
- **The log** goes to `~/.local/state/ssql-faq/questions.jsonl`. Each line
  holds:
  - the time and the question;
  - the counts of pasted columns and rows;
  - the model, the pipeline, the status and the first line of the failure;
  - the corrections and the seconds.

  It never holds the address or the pasted data. It is phase 3's input:
  failed questions become eval cases.

### 11.2 Checks

- **Tests (`serve_test.go`), each watched failing by sabotage:**
  - the allowlist and the Funnel refusal;
  - the jail, by raw shell past the validator: `/etc/passwd`, the home
    directory, `/dev/tcp`, writes to `/usr` and `/run`;
  - an answer verified, with its playground link round-tripping the
    pipeline;
  - the log keeps no address;
  - the limits (413, 400, 503);
  - a visitor's wrong column corrected through schema mode, then run on
    pasted rows;
  - the paste parser.
- **The eval under the jail with the slim build:** gemma4:26b scores
  31/33, the same as §10.3. p50 is 1.9 s and p95 7.8 s. The same two
  cases fail, both model errors.
- **Live:** the same question asked over loopback, the LAN address and the
  tailnet address was verified each time, in about 3.7 s. A request with
  `Tailscale-Funnel-Request` got 403.

### 11.3 The first real session (2026-11-16)

Ross's first questions exposed four faults. One was in ssql: typed codegen
refused a column with no values; it is fixed in 7f9b7f0e. The other three
were in the FAQ, and each is now fixed:

- **A follow-up typed into the columns box became a column.** "How does it
  do the conversion" was taken as the visitor's only column, and the model
  built a `where` over it. The fix:
  - a header with no rows and at most two names, one of which ends in `?`
    or runs to five words, is refused with "ask it in the question box";
  - the page says so before sending;
  - the page shows how many columns are in use, and has a clear button.
- **The prompt turned every question into a pipeline.** With columns
  pasted, it said "answer with a pipeline that reads data.csv", so "Can I
  turn a pipeline into a Go program?" got a pipeline instead of an answer.
  The columns are now context: a pipeline for questions about the data, an
  answer for questions about ssql. The visitor's directory also holds the
  sample files, so an example over `employees.csv` runs and gets its
  playground link. `ran_on` says which file the pipeline read.
- **There were no follow-ups.** The page now follows the last answer until
  "New session" (a button beside Ask, which also clears the answers; it
  was a small "New question" link until Ross found it too easy to miss),
  and each answer has "Follow up on this answer". The
  service gives the model that one earlier exchange as turns. Retrieval
  sees both questions, so "how does it do the conversion?" finds what the
  Go question found.
- **A trailing `> report.go` was refused.** The model kept "save it with
  `> report.go`" through every correction. `runnable` now verifies the
  pipeline before one trailing redirection to a plain file name. Any other
  `>` is still refused: a path, `..`, `2>&1`, two redirections, or one
  mid-pipeline.
- **Shell setup was refused as a pipeline (2026-11-17).** "In bash, is
  there a way of getting pipeline aware field completion" was answered
  with `eval '$(ssql -field-keybinding)'`, and the validator said "eval is
  not an ssql command" through two corrections. The model's line was
  wrong as well: in single quotes `$(...)` never runs. A fenced block whose
  every line is `eval "$(ssql -FLAG)"` (bare, or behind the codelab's
  `command -v ssql >/dev/null &&` guard) or a documented install command is
  now a setup block (`setupLines`, `pipeline.go`). Each eval is checked in
  the jail: ssql prints the script and `bash -n` parses it. An unknown flag
  fails in ssql's words, and a quoting other than double quotes goes back
  to the model as a correction. Install lines are shown, not run (status
  `setup`). The prompt names `eval "$(ssql -shell-init)"` and Ctrl-O, and
  the eval has the question as `doc-pipeline-completion`. The search also
  found `examples/cli-pipelines/README.md` still telling readers to run
  `ssql -bash-completion`, a flag ssql no longer has; it now says
  `-shell-init`.
- **The verified setup named the wrong key.** Asked live, the setup line
  checked out, but the reply said to press Ctrl-X Ctrl-F, the chord v4.47
  shipped. An old CHANGELOG section retrieved for the question still says
  so; the key has been Ctrl-O since. Two changes followed:
  - **The CHANGELOG is no longer retrieved.** The latest release's section
    is already in the facts block. Every older section reads as current,
    which is why DFCs and journals were already left out.
  - **`ssql -help-keybinding` is the authority on keys.** Its cheat-sheet
    rows (the KeyBindings table) go into the facts block. Any reply,
    prose included, that names a Ctrl or Alt key the cheat-sheet lacks
    goes back with the real list (`checkKeys`). Bash's own keys (Ctrl-C,
    Ctrl-R and the like) may be named freely.
- **"Whats the best codelab to do first" got the Go codelab
  (2026-11-17).** It was asked as a follow-up, with columns pasted. The
  facts block now says ssql is a CLI first, answered for the CLI unless the
  question asks about the Go package or Go code. It also carries the
  README's learning path (`readmeSection`), which is the authority on the
  order. The question is the eval case `doc-first-codelab`.
- **The eval's data cases move with any change to the prompt.** Sampling is
  deterministic, but every change to the system prompt shifts the same
  borderline answers. orders-per-month buckets to `2024-11-01T00:00:00Z`
  where the golden has `2024-11`. quickest-from-syd keeps a `src` column.
  flight-city-names keeps the codes. All three have the right rows in
  another shape. With the learning path the eval was 34/37. A one-line
  version without the path was 33/37, so the size of the addition is not
  the cause. Grading presentation (which columns, and how a month is
  written) separately from rows is the open question for phase 3.
- **Reading the log on 2026-11-21 turned up four things.**
  - **The log did not keep the answer**, so the prose answers (a GPU
    question about an RTX 5090 among them) could not be checked. The log
    line now has `reply`, clipped to 8 KB. That is the model's text, not
    the visitor's; the page's notice says answers are kept too.
  - **"Can you give me a analytic function example" failed three times.**
    It built the codelab's Fibonacci fold with `group-by` instead of
    `window`. No user doc said "analytic". The prompt's window example
    (`doc/ai-cli-generation.md` §5) and the codelab now name it, and the
    codelab's window examples have a heading of their own: they sat under
    "Dates". The docsearch chunker also splits a file at a `#` comment
    inside a code block, so that part of the codelab was filed under a
    bash comment. With the heading, the window section is the first user
    doc retrieved for the question. The eval has the question as
    `doc-analytic-function`.
  - **Every signal answer was an illustration.** The samples had no signal
    file, so each fft or spectrogram answer named one (`sensor_data.csv`,
    `chirp.csv`). Run by hand against a stand-in file, both answers used
    real flags, but nothing had checked them. The FAQ's samples now add the
    codelab's `signal.csv` (`samples.go`, built into
    `~/.cache/ssql-faq/samples`). An answer that reads it gets no
    playground link, because the playground lacks the file. The eval has
    `signal-top-frequencies`.
    Asked live, the first signal answer then ran fft at `-rate 1000`. The
    file is sampled at 100 Hz, so every frequency came out ten times too
    high. The run could not catch that. The prompt's file list now
    carries a note for each sample (`sampleNotes`). signal.csv's says
    100 Hz, and a test checks the note against the file's own `t` steps.
    Eval 36/39 after the notes (37/39 before them). orders-per-month
    flipped once more.
- **A review turn, and grading rows apart from shape (2026-11-21).** All
  three misses at 36/39 had the right rows in the wrong shape: a column
  the question did not ask for (orders-per-customer, quickest-from-syd),
  or a month written `2024-11-01T00:00:00Z` where it asked for `2024-01`.
  - **The review turn (`reviewed`, `answer.go`).** After a pipeline runs,
    the model is shown its first output lines beside the question once
    more. It replies OK, or gives a corrected answer. The correction
    replaces the first answer only when it passes every check. A failing
    correction, a refusal or a setup block keeps the first. `-review=false`
    turns it off, and the log marks `revised`.
  - **The eval grades rows apart from shape.** It reports "rows right",
    which counts a pipeline whose rows hold the golden's values with
    columns to spare (`rowsHold`). A value written another way is not a
    right row.
  - **Same build, back to back:**

    | | Pass | rows right | revised | p50 | p95 |
    |---|---|---|---|---|---|
    | without the review | 36/39 | 38/39 | 0 | 1.9 s | 5.3 s |
    | with the review | 38/39 | 39/39 | 2 | 2.6 s | 7.2 s |

    The review fixed orders-per-month and orders-per-customer, and broke
    nothing that passed. quickest-from-syd's review said OK with the extra
    `src` column still there.
  - **Asked live, a shape question failed before the review could run.**
    The model wrote `group-by customer_id name -count order_id count`,
    SQL's `COUNT(col)`. ssql's error said `"count" looks like the -count
    flag; flags need the dash`, which is wrong here: `-count` takes only a
    name, and it had taken `order_id`. Both corrections followed the
    wrong hint. Filed in TODO as an ssql error-message fix.
  - **The first question after hours idle takes ~16 s.** The model is kept
    loaded for only 30 minutes after a question, so it loads again. Left
    as it is, so it doesn't hold the laptop's GPU memory around the clock.

Replayed live, the sentence is refused with a 400. The Go question,
asked with and without pasted columns, is answered and verified. The
follow-up explains the optimiser and generator, and its example ran. A
data question with the same columns reads `data.csv`. The eval is
unchanged: 31/33, p50 1.8 s.

- **"What is the latest release?" got `ssql version`,** and the reply
  explained its own failed first draft ("the error occurred because I
  provided `curl`…"). The model had no source for the answer: retrieval
  found no CHANGELOG section, and when asked what changed, it described
  another release's section. The fixes:
  - the system prompt now has a **facts block**, read from the checkout at
    start-up: the latest release and its date (the CHANGELOG's first
    released heading), the install line, what `ssql version` really
    reports, and that release's CHANGELOG section (clipped to 3 KB). It
    follows every release with no prompt edit;
  - the correction turn now says the visitor never saw the failed answer.

  Two eval cases were added: `doc-latest-release` (`{{latest}}` is filled
  from the CHANGELOG) and `doc-latest-changes`. The latter names v4.148.0's
  subject, so it fails loudly at the next release until it is updated.
  The eval scores 33/35, with the same two model errors.

### 11.4 Next

- Show it to two or three people on the tailnet, and read the log.
- Phase 3: failed logged questions become eval cases, and the fixes go to
  the docs, the prompt or ssql's errors.
- Going public (Funnel or a tunnel) is deferred. It needs per-address
  rate limits, a cost ceiling, and a decision on abuse; see TODO.
