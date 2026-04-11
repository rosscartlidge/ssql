# WASM and WASI Reference

## Three WASM Targets

ssql has three distinct WASM builds:

| Target | GOOS/GOARCH | Purpose | Size |
|--------|-------------|---------|------|
| Browser playground | `js/wasm` | WASM playground on GitHub Pages | ~13MB (slim) |
| WebVM | `linux/386` | CheerpX-based full Linux terminal | ~7MB (slim) |
| WASI | `wasip1/wasm` | Portable binary for wasmtime/wasmer | ~14MB (slim) |

## Building

```bash
make playground           # Browser WASM (js/wasm, slim)
make wasi                 # WASI binary (wasip1/wasm, slim)
# WebVM: CGO_ENABLED=0 GOOS=linux GOARCH=386 go build -tags slim ...
```

All use `-tags slim` to exclude arrow/parquet/xlsx (reduces binary from ~54MB to ~14MB).

## WASI

### Running
```bash
wasmtime ssql.wasm version
wasmtime --dir=. ssql.wasm from data.csv | wasmtime ssql.wasm to table
wasmtime --env SSQLGO=1 --dir=. ssql.wasm from data.csv   # env vars need --env
```

### AOT compilation
```bash
wasmtime compile ssql.wasm -o ssql.cwasm          # compile once per machine
wasmtime --allow-precompiled --dir=. ssql.cwasm from data.csv   # near-native startup
```

AOT eliminates JIT startup: 0.018s vs 0.195s for small data (1.2x native vs 13x).

### Performance characteristics
- **Startup overhead:** ~50ms per WASI instance (JIT), ~6ms (AOT). Each pipe stage is a separate instance.
- **Processing:** ~4x slower than native for large data (1M+ rows). Gap is processing speed, not startup.
- **Slim modules JIT faster:** CLI pipeline (5 × 14MB modules) is faster than one 54MB generated Go WASM module.
- **AOT sweet spot:** Interactive use (many small commands). JIT is fine for batch processing.

### What doesn't work in WASI
- SSH (no network)
- GPU (no CUDA)
- Multi-file pushdown (spawns subprocesses internally)

### goreleaser
WASI build ships as `ssql_<version>_wasi.tar.gz` on every GitHub Release.

## Browser Playground (js/wasm)

### Architecture
- `cmd/ssql-playground/` contains the WASM main and HTML
- JavaScript simulates Unix pipes (splits pipeline, runs each stage via `ssqlExec()`)
- Process substitution `<(...)` resolved by executing inner pipeline, writing to virtual FS
- Static data files fetched from `data/` directory at startup
- Charts rendered via iframe (`to chart` writes HTML to virtual FS)
- Syntax highlighting via Prism.js CDN (Go + SQL)

### Deploying to gh-pages
The WASM binary is NOT tracked in main (gitignored). Deploy process:
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
git add -A && git commit -m "deploy: ..." && git push
git checkout main
```

HTML-only changes don't need WASM rebuild — just copy playground.html.

### OPTIMIZE_EXAMPLES
The `OPTIMIZE_EXAMPLES` Set in playground.html specifies which examples auto-run the optimizer instead of executing. SSH/catalog examples also auto-optimize (detected by `from ssh`/`from catalog` in the pipeline string).

## WebVM (CheerpX)

### Architecture
- Separate repo: `rosscartlidge/ssql-terminal`
- CheerpX runs a real Linux kernel in the browser via x86 emulation
- ext2 filesystem image built from Docker container
- Deploy via GitHub Actions workflow (manual trigger)

### docker cp uid/gid bug
`docker cp -a` does NOT preserve uid/gid ownership (moby/moby#41727). The deploy workflow includes a workaround:
```yaml
sudo docker cp -a ${CONTAINER_ID}:/ /mnt/
sudo chown -R 1000:1000 /mnt/home/user/   # fix ownership
```
Without this, directories like `.ssh/` (mode 700) are inaccessible because CheerpX runs as uid 1000 but the directory is owned by root.

### Updating
```bash
# Cross-compile
CGO_ENABLED=0 GOOS=linux GOARCH=386 go build -tags slim \
  -ldflags "-s -w -X .../version.Commit=$(git rev-parse --short=8 HEAD)" \
  -o ~/src/ssql-terminal/dockerfiles/ssql ./cmd/ssql

# Push and deploy
cd ~/src/ssql-terminal
git add dockerfiles/ssql && git commit && git push
gh workflow run Deploy --ref main \
  -f DOCKERFILE_PATH=dockerfiles/ssql_mini \
  -f IMAGE_SIZE=750M \
  -f DEPLOY_TO_GITHUB_PAGES=true \
  -f GITHUB_RELEASE=false
```
