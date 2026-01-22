# ssql Documentation Validation System

This document explains the 3-level documentation validation system that ensures code, examples, and documentation stay in sync.

---

## Overview

The validation system has **3 levels** of increasing thoroughness:

| Level | Target | Runtime | When to Run | What It Checks |
|-------|--------|---------|-------------|----------------|
| **Level 1** | `make doc-check` | ~10s | Every commit (pre-commit hook) | Syntax, links, patterns, basic compilation |
| **Level 2** | `make doc-test` | ~30-60s | Before push, in CI | godoc matches exports, functions documented |
| **Level 3** | `make doc-verify` | ~2-5min | Before releases, weekly CI | All API refs, full consistency, all examples |

---

## Level 1: Fast Checks (`make doc-check`)

**Purpose**: Catch obvious errors immediately

**Checks**:
1. ✅ Required documentation files exist
2. ✅ Old deprecated files removed
3. ✅ No outdated API patterns (NewRecord, Map, Filter, Take)
4. ✅ Go code examples have valid syntax
5. ✅ Complete examples compile successfully
6. ✅ **Complete examples RUN successfully** (with timeout)
7. ✅ All markdown links aren't broken
8. ✅ `go doc` references present in LLM docs
9. ✅ Critical API patterns documented
10. ✅ Error handling in I/O examples

**Runtime**: ~10 seconds

**Usage**:
```bash
# Run manually
make doc-check

# Install as pre-commit hook
make install-hooks
```

**Example Output**:
```
ssql Documentation Validation
======================================

1. Checking Documentation Files
----------------------------------------
✓ Found: doc/ai-code-generation.md
✓ Found: doc/AI-PROMPT-README.md
...

4. Validating Go Code Examples
----------------------------------------
✓ Compiles: doc/ai-code-generation.md - example_009.go
  ✓ Runs successfully: example_009.go
✓ Compiles: doc/ai-code-generation.md - example_011.go
  ✓ Runs successfully: example_011.go
...

Summary
----------------------------------------
Total checks: 62
Passed: 36
Warnings: 1
Failed: 0

✓ All documentation validation checks passed!
```

---

## Level 2: Medium Testing (`make doc-test`)

**Purpose**: Ensure godoc and exports match documentation

**Checks** (includes all Level 1 checks plus):
1. ✅ All exported functions are documented in LLM guides
2. ✅ All exported types are documented
3. ✅ Critical functions have godoc examples
4. ✅ Function signatures consistent between godoc and docs
5. ✅ Current API patterns present (MakeMutableRecord, Freeze, error handling)

**Runtime**: ~30-60 seconds

**Usage**:
```bash
make doc-test
```

**When to Run**:
- Before pushing to GitHub
- In CI pipeline
- After API changes

**Example Output**:
```
ssql Documentation Testing (Level 2)
==============================================

Running Level 1 Checks...
✓ Level 1 checks passed

1. Verifying Exported Functions are Documented
----------------------------------------
✓ Function documented: Select
✓ Function documented: Where
✓ Function documented: Limit
...

3. Verifying Critical Functions Have godoc Examples
----------------------------------------
✓ godoc has example: Select
✓ godoc has example: Where
✓ godoc has example: GroupByFields
...

✓ Level 2 documentation testing passed!
```

---

## Level 3: Deep Verification (`make doc-verify`)

**Purpose**: Comprehensive validation for releases

**Checks** (includes all Level 2 checks plus):
1. ✅ ALL examples in api-reference.md compile
2. ✅ Cross-reference all documented functions exist in code
3. ✅ README examples use current API
4. ✅ Consistency across all documentation files
5. ✅ Chart examples are current
6. ✅ All import statements correct
7. ✅ go.mod specifies Go 1.23+

**Runtime**: ~2-5 minutes

**Usage**:
```bash
make doc-verify

# Or as part of release workflow
make release
```

**When to Run**:
- Before creating a release
- Weekly in CI (scheduled)
- After major documentation updates

**Example Output**:
```
ssql Documentation Verification (Level 3)
=================================================

Running Level 2 Tests...
✓ Level 2 tests passed

1. Verifying ALL Examples in api-reference.md
----------------------------------------
✓ API example compiles: api_example_001.go
✓ API example valid syntax: api_example_002.go
...
API Reference: 45/50 examples validated

2. Cross-Referencing Documented Functions
----------------------------------------
✓ Documented function exists: Select
✓ Documented function exists: Where
...

✓ Level 3 documentation verification passed!
✓ Ready for release!
```

---

## Complete Workflows

### Developer Workflow

```bash
# 1. Make changes to code/docs
vim core.go
vim doc/ai-code-generation.md

# 2. Run fast validation
make doc-check

# 3. Commit (pre-commit hook runs doc-check automatically)
git add .
git commit -m "Update documentation"

# 4. Before pushing, run medium validation
make doc-test

# 5. Push (CI will run doc-test again)
git push
```

### CI Pipeline

```yaml
# .github/workflows/ci.yml
jobs:
  validate:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v3
      - uses: actions/setup-go@v4
        with:
          go-version: '1.23'

      - name: Run CI validation
        run: make ci  # Runs: fmt, vet, test, doc-check, doc-test
```

### Release Workflow

```bash
# 1. Run comprehensive validation
make release  # Runs: fmt, vet, test, doc-check, doc-test, doc-verify

# 2. If passed, create release
git tag vX.Y.Z
git push origin vX.Y.Z
```

---

## What Each Level Catches

### Real Examples of Issues Caught

**Level 1 catches**:
- ❌ Using `NewRecord().Build()` instead of `MakeMutableRecord().Freeze()`
- ❌ Using `ssql.Map()` instead of `ssql.Select()`
- ❌ Broken links to moved files
- ❌ Examples that don't compile
- ❌ Missing error handling in CSV/JSON reads

**Level 2 catches**:
- ❌ New exported function not documented in LLM guides
- ❌ Missing godoc example for critical function
- ❌ Function signature changed but docs not updated

**Level 3 catches**:
- ❌ Example in api-reference.md uses outdated API
- ❌ Documented function no longer exists in code
- ❌ Inconsistency between README and LLM docs
- ❌ Missing import in code examples

---

## Installation

### Set Up Pre-Commit Hook

Automatically run Level 1 validation before every commit:

```bash
make install-hooks
```

This creates `.git/hooks/pre-commit` that runs `make doc-check`.

To bypass (not recommended):
```bash
git commit --no-verify
```

---

## Customization

### Adding New Checks

To add checks to Level 1, edit `scripts/validate-docs.sh`:

```bash
section "New Check Category"

for file in "${files[@]}"; do
    if check_something "$file"; then
        pass "Check passed for $file"
    else
        fail "Check failed for $file"
    fi
done
```

### Adjusting Thresholds

Edit the scripts to adjust what counts as pass/fail:

```bash
# In validate-docs.sh
if [[ $compiled_examples/$total_examples > 0.8 ]]; then
    pass "80% of examples compile"
else
    fail "Less than 80% compile"
fi
```

---

## Troubleshooting

### "Script not found" Error

```bash
chmod +x scripts/*.sh
```

### Level 2 or 3 Fails But Level 1 Passes

This is expected! Higher levels are more strict. Fix the issues reported.

### Too Many False Positives

Some checks may flag intentional "wrong examples" (showing what NOT to do). The scripts try to filter these out by looking for markers like `❌`, `Wrong`, `NOT`, etc.

If you have a legitimate use case that's being flagged, you can:
1. Add appropriate markers (`// ❌ WRONG:`)
2. Adjust the grep filters in the script

### Examples Timeout During Execution

Level 1 runs examples with a 2-second timeout. If your example legitimately needs longer:

```bash
# In validate-docs.sh, change:
timeout 2s "$compiled_bin"
# To:
timeout 5s "$compiled_bin"
```

---

## Integration with CI

### GitHub Actions

```yaml
name: Documentation Validation

on: [push, pull_request]

jobs:
  docs:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v3
      - uses: actions/setup-go@v4
        with:
          go-version: '1.23'

      - name: Level 1 & 2 Validation
        run: make doc-test

  docs-deep:
    runs-on: ubuntu-latest
    if: github.ref == 'refs/heads/main'
    steps:
      - uses: actions/checkout@v3
      - uses: actions/setup-go@v4
        with:
          go-version: '1.23'

      - name: Level 3 Verification (main branch only)
        run: make doc-verify
```

### Weekly Deep Validation

```yaml
name: Weekly Documentation Audit

on:
  schedule:
    - cron: '0 0 * * 0'  # Every Sunday at midnight

jobs:
  audit:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v3
      - uses: actions/setup-go@v4
        with:
          go-version: '1.23'

      - name: Deep Verification
        run: make doc-verify

      - name: Create issue if failed
        if: failure()
        uses: actions/github-script@v6
        with:
          script: |
            github.rest.issues.create({
              owner: context.repo.owner,
              repo: context.repo.repo,
              title: 'Weekly documentation validation failed',
              body: 'The weekly deep validation found issues. Run `make doc-verify` locally for details.'
            })
```

---

## Benefits

### For Developers
- ✅ Catch documentation errors before committing
- ✅ Confidence that examples actually work
- ✅ No more "the docs are outdated" issues

### For Users
- ✅ Copy-paste examples that actually work
- ✅ Documentation always matches current API
- ✅ Clear error messages when things change

### For Maintainers
- ✅ Automated validation prevents drift
- ✅ Clear quality gates for releases
- ✅ Less time fixing documentation bugs

---

## Summary

Run the appropriate level based on your needs:

```bash
# Quick check before commit
make doc-check        # ~10s

# Thorough check before push
make doc-test         # ~1min

# Comprehensive check before release
make doc-verify       # ~3min

# Complete workflows
make all              # Pre-push: fmt + vet + test + doc-check
make ci               # CI pipeline: all + doc-test
make release          # Release: ci + doc-verify
```

**Install the pre-commit hook to run Level 1 automatically:**
```bash
make install-hooks
```

---

*For questions or issues with the validation system, see the scripts in `scripts/` directory.*
