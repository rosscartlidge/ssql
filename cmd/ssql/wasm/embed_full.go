//go:build !slim

// Package wasm embeds the explore engine: the SAME slim playground WASM
// (gzipped) plus its JS support files. Refresh with: make explore-wasm.
// The old TinyGo mini-engine — a third, untested implementation of ssql
// semantics — was removed in DFC107; explore now runs the real engine.
package wasm

import (
	_ "embed"
	"encoding/base64"
)

//go:embed ssql-playground.wasm.gz
var wasmGz []byte

//go:embed wasm_exec.js
var WasmExecJS string

//go:embed fs-polyfill.js
var FsPolyfillJS string

//go:embed ssql-ui.js
var SsqlUIJS string

// Available reports whether the embedded engine is present (full builds
// only — slim builds stub this out to avoid wasm-embedding-wasm).
func Available() bool { return true }

// WasmGzBase64 returns the gzipped WASM as base64 for inlining in HTML;
// the page decompresses it with DecompressionStream('gzip').
func WasmGzBase64() string {
	return base64.StdEncoding.EncodeToString(wasmGz)
}
