# AI Prompt Fix Request

**Generated**: 2026-04-21 21:00
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
||Read users.csv and set tier to "senior" where age >= 40, set tier to "mid" where age >= 30, otherwise set tier to "junior".|||missing: +
```

---

## Failure 2: 

**Prompt**: 

**Issues found**:
```
CLI-05
```

**Generated output** (`/tmp/ssql-ai-test-results/.sh`):
```bash
||Compute a spectrogram of the value field from measurements.csv with a window size of 4, sample rate 10.|||output missing: time_index
```

---

## Failure 3: 

**Prompt**: 

**Issues found**:
```
CLI-06
```

**Generated output** (`/tmp/ssql-ai-test-results/.sh`):
```bash
||Join orders.csv with customers.csv matching customer_id field on both sides, and rename the customer name field to customer_name.|||missing: -as name customer_name` or `-as
```

---

## Failure 4: 

**Prompt**: 

**Issues found**:
```
CLI-07
```

**Generated output** (`/tmp/ssql-ai-test-results/.sh`):
```bash
||Read orders.csv, filter where quantity * 25 > 60, and add a total field computed as quantity * 25.|||missing: -if-expr` or `-set-expr
```

---

## Failure 5: 

**Prompt**: 

**Issues found**:
```
CLI-08
```

**Generated output** (`/tmp/ssql-ai-test-results/.sh`):
```bash
||Read users.csv, sort by salary descending, skip the first 2, and show the next 3 users.|||missing: ssql offset
```

---

## Failure 6: 

**Prompt**: 

**Issues found**:
```
CLI-09
```

**Generated output** (`/tmp/ssql-ai-test-results/.sh`):
```bash
||Generate a standalone Go program from this pipeline: read users.csv, filter where status equals active, group by dept (as positional arg), count per dept, output to stdout. Remember to export SSQLGO=1 so all pipeline commands see it.|||missing: ssql generate go
```

---

## Failure 7: 

**Prompt**: 

**Issues found**:
```
CLI-10
```

**Generated output** (`/tmp/ssql-ai-test-results/.sh`):
```bash
||Read data from users.csv, filter for active users, then write the output as JSON.|||output missing: "status":"active"` or `"status": "active"
```

---

## Failure 8: 

**Prompt**: 

**Issues found**:
```
CLI-11
```

**Generated output** (`/tmp/ssql-ai-test-results/.sh`):
```bash
||Read users.csv, filter for active status, group by dept (as positional arg), compute count and average salary, sort by count descending, output as a table.|||missing: -count` or `-avg
```

---

## Failure 9: 

**Prompt**: 

**Issues found**:
```
CLI-13
```

**Generated output** (`/tmp/ssql-ai-test-results/.sh`):
```bash
||Combine records from source_a.csv and source_b.csv, removing any duplicates. Use ssql union with -file flag for the second file (wrapped in process substitution for CSV).|||missing: ssql from source_a.csv
```

---

## Failure 10: 

**Prompt**: 

**Issues found**:
```
CLI-14
```

**Generated output** (`/tmp/ssql-ai-test-results/.sh`):
```bash
||Read users.csv, sort by name, skip the first 3 records, and show the next 2.|||missing: ssql offset 3
```

---

