# Code Generation System (CRITICAL FEATURE)

**CRITICAL: This is a core feature that enables 10-100x faster execution by generating standalone Go programs from CLI pipelines.**

## Overview

ssql supports **self-generating pipelines** where commands emit Go code fragments instead of executing. This allows users to:
1. Prototype data processing pipelines using the CLI
2. Generate optimized Go code from the working pipeline
3. Compile and run standalone programs 10-100x faster than CLI execution

## Generated Code Readability (CRITICAL)

**ALWAYS keep generated code simple and readable!**

**Rules for Code Generation:**

1. **Move complexity to helper functions** - Generated code should call helper functions in the ssql package, NOT inline complex logic
   - GOOD: `ssql.DisplayTable(records, 50)` (one line, clear intent)
   - BAD: 80 lines of formatting logic inlined (hard to understand)

2. **Generated code should be self-documenting** - A reader should immediately understand what the pipeline does

3. **When adding new commands:**
   - First: Add helper function to ssql package (io.go, operations.go, etc.)
   - Then: Generate code that calls the helper
   - Test: Read the generated code - is the intent clear?

## Enabling Code Generation

Two ways to enable generation mode:

```bash
# Method 1: Environment variable (affects entire pipeline)
export SSQLGO=1
ssql from data.csv | ssql where -if age gt 25 | ssql generate go

# Method 2: -generate flag per command
ssql from -generate data.csv | ssql where -generate -if age gt 25 | ssql generate go
```

The environment variable approach is preferred for full pipelines.

## Code Fragment System

**Architecture (`cmd/ssql/lib/codefragment.go`):**
- Commands communicate via JSONL code fragments on stdin/stdout
- Each fragment has: Type, Var (variable name), Input (input var), Code, Imports, Command
- The `generate go` command assembles all fragments into a complete Go program
- Fragments are passed through the pipeline, with each command adding its own

**Fragment Types:**
- `init` - First command (e.g., from), creates initial variable, no input
- `stmt` - Middle command (e.g., where, group-by), has input and output variable
- `final` - Last command (e.g., write-csv), has input but no output variable

**Helper Functions (in `cmd/ssql/helpers.go`):**
- `shouldGenerate(flagValue bool)` - Checks flag or SSQLGO env var
- `getCommandString()` - Returns command line that invoked the command (filters out -generate flag)
- `shellQuote(s string)` - Quotes arguments for shell safety

## Generation Support Status (as of v3.1.0)

**Commands with -generate support:**
1. `from` - Generates init fragment with `ssql.ReadCSV()` or `lib.ReadJSON()`
2. `where` - Generates stmt fragment with filter predicate
3. `to csv` - Generates final fragment with `ssql.WriteCSV()`
4. `to json` - Generates final fragment with `ssql.WriteJSON()`
5. `to table` - Generates final fragment with `ssql.DisplayTable()`
6. `to chart` - Generates final fragment with `ssql.QuickChart()`
7. `limit` - Generates stmt fragment with `ssql.Limit[ssql.Record](n)`
8. `offset` - Generates stmt fragment with `ssql.Offset[ssql.Record](n)`
9. `sort` - Generates stmt fragment with `ssql.SortBy()`
10. `distinct` - Generates stmt fragment with `ssql.DistinctBy()`
11. `group-by` - Generates TWO stmt fragments (GroupByFields + Aggregate)
12. `union` - Generates stmt fragment with `ssql.Concat()` and optionally `ssql.DistinctBy(ssql.RecordKey)`
13. `join` - Generates stmt fragment with `ssql.Join()`

**Commands that don't need -generate:**
- `generate go` - it's the assembler that produces the final Go code
- `functions` - displays help information only
- `version` - displays version only

**IMPORTANT:** Commands without generation support will break pipelines in generation mode. Always add generation support when creating new commands.

## Adding Generation Support to Commands

**Step 1: Add generation function to `cmd/ssql/helpers.go`:**

```go
// generateMyCommandCode generates Go code for the my-command command
func generateMyCommandCode(arg1 string, arg2 int) error {
    // 1. Read all previous code fragments from stdin
    fragments, err := lib.ReadAllCodeFragments()
    if err != nil {
        return fmt.Errorf("reading code fragments: %w", err)
    }

    // 2. Pass through all previous fragments
    for _, frag := range fragments {
        if err := lib.WriteCodeFragment(frag); err != nil {
            return fmt.Errorf("writing previous fragment: %w", err)
        }
    }

    // 3. Get input variable from last fragment (or default to "records")
    var inputVar string
    if len(fragments) > 0 {
        inputVar = fragments[len(fragments)-1].Var
    } else {
        inputVar = "records"
    }

    // 4. Generate your command's Go code
    outputVar := "result"
    code := fmt.Sprintf("%s := ssql.MyCommand(%q, %d)(%s)",
        outputVar, arg1, arg2, inputVar)

    // 5. Create and write your fragment
    imports := []string{"fmt"}  // Add any needed imports
    frag := lib.NewStmtFragment(outputVar, inputVar, code, imports, getCommandString())
    return lib.WriteCodeFragment(frag)
}
```

**Step 2: Add -generate flag and check to command handler in `cmd/ssql/main.go`:**

```go
Subcommand("my-command").
    Description("Description of my command").

    Handler(func(ctx *cf.Context) error {
        var arg1 string
        var arg2 int
        var generate bool

        // Extract flags
        if val, ok := ctx.GlobalFlags["-arg1"]; ok {
            arg1 = val.(string)
        }
        if val, ok := ctx.GlobalFlags["-arg2"]; ok {
            arg2 = val.(int)
        }
        if genVal, ok := ctx.GlobalFlags["-generate"]; ok {
            generate = genVal.(bool)
        }

        // Check if generation is enabled (flag or env var)
        if shouldGenerate(generate) {
            return generateMyCommandCode(arg1, arg2)
        }

        // Normal execution follows...
        // ...
    }).

    Flag("-generate", "-g").
        Bool().
        Global().
        Help("Generate Go code instead of executing").
        Done().

    Flag("-arg1").
        String().
        Global().
        Help("First argument").
        Done().

    // ... other flags

    Done().
```

**Step 3: Add tests to `cmd/ssql/generation_test.go`:**

```go
func TestMyCommandGeneration(t *testing.T) {
    buildCmd := exec.Command("go", "build", "-o", "/tmp/ssql_test", ".")
    if err := buildCmd.Run(); err != nil {
        t.Fatalf("Failed to build ssql: %v", err)
    }
    defer os.Remove("/tmp/ssql_test")

    cmdLine := `echo '{"type":"init","var":"records"}' | SSQLGO=1 /tmp/ssql_test my-command -arg1 test -arg2 42`
    cmd := exec.Command("bash", "-c", cmdLine)
    output, err := cmd.CombinedOutput()
    if err != nil {
        t.Logf("Command output: %s", output)
    }

    outputStr := string(output)
    want := []string{`"type":"stmt"`, `"var":"result"`, `ssql.MyCommand`}
    for _, expected := range want {
        if !strings.Contains(outputStr, expected) {
            t.Errorf("Expected output to contain %q, got: %s", expected, outputStr)
        }
    }
}
```

## Special Cases

**Commands with multiple fragments (like group-by):**

Some commands generate multiple code fragments. For example, `group-by` generates:
1. `GroupByFields` fragment (with command string)
2. `Aggregate` fragment (empty command string - part of same CLI command)

```go
// Fragment 1: GroupByFields
frag1 := lib.NewStmtFragment("grouped", inputVar, groupCode, nil, getCommandString())
lib.WriteCodeFragment(frag1)

// Fragment 2: Aggregate (note: empty command string)
frag2 := lib.NewStmtFragment("aggregated", "grouped", aggCode, nil, "")
lib.WriteCodeFragment(frag2)
```

## Testing Code Generation

**Manual testing:**
```bash
# Test individual command
export SSQLGO=1
echo '{"type":"init","var":"records"}' | ./ssql my-command -arg1 test

# Test full pipeline
export SSQLGO=1
./ssql from data.csv | \
  ./ssql where -if age gt 25 | \
  ./ssql my-command -arg1 test | \
  ./ssql generate go > program.go

# Compile and run generated code
go run program.go
```

**Automated tests:**
- All generation tests are in `cmd/ssql/generation_test.go`
- Run with: `go test -v ./cmd/ssql -run TestGeneration`
- Tests ensure the feature is never lost during refactoring

## CLI Commands Must Use ssql Package Primitives (CRITICAL)

**CLI commands must ALWAYS be implemented using ssql package functions, not raw Go code!**

The ssql CLI exists to make the ssql package accessible from the command line. Every CLI command should:
1. Map directly to one or more ssql package functions
2. Generate code that calls those same functions
3. Use minimal glue code between commands

**If a CLI feature requires logic that doesn't exist in the ssql package:**
- CORRECT: Add the functionality to the ssql package first, then use it in CLI
- WRONG: Generate raw Go code (loops, maps, custom logic) in the CLI

**When adding new CLI features:**
1. First: Design and implement the ssql package function
2. Then: Update CLI to use that function
3. Finally: Update code generation to emit calls to that function

## Code Generation Requirements (CRITICAL)

**NEVER release a ssql command that doesn't support code generation!**

Every data-processing command MUST support code generation (`-generate` flag / `SSQLGO=1`). This is non-negotiable because:
- Users rely on the CLI-to-compiled-Go workflow for production systems
- A single command without generation support breaks entire pipelines
- The feature is invisible until users try to generate code, then it fails

**Before releasing any new command:**
1. Implement `-generate` flag support
2. Add generation tests to `cmd/ssql/generation_test.go`
3. Test full pipeline: `SSQLGO=1 ssql from ... | ssql new-command ... | ssql generate go`
4. Verify generated code compiles and runs correctly

**Exception:** Commands that don't process data (like `version`, `functions`, `generate go` itself) don't need generation support.

## Error Handling Requirements (CRITICAL)

**All errors MUST cause pipeline failure with clear error messages!**

This applies to BOTH execution mode AND code generation mode:

**Execution Mode:**
- Errors must be returned, not silently ignored
- Error messages must be clear and actionable
- Pipeline must stop on first error (fail-fast)

**Code Generation Mode:**
- Unsupported features must emit error fragments (`"type":"error"`)
- `generate go` must detect error fragments and fail (no partial code output)
- Error messages must explain what's unsupported and suggest alternatives

**Example - Proper error fragment emission:**
```go
if unsupportedFeature {
    frag := lib.NewErrorFragment("feature X is not yet supported with -generate", getCommandString())
    lib.WriteCodeFragment(frag)
    return fmt.Errorf("feature X is not yet supported with -generate")
}
```
