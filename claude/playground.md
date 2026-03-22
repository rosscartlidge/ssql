# Playground Development Guide

## Data Files

Static CSV files live in `cmd/ssql-playground/data/`:
- `employees.csv` — 50 rows (name, age, dept, salary, city, level, hire_date, status)
- `orders.csv` — 40 rows (order_id, customer_id, product, amount, order_date)
- `customers.csv` — 20 rows (customer_id, name, country, tier)

The playground fetches these at startup. The same files are used for local testing.

## Testing Examples Before Adding to Playground

**Always test examples against the static data files first:**

```bash
cd cmd/ssql-playground/data
ssql from employees.csv | ssql where -if dept eq Engineering | ssql to table
```

This ensures the example works with the exact same data the playground uses.
If a field name, operator, or command doesn't work here, it won't work in the playground.

## Adding a New Example

1. Write the pipeline and test it locally against `cmd/ssql-playground/data/`
2. Add the pipeline string to the `EXAMPLES` array in `playground.html`
3. Add a matching `<button class="example-btn">` with the next index
4. Examples should progress from simple to complex:
   - First: basic view, filter, sort
   - Middle: update, group-by, chart
   - Last: join, window, optimize

## Shell Quoting in Playground

The playground's JavaScript pipeline parser splits on whitespace. Quoted strings
(single or double) are kept as one argument via the `shellSplit()` function.
Example: `'salary / 10'` stays as one arg.

This means expressions with spaces **must** be quoted in the pipeline textarea,
just like a real shell.

## Chart Support

`to chart` writes an HTML file to the virtual filesystem. The playground detects
`to chart` in the pipeline, reads the generated file via `_fsReadFile()`, and
displays it in an iframe below the output area.

## Building and Serving

```bash
make playground                    # builds WASM (~68MB)
cd cmd/ssql-playground && python3 -m http.server 8080
```

No rebuild needed for HTML/CSS/JS/data changes — just refresh the browser.
WASM rebuild only needed when Go code changes.
