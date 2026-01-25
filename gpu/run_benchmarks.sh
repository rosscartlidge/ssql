#!/bin/bash
# GPU benchmark script for ssql
# Runs FFT, convolution, and other GPU benchmarks
#
# Usage: ./run_benchmarks.sh
#
# Prerequisites:
#   - CUDA library built: make (in this directory)
#   - Or system-wide install: sudo make install

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$SCRIPT_DIR"

# Check if libssqlgpu.so exists
if [[ ! -f "libssqlgpu.so" ]]; then
    echo "Error: libssqlgpu.so not found. Run 'make' first."
    exit 1
fi

export LD_LIBRARY_PATH="$SCRIPT_DIR:$LD_LIBRARY_PATH"

echo "=== GPU Benchmarks ==="
echo "Running from: $SCRIPT_DIR"
echo ""

echo "--- FFT Benchmarks ---"
go test -tags gpu -bench 'BenchmarkFFT' -benchtime=2s .

echo ""
echo "--- Convolution Benchmarks ---"
go test -tags gpu -bench 'BenchmarkConvolve' -benchtime=2s .

echo ""
echo "--- Sum Benchmarks ---"
go test -tags gpu -bench 'BenchmarkSum' -benchtime=2s .

echo ""
echo "=== Benchmark Complete ==="
