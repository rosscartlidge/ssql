# Documentation validation: the three levels

How `doc/` is kept in step with the code. Three scripts, three Makefile
targets, each level running the one below it. (This replaces the old
`doc/VALIDATION.md`, which listed ten checks, showed a CI workflow that
never existed, and was a contributor doc sitting among user docs; moved
here under DFC140, 2026-09-29.)

| Level | Target | Script | Runtime | When |
|---|---|---|---|---|
| 1 | `make doc-check` | `scripts/validate-docs.sh` | ~10 s | every commit (pre-commit hook), part of `make all` |
| 2 | `make doc-test` | `scripts/doc-test.sh` | ~1-2 min | before tagging a release (pre-tag gate since 2026-09-29); after any codelab edit |
| 3 | `make doc-verify` | `scripts/doc-verify.sh` | ~2-5 min | before releases, after large API-doc changes |

Nothing in CI runs these: `.github/workflows/` has only `playground.yml`
and `release.yml`. The pre-commit hook is installed with
`make install-hooks` (writes `.git/hooks/pre-commit` running `doc-check`).

## Level 1: `validate-docs.sh` (11 checks)

1. **Required files exist**: `doc/ai-code-generation.md`,
   `doc/ai-human-guide.md`, `doc/api-reference.md`, `README.md`.
2. **Tombstones**: files deliberately removed from `doc/` must stay
   removed (`old_files`). Add to this list whenever a doc is deleted or
   moved out, so a stale branch or an old script cannot recreate it.
3. **Outdated API patterns** in the docs (`NewRecord`, `Map`, `Filter`,
   `Take`, old import paths …).
4. **Go code blocks compile and run** for a fixed list of files in the
   script (README, api-reference, the LLM prompts, install, library-tour …).
   The codelabs are NOT in this list; they are Level 2.
5. **Markdown links resolve** (relative paths and anchors).
6. **`go doc` references** present in the LLM docs.
7. **Critical API patterns documented** (`MakeMutableRecord`, `Freeze`,
   `GetOr`, error handling …).
8. **Error handling** shown in I/O examples.
9. **DFC metadata** (`Reference:` / `Created:` / `Last modified:`) on every
   `doc/research/*.md`; `scripts/dfc.py --stamp && --index` before
   committing research-doc edits.
10. **Release pins**: the `.deb` URLs in `README.md` and `doc/install.md`
    match `cmd/ssql/version/version.txt` and the committed `.deb` files
    (`make deb` rewrites the pins).
11. **API reference coverage** (`scripts/api-coverage.sh`): every exported
    function and method of `ssql` and `ssql/typed` is mentioned in
    `doc/api-reference.md` / `doc/typed-reference.md`; exemptions in
    `doc/api-reference-exclude.txt`. Since DFC140 batch 5 it requires a
    signature line: `func Name` for a function, `func (r *Type) Method`
    for a method, at the start of a line (inside a code block); a bare
    mention in prose does not count.

## Level 2: `doc-test.sh`

Runs Level 1, then:

1. Exported functions documented in the LLM guides.
2. Exported types documented.
3. Critical functions have godoc examples.
4. Function signatures consistent between godoc and the docs.
5. Current API patterns present in examples.
6. **Every code block of every codelab on the learning path runs** against
   the current checkout: `doc/cli-codelab.md` and
   `doc/cli-signal-processing.md` through `doc/codelab-data/codelab-run.sh`
   (fresh binary, a copy of the fixture directory; a block passes on exit 0
   with non-empty output, so it does not compare output text), and
   `doc/codelab-intro.md` / `doc/typed-codelab.md` through
   `scripts/codelab-go-run.sh` (throwaway module with a `replace` to this
   tree). The list mirrors `TestCodelabRuns` in `cmd/ssql/codelab_test.go`;
   keep the two equal. A block whose first line is
   `# codelab: skip — reason` is skipped and the reason printed.

## Level 3: `doc-verify.sh`

Runs Level 2, then: every example in `api-reference.md` compiles;
every documented function exists in the code; README examples use the
current API; consistency across the documentation files; import
statements; `go.mod` Go version.

## Typical use

```bash
make doc-check          # before every commit (the hook does this)
make doc-test           # after editing a codelab; before tagging
make doc-verify         # before a release with large API-doc changes
make all                # fmt + vet + test + doc-check
```

When a check fails, the script prints the file and the offending line or
example number; the compiled examples are extracted to a temp directory
whose path is in the failure output.
