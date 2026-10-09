# Installing ssql

Every way to get `ssql` onto a machine, from Homebrew to the GPU build, plus the Go library and the browser playground for trying it with no install at all. The README has the short version.

[Back to Documentation](README.md)

## Try it without installing

The full CLI runs in your browser via WebAssembly:

**[Launch Playground →](https://rosscartlidge.github.io/ssql/playground.html)** *(instant — optimized WASM, ~13MB)*

**[Launch Full Terminal →](https://rosscartlidge.github.io/ssql-terminal/)** *(real Linux with bash, tab completion, pipes — boots in ~20s)*

Features:
- Type real ssql pipelines and see results instantly
- **Optimize** — see the pipeline optimizer rewrite your commands with `-explain`
- **Generate Go** — compile pipelines to standalone Go code
- **Generate SQL** — convert to DuckDB-compatible SQL
- **Process substitution** — `<(ssql from ... | ssql where ...)` works in joins
- Sample datasets included (employees, orders, customers)
- Upload your own CSV files

> **Note:** SSH and catalog commands require network access and are not available in the browser. Use Optimize or Generate Go to see how those pipelines would be rewritten.

## Prerequisites
- **Go 1.21+** to run `go install` — it downloads the Go 1.26 toolchain
  ssql builds with automatically (about a minute, once). Older Go cannot
  do that and fails with `package cmp is not in GOROOT` and similar.

**Don't have Go installed?**
- Ubuntu 24.04+, Debian 13+: `sudo apt-get install -y golang-go`
- Ubuntu 22.04 (`golang-go` is Go 1.18, too old): `sudo apt-get install -y golang-1.22-go`
  and use `/usr/lib/go-1.22/bin/go` (put that directory first on your PATH)
- Debian 12 (`golang-go` is Go 1.19, too old): [download Go from go.dev](https://go.dev/dl/)
- macOS: `brew install go`
- Windows and others: [Download from go.dev](https://go.dev/dl/)
- No Go at all: the [Debian package](#option-6-debian-packages) or a
  [prebuilt binary](#option-3-download-binary) — everything but `generate go` works without Go
- Verify: `go version` (should show 1.21+)

## Installation

### Option 1: Homebrew (macOS & Linux)

```bash
brew tap rosscartlidge/ssql
brew install ssql
ssql version
```

### Option 2: Go Install

```bash
go install github.com/rosscartlidge/ssql/v4/cmd/ssql@latest

# go install writes to $HOME/go/bin, which is not on the PATH by default
echo 'export PATH="$PATH:$HOME/go/bin"' >> ~/.bashrc
export PATH="$PATH:$HOME/go/bin"

# Verify installation
ssql version

# Try it out
echo "name,age,salary
Alice,30,95000
Bob,25,65000" | ssql from csv | ssql where -if age gt 28
```

Then turn bash into an ssql-aware editor — completion plus the Ctrl-O / Alt-h /
Alt-g / Alt-r / Ctrl-T key bindings (see [The Shell Experience](cli-shell.md)):

```bash
echo 'eval "$(ssql -shell-init)"' >> ~/.bashrc && source ~/.bashrc
```

[**See CLI Tutorial →**](cli-codelab.md)

### Option 3: Download Binary

Pre-built binaries for Linux, macOS and Windows (amd64 and arm64) are on
[GitHub Releases](https://github.com/rosscartlidge/ssql/releases/latest).
Download the archive for your OS and architecture, extract, and put `ssql`
on your PATH:

```bash
# Linux amd64 shown; the archive is ssql_VERSION_OS_ARCH.tar.gz (.zip on Windows)
V=$(curl -s https://api.github.com/repos/rosscartlidge/ssql/releases/latest | sed -n 's/.*"tag_name": *"v\([^"]*\)".*/\1/p')
curl -LO https://github.com/rosscartlidge/ssql/releases/download/v$V/ssql_${V}_linux_amd64.tar.gz
tar xzf ssql_${V}_linux_amd64.tar.gz
sudo install ssql /usr/local/bin/
ssql version
```

Each release also ships `ssql-slim_VERSION_OS_ARCH` archives: a smaller
build without `ssql serve` and the embedded explorer engine
(`to explore -wasm`), for containers and constrained hosts.

### Option 4: WASI (run anywhere)

A single `.wasm` binary that runs on any platform with a WASI runtime ([wasmtime](https://wasmtime.dev/), wasmer, Docker+WASM):

```bash
# Download from GitHub Releases (the archive is versioned: ssql_VERSION_wasi.tar.gz)
V=$(curl -s https://api.github.com/repos/rosscartlidge/ssql/releases/latest | sed -n 's/.*"tag_name": *"v\([^"]*\)".*/\1/p')
curl -LO https://github.com/rosscartlidge/ssql/releases/download/v$V/ssql_${V}_wasi.tar.gz
tar xzf ssql_${V}_wasi.tar.gz

# Run with wasmtime
wasmtime ssql.wasm version
wasmtime --dir=. ssql.wasm from data.csv | wasmtime ssql.wasm where -if age gt 25 | wasmtime ssql.wasm to table
```

No Go, no cross-compilation — one binary for every platform (slim build, 6 MB
download). `wasmtime compile ssql.wasm` precompiles it to near-native speed.

### Option 5: GPU Acceleration (optional)

For GPU-accelerated FFT, convolution and correlation on large signals (16 K samples and up; smaller inputs stay on the CPU, where they are faster):

**Requirements:**
- NVIDIA GPU with CUDA support
- Docker with nvidia-container-toolkit, OR CUDA Toolkit installed locally

**Method 1: Docker Build (Recommended - no local CUDA needed)**

```bash
# Clone the repository
git clone https://github.com/rosscartlidge/ssql.git
cd ssql

# Build and extract the GPU-enabled binary
make docker-gpu-extract

# Install the library system-wide
sudo cp libssqlgpu.so /usr/local/lib && sudo ldconfig

# Install the binary
cp ssql_gpu ~/go/bin/

# Verify GPU is detected
ssql_gpu version
# Output: ssql vX.Y.Z (gpu: yes)
```

**Method 2: Local CUDA Toolkit Build**

```bash
# Clone the repository
git clone https://github.com/rosscartlidge/ssql.git
cd ssql

# Build the CUDA library
cd gpu && make && cd ..

# Build ssql with GPU support
go build -tags gpu -o ssql_gpu ./cmd/ssql

# Install to your Go bin directory
sudo make install-gpu  # Installs libssqlgpu.so to /usr/local/lib
cp ssql_gpu ~/go/bin/

# Verify GPU is detected
ssql_gpu version
```

**Note:** The GPU version falls back to CPU automatically when GPU is unavailable or for small datasets where CPU is faster.

### Option 6: Debian Packages

Pre-built `.deb` packages are available for amd64 Linux systems:

**Standard version (no GPU dependencies):**
```bash
curl -LO https://github.com/rosscartlidge/ssql/raw/main/ssql_4.113.3_amd64.deb
sudo dpkg -i ssql_4.113.3_amd64.deb
ssql version
```

**GPU-accelerated version (requires NVIDIA CUDA runtime):**
```bash
curl -LO https://github.com/rosscartlidge/ssql/raw/main/ssql-gpu_4.113.3_amd64.deb
sudo dpkg -i ssql-gpu_4.113.3_amd64.deb
ssql version
```

The GPU package requires `libcudart` (CUDA runtime) which is typically installed with NVIDIA drivers.

### Option 7: Go Library (for application development)

**Step 1: Create a new project**
```bash
mkdir my-project
cd my-project
go mod init myproject  # Initialize Go module (required!)
```

**Step 2: Install ssql v4**
```bash
go get github.com/rosscartlidge/ssql/v4
```

**Step 3: Check it builds** — a complete program; the [Getting Started Guide](codelab-intro.md) takes it from here
```go
package main

import (
    "fmt"
    "slices"
    "github.com/rosscartlidge/ssql/v4"
)

func main() {
    numbers := slices.Values([]int{1, 2, 3, 4, 5, 6, 7, 8, 9, 10})

    evenNumbers := ssql.Where(func(x int) bool {
        return x%2 == 0
    })(numbers)

    first3 := ssql.Limit[int](3)(evenNumbers)

    fmt.Println("First 3 even numbers:")
    for num := range first3 {
        fmt.Println(num) // 2, 4, 6
    }
}
```
