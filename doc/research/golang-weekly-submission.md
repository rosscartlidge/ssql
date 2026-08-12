# Golang Weekly Submission

Reference: DFC077
Created: 2026-04-08
Last modified: 2026-04-08

[Back to Index](./README.md)

Submit at: https://golangweekly.com/submit

---

**URL:** https://github.com/rosscartlidge/ssql

**Short description (for the newsletter):**

ssql is a stream processing CLI and Go library built on Go 1.23+ iterators and generics. Prototype data pipelines with Unix-style commands, then compile them to standalone Go programs via `generate go` or DuckDB SQL via `generate sql`. Features include a pipeline optimizer (merges filters, pushes predicates into SSH connections), window functions, parallel multi-file processing, GPU acceleration for signal processing, and a browser playground (WASM). The Go package works independently as a library with composable `Filter[T,U]` functions over `iter.Seq[T]`.

Install: `brew tap rosscartlidge/ssql && brew install ssql`
Try in browser: https://rosscartlidge.github.io/ssql/playground.html
