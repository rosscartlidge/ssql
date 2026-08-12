# CompletionFlags Subcommand Migration Plan

Reference: DFC007
Created: 2025-11-02
Last modified: 2025-12-09

[Back to Index](./README.md)

## Current Architecture

ssql currently uses a **custom subcommand dispatcher**:

1. **main.go** manually parses `os.Args[1]` to route to commands
2. **Command interface** with custom `Execute(ctx, args)` method
3. **Registry pattern** - `init()` functions register commands
4. **Custom completion** - manually dispatches `-complete` to subcommands
5. **Each command** owns its own `cf.Command` instance

**Files:**
- `cmd/ssql/main.go` - manual routing (lines 36-55)
- `cmd/ssql/commands/registry.go` - Command interface + registry
- `cmd/ssql/commands/*.go` - individual command implementations

## New Architecture (Native Subcommands)

With completionflags native subcommand support, we can eliminate all custom routing:

1. **Root command** with `.Subcommand()` method
2. **No Command interface needed** - handlers are closures
3. **No registry needed** - subcommands defined inline
4. **Native completion** - completionflags handles everything
5. **Three-level flag scoping** - root global, subcommand global, local

**Structure:**
```go
root := cf.NewCommand("ssql").
    Description("Unix-style data processing tools").
    Flag("-verbose").Global().Bool().Done().
    Subcommand("read-csv").
        Flag("-file").String().Global().Done().
        Handler(func(ctx *cf.Context) error {
            // read-csv logic
        }).
        Done().
    Subcommand("where").
        Flag("-match").Accumulate().Local().Done().
        Handler(func(ctx *cf.Context) error {
            // where logic
        }).
        Done()
```

## Migration Benefits

### ✅ Eliminates Custom Code
- Remove manual routing in main.go
- Remove Command interface
- Remove registry pattern
- Remove custom completion dispatcher

### ✅ Native Features
- Auto-generated help (root + subcommands)
- Context-aware completion
- Root global flags (`-verbose`, `-debug`)
- Cleaner error handling

### ✅ Better UX
- `ssql -help` shows all subcommands
- `ssql read-csv -help` shows command help
- Root flags work before OR after subcommand
- Consistent with git/docker/kubectl

## Migration Strategy

### Option A: Big Bang (Rewrite)
Rewrite main.go to use native subcommands, migrate all commands at once.

**Pros:**
- Clean break, no legacy code
- Smaller final codebase

**Cons:**
- High risk, lots of changes
- Hard to test incrementally

### Option B: Incremental (Recommended)
1. Add native subcommand support alongside current system
2. Migrate commands one-by-one
3. Remove old system when all migrated

**Pros:**
- Low risk, test each step
- Can rollback individual commands

**Cons:**
- Temporary duplication
- Takes longer

### Option C: Hybrid
Keep current command files, but call them from native subcommands.

**Pros:**
- Minimal code changes
- Keeps command organization

**Cons:**
- Still has abstraction layer
- Doesn't fully leverage new API

## Recommended Approach: Option B (Incremental)

### Step 1: Update completionflags dependency
```bash
go get github.com/rosscartlidge/completionflags@latest
go mod tidy
```

### Step 2: Create new main.go structure (parallel)
Create `main_v2.go` with root command + native subcommands.

Start with 2-3 simple commands:
- `read-csv`
- `write-csv`
- `limit`

### Step 3: Add feature flag
```go
useNativeSubcommands := os.Getenv("SSQL_NATIVE_SUBCOMMANDS") == "1"
if useNativeSubcommands {
    // Use main_v2.go logic
} else {
    // Use current logic
}
```

### Step 4: Migrate commands incrementally
Migrate in order of complexity:
1. **Simple** - limit, offset, distinct
2. **Medium** - select, where, sort
3. **Complex** - join, group-by, chart

### Step 5: Remove old system
Once all commands migrated:
- Delete `commands/registry.go`
- Delete old `main.go` routing
- Rename `main_v2.go` to `main.go`
- Remove feature flag

## Code Structure Comparison

### Current (Custom)

**main.go:**
```go
func main() {
    subcommand := os.Args[1]
    for _, cmd := range commands.GetCommands() {
        if cmd.Name() == subcommand {
            cmd.Execute(ctx, args)
            return
        }
    }
}
```

**commands/where.go:**
```go
type whereCommand struct {
    cmd *cf.Command
}

func init() {
    RegisterCommand(newWhereCommand())
}

func (c *whereCommand) Execute(ctx context.Context, args []string) error {
    return c.cmd.Execute(args)
}
```

### New (Native)

**main.go:**
```go
func main() {
    root := cf.NewCommand("ssql").
        Description("Unix-style data processing tools").
        Flag("-verbose").Global().Bool().Done().
        Subcommand("where").
            Description("Filter records based on conditions").
            Flag("-match").Accumulate().Local().Done().
            Handler(func(ctx *cf.Context) error {
                // where logic inline or call helper
                return executeWhere(ctx)
            }).
            Done().
        Build()

    root.Execute(os.Args[1:])
}
```

**No commands/ directory needed!** (or keep as helper functions)

## Root Global Flags to Add

Since we'll have a proper root command, we can add useful global flags:

```go
Flag("-verbose", "-v").
    Description("Enable verbose output").
    Bool().
    Global().
    Done().

Flag("-debug").
    Description("Enable debug output").
    Bool().
    Global().
    Done().

Flag("-version").
    Description("Show version information").
    Bool().
    Global().
    Done().
```

These work in both positions:
- `ssql -verbose read-csv data.csv`
- `ssql read-csv -verbose data.csv`

## Code to Delete

### main.go - ~140 lines removed

**Delete entirely:**
- `printBashCompletion()` function (lines 100-139)
- Manual subcommand routing (lines 36-55)
- Custom flag handling for `-bash-completion` (lines 30-34)
- `printUsage()` function (replaced by auto-generated help)

**What remains:**
- `main()` - simplified to just build and execute root command
- Version constant usage
- Context creation

### commands/registry.go - DELETE ENTIRE FILE

**Delete:**
- `Command` interface
- `RegisterCommand()` function
- `GetCommands()` function
- Global `commands` slice

### All command files (read-csv, where, join, etc.)

**Delete from each:**
- `type *Command struct` - no longer needed
- `init()` registration - no longer needed
- `Execute(ctx context.Context, args []string)` method
- `Name()` method
- `Description()` method
- `GetCFCommand()` method

**Keep/Transform:**
- Flag definitions → move to main.go `.Subcommand()` calls
- Business logic → convert to handler functions or keep as helpers

### Total Code Deletion: ~300-400 lines

## Breaking Changes

### Completion Script Flag (Minor Breaking Change)
- **Removed**: `-bash-completion` (custom flag)
- **Use instead**: `-completion-script` (provided by completionflags natively)
- **Migration**: Update any scripts using `-bash-completion` to `-completion-script`

### Completion Mechanism
- **Old**: Custom `printBashCompletion()` with manual subcommand dispatch
- **New**: Native completionflags generation via `-completion-script`
- **Benefit**: Simpler, more robust, context-aware

### Command Execution
Internal only - user-facing CLI unchanged (except completion flag).

## Testing Strategy

### Phase 1: Parallel Testing
```bash
# Test old system
ssql read-csv data.csv | ssql where -match age gt 30

# Test new system
SSQL_NATIVE_SUBCOMMANDS=1 ssql read-csv data.csv | \
  SSQL_NATIVE_SUBCOMMANDS=1 ssql where -match age gt 30
```

### Phase 2: Integration Tests
Create test suite that runs same commands in both modes:
```go
func TestCommandParity(t *testing.T) {
    tests := []struct{
        name string
        args []string
    }{
        {"read-csv", []string{"read-csv", "data.csv"}},
        {"where", []string{"where", "-match", "age", "gt", "30"}},
    }
    // Run with old and new, compare output
}
```

### Phase 3: Completion Tests
```bash
# Test completion still works
complete -p ssql  # Should show completion is installed
ssql read-<TAB>   # Should complete to read-csv
ssql read-csv -<TAB>  # Should show flags
```

## File Organization

### Current
```
cmd/ssql/
  main.go               (manual routing)
  commands/
    registry.go         (Command interface)
    readcsv.go
    writecsv.go
    where.go
    ... (15+ files)
```

### Option 1: Monolithic
```
cmd/ssql/
  main.go               (all subcommands inline)
```

### Option 2: Helpers (Recommended)
```
cmd/ssql/
  main.go               (root + subcommand definitions)
  handlers/
    readcsv.go          (executeReadCSV helper)
    writecsv.go         (executeWriteCSV helper)
    where.go            (executeWhere helper)
```

Keeps code organized without abstraction layer.

## Timeline Estimate

### Incremental Migration (Recommended)
- **Step 1** (Update dependency): 15 min
- **Step 2** (Create main_v2 + 3 commands): 2 hours
- **Step 3** (Add feature flag): 30 min
- **Step 4** (Migrate remaining ~12 commands): 4 hours (20 min each)
- **Step 5** (Cleanup old system): 1 hour
- **Testing**: 2 hours

**Total: ~10 hours** (1-2 days)

### Big Bang Migration
- **Rewrite main.go**: 1 hour
- **Migrate all 15 commands**: 5 hours
- **Fix all issues**: 3 hours
- **Testing**: 3 hours

**Total: ~12 hours** (higher risk)

## Decision Points

### 1. Incremental vs Big Bang?
**Recommendation:** Incremental - lower risk, easier to test

### 2. Keep commands/ directory?
**Recommendation:** Yes, as `handlers/` - keeps code organized

### 3. When to switch?
**Recommendation:** After v1.1.0 stabilizes, target v1.2.0

### 4. Breaking change?
**No** - User-facing CLI unchanged, only internal architecture

## Next Steps

1. ✅ Read completionflags USAGE.md documentation
2. ⏳ Create this migration plan
3. ⏳ Update completionflags dependency
4. ⏳ Create proof-of-concept with 3 commands
5. ⏳ Test POC thoroughly
6. ⏳ Decide: proceed with full migration or iterate on POC
7. ⏳ Full migration if POC successful

## Questions for User

1. Should we do this migration now or wait?
2. Prefer incremental or big bang approach?
3. Keep commands as separate files (handlers/) or inline in main.go?
4. Any commands that should be prioritized for migration?
