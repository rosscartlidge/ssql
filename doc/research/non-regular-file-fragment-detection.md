# Plan: Non-Regular File Fragment Detection

Reference: DFC025
Created: 2025-12-09
Last modified: 2026-03-20

[Back to Index](./README.md)

**Status**: Implemented (v3.5.2)
**Author**: Ross Cartlidge
**Date**: December 2025

## Overview

Currently, the code generation fragment merging feature (for `join` and `union` commands) detects nested pipelines by checking if the file path starts with `/dev/fd/`. This works for bash process substitution but is unnecessarily restrictive.

This document proposes generalizing the detection to "not a regular file" which would support additional use cases.

## Current Implementation

```go
// In join.go and union.go
if strings.HasPrefix(file, "/dev/fd/") {
    fragments, err := lib.ReadCodeFragmentsFromFile(file)
    if err == nil && len(fragments) > 0 {
        // Process as code fragments
    }
}
```

## Proposed Change

Replace the path prefix check with a file type check:

```go
info, err := os.Stat(file)
if err == nil && !info.Mode().IsRegular() {
    fragments, err := lib.ReadCodeFragmentsFromFile(file)
    if err == nil && len(fragments) > 0 {
        // Process as code fragments
    }
    // Fall through to normal JSONL handling if fragment reading fails
}
```

## Supported Use Cases

### Currently Supported

1. **Process substitution** (bash/zsh)
   ```bash
   ssql from users.csv | ssql join <(ssql from orders.csv) -on id
   ```
   Path appears as `/dev/fd/63` or similar.

### Newly Supported

2. **Named pipes (FIFOs)**
   ```bash
   mkfifo /tmp/orders_pipe

   # Terminal 1: Producer
   ssql from orders.csv > /tmp/orders_pipe

   # Terminal 2: Consumer
   ssql from users.csv | ssql join /tmp/orders_pipe -on id
   ```

   With `SSQLGO=1`, both sides would emit fragments and they'd merge correctly.

3. **Character devices** (theoretical)
   ```bash
   ssql from users.csv | ssql join /dev/some_device -on id
   ```
   Unlikely use case, but would work if the device emits valid fragments.

4. **Sockets presented as files** (theoretical)
   Some systems allow Unix domain sockets to be accessed as files.

## Implementation Details

### Files to Modify

1. `cmd/ssql/commands/join.go` - `generateJoinCode()` function (~line 222)
2. `cmd/ssql/commands/union.go` - `generateUnionCode()` function (~line 137)

### Code Change

```go
// Before (in both files):
if strings.HasPrefix(file, "/dev/fd/") {

// After:
info, statErr := os.Stat(file)
if statErr == nil && !info.Mode().IsRegular() {
```

### Behavior Matrix

| File Type | Generation Mode | Execution Mode |
|-----------|-----------------|----------------|
| Regular file (`.jsonl`) | Read as JSONL, generate `lib.ReadJSONL()` code | Read as JSONL data |
| Regular file (`.csv`) | Error with helpful message | Error with helpful message |
| `/dev/fd/N` (process sub) | Try fragments first, fall back to JSONL | Read as JSONL data |
| Named pipe (FIFO) | Try fragments first, fall back to JSONL | Read as JSONL data |
| Symlink to regular | Treat as regular file | Treat as regular file |
| Symlink to pipe | Try fragments first | Read as JSONL data |

### Error Handling

The fragment reading already has graceful fallback:

```go
fragments, err := lib.ReadCodeFragmentsFromFile(file)
if err == nil && len(fragments) > 0 {
    // Process fragments
} else {
    // Fall through to normal file handling
}
```

If the non-regular file contains JSONL data instead of fragments (execution mode piped to generation mode accidentally), the JSON decoder will fail on the first line and fall back to JSONL handling.

## Testing Plan

### Unit Tests

Add to `cmd/ssql/generation_test.go`:

```go
func TestJoinWithNamedPipe(t *testing.T) {
    // Create a named pipe
    pipePath := filepath.Join(t.TempDir(), "test.pipe")
    if err := syscall.Mkfifo(pipePath, 0644); err != nil {
        t.Skip("Cannot create named pipe on this system")
    }

    // Write fragments to pipe in goroutine
    go func() {
        f, _ := os.OpenFile(pipePath, os.O_WRONLY, 0)
        defer f.Close()
        f.WriteString(`{"type":"init","var":"records","code":"..."}`)
    }()

    // Test that join reads fragments from pipe
    // ...
}
```

### Manual Tests

```bash
# Test 1: Named pipe with process substitution equivalent
mkfifo /tmp/test_pipe
(export SSQLGO=1 && ssql from orders.csv > /tmp/test_pipe) &
export SSQLGO=1 && ssql from users.csv | ssql join /tmp/test_pipe -on id | ssql generate go

# Test 2: Named pipe in execution mode (should work as JSONL)
mkfifo /tmp/data_pipe
(ssql from orders.csv > /tmp/data_pipe) &
ssql from users.csv | ssql join /tmp/data_pipe -on id
```

## Risks and Mitigations

### Risk 1: Performance overhead of `os.Stat()`

**Impact**: Minimal - one stat call per secondary file
**Mitigation**: None needed, stat is fast

### Risk 2: Unexpected behavior with special files

**Impact**: Low - fragment parsing will fail fast and fall back
**Mitigation**: Clear error messages if fallback also fails

### Risk 3: Platform differences

**Impact**: Medium - named pipes work differently on Windows
**Mitigation**:
- `IsRegular()` is cross-platform
- Named pipes are primarily a Unix feature anyway
- Process substitution (main use case) is also Unix-only

## Future Considerations

1. **Explicit flag**: Could add `--fragments` flag to force fragment reading from any source, but auto-detection is cleaner.

2. **Network sources**: Could extend to read fragments from HTTP endpoints or other network sources in the future.

3. **Parallel fragment sources**: Multiple named pipes could enable parallel secondary pipelines (not currently supported).

## Summary

Changing from `/dev/fd/` prefix detection to `!IsRegular()` check:

- **Broader compatibility**: Works with named pipes, not just process substitution
- **Minimal code change**: Two lines in two files
- **No breaking changes**: All existing usage continues to work
- **Graceful fallback**: Invalid fragments fall back to JSONL handling
- **Low risk**: Simple change with clear behavior
