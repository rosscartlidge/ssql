//go:build slim

package lib

// gofmtSource in the slim build returns the source as assembled:
// go/format is 1.4 MB of js/wasm (DFC151) and library mode writes a
// file for a Go build, which a browser or WASI host never runs.
func gofmtSource(src []byte) ([]byte, error) { return src, nil }
