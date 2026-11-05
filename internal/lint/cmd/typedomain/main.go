// Command typedomain runs the domain lint for type switches (DFC163):
// `go run ./internal/lint/cmd/typedomain ./...`, part of `make vet`.
package main

import (
	"golang.org/x/tools/go/analysis/singlechecker"

	"github.com/rosscartlidge/ssql/v4/internal/lint/typedomain"
)

func main() { singlechecker.Main(typedomain.Analyzer) }
