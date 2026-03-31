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

## Adding Examples That Auto-Optimize

Some examples (SSH pushdown, optimizer demos) should run the optimizer instead of
executing. Add their index to the `OPTIMIZE_EXAMPLES` Set in `playground.html`.
Examples containing `from ssh` or `from catalog` auto-optimize regardless.

## Building and Serving

```bash
make playground                    # builds slim WASM (~13MB, excludes arrow/parquet/xlsx)
cd cmd/ssql-playground && python3 -m http.server 8080
```

No rebuild needed for HTML/CSS/JS/data changes — just refresh the browser.
WASM rebuild only needed when Go code changes.

## Deploying to GitHub Pages

### WASM Playground (rosscartlidge.github.io/ssql/playground.html)

Served from the `gh-pages` branch. The WASM binary is NOT tracked in main (gitignored),
so you must build it on main, save to /tmp, then switch branches to deploy.

**HTML-only changes** (no WASM rebuild needed):
```bash
git checkout gh-pages
git show main:cmd/ssql-playground/playground.html > playground.html
git add playground.html && git commit -m "deploy: update playground" && git push
git checkout main
```

**Go code changes** (WASM rebuild required):
```bash
# 1. Build on main
make playground
cp cmd/ssql-playground/ssql-playground.wasm /tmp/
cp cmd/ssql-playground/wasm_exec.js /tmp/

# 2. Deploy from gh-pages
git checkout gh-pages
cp /tmp/ssql-playground.wasm .
cp /tmp/wasm_exec.js .
git show main:cmd/ssql-playground/playground.html > playground.html
git add -A && git commit -m "deploy: rebuild WASM" && git push
git checkout main
```

### WebVM Terminal (rosscartlidge.github.io/ssql-terminal/)

Separate repo: `rosscartlidge/ssql-terminal`. Deploy is **manual trigger only** (workflow_dispatch).

**Updating the ssql binary:**
```bash
# Cross-compile slim build for linux/386
CGO_ENABLED=0 GOOS=linux GOARCH=386 go build -tags slim \
  -ldflags "-s -w -X github.com/rosscartlidge/ssql/v4/cmd/ssql/version.Commit=$(git rev-parse --short=8 HEAD)" \
  -o ~/src/ssql-terminal/dockerfiles/ssql ./cmd/ssql

cd ~/src/ssql-terminal
git add dockerfiles/ssql && git commit -m "update ssql binary" && git push
```

**Triggering deploy:**
```bash
cd ~/src/ssql-terminal
gh workflow run Deploy --ref main \
  -f DOCKERFILE_PATH=dockerfiles/ssql_mini \
  -f IMAGE_SIZE=750M \
  -f DEPLOY_TO_GITHUB_PAGES=true \
  -f GITHUB_RELEASE=false
```

This builds Docker → ext2 image → deploys to Pages. Takes ~2 minutes.

### WebVM filesystem and `docker cp -a` uid/gid bug

CheerpX uses an ext2 overlay filesystem — reads from HTTP, writes to IndexedDB.
Runtime writes **do work**, but the deploy workflow uses `docker cp -a` to extract
the container filesystem, which has a known bug (moby/moby#41727): it does NOT
preserve uid/gid ownership. All files end up owned by root in the ext2 image.

The deploy workflow includes a workaround:
```yaml
sudo docker cp -a ${CONTAINER_ID}:/ /mnt/
sudo chown -R 1000:1000 /mnt/home/user/   # fix ownership
```

Without this fix, directories like `.ssh/` (mode 700) are inaccessible at runtime
because CheerpX runs as uid 1000 but the directory is owned by root.

**Key implication:** File permissions (mode bits) ARE preserved by `docker cp -a`,
but ownership is not. World-readable dirs like `data/` work fine; restricted dirs
like `.ssh/` fail without the chown fix.

### Keeping WebVM in sync

When updating the playground data or examples:
1. Copy CSV files: `cp cmd/ssql-playground/data/*.csv ~/src/ssql-terminal/dockerfiles/data/`
2. Update `~/src/ssql-terminal/dockerfiles/data/examples.sh` with new examples
3. Rebuild ssql for linux/386 (see above)
4. Commit, push, and trigger the Deploy workflow
