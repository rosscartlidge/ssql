# ssql Makefile

.PHONY: help test build clean doc-check doc-test doc-verify doc-update fmt vet all ci install-hooks
.PHONY: gpu build-gpu install-gpu docker-gpu docker-gpu-image docker-gpu-extract deb
.PHONY: ai-test ai-test-go ai-test-cli
.PHONY: wasm wasm-go

VERSION := $(shell cat cmd/ssql/version/version.txt | tr -d '[:space:]')
COMMIT := $(shell git rev-parse --short=8 HEAD)
LDFLAGS := -X github.com/rosscartlidge/ssql/v4/cmd/ssql/version.Commit=$(COMMIT)

# Default target
help:
	@echo "ssql Makefile Targets:"
	@echo ""
	@echo "Quality Checks:"
	@echo "  make test         - Run all tests"
	@echo "  make build        - Build the project"
	@echo "  make fmt          - Format all Go code"
	@echo "  make vet          - Run go vet"
	@echo ""
	@echo "GPU Build (requires CUDA toolkit locally):"
	@echo "  make gpu          - Build CUDA library (gpu/libssqlgpu.so)"
	@echo "  make build-gpu    - Build ssql_gpu binary with GPU support"
	@echo "  make install-gpu  - Install CUDA library to /usr/local/lib (requires sudo)"
	@echo ""
	@echo "GPU Build via Docker (no local CUDA needed):"
	@echo "  make docker-gpu-image   - Build Docker image with ssql_gpu"
	@echo "  make docker-gpu-extract - Build and extract ssql_gpu binary to current dir"
	@echo ""
	@echo "WASM Build:"
	@echo "  make wasm         - Build ssql.wasm with TinyGo (~300-500KB)"
	@echo "  make wasm-go      - Build ssql.wasm with standard Go (~5MB)"
	@echo "  make playground   - Build WASM playground (full CLI in browser)"
	@echo ""
	@echo "Documentation Validation (3 levels):"
	@echo "  make doc-check    - Level 1: Fast checks (syntax, links, patterns)"
	@echo "  make doc-test     - Level 2: Medium checks (godoc, exports, run examples)"
	@echo "  make doc-verify   - Level 3: Deep verification (all API refs, consistency)"
	@echo "  make doc-update   - Update godoc and run validation"
	@echo ""
	@echo "AI Prompt Testing:"
	@echo "  make ai-test      - Test AI prompts (Go + CLI, requires 'claude' CLI)"
	@echo "  make ai-test-go   - Test Go code generation prompt only"
	@echo "  make ai-test-cli  - Test CLI pipeline generation prompt only"
	@echo ""
	@echo "Workflows:"
	@echo "  make all          - Run fmt, vet, test, doc-check (pre-push)"
	@echo "  make ci           - Full CI pipeline (all + doc-test)"
	@echo "  make release      - Release validation (ci + doc-verify)"
	@echo "  make deb          - Build .deb packages for current version"
	@echo ""
	@echo "Setup:"
	@echo "  make install-hooks - Install git pre-commit hook"
	@echo "  make clean         - Clean build artifacts"
	@echo ""

# Run tests
test:
	@echo "Running tests..."
	go test -v ./...

# Build the project
build:
	@echo "Building ssql..."
	go build ./...
	@echo "Building CLI tool..."
	cd cmd/ssql && go build -ldflags "$(LDFLAGS)"

# Format code
fmt:
	@echo "Formatting Go code..."
	go fmt ./...

# Run go vet
vet:
	@echo "Running go vet..."
	go vet ./...

# Level 1: Fast documentation validation
doc-check:
	@echo "Level 1: Fast Documentation Validation"
	@echo "======================================="
	@./scripts/validate-docs.sh

# Level 2: Medium documentation testing
doc-test:
	@echo "Level 2: Documentation Testing"
	@echo "==============================="
	@./scripts/doc-test.sh

# Level 3: Deep documentation verification
doc-verify:
	@echo "Level 3: Deep Documentation Verification"
	@echo "========================================="
	@./scripts/doc-verify.sh

# Update and validate documentation
doc-update: fmt
	@echo "Regenerating godoc..."
	@echo "(godoc is generated automatically from source code)"
	@echo ""
	@$(MAKE) doc-check

# Clean build artifacts
clean:
	@echo "Cleaning build artifacts..."
	go clean ./...
	rm -f cmd/ssql/ssql
	rm -f ssql_gpu libssqlgpu.so
	rm -f cmd/ssql-wasm/ssql.wasm
	cd gpu && $(MAKE) clean 2>/dev/null || true

# Run all quality checks (pre-push)
all: fmt vet test doc-check
	@echo ""
	@echo "✓ All pre-push checks passed!"

# CI target - for continuous integration
ci: all doc-test
	@echo ""
	@echo "✓ CI pipeline complete!"

# Release target - comprehensive validation
release: ci doc-verify
	@echo ""
	@echo "✓ Release validation complete!"
	@echo "✓ Ready for release!"
	@echo ""
	@echo "Don't forget: make deb"

# Build debian packages for current version
VERSION := $(shell cat cmd/ssql/version/version.txt)

deb: build-gpu
	@echo "Building debian packages for v$(VERSION)..."
	@# Remove old debs
	rm -f ssql_*_amd64.deb ssql-gpu_*_amd64.deb
	@# Standard package
	rm -rf /tmp/ssql-deb
	mkdir -p /tmp/ssql-deb/DEBIAN /tmp/ssql-deb/usr/bin
	go build -ldflags "$(LDFLAGS)" -o /tmp/ssql-deb/usr/bin/ssql ./cmd/ssql
	printf 'Package: ssql\nVersion: $(VERSION)\nSection: utils\nPriority: optional\nArchitecture: amd64\nDepends: libc6\nMaintainer: Ross Cartlidge <ross@cartlidge.com>\nDescription: Unix-style data processing tools\nHomepage: https://github.com/rosscartlidge/ssql\n' > /tmp/ssql-deb/DEBIAN/control
	dpkg-deb --build /tmp/ssql-deb ssql_$(VERSION)_amd64.deb
	@# GPU package
	rm -rf /tmp/ssql-gpu-deb
	mkdir -p /tmp/ssql-gpu-deb/DEBIAN /tmp/ssql-gpu-deb/usr/bin /tmp/ssql-gpu-deb/usr/lib
	cp ssql_gpu /tmp/ssql-gpu-deb/usr/bin/ssql_gpu
	cp gpu/libssqlgpu.so /tmp/ssql-gpu-deb/usr/lib/
	printf 'Package: ssql-gpu\nVersion: $(VERSION)\nSection: utils\nPriority: optional\nArchitecture: amd64\nDepends: libc6, libcudart12\nMaintainer: Ross Cartlidge <ross@cartlidge.com>\nDescription: Unix-style data processing tools (GPU-accelerated)\nHomepage: https://github.com/rosscartlidge/ssql\n' > /tmp/ssql-gpu-deb/DEBIAN/control
	printf '#!/bin/sh\nldconfig\n' > /tmp/ssql-gpu-deb/DEBIAN/postinst
	chmod 755 /tmp/ssql-gpu-deb/DEBIAN/postinst
	printf '#!/bin/sh\nldconfig\n' > /tmp/ssql-gpu-deb/DEBIAN/postrm
	chmod 755 /tmp/ssql-gpu-deb/DEBIAN/postrm
	dpkg-deb --build /tmp/ssql-gpu-deb ssql-gpu_$(VERSION)_amd64.deb
	@echo ""
	@echo "✓ Built ssql_$(VERSION)_amd64.deb"
	@echo "✓ Built ssql-gpu_$(VERSION)_amd64.deb"

# Install git hooks
install-hooks:
	@echo "Installing git pre-commit hook..."
	@mkdir -p .git/hooks
	@echo '#!/bin/bash' > .git/hooks/pre-commit
	@echo 'make doc-check' >> .git/hooks/pre-commit
	@chmod +x .git/hooks/pre-commit
	@echo "✓ Pre-commit hook installed (runs doc-check before each commit)"

# =============================================================================
# AI Prompt Testing Targets
# =============================================================================

# Test all AI prompts (Go + CLI)
ai-test:
	@./scripts/test-ai-prompts.sh all

# Test Go code generation prompt only
ai-test-go:
	@./scripts/test-ai-prompts.sh go

# Test CLI pipeline generation prompt only
ai-test-cli:
	@./scripts/test-ai-prompts.sh cli

# Run AI tests with integration (actually execute generated code)
ai-test-integration:
	@./scripts/test-ai-prompts.sh all --integration

# =============================================================================
# GPU Build Targets
# =============================================================================

# Build CUDA library locally (requires CUDA toolkit)
gpu:
	@echo "Building CUDA library..."
	cd gpu && $(MAKE)
	@echo "✓ Built gpu/libssqlgpu.so"

# Build ssql with GPU support locally (requires CUDA toolkit)
build-gpu: gpu
	@echo "Building ssql_gpu..."
	CGO_ENABLED=1 \
	CGO_LDFLAGS="-L$(PWD)/gpu -L/usr/local/cuda/lib64" \
	LD_LIBRARY_PATH="$(PWD)/gpu:/usr/local/cuda/lib64" \
	go build -tags gpu -ldflags "$(LDFLAGS)" -o ssql_gpu ./cmd/ssql
	@echo "✓ Built ssql_gpu"
	@echo ""
	@echo "To run: LD_LIBRARY_PATH=$(PWD)/gpu ./ssql_gpu version"
	@echo "Or install the library: sudo make install-gpu"

# Install CUDA library system-wide (one-time setup)
install-gpu: gpu
	@echo "Installing CUDA library to /usr/local/lib..."
	sudo cp gpu/libssqlgpu.so /usr/local/lib/
	sudo ldconfig
	@echo "✓ Installed libssqlgpu.so"
	@echo ""
	@echo "You can now build and run without LD_LIBRARY_PATH:"
	@echo "  go build -tags gpu -o ssql_gpu ./cmd/ssql"
	@echo "  ./ssql_gpu version"

# Build Docker image with GPU support
docker-gpu-image:
	@echo "Building Docker image with GPU support..."
	docker build -f Dockerfile.gpu -t ssql:gpu .
	@echo "✓ Built ssql:gpu image"
	@echo ""
	@echo "Run with: docker run --gpus all ssql:gpu version"

# Build and extract ssql_gpu binary using Docker (no local CUDA needed)
docker-gpu-extract:
	@echo "Building ssql_gpu via Docker..."
	docker build -f Dockerfile.gpu -t ssql:gpu-builder .
	@echo "Extracting binary..."
	docker create --name ssql-gpu-extract ssql:gpu-builder
	docker cp ssql-gpu-extract:/usr/local/bin/ssql_gpu ./ssql_gpu
	docker cp ssql-gpu-extract:/usr/local/lib/libssqlgpu.so ./libssqlgpu.so
	docker rm ssql-gpu-extract
	@echo "✓ Extracted ssql_gpu and libssqlgpu.so"
	@echo ""
	@echo "To run: LD_LIBRARY_PATH=. ./ssql_gpu version"
	@echo "Or install: sudo cp libssqlgpu.so /usr/local/lib && sudo ldconfig"

# Shortcut for docker build
docker-gpu: docker-gpu-extract

# =============================================================================
# WASM Build Targets
# =============================================================================

# Build WASM module with TinyGo (small binary, ~300-500KB)
wasm:
	@echo "Building ssql.wasm with TinyGo..."
	tinygo build -o cmd/ssql-wasm/ssql.wasm -target wasm -no-debug -panic=trap -opt=z ./cmd/ssql-wasm
	rm -f cmd/ssql-wasm/js/wasm_exec.js
	cp "$$(tinygo env TINYGOROOT)/targets/wasm_exec.js" cmd/ssql-wasm/js/
	cp cmd/ssql-wasm/ssql.wasm cmd/ssql/wasm/ssql.wasm
	cp cmd/ssql-wasm/js/wasm_exec.js cmd/ssql/wasm/wasm_exec.js
	cp cmd/ssql-wasm/js/ssql-wasm.js cmd/ssql/wasm/ssql-wasm.js
	@echo "✓ Built cmd/ssql-wasm/ssql.wasm ($$(du -h cmd/ssql-wasm/ssql.wasm | cut -f1) raw)"
	@echo "✓ Embedded WASM files copied to cmd/ssql/wasm/"

# Build WASM playground (full CLI in browser)
playground:
	@echo "Building playground WASM (slim build — no arrow/parquet/xlsx)..."
	GOOS=js GOARCH=wasm go build -tags slim -ldflags="-s -w $(LDFLAGS)" -o cmd/ssql-playground/ssql-playground.wasm ./cmd/ssql-playground
	cp "$$(go env GOROOT)/lib/wasm/wasm_exec.js" cmd/ssql-playground/
	@echo "✓ Built cmd/ssql-playground/ssql-playground.wasm ($$(du -h cmd/ssql-playground/ssql-playground.wasm | cut -f1) raw)"
	@echo "Serve with: cd cmd/ssql-playground && python3 -m http.server 8080"

# Build WASM module with standard Go (larger binary, ~5MB)
wasm-go:
	@echo "Building ssql.wasm with standard Go..."
	GOOS=js GOARCH=wasm go build -ldflags="-s -w" -o cmd/ssql-wasm/ssql.wasm ./cmd/ssql-wasm
	rm -f cmd/ssql-wasm/js/wasm_exec.js
	cp "$$(go env GOROOT)/lib/wasm/wasm_exec.js" cmd/ssql-wasm/js/
	cp cmd/ssql-wasm/ssql.wasm cmd/ssql/wasm/ssql.wasm
	cp cmd/ssql-wasm/js/wasm_exec.js cmd/ssql/wasm/wasm_exec.js
	cp cmd/ssql-wasm/js/ssql-wasm.js cmd/ssql/wasm/ssql-wasm.js
	@echo "✓ Built cmd/ssql-wasm/ssql.wasm ($$(du -h cmd/ssql-wasm/ssql.wasm | cut -f1) raw)"
	@echo "✓ Embedded WASM files copied to cmd/ssql/wasm/"
