# AI Prompt Test Cases

Structured test catalog for validating both the Go code generation prompt and the CLI pipeline generation prompt. Each test case has objective validation criteria that can be checked automatically.

---

## Test Format

Each test case specifies:
- **Prompt**: Natural language input given to the LLM
- **Expected patterns**: Strings that MUST appear in the output
- **Negative patterns**: Strings that MUST NOT appear in the output
- **Validation**: `compile` (Go code must compile) or `parse` (CLI patterns must match)

---

## Go Code Generation Tests

### GO-01: Basic Filter and Aggregate

**Prompt**: Read employee data from employees.csv, filter for employees with salary over 80000, group by department, and count employees per department.

**Expected patterns**:
- `ssql.ReadCSV("employees.csv")`
- `ssql.Where(`
- `ssql.GetOr(`
- `ssql.GroupByFields(`
- `ssql.Aggregate(`
- `ssql.Count()`
- `ssql.Chain(`
- `if err != nil`
- `github.com/rosscartlidge/ssql/v4`

**Negative patterns**:
- `record["` (direct map access)
- `ssql.Filter(` (wrong name)
- `ssql.GroupBy(` (wrong name, must be GroupByFields)
- `Count("` (Count takes no parameters)
- `github.com/rosscartlidge/ssql"` (missing /v4)

**Validation**: compile

---

### GO-02: Top N with Sort

**Prompt**: Find the top 5 products by total revenue from sales.csv. Group by product_name and show the total revenue for each.

**Expected patterns**:
- `ssql.ReadCSV("sales.csv")`
- `ssql.GroupByFields(`
- `ssql.Sum("revenue")`
- `ssql.SortBy(`
- `ssql.Limit[ssql.Record](5)`
- `ssql.Chain(`
- `-ssql.GetOr(` (negative for descending sort)

**Negative patterns**:
- `ssql.Take(` (wrong name)
- `ssql.OrderBy(` (wrong name)
- `record["` (direct map access)

**Validation**: compile

---

### GO-03: Signal Processing FFT

**Prompt**: Read a signal from sensor_data.csv (field name "voltage"), compute the FFT, convert the spectrum to records with frequency and magnitude, and write the result to spectrum.csv.

**Expected patterns**:
- `ssql.ReadCSV("sensor_data.csv")`
- `ssql.ExtractSignal(`
- `ssql.FFT(` or `ssql.FFTWithPhase(`
- `ssql.SpectrumToRecords(`
- `ssql.WriteCSV(`

**Negative patterns**:
- `fft.Transform(` (wrong package)
- `record["` (direct map access)

**Validation**: compile

---

### GO-04: Spectrogram Analysis

**Prompt**: Compute a spectrogram of a signal from audio.csv (field "amplitude", sample rate 44100 Hz) using a Hann window of size 1024 with hop size 512. Output the result to spectrogram.csv.

**Expected patterns**:
- `ssql.ReadCSV("audio.csv")`
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

---

### GO-05: Update with Computed Fields

**Prompt**: Read orders.csv, add a total field computed from price * quantity, then add a tax field at 8% of total.

**Expected patterns**:
- `ssql.ReadCSV("orders.csv")`
- `ssql.Update(`
- `MutableRecord`
- `.Freeze()`
- `ssql.GetOr(`
- `.Float(`

**Negative patterns**:
- `record["` (direct map access)
- `SetAny(` (removed in v2)
- `r["price"]` (direct map access)

**Validation**: compile

---

### GO-06: Join with Lookup

**Prompt**: Join user data from users.csv with department data from departments.csv using the dept_id field, keeping all users even if they have no matching department.

**Expected patterns**:
- `ssql.ReadCSV(`
- `ssql.LeftJoin(` or `ssql.InnerJoin(`
- `ssql.OnFields("dept_id")` or `ssql.OnFieldPair(`

**Negative patterns**:
- `record["` (direct map access)
- `ssql.Join(` (bare Join doesn't exist, must be InnerJoin/LeftJoin etc)
- `sql.Join(` (wrong package)

**Validation**: compile

---

### GO-07: Update with Conditional Logic

**Prompt**: Read customers.csv and add a tier field: "Gold" if total_purchases > 5000, "Silver" if >= 1000, otherwise "Bronze".

**Expected patterns**:
- `ssql.ReadCSV("customers.csv")`
- `ssql.Update(`
- `MutableRecord`
- `ssql.GetOr(`
- `Gold`
- `Silver`
- `Bronze`
- `.String(`

**Negative patterns**:
- `record["` (direct map access)
- `r["total_purchases"]` (direct map access)

**Validation**: compile

---

### GO-08: JSON I/O Pipeline

**Prompt**: Read data from input.jsonl, filter records where status equals "active", and write the result to output.jsonl.

**Expected patterns**:
- `ssql.ReadJSON("input.jsonl")`
- `ssql.Where(`
- `ssql.GetOr(`
- `ssql.WriteJSON(`
- `if err != nil`

**Negative patterns**:
- `record["` (direct map access)
- `json.Unmarshal` (should use ssql's reader)
- `ReadCSV` (wrong format)

**Validation**: compile

---

### GO-09: Convolution Pipeline

**Prompt**: Read a signal from measurements.csv (field "value"), smooth it with a Gaussian kernel of size 11 and sigma 2.0, and write the smoothed signal back to smoothed.csv.

**Expected patterns**:
- `ssql.ReadCSV("measurements.csv")`
- `ssql.ExtractSignal(`
- `ssql.ConvolveSame(` or `ssql.Convolve(`
- `ssql.GaussianKernel(11`

**Negative patterns**:
- `record["` (direct map access)
- `convolve(` (wrong casing)
- `ssql.FromSlice` (doesn't exist, use slices.Values)

**Validation**: compile

---

### GO-10: Distinct and Union

**Prompt**: Read data from file_a.csv and file_b.csv, combine them, and remove duplicate records.

**Expected patterns**:
- `ssql.ReadCSV(`
- `ssql.Concat(` or `ssql.DistinctBy(`
- `ssql.RecordKey`

**Negative patterns**:
- `record["` (direct map access)
- `ssql.Union(` (wrong name, Union is CLI only)
- `append(` (should use Concat, not Go append)

**Validation**: compile

---

## CLI Pipeline Generation Tests

### CLI-01: Basic Filter Pipeline

**Prompt**: Read users.csv, filter for users older than 25, show only name and email columns, output as a table.

**Expected patterns**:
- `ssql from users.csv`
- `ssql where`
- `-where age`
- `gt 25`
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

---

### CLI-02: Group-By with Aggregation

**Prompt**: Read sales.csv, group by region, compute the count, total amount, and average amount per region.

**Expected patterns**:
- `ssql from sales.csv`
- `ssql group-by`
- `-field region` or `-fields region`
- `-count`
- `-sum amount`
- `-avg amount`
- `|`

**Negative patterns**:
- `read-csv` (old command)
- `-match` (old flag)
- `ssql group-by sales.csv` (transform commands don't take FILE)

**Validation**: parse

---

### CLI-03: Update with If-Else Clauses

**Prompt**: Read data.csv and set status to "premium" where revenue > 10000, set status to "standard" where revenue > 1000, otherwise set status to "basic".

**Expected patterns**:
- `ssql from data.csv`
- `ssql update`
- `-where`
- `-set status`
- `premium`
- `standard`
- `basic`
- `+` (clause separator)
- `|`

**Negative patterns**:
- `-match` (old flag name)
- `ssql update data.csv` (transform commands don't take FILE)

**Validation**: parse

---

### CLI-04: Signal Processing FFT

**Prompt**: Read sensor.csv and compute the FFT of the voltage field at a sample rate of 1000 Hz, output as a table.

**Expected patterns**:
- `ssql from sensor.csv`
- `ssql fft`
- `-field voltage`
- `-rate 1000`
- `ssql to table`
- `|`

**Negative patterns**:
- `ssql fft sensor.csv` (transform commands don't take FILE)

**Validation**: parse

---

### CLI-05: Spectrogram

**Prompt**: Compute a spectrogram of the amplitude field from audio.csv with a window size of 2048, sample rate 44100.

**Expected patterns**:
- `ssql from audio.csv`
- `ssql spectrogram`
- `-field amplitude`
- `-window-size 2048`
- `-rate 44100`
- `|`

**Negative patterns**:
- `ssql spectrogram audio.csv` (transform commands don't take FILE)
- `stft` (wrong command name)

**Validation**: parse

---

### CLI-06: Join with Rename

**Prompt**: Join users.csv with departments.csv matching user dept_id to department id, and rename the department name field to dept_name.

**Expected patterns**:
- `ssql from users.csv`
- `ssql join departments.csv`
- `-on dept_id id` or `-using`
- `-as name dept_name` or `-as`
- `|`

**Negative patterns**:
- `-right` (old flag)
- `-left-field` (old flag)
- `-right-field` (old flag)

**Validation**: parse

---

### CLI-07: Expression Filter and Compute

**Prompt**: Read transactions.csv, filter where amount * quantity > 500, and add a total field computed as amount * quantity.

**Expected patterns**:
- `ssql from transactions.csv`
- `-where-expr` or `-set-expr`
- `amount`
- `quantity`
- `|`

**Negative patterns**:
- `where -expr` (old flag: use -where-expr instead)
- `update -expr` (old flag: use -set-expr instead)
- `-match` (old flag name)

**Validation**: parse

---

### CLI-08: Sort, Limit, Offset

**Prompt**: Read products.csv, sort by price descending, skip the first 10, and show the next 5 products.

**Expected patterns**:
- `ssql from products.csv`
- `ssql sort`
- `price`
- `-desc` or `desc`
- `ssql offset`
- `10`
- `ssql limit`
- `5`
- `|`

**Negative patterns**:
- `ssql sort products.csv` (transform commands don't take FILE)
- `OFFSET` (SQL keyword, not ssql flag)
- `LIMIT` (SQL keyword, not ssql flag)

**Validation**: parse

---

### CLI-09: Code Generation Pipeline

**Prompt**: Generate a standalone Go program from this pipeline: read data.csv, filter where status equals active, group by category, count per category, output to result.csv.

**Expected patterns**:
- `SSQLGO=1`
- `ssql from data.csv`
- `ssql where`
- `-where status eq active`
- `ssql group-by`
- `-count`
- `ssql generate-go`
- `|`

**Negative patterns**:
- `-generate` (prefer SSQLGO=1 for full pipelines)
- `read-csv` (old command)

**Validation**: parse

---

### CLI-10: Multi-Format Pipeline

**Prompt**: Read data from a CSV file, filter and transform it, then write the output as JSON.

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

---

## Extended Tests - Error Handling and Edge Cases

### GO-11: Multi-Stage Pipeline with Chain

**Prompt**: Read logs.csv, filter for ERROR level entries, extract the timestamp and message fields, sort by timestamp descending, take the first 100, and write to recent_errors.csv.

**Expected patterns**:
- `ssql.ReadCSV("logs.csv")`
- `ssql.Where(`
- `ssql.Select(` or `ssql.Update(`
- `ssql.SortBy(`
- `ssql.Limit[ssql.Record](100)` or `ssql.Take[ssql.Record](100)`
- `ssql.Chain(`
- `ssql.WriteCSV(`

**Negative patterns**:
- `record["` (direct map access)
- `ssql.Limit(100)` (missing type parameter)
- `Pipe(` (wrong function, use Chain)

**Validation**: compile

---

### GO-12: Update with Computed Fields

**Prompt**: Read inventory.csv and add a "value" field computed as quantity * unit_price, then add a "status" field set to "low" if quantity < 10, otherwise "ok".

**Expected patterns**:
- `ssql.ReadCSV("inventory.csv")`
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

---

### GO-13: Safe Field Access with Defaults

**Prompt**: Read users.csv where the age field might be missing for some records. Calculate the average age, treating missing ages as 0.

**Expected patterns**:
- `ssql.ReadCSV("users.csv")`
- `ssql.GetOr(`
- `int64(0)` or `float64(0)` or `, 0)` or `, 0.0)`
- `ssql.Avg(` or `Sum(`

**Negative patterns**:
- `record["age"]` (unsafe direct access)
- `r["age"]` (unsafe direct access)
- `.(int)` (type assertion without GetOr)
- `.(float64)` (type assertion without GetOr)

**Validation**: compile

---

### GO-14: Early Limit for Performance

**Prompt**: Read a large file huge_data.csv and get just the first 10 records that match a filter where status equals "active".

**Expected patterns**:
- `ssql.ReadCSV("huge_data.csv")`
- `ssql.Where(`
- `ssql.Limit[ssql.Record](10)` or `ssql.Take[ssql.Record](10)`
- `ssql.Chain(`

**Negative patterns**:
- `ssql.Limit(10)` (missing type parameter)
- `Collect(` (don't collect before limit for large files)

**Validation**: compile

---

### GO-15: JSON Input with Nested Fields

**Prompt**: Read events from events.jsonl, filter where the event type is "click", and count events per user_id.

**Expected patterns**:
- `ssql.ReadJSON("events.jsonl")` or `ssql.ReadJSONFast(`
- `ssql.Where(`
- `ssql.GroupByFields(`
- `ssql.Count()`

**Negative patterns**:
- `json.Unmarshal` (use ssql.ReadJSON)
- `record["` (direct map access)

**Validation**: compile

---

### CLI-11: Complex Multi-Stage Pipeline

**Prompt**: Read sales.csv, filter for 2024 transactions, group by product and region, compute total and average sales, sort by total descending, take top 20, output as a table.

**Expected patterns**:
- `ssql from sales.csv`
- `ssql where`
- `ssql group-by`
- `-sum` or `-avg`
- `ssql sort`
- `-desc`
- `ssql limit 20`
- `ssql to table`
- `|`

**Negative patterns**:
- `read-csv` (old command)
- `write-table` (old command)

**Validation**: parse

---

### CLI-12: Chart Visualization

**Prompt**: Read sensor.csv and create a line chart showing temperature over time, save to sensor_chart.html.

**Expected patterns**:
- `ssql from sensor.csv`
- `ssql to chart`
- `-x` or `-y`
- `.html`
- `|`

**Negative patterns**:
- `chart sensor.csv` (chart doesn't take FILE argument)
- `write-chart` (old command)

**Validation**: parse

---

### CLI-13: Distinct with Union

**Prompt**: Combine records from source_a.csv and source_b.csv, removing any duplicates.

**Expected patterns**:
- `ssql from source_a.csv`
- `ssql union`
- `source_b.csv`
- `-distinct` or `ssql distinct`
- `|`

**Negative patterns**:
- `concat` (union handles both files)
- `UNION` (SQL keyword, not ssql)

**Validation**: parse

---

### CLI-14: Offset and Pagination

**Prompt**: Read products.csv, sort by name, skip the first 50 records, and show the next 25.

**Expected patterns**:
- `ssql from products.csv`
- `ssql sort`
- `ssql offset 50`
- `ssql limit 25`
- `|`

**Negative patterns**:
- `OFFSET` (SQL keyword)
- `LIMIT` (SQL keyword)
- `skip` (wrong command, use offset)

**Validation**: parse

---

### CLI-15: Include/Exclude Field Selection

**Prompt**: Read employees.csv and output only the name, email, and department fields.

**Expected patterns**:
- `ssql from employees.csv`
- `ssql include` or `ssql exclude`
- `name`
- `email`
- `department`
- `|`

**Negative patterns**:
- `SELECT` (SQL keyword)
- `select` (wrong command, use include/exclude)
- `-fields` (wrong flag for include)

**Validation**: parse

---

## Running Tests

```bash
# Run all tests
./scripts/test-ai-prompts.sh all

# Run only Go code tests
./scripts/test-ai-prompts.sh go

# Run only CLI pipeline tests
./scripts/test-ai-prompts.sh cli

# Run via Makefile
make ai-test
```

## Adding New Test Cases

To add a new test case:

1. Choose the appropriate section (Go or CLI)
2. Use the next sequential ID (GO-11, CLI-11, etc.)
3. Include all required fields: Prompt, Expected patterns, Negative patterns, Validation
4. Run `./scripts/test-ai-prompts.sh` to verify the test works
5. If the test fails, update the corresponding prompt file to fix it
