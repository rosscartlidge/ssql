//go:build slim

// Slim builds (the playground itself, WebVM) carry no embedded engine —
// a wasm cannot embed itself, and slim exists to be small. `to explore
// -wasm` errors loudly under slim.
package wasm

var (
	WasmExecJS   string
	FsPolyfillJS string
	SsqlUIJS     string
)

func Available() bool { return false }

func WasmGzBase64() string { return "" }
