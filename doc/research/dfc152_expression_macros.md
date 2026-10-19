# Expression macros: named expressions with field-completing arguments

Reference: DFC152
Created: 2026-10-19
Last modified: 2026-10-20

[Back to Index](./README.md)

## 1. The ask

Ross, 2026-10-19: "expr macros that tie into the completion. Imagine
`add3(x,y,z) -> x+y+z`, and that integrating with completion so that
`ssql update set-macro add3 s3 f1 f2 f3` sets s3 to f1+f2+f3, and add3
can complete on Tab and f1, f2, f3 can field-complete on Ctrl-O. I
assume we would need a macro definition syntax and a location like
`~/.ssql.conf` (or similar)."

Two things are being asked for, and they are separable:

1. **A named expression** written once, used many times, instead of
   retyping `(price - cost) / price` in every pipeline.
2. **Field completion for its arguments.** Inside a quoted
   `-set-expr` string the shell cannot complete anything, which is the
   same reason `-if-field`, `-set-field` and `-param-field` exist
   ([DFC135 §3.5](./dfc135_field_references_in_value_slots.md)): a
   field reference in its own argv slot is a `FieldsFromFlag` slot.

The second is the one that decides the shape. A macro whose arguments
live inside expression text gives no completion; a macro whose
arguments are argv slots does.

## 2. What already exists

The pieces are in the tree under other names. This DFC mostly names
the composition.

- **`-param-field NAME TYPE FIELD`** (DFC135 §3.3, where and update)
  binds a column, per row, cast to a declared type, as an identifier
  of the clause's expressions. It is lowered in every lane (exec env,
  record transpiler binding, typed `r.C` with cast, SQL `CAST(c AS …)`,
  generate ssql verbatim), FIELD is a `FieldsFromFlag` slot, and a
  column whose name is not an expression identifier (`1st`, `a.b`)
  is reachable through it. A macro call is exactly a `-set-expr` whose
  text is the macro body plus one `-param-field` per argument.
- **`-set-bucket FIELD SOURCE WIDTH`** is the precedent for a flag
  that is the spelling of a `-set-expr` (`bucketSetExpr` in
  `commands/update.go`, DFC121). It is desugared in each consumer
  that reads update's argv: exec, `generate sql`, and the field-reading
  walk in `generate ssql`. Three sites know the desugaring, which is
  fine for a fixed one and wrong for one that depends on a user file
  (§5).
- **A command rewriting its own fragment argv.** A source resolves its
  zone and writes `-tz NAME` into `Op.Argv` and `Command` so every
  generate target pins it (DFC147, `ResolvedZone` in
  `lib/codefragment.go`). The fragment's `Command` is otherwise built
  from `os.Args` (`getCommandString`, `commands/helpers.go`), so what
  the generators re-parse is what the user typed, unless the command
  says otherwise.
- **Completion** is autocli: a flag's args are declared with
  completers (`StaticCompleter`, `FieldsFromFlag`), Tab on a flag name
  lists flags, Ctrl-O completes a field at the cursor through
  `SSQL_MODE=schema` (`claude/completion-system.md`).
- **No config file exists.** ssql reads thirteen `SSQL_*` environment
  variables, all but `SSQL_MODE`, `SSQL_DEBUG` and `SSQL_SCHEMA_SAMPLE`
  for tests and development. A macro file is ssql's first per-user
  file, which is a bigger decision than the macros (§5).

## 3. Design

### 3.1 The definition file

One macro per line, a name, typed parameters and a body:

```
# ~/.config/ssql/macros
add3(x int, y int, z int) = x + y + z
margin(price float, cost float) = (price - cost) / price
overdue(due time, grace int) = due < now() - duration(string(grace) + "h")
```

- **Name**: an expression identifier (`exprParamName`), not an
  expr-lang keyword, not the name of a built-in or ssql function
  (`len`, `bucket`, `date_trunc`, …), so a macro can never shadow a
  function a lane translates. Duplicate names are a load error.
- **Parameters**: `name type`, type one of `string int float bool
  time`, the `-param-field` set. Declared, never inferred (DFC134's
  rule). A parameter is monomorphic, but `-param-field` casts the
  column to the declared type, so a `float` macro serves an int column.
- **Body**: expression text in the expr-lang the clause uses. At load
  the body is compiled once with the parameters as its only
  environment, so an unknown identifier, a wrong arity to a built-in or
  a syntax error is reported as `macros:3: add3: …` before any pipeline
  runs. A body may not reference a field that is not a parameter: a
  macro sees only its arguments.
- **Location**: `$SSQL_MACROS` if set, else
  `$XDG_CONFIG_HOME/ssql/macros`, else `~/.config/ssql/macros`. An
  absent file is an empty macro set. `SSQL_MACROS=/dev/null` turns
  macros off for one command, which tests and the fuzzers use.

Why its own file and not `~/.ssql.conf`: a general config file would
be read by every command and would need a grammar for every setting
anyone later wants (zone, mode, sample size). Nothing today needs
that, and a file that silently changes what a pipeline means on one
machine is the thing DFC115 warns about. A macros file holds one kind
of thing with one grammar; if a general config file is ever wanted it
can include the macros file, not the other way round.

### 3.2 The flags

```
ssql update -set-macro FIELD NAME ARG...      # FIELD = NAME(ARG...)
ssql where  -if-macro NAME ARG...             # condition NAME(ARG...)
ssql update -if-macro NAME ARG... -set …      # update's clause condition
```

- `FIELD` first, matching every other `-set-*` flag (`-set FIELD
  VALUE`, `-set-expr FIELD EXPR`, `-set-field FIELD SOURCE`,
  `-set-bucket FIELD SOURCE WIDTH`).
- `NAME` completes from the macro file (a completer that loads the
  file and offers the names, with each macro's signature as its
  description where the completer supports one).
- `ARG...` is a repeating `FieldsFromFlag` slot whose count the
  command decides (Ross, 2026-10-20: a variadic tail "means you don't
  know how many args the macro takes"). autocli v4.22.0 gained
  `Arg(...).Repeat(f)`: the last Arg takes N values, N returned by `f`
  from the fixed args before it, and ssql's `f` is the macro's
  parameter count. The parser consumes exactly N words (`-set-macro s3
  add3 a b` fails as "requires 3 value(s) for args, got 2", an unknown
  name fails before any value is read), completion offers a field at
  positions 1..N and flag names after, help-at marks `→ args...`.
  Neither a variadic-tail guess (where do the arguments end?) nor one
  flag per macro (a user file rewriting a command's grammar) was taken.
- Every ARG is a field. A literal argument (`margin price 0.4`) is
  not supported in this DFC: a slot that is sometimes a field and
  sometimes a literal is the sigil problem DFC135 §1 removed. A
  constant goes in the macro body or is a `-param` in the same clause
  (the body may use any `-param NAME` the clause declares, since the
  lowering puts both in one clause; §7 asks whether that is wanted).
- `-if-macro` requires a body that is boolean; `-set-macro` accepts
  any result type. Checked at use, not at load, since the same macro
  could serve both.

### 3.3 Lowering: once, in the command, into the fragment

`-set-macro s3 add3 f1 f2 f3` is rewritten by `update` itself to

```
-set-expr s3 'x + y + z' -param-field x int f1 -param-field y int f2 -param-field z int f3
```

and `-if-macro overdue due_at grace_h` by `where`/`update` to

```
-if-expr 'due < now() - duration(string(grace) + "h")' -param-field due time due_at -param-field grace int grace_h
```

The rewrite happens before clause parsing (the command's own argv,
after autocli has matched the flags), and the command writes the
lowered argv into its fragment's `Op.Argv` and `Command`, the way a
source writes `-tz NAME` (DFC147). Consequences:

- **Exec, record, typed, parallel and SQL lanes need no change.** They
  see `-set-expr` and `-param-field`, which DFC134/DFC135 already
  lowered in all of them. There is no transpiler, template or SQL
  translator work, and `generate sql`'s re-parse of update's argv
  (the DFC115 legacy exception) never meets a macro.
- **`generate ssql` emits the expanded form.** A saved pipeline is
  self-contained; it does not depend on the macro file of the machine
  it is later run on. The macro name is lost in the saved text; §7.
- **`generate go` programs are self-contained** for the same reason.
- **The `-set-bucket` pattern is not followed.** Desugaring in each
  consumer would put the macro file's contents into three parsers.

Parameter names stay the macro's own (`x`, `y`, `z`), because a
`-param-field` name is an identifier of the body and renaming would
mean rewriting the body's AST (ssql has no expression printer). That
met DFC134's rule that a parameter must not be a field of the input on
the first smoke run: `margin(price float, cost float)` applied to
columns `price` and `cost` — the natural way to write a macro — was a
collision error in every lane. The rule's reason is that an expression
could mean either the column or the parameter, and a new upstream
column must not flip it. A macro body cannot: every identifier in it
is a parameter by construction (§3.1), so the input's columns are not
in play. The rule gained that exact exemption (built 2026-10-20): **a
field parameter in a closed expression — one whose every identifier is
a declared parameter — may be named like a column.** It is decided
where the check lives, from the expression's own identifiers: the
runtime binding on the first record (`paramBinding.closed`, exec and
the VM tiers), the transpiler env (`exprGoEnv.closed`, record and
typed codegen); SQL never checked. A static `-param` named like a
column stays an error (a value is not a view of the column), and so
does any expression that also names a column. Two macro calls in one
clause that declare the same parameter name, or a call and the clause's
own `-param`, are reported by name with "call it in its own clause (+)
or rename the parameter".

### 3.4 Completion detail

| Position | Completer | Source |
|---|---|---|
| `-set-macro`, `-if-macro` | flag-name Tab | the command's flag list, as today |
| FIELD (set target) | `FieldsFromFlag("")` | existing field completion; a new name is also fine |
| NAME | macro completer | the macro file, loaded on each completion call |
| ARG… | `FieldsFromFlag("")`, variadic | existing field completion, Ctrl-O through schema mode |

`-help` for the flag lists the macro names and signatures found, so
`ssql update -help` on a machine shows what that machine's file
defines. `-spec-json` lists the flags with their arg kinds, as DFC135
did; the NAME slot's kind is `macro`.

## 4. Lanes

| Lane | What it sees | Work |
|---|---|---|
| exec | `-set-expr` + `-param-field` after the rewrite | the rewrite itself |
| record codegen | same, through the fragment | none |
| typed / parallel codegen | same | none |
| generate sql | same (`Op.Argv` is the lowered form) | none |
| generate ssql | same; emits the expanded form | none; one test that the output is self-contained |
| serve / run / Alt-r | the command runs on the serving machine, reads its file | §5 |

Gate: one `TestPipelineEquivalence` case per flag with
`SSQL_MACROS` pointing at a fixture, `Golden` on a shuffled fixture.
The equivalence harness already runs every lane with the same
environment, so a lane that re-parsed the original argv instead of the
lowered form would fail on `-set-macro` being unknown, which is the
failure the gate should be watched producing once (remove the fragment
rewrite and run it).

## 5. The file is outside the pipeline

The principle (DFC115, matured 2026-08-29): what a pipeline means is
its text. A macro file breaks that by one step, as a bash alias does
for a shell line. Where it bites:

| Context | What happens | Answer |
|---|---|---|
| Interactive shell | reads the user's file | the intended use |
| `generate go` / `generate sql` / `generate ssql` output | expanded at generation time | self-contained (§3.3) |
| `ssql run DOC` | each stage runs `ssql update …` on this machine | reads this machine's file; a document that uses a macro is not portable unless expanded |
| `serve` workspace | stages run on the serving host | the serving host's file, not the browser user's; the bar shows `-set-macro`, the generate tab shows the expansion |
| `from ssh HOST 'pipeline'` | the pipeline text runs on HOST | HOST's file. `BuildRemoteCommand` could expand before shipping; deferred until wanted |
| WebVM / playground | no home directory | no macros; `-set-macro` errors "no macros defined" |
| Codelab | `~/csvs/ssql-codelab` is the data dir, not config | a codelab step can `export SSQL_MACROS=…` |

The rule this DFC adopts: **a macro is a keyboard convenience; anything
saved is expanded.** `generate ssql` is the save path, and it emits the
expansion. A `run` document that keeps `-set-macro` is the author's
choice to depend on the file, the same choice as a document that
depends on a catalog path.

## 6. What this is not

- **Not expr-level macros.** `-set-expr s3 'add3(f1, f2, f3)'`
  expanded by an AST patcher (the DFC036 pattern) would work in every
  expression slot with no new flags, but the arguments would sit inside
  quoted text (no completion, the thing asked for), identifiers in
  the text would be spliced (the DFC134 problem), and each lane parses
  expression text separately (`exprToGo`, `exprToSQL`, the VM), so
  the expansion would have to run in each. A later DFC can add it on
  top of the same file if wanted; the file format does not preclude
  it.
- **Not a general config file.** §3.1.
- **Not user-defined functions in generated Go.** The expansion is
  inline; a macro used twice is two expressions. Fine at this scale.
- **Not aggregate macros.** `group-by -expr` has its own grammar
  (DFC036); macros there are a separate question.

## 7. Open questions for Ross

Answered 2026-10-20 (plan mode): 1 expand by default, `-as-written`
keeps the call; 2 parameters only; 3 yes, both commands; 4 `list` and
`check`. Plus the arity decision recorded in §3.2.

1. **`generate ssql` loses the macro name.** The expansion is correct
   and self-contained, but `ssql update -set-expr s3 'x + y + z'
   -param-field x int f1 …` reads worse than `-set-macro s3 add3 f1 f2
   f3`. Argv has no comments, so the name cannot ride along. Options: emit
   `-set-macro` only under `generate ssql -as-written` (which already
   exists for `-tz`), expanded otherwise. Proposed: `-as-written` keeps
   the macro, default expands.
2. **May a body use the clause's `-param`s?** It falls out of the
   lowering (one clause, one environment). It makes a macro's meaning
   depend on the call site, which a reader of the file cannot see.
   Proposed: no; the load-time compile rejects any identifier that is
   not a parameter, and a constant is a parameter or a literal in the
   body.
3. **`-if-macro` in `update` as a clause condition**, alongside `-if`,
   `-if-field`, `-if-expr`: proposed yes, it is the same rewrite.
4. **A `ssql macro` command** (`list`, `show NAME`, `check FILE`) so
   the file is queryable through a protocol rather than by reading it
   (DFC115). Proposed: `ssql macro list` and `ssql macro check` in
   this change, since `check` is the load-time compile and `list` is
   what the completer runs; no `add`/`remove` (the file is the user's).

## 8. Plan and size

| Piece | Where | Lines (est.) |
|---|---|---|
| Loader: locate, parse lines, compile bodies, errors with line numbers | `commands/macros.go` | 120 |
| `-set-macro` / `-if-macro` flags, arity and boolean checks, rewrite into argv and fragment | `update.go`, `where.go`, `expr_param.go` helpers | 120 |
| Macro-name completer; `-help` listing | `macros.go` | 40 |
| `ssql macro list|check` | `commands/macro.go`, both `main.go` files | 60 |
| Tests: loader table, rewrite unit, equivalence cases, generate ssql self-contained, completion config, `TestFieldCompletionConfiguration` entries | `*_test.go` | 150 |
| Docs: cli-codelab paragraph, cli-reference, EXPRESSIONS.md, CHANGELOG, TODO | | 60 |

About 550 lines, no lane touched. Order: loader and `macro check`
first (the file format is the part users will keep), then `-set-macro`
with its equivalence case, then `-if-macro`, then completion and docs.

## 9. Tests (as planned; §10 has what was built)

- Loader: good lines, each error class (bad name, keyword, duplicate,
  unknown type, body references non-parameter, body syntax error) with
  the line number in the message; `SSQL_MACROS` unset and file absent
  gives an empty set; `SSQL_MACROS=/dev/null` likewise.
- Rewrite: `-set-macro s3 add3 f1 f2 f3` argv becomes the expected
  `-set-expr`/`-param-field` argv; wrong arity; non-boolean body under
  `-if-macro`; parameter name colliding with an input field.
- Equivalence: `macro_set_add3` (Golden), `macro_if_overdue`
  (Golden), `macro_set_float_on_int_columns` (the cast), all with
  `SSQL_MACROS` set to a fixture under `testdata/`.
- `generate ssql` of a macro pipeline contains no `-set-macro` and
  runs identically with `SSQL_MACROS=/dev/null`.
- Completion: `ssql -complete N update -set-macro s3 ""` lists the
  fixture's macro names; the ARG positions are field completers
  (`TestFieldCompletionConfiguration`).
- Slim build: the loader has no new imports; `go test -tags slim`.

## 10. Built (2026-10-20)

autocli v4.22.0 (`Arg(...).Repeat`, `FlagSpec.ArgRepeat`, `ArityFunc`;
parser, completion, help/man/help-at/spec-json; `arg_repeat_test.go`).
ssql: `commands/macros.go` (file, loader, arity callback, name
completer), `commands/macro_flags.go` (`-set-macro`, `-if-macro`,
`expandClauseMacros` once per command before the exec/generate branch,
`lowerMacroArgv` + `applyMacroLowering` at the four fragment emit sites,
`restoreAsWritten`), `commands/macro_cmd.go` (`ssql macro list|check`),
`lib.CodeFragment.AsWrittenArgv`, the closed-expression exemption
(§3.3) in `lib/runtime/params.go` and `commands/expr_go.go`. Gates:
five `TestPipelineEquivalence` cases (`macro_*`, `SSQL_MACROS` through
the case `Env`, which now takes `{{.data}}`), watched failing with the
fragment rewrite disabled (DuckDB lane); `TestGenerateSSQLAsWritten`
macro pipeline; `TestFieldKeybindingPTY` row for Ctrl-O on the third
macro argument; loader, lowering and completion-table unit tests.
Deferred: expanding macros before `BuildRemoteCommand` ships a pipeline
to a `from ssh` host (TODO).
