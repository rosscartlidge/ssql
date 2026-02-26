// Package wasm embeds the ssql WASM module and its JavaScript support files.
// Build the WASM files first with: make wasm
package wasm

import (
	_ "embed"
	"encoding/base64"
)

//go:embed ssql.wasm
var wasmBinary []byte

//go:embed wasm_exec.js
var WasmExecJS string

//go:embed ssql-wasm.js
var SsqlWasmJS string

// WasmBinaryBase64 returns the WASM binary as a base64-encoded string
// suitable for embedding in HTML.
func WasmBinaryBase64() string {
	return base64.StdEncoding.EncodeToString(wasmBinary)
}
