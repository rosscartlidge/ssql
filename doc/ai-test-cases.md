# AI Prompt Test Cases

Structured test catalog for validating both the Go code generation prompt and the CLI pipeline generation prompt. Each test case has objective validation criteria that can be checked automatically, including **integration tests** that run the generated code and verify output.

---

## Test Format

Each test case specifies:
- **Prompt**: Natural language input given to the LLM
- **Expected patterns**: Strings that MUST appear in the output
- **Negative patterns**: Strings that MUST NOT appear in the output
- **Validation**: `compile` (Go code must compile) or `parse` (CLI patterns must match)
- **Test Data**: Input file(s) from `test-data/` directory
- **Expected Output**: Patterns or exact values that must appear in execution output

---

## Go Code Generation Tests

### GO-01: Basic Filter and Aggregate

**Prompt**: Read employee data from employees.csv, filter for employees with salary over 80000, group by the dept field, and count employees per dept. Write the results as JSON to os.Stdout.

**Expected patterns**:
- `ssql.ReadCSV("employees.csv")`
- `ssql.Where(`
- `ssql.GetOr(`
- `ssql.GroupByFields(`
- `ssql.Aggregate(`
- `ssql.Count()`
- `ssql.Chain(`
- `os.Stdout`
- `github.com/rosscartlidge/ssql/v4`

**Negative patterns**:
- `record["` (direct map access)
- `ssql.Filter(` (wrong name)
- `ssql.GroupBy(` (wrong name, must be GroupByFields)
- `Count("` (Count takes no parameters)
- `github.com/rosscartlidge/ssql"` (missing /v4)

**Validation**: compile

**Test Data**: `test-data/employees.csv`

**Expected Output**:
- `dept`
- `Engineering`

---

### GO-02: Top N with Sort

**Prompt**: Read orders.csv, group by product_id, compute total quantity sold for each product, sort by total descending, and show the top 3 products. Write the results as JSON to os.Stdout.

**Expected patterns**:
- `ssql.ReadCSV("orders.csv")`
- `ssql.GroupByFields(`
- `ssql.Sum(`
- `ssql.SortBy(`
- `ssql.Limit[ssql.Record](3)`
- `ssql.Chain(`
- `os.Stdout`

**Negative patterns**:
- `ssql.Take(` (wrong name)
- `ssql.OrderBy(` (wrong name)
- `record["` (direct map access)

**Validation**: compile

**Test Data**: `test-data/orders.csv`

**Expected Output**:
- `product_id`
- `P2` or `P1`

---

### GO-03: Signal Processing FFT

**Prompt**: Read a signal from measurements.csv (field name "value"), compute the FFT with sample rate 10 Hz, convert the spectrum to records with frequency and magnitude, and write the result as JSON to stdout.

**Expected patterns**:
- `ssql.ReadCSV("measurements.csv")`
- `ssql.ExtractSignal(`
- `ssql.FFT(` or `ssql.FFTWithPhase(`
- `ssql.SpectrumToRecords(`

**Negative patterns**:
- `fft.Transform(` (wrong package)
- `record["` (direct map access)

**Validation**: compile

**Test Data**: `test-data/measurements.csv`

**Expected Output**:
- `frequency`
- `magnitude`

---

### GO-04: Spectrogram Analysis

**Prompt**: Compute a spectrogram of a signal from measurements.csv (field "value", sample rate 10 Hz) using a Hann window of size 4 with hop size 2. Output the first 5 bins as JSON to stdout.

**Expected patterns**:
- `ssql.ReadCSV("measurements.csv")`
- `ssql.ExtractSignal(`
- `ssql.Spectrogram(`
- `ssql.SpectrogramOptions`
- `WindowSize` or `WindowSize:`
- `HopSize` or `HopSize:`
- `SampleRate` or `SampleRate:`
- `ssql.SpectrogramToRecords(`

**Negative patterns**:
- `record["` (direct map access)
- `STFT(` (wrong function name)

**Validation**: compile

**Test Data**: `test-data/measurements.csv`

**Expected Output**:
- `time_index`
- `frequency`
- `magnitude`

---

### GO-05: Update with Computed Fields

**Prompt**: Read orders.csv, add a "total" field computed from quantity * 25 (assuming fixed price of 25), then output as JSON to stdout.

**Expected patterns**:
- `ssql.ReadCSV("orders.csv")`
- `ssql.Update(`
- `MutableRecord`
- `.Freeze()`
- `ssql.GetOr(`
- `.Float(` or `.Int(`

**Negative patterns**:
- `record["` (direct map access)
- `SetAny(` (removed in v2)
- `r["quantity"]` (direct map access)

**Validation**: compile

**Test Data**: `test-data/orders.csv`

**Expected Output**:
- `total`
- `50` or `75` or `25`

---

### GO-06: Join with Lookup

**Prompt**: Join orders from orders.csv with customer data from customers.csv using the customer_id field, output as JSON to stdout.

**Expected patterns**:
- `ssql.ReadCSV(`
- `ssql.LeftJoin(` or `ssql.InnerJoin(`
- `ssql.OnFields("customer_id")` or `ssql.OnFieldPair(`

**Negative patterns**:
- `record["` (direct map access)
- `ssql.Join(` (bare Join doesn't exist, must be InnerJoin/LeftJoin etc)
- `sql.Join(` (wrong package)

**Validation**: compile

**Test Data**: `test-data/orders.csv`, `test-data/customers.csv`

**Expected Output**:
- `customer_id`
- `name`
- `Acme Corp` or `Beta Inc`

---

### GO-07: Update with Conditional Logic

**Prompt**: Read orders.csv and add a "size" field: "large" if quantity > 2, "medium" if quantity == 2, otherwise "small". Output as JSON to stdout.

**Expected patterns**:
- `ssql.ReadCSV("orders.csv")`
- `ssql.Update(`
- `MutableRecord`
- `ssql.GetOr(`
- `large`
- `medium` or `small`
- `.String(`

**Negative patterns**:
- `record["` (direct map access)
- `r["quantity"]` (direct map access)

**Validation**: compile

**Test Data**: `test-data/orders.csv`

**Expected Output**:
- `size`
- `large`
- `small`

---

### GO-08: JSON I/O Pipeline

**Prompt**: Read data from users.jsonl (JSONL format), filter records where status equals "active", and write the result as JSON to stdout.

**Expected patterns**:
- `ssql.ReadJSON(`
- `ssql.Where(`
- `ssql.GetOr(`
- `if err != nil`

**Negative patterns**:
- `record["` (direct map access)
- `json.Unmarshal` (should use ssql's reader)
- `ReadCSV` (wrong format)

**Validation**: compile

**Test Data**: `test-data/users.jsonl`

**Expected Output**:
- `active`
- `name`

---

### GO-09: Convolution Pipeline

**Prompt**: Read a signal from measurements.csv (field "value"), smooth it with a Gaussian kernel of size 5 and sigma 1.0, and output the smoothed values as JSON to stdout.

**Expected patterns**:
- `ssql.ReadCSV("measurements.csv")`
- `ssql.ExtractSignal(` or `ssql.ExtractSignalFromSlice(`
- `ssql.ConvolveSame(` or `ssql.Convolve(`
- `ssql.GaussianKernel(5`

**Negative patterns**:
- `record["` (direct map access)
- `convolve(` (wrong casing)
- `ssql.FromSlice` (doesn't exist, use slices.Values)

**Validation**: compile

**Test Data**: `test-data/measurements.csv`

**Expected Output**:
- (any numeric output indicates successful convolution)

---

### GO-10: Distinct and Union

**Prompt**: Read data from source_a.csv and source_b.csv, combine them, remove duplicates based on all fields, and output unique records as JSON to stdout.

**Expected patterns**:
- `ssql.ReadCSV(`
- `ssql.Concat(` or `ssql.DistinctBy(`
- `ssql.RecordKey`

**Negative patterns**:
- `record["` (direct map access)
- `ssql.Union(` (wrong name, Union is CLI only)
- `append(` (should use Concat, not Go append)

**Validation**: compile

**Test Data**: `test-data/source_a.csv`, `test-data/source_b.csv`

**Expected Output**:
- `Alpha`
- `Delta`
- `Gamma`

---

## CLI Pipeline Generation Tests

### CLI-01: Basic Filter Pipeline

**Prompt**: Read users.csv, filter for users older than 30, show only name and email columns, output as a table.

**Expected patterns**:
- `ssql from users.csv`
- `ssql where`
- `-where age`
- `gt 30`
- `ssql include`
- `name`
- `email`
- `ssql to table`
- `|` (pipe character)

**Negative patterns**:
- `read-csv` (old command name)
- `write-csv` (old command name)
- `-match` (old flag name, use -where)
- `ssql where users.csv` (transform commands don't take FILE)

**Validation**: parse

**Test Data**: `test-data/users.csv`

**Expected Output**:
- `Alice`
- `Carol`
- `Frank`
- `alice@example.com`

---

### CLI-02: Group-By with Aggregation

**Prompt**: Read users.csv, group by dept, count the number of users in each department. The group-by command takes field names as positional arguments.

**Expected patterns**:
- `ssql from users.csv`
- `ssql group-by`
- `dept`
- `-count`
- `|`

**Negative patterns**:
- `read-csv` (old command)
- `-match` (old flag)
- `ssql group-by users.csv` (transform commands don't take FILE)
- `-field` (wrong - use positional args)

**Validation**: parse

**Test Data**: `test-data/users.csv`

**Expected Output**:
- `Engineering`
- `Sales`
- `Marketing`
- `3`
- `2`

---

### CLI-03: Update with If-Else Clauses

**Prompt**: Read users.csv and set tier to "senior" where age >= 40, set tier to "mid" where age >= 30, otherwise set tier to "junior".

**Expected patterns**:
- `ssql from users.csv`
- `ssql update`
- `-where`
- `-set tier`
- `senior`
- `mid` or `junior`
- `+` (clause separator)
- `|`

**Negative patterns**:
- `-match` (old flag name)
- `ssql update users.csv` (transform commands don't take FILE)

**Validation**: parse

**Test Data**: `test-data/users.csv`

**Expected Output**:
- `tier`
- `senior`
- `mid`

---

### CLI-04: Signal Processing FFT

**Prompt**: Read measurements.csv and compute the FFT of the value field at a sample rate of 10 Hz, output as a table.

**Expected patterns**:
- `ssql from measurements.csv`
- `ssql fft`
- `-field value`
- `-rate 10`
- `ssql to table`
- `|`

**Negative patterns**:
- `ssql fft measurements.csv` (transform commands don't take FILE)

**Validation**: parse

**Test Data**: `test-data/measurements.csv`

**Expected Output**:
- `frequency`
- `magnitude`

---

### CLI-05: Spectrogram

**Prompt**: Compute a spectrogram of the value field from measurements.csv with a window size of 4, sample rate 10.

**Expected patterns**:
- `ssql from measurements.csv`
- `ssql spectrogram`
- `-field value`
- `-window-size 4`
- `-rate 10`
- `|`

**Negative patterns**:
- `ssql spectrogram measurements.csv` (transform commands don't take FILE)
- `stft` (wrong command name)

**Validation**: parse

**Test Data**: `test-data/measurements.csv`

**Expected Output**:
- `time_index`
- `frequency`
- `magnitude`

---

### CLI-06: Join with Rename

**Prompt**: Join orders.csv with customers.csv matching customer_id field on both sides, and rename the customer name field to customer_name.

**Expected patterns**:
- `ssql from orders.csv`
- `ssql join customers.csv`
- `-using customer_id` or `-on customer_id`
- `-as name customer_name` or `-as`
- `|`

**Negative patterns**:
- `-right` (old flag)
- `-left-field` (old flag)
- `-right-field` (old flag)

**Validation**: parse

**Test Data**: `test-data/orders.csv`, `test-data/customers.csv`

**Expected Output**:
- `customer_id`
- `order_id`

---

### CLI-07: Expression Filter and Compute

**Prompt**: Read orders.csv, filter where quantity * 25 > 60, and add a total field computed as quantity * 25.

**Expected patterns**:
- `ssql from orders.csv`
- `-where-expr` or `-set-expr`
- `quantity`
- `|`

**Negative patterns**:
- `where -expr` (old flag: use -where-expr instead)
- `update -expr` (old flag: use -set-expr instead)
- `-match` (old flag name)

**Validation**: parse

**Test Data**: `test-data/orders.csv`

**Expected Output**:
- `total`
- `75`

---

### CLI-08: Sort, Limit, Offset

**Prompt**: Read users.csv, sort by salary descending, skip the first 2, and show the next 3 users.

**Expected patterns**:
- `ssql from users.csv`
- `ssql sort`
- `salary`
- `-desc` or `desc`
- `ssql offset`
- `2`
- `ssql limit`
- `3`
- `|`

**Negative patterns**:
- `ssql sort users.csv` (transform commands don't take FILE)
- `OFFSET` (SQL keyword, not ssql flag)
- `LIMIT` (SQL keyword, not ssql flag)

**Validation**: parse

**Test Data**: `test-data/users.csv`

**Expected Output**:
- (should show 3 users after skipping top 2 salaries)

---

### CLI-09: Code Generation Pipeline

**Prompt**: Generate a standalone Go program from this pipeline: read users.csv, filter where status equals active, group by dept (as positional arg), count per dept, output to stdout.

**Expected patterns**:
- `SSQLGO=1`
- `ssql from users.csv`
- `ssql where`
- `-where status eq active`
- `ssql group-by`
- `dept`
- `-count`
- `ssql generate-go`
- `|`

**Negative patterns**:
- `-generate` (prefer SSQLGO=1 for full pipelines)
- `read-csv` (old command)
- `-field` (wrong - use positional args for group-by)

**Validation**: parse

**Test Data**: `test-data/users.csv`

**Expected Output**:
- `package main`
- `ssql.ReadCSV`

---

### CLI-10: Multi-Format Pipeline

**Prompt**: Read data from users.csv, filter for active users, then write the output as JSON.

**Expected patterns**:
- `ssql from`
- `.csv`
- `ssql to json`
- `|`

**Negative patterns**:
- `write-csv` (old command)
- `write-json` (old command)
- `> output.json` (should use ssql to json, not shell redirect)

**Validation**: parse

**Test Data**: `test-data/users.csv`

**Expected Output**:
- `"status":"active"`
- `"name"`

---

## Extended Tests

### GO-11: Multi-Stage Pipeline with Chain

**Prompt**: Read users.csv, filter for active status, sort by salary descending, take the first 3, and output as JSON to stdout.

**Expected patterns**:
- `ssql.ReadCSV("users.csv")`
- `ssql.Where(`
- `ssql.SortBy(`
- `ssql.Limit[ssql.Record](3)` or `ssql.Take[ssql.Record](3)`
- `ssql.Chain(`

**Negative patterns**:
- `record["` (direct map access)
- `ssql.Limit(3)` (missing type parameter)
- `Pipe(` (wrong function, use Chain)

**Validation**: compile

**Test Data**: `test-data/users.csv`

**Expected Output**:
- `Frank`
- `Carol`
- `Alice`

---

### GO-12: Update with Computed Fields

**Prompt**: Read users.csv and add a "bonus" field computed as salary * 0.1, then add a "level" field set to "senior" if age >= 35, otherwise "standard". Output as JSON to stdout.

**Expected patterns**:
- `ssql.ReadCSV("users.csv")`
- `ssql.Update(`
- `ssql.MutableRecord` or `MutableRecord`
- `ssql.GetOr(`
- `Float(` or `Int(`
- `String(`

**Negative patterns**:
- `record["` (direct map access)
- `SetAny(` (removed in v2)
- `.Set(` (wrong method, use typed setters)

**Validation**: compile

**Test Data**: `test-data/users.csv`

**Expected Output**:
- `bonus`
- `level`
- `senior`
- `standard`

---

### GO-13: Safe Field Access with Defaults

**Prompt**: Read users.csv and calculate the average salary using GetOr with a default of 0 for missing values. Output the average as a simple number to stdout.

**Expected patterns**:
- `ssql.ReadCSV("users.csv")`
- `ssql.GetOr(`
- `int64(0)` or `float64(0)` or `, 0)` or `, 0.0)`

**Negative patterns**:
- `record["salary"]` (unsafe direct access)
- `r["salary"]` (unsafe direct access)
- `.(int)` (type assertion without GetOr)
- `.(float64)` (type assertion without GetOr)

**Validation**: compile

**Test Data**: `test-data/users.csv`

**Expected Output**:
- (any numeric output for average)

---

### GO-14: Early Limit for Performance

**Prompt**: Read users.csv and get just the first 2 records that match a filter where status equals "active". Output as JSON to stdout.

**Expected patterns**:
- `ssql.ReadCSV("users.csv")`
- `ssql.Where(`
- `ssql.Limit[ssql.Record](2)` or `ssql.Take[ssql.Record](2)`
- `ssql.Chain(`

**Negative patterns**:
- `ssql.Limit(2)` (missing type parameter)
- `Collect(` (don't collect before limit for large files)

**Validation**: compile

**Test Data**: `test-data/users.csv`

**Expected Output**:
- `active`
- (should have exactly 2 records)

---

### GO-15: JSON Input with Filter

**Prompt**: Read events from users.jsonl (JSONL format), filter where status equals "active", and count total matching records. Output the count to stdout.

**Expected patterns**:
- `ssql.ReadJSON("users.jsonl")` or `ssql.ReadJSONFast(`
- `ssql.Where(`

**Negative patterns**:
- `json.Unmarshal` (use ssql.ReadJSON)
- `record["` (direct map access)

**Validation**: compile

**Test Data**: `test-data/users.jsonl`

**Expected Output**:
- (a number representing count of active users)

---

### CLI-11: Complex Multi-Stage Pipeline

**Prompt**: Read users.csv, filter for active status, group by dept (as positional arg), compute count and average salary, sort by count descending, output as a table.

**Expected patterns**:
- `ssql from users.csv`
- `ssql where`
- `ssql group-by`
- `dept`
- `-count` or `-avg`
- `ssql sort`
- `-desc`
- `ssql to table`
- `|`

**Negative patterns**:
- `read-csv` (old command)
- `write-table` (old command)
- `-field` (wrong - use positional args for group-by)

**Validation**: parse

**Test Data**: `test-data/users.csv`

**Expected Output**:
- `Engineering`
- `Sales`
- `count`

---

### CLI-12: Chart Visualization

**Prompt**: Read users.csv and create a chart showing salary by name, save to chart.html.

**Expected patterns**:
- `ssql from users.csv`
- `ssql to chart`
- `-x` or `-y`
- `.html`
- `|`

**Negative patterns**:
- `chart users.csv` (chart doesn't take FILE argument)
- `write-chart` (old command)

**Validation**: parse

**Test Data**: `test-data/users.csv`

**Expected Output**:
- `<!DOCTYPE html>` or `<html` or `Chart`

---

### CLI-13: Distinct with Union

**Prompt**: Combine records from source_a.csv and source_b.csv, removing any duplicates. Use ssql union with -file flag for the second file (wrapped in process substitution for CSV).

**Expected patterns**:
- `ssql from source_a.csv`
- `ssql union`
- `-file`
- `source_b.csv`
- `|`

**Negative patterns**:
- `concat` (union handles both files)
- `UNION` (SQL keyword, not ssql)
- `-distinct` (wrong - union deduplicates by default)

**Validation**: parse

**Test Data**: `test-data/source_a.csv`, `test-data/source_b.csv`

**Expected Output**:
- `Alpha`
- `Beta`
- `Gamma`
- `Delta`
- `Epsilon`

---

### CLI-14: Offset and Pagination

**Prompt**: Read users.csv, sort by name, skip the first 3 records, and show the next 2.

**Expected patterns**:
- `ssql from users.csv`
- `ssql sort`
- `ssql offset 3`
- `ssql limit 2`
- `|`

**Negative patterns**:
- `OFFSET` (SQL keyword)
- `LIMIT` (SQL keyword)
- `skip` (wrong command, use offset)

**Validation**: parse

**Test Data**: `test-data/users.csv`

**Expected Output**:
- (should show 2 users after alphabetical skip)

---

### CLI-15: Include/Exclude Field Selection

**Prompt**: Read users.csv and output only the name and dept fields.

**Expected patterns**:
- `ssql from users.csv`
- `ssql include` or `ssql exclude`
- `name`
- `dept`
- `|`

**Negative patterns**:
- `SELECT` (SQL keyword)
- `select` (wrong command, use include/exclude)
- `-fields` (wrong flag for include)

**Validation**: parse

**Test Data**: `test-data/users.csv`

**Expected Output**:
- `name`
- `dept`
- `Alice`
- `Engineering`

---

## Running Tests

```bash
# Run all tests (pattern matching only)
./scripts/test-ai-prompts.sh all

# Run all tests with integration testing
./scripts/test-ai-prompts.sh all --integration

# Run only Go code tests
./scripts/test-ai-prompts.sh go

# Run only CLI pipeline tests
./scripts/test-ai-prompts.sh cli

# Run via Makefile
make ai-test
make ai-test-integration
```

## Adding New Test Cases

To add a new test case:

1. Choose the appropriate section (Go or CLI)
2. Use the next sequential ID (GO-16, CLI-16, etc.)
3. Include all required fields: Prompt, Expected patterns, Negative patterns, Validation
4. Add Test Data and Expected Output for integration testing
5. Run `./scripts/test-ai-prompts.sh --integration` to verify the test works
6. If the test fails, update the corresponding prompt file to fix it
