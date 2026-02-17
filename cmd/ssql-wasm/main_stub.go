//go:build !(js && wasm)

// Stub so that `go build ./cmd/ssql-wasm/` and `go test ./cmd/ssql-wasm/`
// work on non-WASM platforms.
package main

func main() {}
