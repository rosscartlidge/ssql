# A Dynamic FAQ on a Local Model: Teaching Gemma to Answer for ssql

Reference: DFC169
Created: 2026-11-15
Last modified: 2026-11-16

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

## 9. Open questions for Ross

1. **Where it lives.** A panel in the playground page (it shares the data
   and the run button), or its own page?
2. **Go questions too, or CLI only at first?** CLI is where small models
   are strong (15/15). Go needs compile-verify, a heavier jail and 2–5 s
   builds, now smaller after DFC168.
3. **Visitors' own data:** header-paste only, or a small upload (≤1 MB,
   deleted after the answer)? Upload makes it far more useful and makes
   the jail matter more.
4. **The availability promise:** "when the laptop is on", or a cheap
   always-on box for the fallback search?
5. **Logging.** Is keeping the questions (no IPs) acceptable to say on the
   page?

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

Phase 2 is the service and the page (§7):
- the jail, since phase 1's sandbox is a scratch directory on the developer's
  machine;
- the queue;
- the tunnel.

The eval should grow toward the ~100 cases of §6 from real questions.
Ross's §9 questions still decide where the page lives and whether visitors
can bring their own data.
