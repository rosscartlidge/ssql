# Plan: SQL-Like CLI Commands for ssql

## Current State (CLI Commands)

**Existing Commands:**
- `read-csv` - Data source (FROM table)
- `where` - Filtering (WHERE clause) ✅
- `select` - Field projection/renaming (SELECT fields) ✅
- `group` - Grouping + aggregations (GROUP BY with COUNT/SUM/AVG/MIN/MAX) ✅
- `sort` - Ordering (ORDER BY single field, ASC/DESC) ✅
- `limit` - Result limiting (LIMIT N) ✅
- `write-csv` - Data sink (INTO OUTFILE)
- `chart` - Visualization
- `exec` - Execute commands as data source
- `generate go` - Code generation

**Existing Library Functions (Not Yet in CLI):**
- `InnerJoin`, `LeftJoin`, `RightJoin`, `FullJoin` - All JOIN types
- `Offset` - Skip N records (OFFSET N)
- `Distinct`, `DistinctBy` - Remove duplicates (DISTINCT)

## Missing SQL Features

### Priority 1: Essential SQL Operations

#### 1. JOIN Command
**SQL:** `SELECT * FROM a INNER JOIN b ON a.id = b.id`
**Current:** Not available in CLI
**Library:** ✅ Already implemented (`InnerJoin`, `LeftJoin`, `RightJoin`, `FullJoin`)

**Proposed CLI Syntax:**
```bash
# Inner join
ssql read-csv employees.csv | \
  ssql join -type inner -right departments.csv -on department_id

# Left join with explicit field names
ssql read-csv employees.csv | \
  ssql join -type left -right departments.csv \
    -left-field dept_id -right-field id

# Join on multiple fields
ssql read-csv employees.csv | \
  ssql join -type inner -right assignments.csv \
    -on employee_id -on department_id
```

**Implementation:**
- Read right-side file into memory (materialized)
- Support `-type` flag: inner, left, right, full (default: inner)
- Support `-on field` for simple equality joins (accumulate multiple fields)
- Support `-left-field` and `-right-field` for different field names
- Use library's `OnFields()` or `OnCondition()` predicates

#### 2. DISTINCT Command
**SQL:** `SELECT DISTINCT department FROM employees`
**Current:** Not available in CLI
**Library:** ✅ Already implemented (`Distinct`, `DistinctBy`)

**Proposed CLI Syntax:**
```bash
# Distinct on all fields (whole record)
ssql read-csv data.csv | ssql distinct

# Distinct on specific fields
ssql read-csv data.csv | \
  ssql distinct -by department -by location
```

**Implementation:**
- No flags = use `Distinct()` on full records
- `-by field` flags = use `DistinctBy()` with composite key
- Accumulate `-by` flags to support multiple fields

#### 3. OFFSET Command
**SQL:** `SELECT * FROM employees LIMIT 10 OFFSET 20`
**Current:** `limit` exists, but no `offset`
**Library:** ✅ Already implemented (`Offset`)

**Proposed CLI Syntax:**
```bash
# Skip first 20 records
ssql read-csv data.csv | ssql offset -n 20

# Pagination: skip 20, take 10
ssql read-csv data.csv | \
  ssql offset -n 20 | \
  ssql limit -n 10
```

**Implementation:**
- Use library's `Offset()` function
- `-n` flag for number of records to skip

#### 4. UNION/UNION ALL Command
**SQL:** `SELECT * FROM a UNION SELECT * FROM b`
**Current:** Not available
**Library:** ❌ Need to implement

**Proposed CLI Syntax:**
```bash
# Union (distinct)
ssql read-csv file1.csv | \
  ssql union file2.csv file3.csv

# Union all (keep duplicates)
ssql read-csv file1.csv | \
  ssql union -all file2.csv file3.csv
```

**Implementation:**
- Read files from positional arguments
- Default: apply Distinct at the end (UNION)
- `-all` flag: skip Distinct (UNION ALL)
- Chain iterators together

### Priority 2: Enhanced SQL Operations

#### 5. Enhanced SORT Command
**SQL:** `SELECT * FROM employees ORDER BY dept ASC, salary DESC`
**Current:** `sort -field X [-desc]` (single field only)
**Library:** ✅ `SortBy` can handle complex keys

**Proposed Enhancement:**
```bash
# Multiple sort fields with mixed order
ssql read-csv data.csv | \
  ssql sort -field department + -field salary -desc

# Alternative: use repeating flags
ssql read-csv data.csv | \
  ssql sort -field department -asc -field salary -desc
```

**Implementation:**
- Support multiple `-field` clauses with `+` separator
- Each clause can have `-asc` or `-desc` (default asc)
- Generate composite sort key function

#### 6. HAVING Command (Post-GROUP BY Filter)
**SQL:** `SELECT dept, COUNT(*) FROM emp GROUP BY dept HAVING COUNT(*) > 5`
**Current:** Must use `where` after `group` (works but not SQL-like naming)
**Library:** ✅ Can use `Where` after `GroupByFields`/`Aggregate`

**Proposed CLI Syntax:**
```bash
ssql read-csv data.csv | \
  ssql group -by department -function count -result total | \
  ssql having -match total gt 5
```

**Implementation:**
- Essentially an alias for `where` that works on aggregated results
- Same syntax as `where` command
- Makes SQL translation more direct

### Priority 3: Advanced SQL Features

#### 7. DERIVE/COMPUTE Command (Calculated Columns)
**SQL:** `SELECT name, salary, salary * 1.1 AS new_salary FROM employees`
**Current:** Must use Go code generation
**Library:** ❌ Need expression evaluator

**Proposed CLI Syntax:**
```bash
# Simple arithmetic
ssql read-csv data.csv | \
  ssql derive -expr "salary * 1.1" -as new_salary

# Multiple derivations
ssql read-csv data.csv | \
  ssql derive -expr "salary * 1.1" -as new_salary + \
    -expr "age + 1" -as next_year_age
```

**Implementation:**
- Need expression parser/evaluator (consider: govaluate, expr, or simple eval)
- Support basic arithmetic: +, -, *, /
- Support field references
- Type-safe evaluation

#### 8. CASE/WHEN Command (Conditional Logic)
**SQL:** `SELECT CASE WHEN age < 18 THEN 'minor' ELSE 'adult' END AS status`
**Current:** Not available
**Library:** ❌ Need to implement

**Proposed CLI Syntax:**
```bash
ssql read-csv data.csv | \
  ssql case -field age \
    -when "lt 18" -then minor \
    -when "ge 18" -then adult \
    -else unknown \
    -as age_group
```

**Implementation:**
- Could be complex - consider deferring to Priority 4
- Needs condition parsing

## Implementation Roadmap

### Phase 1: Essential SQL Commands (v0.7.0) ✅ COMPLETED
**Goal:** Match 80% of common SQL queries

1. ✅ `join` - All JOIN types (INNER, LEFT, RIGHT, FULL)
2. ✅ `distinct` - Remove duplicates
3. ✅ `offset` - Skip N records
4. ✅ `union` - Combine datasets

**Status:** COMPLETE (v0.7.0 released)
**Impact:** High - enables multi-table queries

### Phase 2: Enhanced Commands (v0.7.1)
**Goal:** Improve existing SQL operations

1. ✅ Enhanced `sort` - Multi-field sorting
2. ✅ `having` - Post-aggregation filtering (alias for where)

**Estimated:** 1 day
**Impact:** Medium - improves SQL compatibility

### Phase 3: Advanced Features (v0.8.0)
**Goal:** Support computed columns and expressions

1. ⚠️ `derive` - Calculated columns
2. ⚠️ `case` - Conditional expressions

**Estimated:** 3-4 days
**Impact:** Medium - requires expression evaluator

## Command Design Principles

### 1. Consistent Flag Naming
- `-type` for operation types (join type, etc.)
- `-on` for equality conditions
- `-by` for grouping/distincting keys
- `-field` for field names
- `-as` for aliases

### 2. Multi-Argument Patterns
- Use `.Arg()` API for flags with multiple arguments
- Use `+` separator for OR logic (clauses)
- Use `-accumulate` for repeated conditions (AND logic)

### 3. Backward Compatibility
- Don't break existing commands
- Enhance existing commands carefully
- Add new commands for new functionality

### 4. Code Generation Support
- Every command must support `-generate` flag
- Generate equivalent Go code using ssql library
- Pass through code fragments correctly

## SQL Feature Comparison

| SQL Feature | Current CLI | Priority | Library Support | Effort |
|-------------|-------------|----------|-----------------|--------|
| SELECT fields | `select` | ✅ Done | ✅ | - |
| WHERE | `where` | ✅ Done | ✅ | - |
| GROUP BY | `group` | ✅ Done | ✅ | - |
| ORDER BY | `sort` | ✅ Done | ✅ | - |
| LIMIT | `limit` | ✅ Done | ✅ | - |
| OFFSET | `offset` | ✅ Done | ✅ | - |
| DISTINCT | `distinct` | ✅ Done | ✅ | - |
| INNER JOIN | `join` | ✅ Done | ✅ | - |
| LEFT JOIN | `join` | ✅ Done | ✅ | - |
| RIGHT JOIN | `join` | ✅ Done | ✅ | - |
| FULL JOIN | `join` | ✅ Done | ✅ | - |
| UNION | `union` | ✅ Done | ✅ | - |
| UNION ALL | `union -all` | ✅ Done | ✅ | - |
| HAVING | ❌ Missing | P2 | ✅ | Low |
| Multi-field ORDER BY | ⚠️ Partial | P2 | ✅ | Medium |
| Computed columns | ❌ Missing | P3 | ❌ | High |
| CASE/WHEN | ❌ Missing | P3 | ❌ | High |

## Examples: SQL to ssql CLI

### Example 1: Simple Join
**SQL:**
```sql
SELECT e.name, e.salary, d.department_name
FROM employees e
INNER JOIN departments d ON e.dept_id = d.id
WHERE e.salary > 50000
```

**ssql CLI (After Implementation):**
```bash
ssql read-csv employees.csv | \
  ssql join -type inner -right departments.csv \
    -left-field dept_id -right-field id | \
  ssql where -match salary gt 50000 | \
  ssql select -field name + -field salary + -field department_name
```

### Example 2: Group By with Having
**SQL:**
```sql
SELECT department, COUNT(*) as total, AVG(salary) as avg_salary
FROM employees
GROUP BY department
HAVING COUNT(*) > 5
ORDER BY avg_salary DESC
```

**ssql CLI (After Implementation):**
```bash
ssql read-csv employees.csv | \
  ssql group -by department \
    -function count -result total + \
    -function avg -field salary -result avg_salary | \
  ssql having -match total gt 5 | \
  ssql sort -field avg_salary -desc
```

### Example 3: Union
**SQL:**
```sql
SELECT name, city FROM customers
UNION
SELECT name, city FROM suppliers
```

**ssql CLI (After Implementation):**
```bash
ssql read-csv customers.csv | \
  ssql select -field name + -field city | \
  ssql union suppliers.csv
```

## Testing Strategy

1. **Unit Tests**: Test each command in isolation
2. **Integration Tests**: Test SQL-equivalent pipelines
3. **Example Scripts**: Create `examples/sql-equivalents/` with side-by-side comparisons
4. **Documentation**: Update CLI tutorial with SQL comparison table

## Documentation Updates Needed

1. **CLI Tutorial** - Add SQL translation guide
2. **Command Reference** - Document all new commands
3. **Examples** - Add `examples/sql-queries/` directory
4. **Troubleshooting** - Add SQL-to-CLI troubleshooting section

## Version Tagging

- **v0.7.0** - Phase 1 complete (join, distinct, offset, union)
- **v0.7.1** - Phase 2 complete (enhanced sort, having)
- **v0.8.0** - Phase 3 complete (derive, case)

## Next Steps

1. Review and approve this plan
2. Create feature branches for each phase
3. Implement Phase 1 commands (highest priority)
4. Update version to 0.7.0 when Phase 1 complete
