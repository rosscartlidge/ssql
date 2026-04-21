# AI Prompt Fix Request

**Generated**: 2026-04-21 16:58
**Mode**: go
**Prompt file**: /home/rossc/src/ssql/doc/ai-code-generation.md

## Summary

The following test cases failed. Please update the prompt file to fix these issues.

**Rules:**
- Do NOT modify the test cases in `doc/ai-test-cases.md`
- Only modify the prompt file: `/home/rossc/src/ssql/doc/ai-code-generation.md`
- Focus on adding missing patterns, clarifying instructions, or adding examples
- Keep changes minimal and targeted to fix the specific failures

---

## Failure 1: 

**Prompt**: 

**Issues found**:
```
GO-04
```

**Generated output** (`/tmp/ssql-ai-test-results/.go`):
```go
||Compute a spectrogram of a signal from measurements.csv (field "value", sample rate 10 Hz) using a Hann window of size 4 with hop size 2. Output the first 5 bins as JSON to stdout.|||compile error: # ssql-ai-test
```

---

## Failure 2: 

**Prompt**: 

**Issues found**:
```
GO-15
```

**Generated output** (`/tmp/ssql-ai-test-results/.go`):
```go
||Read events from users.jsonl (JSONL format), filter where status equals "active", and count total matching records. Output the count to stdout.|||compile error: # ssql-ai-test
```

---

