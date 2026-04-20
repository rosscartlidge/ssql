# AI Prompt Fix Request

**Generated**: 2026-04-21 09:26
**Mode**: cli
**Prompt file**: /home/rossc/src/ssql/doc/ai-cli-generation.md

## Summary

The following test cases failed. Please update the prompt file to fix these issues.

**Rules:**
- Do NOT modify the test cases in `doc/ai-test-cases.md`
- Only modify the prompt file: `/home/rossc/src/ssql/doc/ai-cli-generation.md`
- Focus on adding missing patterns, clarifying instructions, or adding examples
- Keep changes minimal and targeted to fix the specific failures

---

## Failure 1: 

**Prompt**: 

**Issues found**:
```
CLI-09
```

**Generated output** (`/tmp/ssql-ai-test-results/.sh`):
```bash
||Generate a standalone Go program from this pipeline: read users.csv, filter where status equals active, group by dept (as positional arg), count per dept, output to stdout. Remember to export SSQLGO=1 so all pipeline commands see it.|||output missing: package main
```

---

