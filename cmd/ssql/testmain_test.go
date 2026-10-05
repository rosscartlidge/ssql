package main

import (
	"os"
	"path/filepath"
	"testing"
)

// TestMain points every `generate go -run` / `-build` the tests spawn
// (directly, through serve's typed-head build, or through the catalog
// remote-go path) at THIS checkout. By default a generated program
// compiles against the released module at the binary's version, so a
// library function the generated code uses before it has shipped
// (ssql.Run in every generated main(), DFC142) is "undefined" — eleven
// tests failed that way on 2026-10-05. The tests test the checkout; the
// released-module default stays for users. An explicit SSQL_MODULE_DIR
// in the environment wins.
func TestMain(m *testing.M) {
	if os.Getenv("SSQL_MODULE_DIR") == "" {
		if repo, err := filepath.Abs("../.."); err == nil {
			os.Setenv("SSQL_MODULE_DIR", repo)
		}
	}
	os.Exit(m.Run())
}
