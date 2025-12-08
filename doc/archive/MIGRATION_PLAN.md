# ssql CLI Migration to completionflags

**Status:** Waiting for completionflags v0.2.0 (positional args feature)
**Date:** 2025-10-28

## Summary

We successfully prototyped the migration of all 10 ssql CLI commands from gogstools/gs to completionflags. The migration works great, but we're waiting for the positional arguments feature (see `/home/rossc/src/completionflags/docs/POSITIONAL_ARGS_PROPOSAL.md`) to complete it cleanly.

## Migration Pattern Established

### Old (gs package)
```go
type ReadCSVConfig struct {
    Generate bool   `gs:"flag,global,last,help=Generate Go code instead of executing"`
    Argv     string `gs:"file,global,last,help=Input CSV file,suffix=.csv"`
}

func (c *ReadCSVConfig) Execute(ctx context.Context, clauses []gs.ClauseSet) error {
    inputFile := c.Argv
    // ... implementation
}
```

### New (completionflags - after positional args feature)
```go
func newReadCSVCommand() *readCSVCommand {
    var inputFile string
    var generate bool

    cmd := cf.NewCommand("read-csv").
        Description("Read CSV file and output JSONL stream").

        Flag("-generate", "-g").
            Bool().
            Bind(&generate).
            Global().
            Help("Generate Go code instead of executing").
            Done().

        Flag("FILE").  // Positional - will work once feature lands!
            String().
            Bind(&inputFile).
            Global().
            Default("").
            FilePattern("*.csv").
            Help("Input CSV file (or stdin if not specified)").
            Done().

        Handler(func(ctx *cf.Context) error {
            // inputFile automatically populated!
            if generate {
                return generateReadCSVCode(inputFile)
            }
            records, err := ssql.ReadCSV(inputFile)
            // ...
        }).
        Build()

    return &readCSVCommand{cmd: cmd}
}
```

## Commands to Migrate (10 total)

All commands follow the same pattern:

1. ✅ **read-csv** - Single optional FILE positional
2. ✅ **write-csv** - Single optional FILE positional
3. ✅ **limit** - Just -n flag (no positionals)
4. ✅ **select** - Local -field/-as flags (no positionals)
5. ✅ **generate-go** - Single optional FILE positional
6. ✅ **sort** - Optional FILE positional, -field and -desc flags
7. ✅ **exec** - Special `--` handling, no FILE positional
8. ✅ **chart** - Optional FILE positional, -x, -y, -output flags
9. ✅ **groupby** - Optional FILE positional, -by flag, local -function/-field/-result
10. ✅ **where** - Optional FILE positional, local -match with multi-args

## Key Changes

### For Each Command:
1. Remove config struct with gs tags
2. Create local variables for flags
3. Use fluent builder API with `.Flag().Type().Bind().Done()`
4. Implement handler as closure
5. Update `GetGSCommand() -> nil` and `GetCFCommand() -> cmd`
6. Simplify `Execute()` to just `cmd.Execute(args)`

### Multi-Argument Flags (where command):
```go
Flag("-match").
    Arg("FIELD").Done().
    Arg("OPERATOR").
        Completer(&cf.StaticCompleter{
            Options: []string{"eq", "ne", "gt", "ge", "lt", "le", ...},
        }).
        Done().
    Arg("VALUE").Done().
    Accumulate().
    Local().
    Help("Filter condition: field operator value").
    Done()
```

### Clause Handling:
```go
// Iterate through clauses for local flags
for _, clause := range ctx.Clauses {
    field, _ := clause.Flags["-field"].(string)
    // ... process
}
```

## Benefits After Migration

✅ **Modern API** - Fluent builder pattern, type-safe
✅ **Better Completions** - Built-in file patterns, static/dynamic completers
✅ **Cleaner Code** - No struct tags, everything explicit
✅ **Better Help** - Auto-generated with examples, man pages
✅ **Same Functionality** - Clauses, separators, local/global scopes all work

## What We're Waiting For

The **positional arguments feature** in completionflags (see proposal at `/home/rossc/src/completionflags/docs/POSITIONAL_ARGS_PROPOSAL.md`).

Once implemented, `Flag("FILE")` (no leading `-`) will automatically:
- Match positional command-line arguments
- Populate bound variables via `.Bind()`
- Validate with `.Required()` or `.Default()`
- Generate help text
- Support file completion with `.FilePattern()`

## When Ready to Migrate

1. Check completionflags has positional args support:
   ```bash
   cd /home/rossc/src/completionflags
   git log --oneline | grep -i positional
   ```

2. Update dependency:
   ```bash
   cd /home/rossc/src/ssql
   go get github.com/rosscartlidge/completionflags@latest
   go mod tidy
   ```

3. Migrate commands one at a time:
   - Start with `read-csv` (simplest)
   - Test thoroughly: `go build ./cmd/ssql && ./ssql read-csv -help`
   - Continue with others

4. Remove gs dependency when done:
   ```bash
   go mod edit -droprequire github.com/rosscartlidge/gogstools
   go mod tidy
   ```

## Testing Checklist

For each migrated command:
- [ ] `ssql <cmd> -help` shows proper help
- [ ] `ssql <cmd> -man` generates man page
- [ ] Tab completion works (if shell configured)
- [ ] All flags work as before
- [ ] Positional file arguments work
- [ ] Error messages are helpful
- [ ] `-generate` flag works (for commands that support it)

## Test Commands

```bash
# Basic functionality
./ssql read-csv test.csv
echo "name,age\nAlice,30" | ./ssql read-csv

# Piping
./ssql read-csv test.csv | ./ssql where -match age gt 25 | ./ssql select -field name

# Code generation
./ssql read-csv -generate test.csv | ./ssql generate-go

# Complex pipeline
./ssql read-csv data.csv | \
  ./ssql where -match status eq active + -match role eq admin | \
  ./ssql group -by department -function count -result n | \
  ./ssql chart -x department -y n -output viz.html
```

## Estimated Effort

- **Per command:** 15-30 minutes
- **Total:** 3-5 hours including testing
- **Complexity:** Low (pattern established)

## Notes

- All commands were successfully prototyped
- Build and basic tests passed
- Only blocker is positional args feature
- When ready, migration should be straightforward
- Consider creating a branch for migration work

## References

- Positional args proposal: `/home/rossc/src/completionflags/docs/POSITIONAL_ARGS_PROPOSAL.md`
- Completionflags package: `/home/rossc/src/completionflags`
- Old implementation: `cmd/ssql/commands/*.go` (current state)
