# ssql Subcommand Migration - Production Checklist

## Overview

Migrate ssql from custom subcommand routing to native completionflags subcommand support.

**Target Release:** v1.2.0
**Estimated Time:** 10-12 hours
**Risk Level:** Low (incremental approach)

---

## Phase 1: Preparation (2 hours)

### 1.1 Update Dependencies ✅

- [ ] Update completionflags to latest version
  ```bash
  cd /home/rossc/src/ssql
  go get github.com/rosscartlidge/completionflags@latest
  go mod tidy
  ```

- [ ] Verify update was successful
  ```bash
  go list -m github.com/rosscartlidge/completionflags
  # Should show latest version with subcommand support
  ```

- [ ] Check for API breaking changes
  ```bash
  go build ./...
  # Fix any compilation errors
  ```

**Expected Output:** Clean build with updated dependency

---

### 1.2 Study Examples (30 min)

- [ ] Review `/home/rossc/src/completionflags/examples/subcommand/main.go`
  - Understand root command structure
  - Study `.Subcommand()` API
  - Note `.Build()` and `.Execute()` pattern

- [ ] Review `/home/rossc/src/completionflags/examples/subcommand_clauses/main.go`
  - Understand clause support with subcommands
  - Study three-level flag scoping (root global, subcommand global, local)
  - Note how handlers access context

- [ ] Document key patterns
  ```go
  // Root global flags - available everywhere
  Flag("-verbose").Bool().Bind(&verbose).Global().Done()

  // Subcommand definition
  Subcommand("query").
      Description("...").
      Flag("-filter").Local().Done().  // Per-clause
      Handler(func(ctx *cf.Context) error { ... }).
      Done()
  ```

**Expected Output:** Clear understanding of API patterns

---

### 1.3 Create Migration Branch (15 min)

- [ ] Create feature branch
  ```bash
  git checkout -b feature/native-subcommands
  ```

- [ ] Create tracking document
  ```bash
  mkdir -p doc/migration
  touch doc/migration/subcommand_progress.md
  ```

- [ ] Document initial state
  ```bash
  # Count lines in files to be deleted
  wc -l cmd/ssql/main.go
  wc -l cmd/ssql/commands/registry.go
  wc -l cmd/ssql/commands/*.go
  ```

**Expected Output:** Clean branch ready for work

---

## Phase 2: Proof of Concept (3 hours)

### 2.1 Create New Main Structure (1 hour)

Create `cmd/ssql/main_v2.go` with root command and 3 simple subcommands.

- [ ] Create file skeleton
  ```go
  package main

  import (
      "fmt"
      "os"
      cf "github.com/rosscartlidge/completionflags"
      "github.com/rosscartlidge/ssql"
      "github.com/rosscartlidge/ssql/cmd/ssql/version"
  )

  func buildRootCommand() *cf.Command {
      var verbose bool

      return cf.NewCommand("ssql").
          Version(version.Version).
          Description("Unix-style data processing tools").

          // Root global flags
          Flag("-verbose", "-v").
              Bool().
              Bind(&verbose).
              Global().
              Help("Enable verbose output").
              Done().

          // TODO: Add subcommands

          Build()
  }

  func mainV2() {
      cmd := buildRootCommand()
      if err := cmd.Execute(os.Args[1:]); err != nil {
          fmt.Fprintf(os.Stderr, "Error: %v\n", err)
          os.Exit(1)
      }
  }
  ```

- [ ] Add 3 simple subcommands to POC:
  - **limit** - simplest, just one `-n` flag
  - **offset** - similar to limit
  - **distinct** - simple, no complex flags

**File:** `cmd/ssql/main_v2.go`

---

### 2.2 Implement "limit" Subcommand (30 min)

- [ ] Extract current limit logic from `commands/limit.go`
- [ ] Add to main_v2.go:
  ```go
  Subcommand("limit").
      Description("Limit the number of records (SQL LIMIT)").

      Flag("-n").
          Int().
          Required().
          Global().
          Help("Maximum number of records to output").
          Done().

      Handler(func(ctx *cf.Context) error {
          n := ctx.GlobalFlags["-n"].(int)

          // Read from stdin
          input := lib.ReadJSONL(os.Stdin)

          // Apply limit
          limited := ssql.Limit[ssql.Record](n)(input)

          // Write to stdout
          return lib.WriteJSONL(os.Stdout, limited)
      }).
      Done()
  ```

- [ ] Test limit subcommand
  ```bash
  # Build with POC
  go build -o ssql_v2 -tags=v2 ./cmd/ssql

  # Test
  echo '{"id":1}
  {"id":2}
  {"id":3}' | ./ssql_v2 limit -n 2

  # Expected: First 2 records only
  ```

**Expected Output:** Limit command works identically to current version

---

### 2.3 Implement "offset" Subcommand (20 min)

- [ ] Extract offset logic from `commands/offset.go`
- [ ] Add to main_v2.go following same pattern as limit
- [ ] Test offset subcommand
  ```bash
  echo '{"id":1}
  {"id":2}
  {"id":3}' | ./ssql_v2 offset -n 1

  # Expected: Last 2 records (skip first)
  ```

**Expected Output:** Offset command works correctly

---

### 2.4 Implement "distinct" Subcommand (20 min)

- [ ] Extract distinct logic from `commands/distinct.go`
- [ ] Add to main_v2.go
- [ ] Test distinct subcommand
  ```bash
  echo '{"name":"Alice"}
  {"name":"Bob"}
  {"name":"Alice"}' | ./ssql_v2 distinct

  # Expected: Only unique records
  ```

**Expected Output:** Distinct command works correctly

---

### 2.5 Test Completion (30 min)

- [ ] Generate completion script
  ```bash
  ./ssql_v2 -completion-script > /tmp/ssql_v2_completion.sh
  ```

- [ ] Source completion script
  ```bash
  source /tmp/ssql_v2_completion.sh
  ```

- [ ] Test subcommand completion
  ```bash
  ssql_v2 lim<TAB>    # Should complete to "limit"
  ssql_v2 off<TAB>    # Should complete to "offset"
  ssql_v2 dis<TAB>    # Should complete to "distinct"
  ```

- [ ] Test flag completion
  ```bash
  ssql_v2 limit -<TAB>    # Should show "-n" and "-help"
  ssql_v2 -<TAB>          # Should show "-verbose", "-version", etc.
  ```

- [ ] Test help
  ```bash
  ssql_v2 -help           # Should show all subcommands
  ssql_v2 limit -help     # Should show limit-specific help
  ```

**Expected Output:** All completion and help working correctly

---

### 2.6 POC Review & Decision (30 min)

- [ ] Compare POC vs current implementation
  - Code complexity
  - Lines of code
  - User experience
  - Performance

- [ ] Test POC in pipeline
  ```bash
  cat data.jsonl | ./ssql_v2 limit -n 10 | ./ssql_v2 offset -n 5
  ```

- [ ] Document findings in `doc/migration/poc_results.md`

- [ ] **Decision Point:** Proceed with full migration?
  - ✅ **YES** → Continue to Phase 3
  - ❌ **NO** → Document issues, iterate on POC

**Expected Output:** Clear go/no-go decision

---

## Phase 3: Full Migration (4 hours)

### 3.1 Migrate Simple Commands (1 hour)

Commands with no clause support, simple flags only.

**Commands to migrate:**
1. **select** - `-field` flag (accumulate)
2. **sort** - `-field`, `-desc` flags
3. **reverse** - no flags

**Pattern for each:**
```go
Subcommand("select").
    Description("Select specific fields from records").

    Flag("-field", "-f").
        String().
        Accumulate().
        Global().
        Help("Field to include (can specify multiple)").
        Done().

    Handler(func(ctx *cf.Context) error {
        // Extract fields
        var fields []string
        if fieldsRaw, ok := ctx.GlobalFlags["-field"]; ok {
            for _, f := range fieldsRaw.([]interface{}) {
                fields = append(fields, f.(string))
            }
        }

        // Apply select operation
        input := lib.ReadJSONL(os.Stdin)
        selected := ssql.Select(func(r ssql.Record) ssql.Record {
            // ... select logic
        })(input)

        return lib.WriteJSONL(os.Stdout, selected)
    }).
    Done()
```

- [ ] Migrate `select` command
- [ ] Test: `echo '{"a":1,"b":2}' | ssql_v2 select -field a`
- [ ] Migrate `sort` command
- [ ] Test: `cat data.jsonl | ssql_v2 sort -field age -desc`
- [ ] Migrate `reverse` command
- [ ] Test: `cat data.jsonl | ssql_v2 reverse`

**Expected Output:** 6 commands migrated (limit, offset, distinct, select, sort, reverse)

---

### 3.2 Migrate I/O Commands (1 hour)

Commands that read/write files.

**Commands to migrate:**
1. **read-csv** - `-file`, `-delimiter` flags
2. **write-csv** - `-file`, `-delimiter` flags
3. **read-jsonl** - `-file` flag
4. **write-jsonl** - `-file` flag

**Special handling:**
- Input file flag (for read commands)
- Output file flag (for write commands)
- Handle stdin/stdout defaults

- [ ] Migrate `read-csv` command
- [ ] Test: `ssql_v2 read-csv -file data.csv`
- [ ] Migrate `write-csv` command
- [ ] Test: `cat data.jsonl | ssql_v2 write-csv -file out.csv`
- [ ] Migrate `read-jsonl` command
- [ ] Migrate `write-jsonl` command

**Expected Output:** 10 commands migrated total

---

### 3.3 Migrate Clause-Based Commands (1.5 hours)

Commands that use clause support (`+` separator for OR logic).

**Commands to migrate:**
1. **where** - `-match` flag with multi-arg (field, operator, value)
2. **group-by** - `-field`, `-aggregate` flags

**Pattern for where:**
```go
Subcommand("where").
    Description("Filter records based on conditions").

    Flag("-match", "-m").
        Arg("field").
            Completer(cf.NoCompleter{Hint: "<field-name>"}).
            Done().
        Arg("operator").
            Completer(&cf.StaticCompleter{
                Options: []string{"eq", "ne", "gt", "ge", "lt", "le", "contains", "startswith", "endswith", "regexp"},
            }).
            Done().
        Arg("value").
            Completer(cf.NoCompleter{Hint: "<value>"}).
            Done().
        Accumulate().
        Local().  // Per-clause!
        Help("Match condition (field operator value)").
        Done().

    Handler(func(ctx *cf.Context) error {
        // Build filter from clauses
        // ... (current where logic)
    }).
    Done()
```

- [ ] Migrate `where` command
- [ ] Test simple: `cat data.jsonl | ssql_v2 where -match age gt 30`
- [ ] Test AND: `cat data.jsonl | ssql_v2 where -match age gt 30 -match dept eq Eng`
- [ ] Test OR: `cat data.jsonl | ssql_v2 where -match age gt 30 + -match salary gt 100000`
- [ ] Migrate `group-by` command
- [ ] Test: `cat data.jsonl | ssql_v2 group-by -field dept -aggregate salary sum`

**Expected Output:** 12 commands migrated total

---

### 3.4 Migrate Complex Commands (30 min)

**Commands to migrate:**
1. **join** - Most complex, multiple flags, file reading
2. **chart** - Complex with many options
3. **exec** - Special handling for command execution

- [ ] Migrate `join` command
  - Note: Already uses `OnFields()` and `OnCondition()` correctly!
  - Should work with minimal changes
- [ ] Test join: `cat left.jsonl | ssql_v2 join -right right.jsonl -on id`
- [ ] Migrate `chart` command
- [ ] Test chart: `cat data.jsonl | ssql_v2 chart -x date -y value -file chart.html`
- [ ] Migrate `exec` command
- [ ] Test exec: `ssql_v2 exec -cmd "seq 1 10"`

**Expected Output:** All 15 commands migrated!

---

## Phase 4: Integration & Testing (2 hours)

### 4.1 Switch to New Implementation (30 min)

- [ ] Rename files
  ```bash
  mv cmd/ssql/main.go cmd/ssql/main_old.go
  mv cmd/ssql/main_v2.go cmd/ssql/main.go
  ```

- [ ] Add build tag to old main (temporary)
  ```go
  //go:build old
  // +build old

  package main
  ```

- [ ] Rebuild
  ```bash
  go build ./cmd/ssql
  ssql -version  # Should show v1.2.0-dev
  ```

**Expected Output:** New implementation is now default

---

### 4.2 Run Test Suite (1 hour)

- [ ] Run all unit tests
  ```bash
  go test ./... -v
  ```

- [ ] Test all subcommands manually
  ```bash
  # Create test script
  cat > /tmp/test_all_commands.sh <<'EOF'
  #!/bin/bash
  set -e

  echo "Testing limit..."
  echo '{"id":1}
  {"id":2}
  {"id":3}' | ssql limit -n 2

  echo "Testing offset..."
  echo '{"id":1}
  {"id":2}
  {"id":3}' | ssql offset -n 1

  echo "Testing where..."
  echo '{"age":25}
  {"age":35}
  {"age":45}' | ssql where -match age gt 30

  echo "Testing join..."
  echo '{"id":1,"name":"Alice"}' > /tmp/left.jsonl
  echo '{"id":1,"dept":"Eng"}' > /tmp/right.jsonl
  cat /tmp/left.jsonl | ssql join -right /tmp/right.jsonl -on id

  # ... etc for all commands

  echo "All tests passed!"
  EOF

  chmod +x /tmp/test_all_commands.sh
  /tmp/test_all_commands.sh
  ```

- [ ] Test completion
  ```bash
  ssql -completion-script > /tmp/ssql_completion.sh
  source /tmp/ssql_completion.sh

  # Test completion works
  ssql wh<TAB>
  ssql where -m<TAB>
  ```

- [ ] Test help
  ```bash
  ssql -help              # Shows all subcommands
  ssql where -help        # Shows where-specific help
  ssql join -help         # Shows join-specific help
  ```

- [ ] Test pipelines
  ```bash
  ssql read-csv data.csv | \
    ssql where -match age gt 30 | \
    ssql select -field name -field age | \
    ssql sort -field age -desc | \
    ssql limit -n 10 | \
    ssql write-csv output.csv
  ```

**Expected Output:** All tests pass, completion works

---

### 4.3 Performance Testing (30 min)

- [ ] Benchmark simple command
  ```bash
  time cat large.jsonl | ssql limit -n 10000 > /dev/null
  # Compare with old version
  ```

- [ ] Benchmark complex pipeline
  ```bash
  time ssql read-csv large.csv | \
    ssql where -match age gt 30 | \
    ssql group-by -field dept -aggregate salary sum | \
    ssql write-csv output.csv
  ```

- [ ] Document any performance regressions

**Expected Output:** No significant performance difference

---

## Phase 5: Cleanup (1 hour)

### 5.1 Delete Old Code (30 min)

- [ ] Delete old main
  ```bash
  rm cmd/ssql/main_old.go
  ```

- [ ] Delete commands directory
  ```bash
  rm -rf cmd/ssql/commands/
  ```

- [ ] Delete custom completion code
  - Already removed from new main.go

- [ ] Clean up imports
  ```bash
  go mod tidy
  ```

- [ ] Count lines saved
  ```bash
  git diff --stat main
  # Should show ~300-400 lines deleted
  ```

**Expected Output:** Cleaner codebase, reduced complexity

---

### 5.2 Update Documentation (30 min)

- [ ] Update CLAUDE.md
  - Remove references to Command interface
  - Remove references to registry pattern
  - Update completion instructions (`-completion-script` only)

- [ ] Update README.md (if exists)
  - Update completion setup instructions
  - Update examples to use `-completion-script`

- [ ] Update CHANGELOG.md
  ```markdown
  ## [v1.2.0] - YYYY-MM-DD

  ### Changed
  - Migrated to native completionflags subcommand support
  - Completion flag changed from `-bash-completion` to `-completion-script`
  - Simpler architecture with ~300 lines of code removed

  ### Improved
  - Better context-aware completion
  - Auto-generated help for all subcommands
  - Root global flags (`-verbose`, `-debug`)

  ### Removed
  - Custom subcommand routing
  - Command interface and registry
  - Custom `-bash-completion` flag (use `-completion-script`)

  ### Migration Guide
  **Completion Setup:**
  - Old: `eval "$(ssql -bash-completion)"`
  - New: `eval "$(ssql -completion-script)"`
  ```

**Expected Output:** Documentation up to date

---

## Phase 6: Release (1 hour)

### 6.1 Pre-Release Checks (30 min)

- [ ] All tests pass
  ```bash
  go test ./... -v
  ```

- [ ] All commands work
  ```bash
  /tmp/test_all_commands.sh
  ```

- [ ] Completion works
  ```bash
  source <(ssql -completion-script)
  ssql <TAB>
  ```

- [ ] Documentation updated

- [ ] CHANGELOG.md updated

**Expected Output:** Ready for release

---

### 6.2 Version Update (15 min)

- [ ] Update version
  ```bash
  echo "v1.2.0" > cmd/ssql/version/version.txt
  ```

- [ ] Commit version
  ```bash
  git add cmd/ssql/version/version.txt
  git commit -m "Bump version to v1.2.0"
  ```

**Expected Output:** Version updated

---

### 6.3 Create Release (15 min)

- [ ] Merge to main
  ```bash
  git checkout main
  git merge feature/native-subcommands
  ```

- [ ] Create tag
  ```bash
  git tag -a v1.2.0 -m "$(cat <<'EOF'
  ssql v1.2.0 - Native Subcommand Support

  This release migrates to completionflags native subcommand support,
  simplifying the codebase and improving user experience.

  CHANGES:
  - Native subcommand support (~300 lines removed)
  - Better context-aware completion
  - Auto-generated help for all commands
  - Root global flags (-verbose, -debug)

  BREAKING CHANGES:
  - Completion flag: Use -completion-script instead of -bash-completion

  MIGRATION:
  - Update: eval "$(ssql -completion-script)"
  - Everything else works the same

  For details, see CHANGELOG.md
  EOF
  )"
  ```

- [ ] Push
  ```bash
  git push && git push --tags
  ```

- [ ] Rebuild and install
  ```bash
  go install ./cmd/ssql
  ssql -version  # Should show v1.2.0
  ```

**Expected Output:** v1.2.0 released!

---

## Verification Checklist

After release, verify:

- [ ] `ssql -version` shows v1.2.0
- [ ] `ssql -help` shows all subcommands
- [ ] `ssql -completion-script` generates completion
- [ ] Completion works: `ssql wh<TAB>` → `where`
- [ ] All commands execute correctly
- [ ] Pipelines work
- [ ] Join with hash optimization still works
- [ ] Code generation still works (`-generate` flag)

---

## Rollback Plan

If issues arise:

### Option 1: Quick Fix
1. Identify and fix bug
2. Release v1.2.1 patch

### Option 2: Revert Tag
```bash
git tag -d v1.2.0
git push origin :refs/tags/v1.2.0
git revert <commit-hash>
git tag -a v1.2.1 -m "Revert to old subcommand system"
git push && git push --tags
```

### Option 3: Restore Old Code
```bash
git checkout v1.1.0
# Cherry-pick critical fixes
git tag -a v1.1.1 -m "Backport fixes"
```

---

## Success Criteria

Migration is successful when:

✅ All 15 commands work identically to v1.1.0
✅ Completion works better than before
✅ Help is auto-generated and comprehensive
✅ ~300 lines of code removed
✅ No performance regressions
✅ Documentation updated
✅ All tests pass
✅ Tag created and pushed

---

## Time Tracking

| Phase | Estimated | Actual | Notes |
|-------|-----------|--------|-------|
| 1. Preparation | 2h | | |
| 2. POC | 3h | | |
| 3. Full Migration | 4h | | |
| 4. Integration & Testing | 2h | | |
| 5. Cleanup | 1h | | |
| 6. Release | 1h | | |
| **Total** | **13h** | | |

---

## Notes & Learnings

(Add notes as you go)

-
-
-

---

## Next Steps After v1.2.0

1. Monitor for user feedback
2. Fix any issues found
3. Consider adding more root global flags (`-quiet`, `-json-errors`, etc.)
4. Explore advanced completionflags features
5. Document best practices for future commands
