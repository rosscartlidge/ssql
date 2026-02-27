# Streaming Window Functions: Design Report

## Problem Statement

The current `Window()` implementation materializes all input records before computing any window function. For a 10M-row dataset, this means 10M records buffered in memory before the first output row appears. This is the correct approach for the general case (arbitrary partitioning, unbounded frames, ranking functions), but many real-world window patterns could stream with bounded memory.

The goal: identify which window function + frame + partition combinations can be computed in a streaming fashion, and design an implementation that is **safe** (identical output to `Window()`), **efficient** (O(frame size) or O(1) memory per partition), and **maintainable** (no duplicated logic, clear fast-path selection).

## Background: How Databases Do It

Stream processing systems (RisingWave, Apache Flink, Synnada) have established patterns:

1. **Presorted input assumption**: Streaming windows require input ordered by partition keys then order keys. The system either requires this (flag) or detects it.

2. **Emit-on-window-close (EOWC)**: Buffer rows until the system can guarantee a window is complete, then emit results and evict old rows from the buffer.

3. **Incremental aggregates**: For commutative aggregates (sum, count), maintain a running accumulator and subtract values leaving the frame, avoiding re-scanning the frame on every row.

4. **Two-mode execution**: Keep the full-materialization path for the general case. Add a streaming fast-path with clear, compile-time eligibility checks.

## Classification of Window Functions

Not all 15 window functions can stream. The key question for each: **can we compute this function's value for row N using only a bounded buffer around N?**

### Can Stream with Bounded Frame (7 functions)

These functions only need to see the rows within the frame boundaries:

| Function | State Needed | Incremental? | Notes |
|----------|-------------|-------------|-------|
| `SUM(field)` | Running sum + ring buffer of values | Yes: add entering, subtract leaving | Subtraction can accumulate float error over millions of rows. Use Kahan compensation. |
| `AVG(field)` | Running sum + count + ring buffer | Yes: same as SUM + count | |
| `COUNT(*)` | Frame size (int) | Yes: trivially `end - start + 1` | No ring buffer needed if frame is purely positional. |
| `MIN(field)` | Monotonic deque (ascending) | Yes: O(1) amortized | Classic sliding window minimum. Deque holds indices of candidates. |
| `MAX(field)` | Monotonic deque (descending) | Yes: O(1) amortized | Mirror of MIN. |
| `FIRST_VALUE(field)` | Single value + ring buffer | Yes: front of ring buffer | |
| `LAST_VALUE(field)` | Single value | Yes: always the newest value in frame | |

### Can Stream Without Frame (2 functions)

These reference specific offsets from the current row, not a frame:

| Function | State Needed | Notes |
|----------|-------------|-------|
| `LAG(field, N)` | Ring buffer of size N+1 | Look back N rows in partition. Ignores frame spec. |
| `LEAD(field, N)` | Buffer of N+1 future rows | Must read N rows ahead before emitting. Adds latency but bounded memory. |

### Cannot Stream (6 functions)

These need the full partition to compute:

| Function | Why |
|----------|-----|
| `ROW_NUMBER()` | Can stream trivially (counter), BUT only useful with PARTITION BY, which requires knowing partition size or at least partition boundaries. See section below. |
| `RANK()` | Needs to count rows with same value — requires seeing all ties. Can stream if presorted. |
| `DENSE_RANK()` | Same as RANK — needs full tie-group visibility. Can stream if presorted. |
| `NTILE(n)` | Needs total partition size to divide into buckets. Cannot stream. |
| `PERCENT_RANK()` | Needs total partition size for denominator. Cannot stream. |

### Re-evaluation: Ranking Functions with Presorted Input

If input is presorted by partition + order keys, ranking functions CAN stream partition-at-a-time (same approach as `StreamGroupByFields`):

- **ROW_NUMBER()**: Just a counter per partition. Reset on partition change. Trivially streaming.
- **RANK()**: Track position + previous sort key. When key changes, rank = position+1. When same, rank stays.
- **DENSE_RANK()**: Track distinct-key counter. Increment on key change.

However, NTILE and PERCENT_RANK **fundamentally cannot stream** because they need the total partition size before emitting any row.

### Final Streamability Matrix

| Function | No Partition | With Partition (presorted) | With Partition (unsorted) |
|----------|-------------|---------------------------|--------------------------|
| SUM/AVG/COUNT | Bounded frame | Bounded frame per partition | Cannot stream |
| MIN/MAX | Bounded frame | Bounded frame per partition | Cannot stream |
| FIRST/LAST | Bounded frame | Bounded frame per partition | Cannot stream |
| LAG | Ring buffer | Ring buffer per partition | Cannot stream |
| LEAD | Lookahead buffer | Lookahead buffer per partition | Cannot stream |
| ROW_NUMBER | Counter | Counter per partition | Cannot stream |
| RANK | Counter + prev key | Counter + prev key per partition | Cannot stream |
| DENSE_RANK | Counter + prev key | Counter + prev key per partition | Cannot stream |
| NTILE | **Cannot stream** | **Cannot stream** | Cannot stream |
| PERCENT_RANK | **Cannot stream** | **Cannot stream** | Cannot stream |

## Proposed Design

### Approach: `StreamWindow()` — Separate Function, Same Types

Add a new function `StreamWindow()` alongside existing `Window()`, using the same `WindowConfig` types. This avoids the "two code paths in one function" problem — each function has a single, clear purpose.

```go
// StreamWindow computes window functions in streaming mode.
// Input MUST be presorted by partition fields then order fields.
// Only supports bounded frames (no UNBOUNDED FOLLOWING).
// Returns error for unsupported function/frame combinations at construction time.
func StreamWindow(configs []WindowConfig) (Filter[Record, Record], error)
```

Key design decision: **return an error at construction time** if any config uses an unsupported combination, rather than silently falling back. This makes the contract explicit.

### Why a Separate Function (Not a Flag on Window())

1. **Different preconditions**: `StreamWindow` requires presorted input. `Window` does not. Mixing these in one function with a flag creates a confusing API where the flag changes the function's requirements.

2. **Type safety**: Returning `(Filter, error)` at construction time catches invalid configs early. `Window()` currently returns just `Filter` with no error path.

3. **No behavioral ambiguity**: Users explicitly choose streaming behavior. No silent fallback that might produce unexpected results if the input happens to not be sorted.

4. **Matches existing pattern**: `StreamGroupByFields` is a separate function from `GroupByFields`, not a flag.

### Eligibility Check

```go
func canStreamWindow(cfg WindowConfig) error {
    for _, spec := range cfg.Specs {
        switch spec.Function.(type) {
        case wNtile:
            return fmt.Errorf("NTILE cannot be computed in streaming mode (needs partition size)")
        case wPercentRank:
            return fmt.Errorf("PERCENT_RANK cannot be computed in streaming mode (needs partition size)")
        }
    }
    // Check frame: UNBOUNDED FOLLOWING is not streamable
    if cfg.Frame.Following < 0 {
        return fmt.Errorf("UNBOUNDED FOLLOWING is not supported in streaming mode")
    }
    return nil
}
```

This is a compile-time-style check — called once when building the pipeline, not per record.

### Algorithm

#### Case 1: No PARTITION BY, Bounded Frame

Simplest case. Input is a single partition, already in order.

```
State per aggregate function:
  SUM: running_sum float64, ring_buffer []float64
  AVG: running_sum float64, count int, ring_buffer []float64
  COUNT: frame_size int (no ring buffer needed)
  MIN: monotonic_deque (ascending)
  MAX: monotonic_deque (descending)
  FIRST: ring_buffer[0]
  LAST: current_value
  LAG(N): ring_buffer of size N+1
  LEAD(N): lookahead_buffer of size N+1

For each input record:
  1. Add record to ring buffer / deque
  2. If buffer has enough records (pos >= frame.Following):
     - Compute window values from state
     - Emit enriched record
     - Evict record leaving the frame from state
  3. At end of input: flush remaining buffered records
```

Memory: O(frame_size) per function.

#### Case 2: With PARTITION BY, Presorted Input

Same as Case 1, but with partition boundary detection:

```
State: same as Case 1, plus partition_key tracking

For each input record:
  1. Extract partition key
  2. If partition changed:
     a. Flush remaining records from previous partition
     b. Reset all state (ring buffers, accumulators, counters)
  3. Process record as in Case 1
```

Memory: O(frame_size) — state for only one partition at a time.

#### Case 3: LEAD Function (Lookahead)

LEAD requires reading ahead N rows. This means we must buffer N+1 rows before emitting the first one:

```
Buffer: sliding window of (N+1) records

For each input record:
  1. Add to buffer
  2. If buffer.size > N:
     - Emit buffer[0] enriched with LEAD value = buffer[N].field
     - Remove buffer[0]
At end:
  - Emit remaining records with nil LEAD values
```

This is equivalent to a frame with Following=N, Preceding=0 for offset functions.

### Incremental Aggregate Details

#### SUM — Kahan Compensated Sliding Sum

Naive sliding sum (add entering value, subtract leaving value) accumulates floating-point error. After millions of iterations, the error can become significant.

```go
type slidingSum struct {
    sum  float64
    comp float64 // Kahan compensation
    buf  []float64
    head int
    size int
}

func (s *slidingSum) add(val float64) {
    y := val - s.comp
    t := s.sum + y
    s.comp = (t - s.sum) - y
    s.sum = t
}

func (s *slidingSum) sub(val float64) {
    s.add(-val)
}
```

Alternative: For integer fields (int64), use exact integer arithmetic. Only use float compensation for float64 fields.

#### MIN/MAX — Monotonic Deque

The sliding window minimum/maximum is a well-known O(1) amortized algorithm using a deque:

```go
type slidingMin struct {
    deque []dequeEntry // indices + values, front = current min
}

type dequeEntry struct {
    pos int
    val any
}

func (s *slidingMin) push(pos int, val any) {
    // Remove all entries from back that are >= val
    for len(s.deque) > 0 && CompareAny(s.deque[len(s.deque)-1].val, val) >= 0 {
        s.deque = s.deque[:len(s.deque)-1]
    }
    s.deque = append(s.deque, dequeEntry{pos, val})
}

func (s *slidingMin) evict(pos int) {
    // Remove front if it's leaving the window
    for len(s.deque) > 0 && s.deque[0].pos <= pos {
        s.deque = s.deque[1:]
    }
}

func (s *slidingMin) min() any {
    return s.deque[0].val
}
```

This gives O(1) amortized per-row min/max instead of O(frame_size).

### Ring Buffer Implementation

A shared ring buffer holds the frame's records. Each incremental aggregate reads from it:

```go
type frameBuffer struct {
    records []Record
    head    int
    count   int
    cap     int // = preceding + following + 1
}

func (fb *frameBuffer) push(r Record) {
    idx := (fb.head + fb.count) % fb.cap
    fb.records[idx] = r
    if fb.count < fb.cap {
        fb.count++
    } else {
        fb.head = (fb.head + 1) % fb.cap
    }
}

func (fb *frameBuffer) oldest() Record { return fb.records[fb.head] }
func (fb *frameBuffer) newest() Record { return fb.records[(fb.head+fb.count-1)%fb.cap] }
func (fb *frameBuffer) at(i int) Record { return fb.records[(fb.head+i)%fb.cap] }
```

### Handling Multiple Functions in One Config

A single `WindowConfig` can have multiple specs (e.g., `-sum revenue running_total -avg revenue avg_rev -order date`). All specs share the same frame, so they share the same ring buffer. Each spec maintains its own incremental state.

```go
type streamWindowState struct {
    buffer     *frameBuffer
    aggregates []incrementalAgg  // one per spec
    pos        int               // current position in partition
}
```

### Handling Multiple Configs (Clauses)

Multiple clauses (`+` separator in CLI) mean multiple configs with potentially different partitions, orderings, and frames. For streaming, ALL configs must be compatible with the same input ordering.

**Constraint**: All configs must partition and order by the same fields (or a prefix thereof). If config A partitions by `dept` and orders by `salary`, while config B partitions by `region` and orders by `date`, they cannot both stream from the same presorted input.

**Approach**: Validate that all configs agree on partition + order fields at construction time. If they don't, return an error.

```go
// All configs must use the same partition+order fields for streaming
func validateStreamConfigs(configs []WindowConfig) error {
    if len(configs) <= 1 {
        return nil
    }
    ref := configs[0]
    for i := 1; i < len(configs); i++ {
        if !samePartitionOrder(ref, configs[i]) {
            return fmt.Errorf("streaming window requires all clauses to use the same partition and order fields")
        }
    }
    return nil
}
```

For the common case of a single config, this is a no-op.

## CLI Integration

### Flag: `-presorted` on the `window` command

Following the same pattern as `group-by -presorted`:

```bash
# Streaming window — input must be presorted by order field
ssql from data.csv | ssql sort date | ssql window -sum revenue running_total -order date -presorted

# Streaming per partition — input must be presorted by partition then order
ssql from data.csv | ssql sort dept date | ssql window -sum revenue running_total -partition dept -order date -presorted

# Error: NTILE cannot stream
ssql from data.csv | ssql window -ntile 4 rank -order salary -presorted
# Error: NTILE cannot be computed in streaming mode (needs partition size)

# Error: unbounded following cannot stream
ssql from data.csv | ssql window -sum revenue total -order date -rows '*,*' -presorted
# Error: UNBOUNDED FOLLOWING is not supported in streaming mode
```

### Code Generation

When `-presorted` is set, generate `ssql.StreamWindow(...)` instead of `ssql.Window(...)`. Since `StreamWindow` returns `(Filter, error)`, the generated code must handle the error:

```go
windowFn, err := ssql.StreamWindow([]ssql.WindowConfig{...})
if err != nil {
    log.Fatal(err)
}
windowed := windowFn(records)
```

## Correctness Guarantees

### Testing Strategy: Differential Testing

The strongest correctness guarantee: **for any input that is sorted correctly, `StreamWindow` and `Window` must produce identical output**.

```go
func TestStreamWindow_MatchesWindow(t *testing.T) {
    // Generate random test data, sort by partition + order fields
    // Run both Window() and StreamWindow() on the same input
    // Compare output record-by-record
}
```

This should be a table-driven test covering:
- All 13 streamable functions (exclude NTILE, PERCENT_RANK)
- Various frame sizes: 1-row, 3-row, 10-row, entire-partition
- Edge cases: partition with 1 record, frame larger than partition
- Data types: int64 values, float64 values, string values (for MIN/MAX)
- Multiple functions in one config
- Multiple records with identical sort keys (ties)

### Float Precision

For SUM/AVG with Kahan compensation, the streaming result may differ from the materializing result by a few ULPs (units in the last place). The test should use approximate comparison for float64 values:

```go
func approxEqual(a, b float64) bool {
    return math.Abs(a-b) < 1e-10*math.Max(math.Abs(a), math.Abs(b))
}
```

### Unsorted Input Detection (Optional Safety Net)

Optionally, `StreamWindow` could verify that input is actually sorted by checking each consecutive pair of records. This adds O(1) overhead per record:

```go
if prevRecord != nil {
    if CompareRecordFields(prevRecord, record, orderBy) > 0 {
        // Input is not sorted — this is a caller bug
        // Option A: return error
        // Option B: log warning and continue (results will be wrong)
    }
}
```

Recommendation: **make this a debug mode** (e.g., `StreamWindowDebug()` or an environment variable). In production, the caller guarantees sort order; checking adds overhead.

## Performance Analysis

### Memory Comparison

| Scenario | Window() | StreamWindow() |
|----------|----------|----------------|
| 10M rows, no partition | 10M records | frame_size records |
| 10M rows, 1K partitions of 10K | 10M records | 10K records (largest partition) |
| 10M rows, frame=ROWS 2,0 | 10M records | 3 records + aggregate state |
| 10M rows, running sum (*,0) | 10M records | 1 accumulator (no buffer needed) |

### Special Case: UNBOUNDED PRECEDING, 0 FOLLOWING (Running Aggregates)

The default frame `ROWS BETWEEN UNBOUNDED PRECEDING AND CURRENT ROW` is the most common. For this frame:
- **No ring buffer needed** — values never leave the frame
- **SUM/COUNT**: single accumulator, O(1) memory
- **AVG**: sum + count, O(1) memory
- **MIN**: still needs deque (min can change as values enter)
- **MAX**: still needs deque
- **FIRST_VALUE**: single value (first record's value, never changes)
- **LAST_VALUE**: current row's value
- **ROW_NUMBER/RANK/DENSE_RANK**: counters only

This is the highest-value optimization — running aggregates are extremely common and can run with O(1) memory.

### Time Complexity

| Function | Window() | StreamWindow() |
|----------|----------|----------------|
| SUM/AVG | O(N * frame_size) | O(N) with incremental |
| COUNT | O(N) | O(N) |
| MIN/MAX | O(N * frame_size) | O(N) amortized with deque |
| RANK/DENSE_RANK | O(N^2) in current impl | O(N) with counter |

The current Window() implementation of RANK is O(N^2) per partition because it scans backwards to find the tie group start. StreamWindow's counter approach is O(N).

## Implementation Plan

### Phase 1: Core StreamWindow (Running Aggregates Only)

Scope: `ROWS BETWEEN UNBOUNDED PRECEDING AND CURRENT ROW` (the default frame) with no partition or presorted partition.

Functions: SUM, AVG, COUNT, FIRST_VALUE, ROW_NUMBER, RANK, DENSE_RANK.

This covers the most common use case (running totals, row numbering) with O(1) memory.

**Files:**
- `sql.go`: `StreamWindow()`, `canStreamWindow()`, incremental state types
- `sql_test.go`: Differential tests against `Window()`
- `cmd/ssql/commands/window.go`: `-presorted` flag
- `cmd/ssql/generation_test.go`: Generation test for presorted window

### Phase 2: Bounded Frames

Scope: `ROWS BETWEEN N PRECEDING AND M FOLLOWING` with fixed N, M.

Adds: Ring buffer, sliding SUM/AVG with Kahan compensation, monotonic deque for MIN/MAX, LAST_VALUE, LEAD/LAG.

**Files:**
- `sql.go`: `frameBuffer`, `slidingSum`, `slidingMin`, `slidingMax` types
- `sql_test.go`: Additional differential tests with various frame sizes

### Phase 3: Performance Validation

Benchmark `StreamWindow` vs `Window` on realistic workloads to verify the memory and time improvements.

## Risk Assessment

| Risk | Severity | Mitigation |
|------|----------|------------|
| Output differs from Window() | High | Differential testing with randomized inputs |
| Float precision drift (SUM) | Medium | Kahan compensation; approximate test comparison |
| Unsorted input → wrong results | Medium | Optional sort-order verification in debug mode |
| Two functions to maintain | Low | Shared types (WindowConfig, WindowFunc); incremental state is isolated |
| Users confused about when to use which | Low | Clear error messages from StreamWindow; doc examples |

## Summary

| What | Decision |
|------|----------|
| API | `StreamWindow([]WindowConfig) (Filter[Record, Record], error)` |
| Eligibility | 13 of 15 functions (not NTILE, PERCENT_RANK) |
| Frame constraint | No UNBOUNDED FOLLOWING |
| Partition constraint | Input presorted by partition + order fields |
| Multi-clause | All clauses must share partition + order fields |
| Incremental SUM | Kahan-compensated sliding sum |
| Incremental MIN/MAX | Monotonic deque, O(1) amortized |
| CLI flag | `-presorted` on `window` command |
| Testing | Differential: StreamWindow output == Window output |
| Implementation | 2 phases: running aggregates first, bounded frames second |

## References

- [RisingWave: Window Functions — The Art of Sliding](https://risingwave.com/blog/risingwave-window-functions-the-art-of-sliding-and-the-aesthetics-of-symmetry/) — EOWC mode and incremental recomputation strategies
- [Synnada: Running Windowing Queries in Stream Processing](https://www.synnada.ai/blog/running-window-query-in-stream-processing) — Streamability conditions, partition detection, ordering requirements
- [Upsolver: SQL Window Function in Stream Analytics](https://www.upsolver.com/blog/sql-window-function-in-stream-analytics) — Window close semantics and bounded buffering
