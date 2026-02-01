# AI Prompt Fix Request

**Generated**: 2026-02-01 15:28
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
CLI-03
```

**Generated output** (`/tmp/ssql-ai-test-results/.sh`):
```bash
||Read users.csv and set tier to "senior" where age >= 40, set tier to "mid" where age >= 30, otherwise set tier to "junior".|||output missing: tier
```

---

## Failure 2: 

**Prompt**: 

**Issues found**:
```
CLI-10
```

**Generated output** (`/tmp/ssql-ai-test-results/.sh`):
```bash
||Read data from users.csv, filter for active users, then write the output as JSON.|||output missing: "status":"active"
```

---

