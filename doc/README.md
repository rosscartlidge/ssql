# Documentation

## Learning path

Do these in order; each assumes the one before it.

1. [CLI Codelab](cli-codelab.md) — start here: ten minutes to useful, then the sophisticated features one at a time
   - [SSH Operator Console](cli-codelab-serve.md) — runbook for `ssql serve` when a box holds the data (section 8 of the codelab)
   - [tmux for the ssql Codelab](tmux-for-ssql.md) — the five things you need from tmux: mouse scrolling and copy, named sessions, a second shell, `escape-time` for the Alt keys
2. [Structured Data](cli-nested-data.md) — optional CLI branch for JSON with lists and objects inside fields: dotted paths, expressions over lists, `explode`, `flatten`, `-collect`
3. [Signal Processing](cli-signal-processing.md) — optional CLI branch for time series: FFT, convolution, correlation, spectrogram; the GPU build as the last section
4. [Getting Started Guide (Go)](codelab-intro.md) — the `Record` library the CLI is built on; read it once you have seen `generate go` output
5. [Typed Codelab (Go)](typed-codelab.md) — the `ssql/typed` struct API for hot pipelines; what `generate go` emits by default

## Install and set up

- [Installing ssql](install.md) — Homebrew, `go install`, prebuilt binaries, WASI, the GPU build, the Debian packages, the Go library, and the browser playground
- [The Shell Experience](cli-shell.md) — `ssql -shell-init`: completion and the Ctrl-O / Alt-h / Alt-g / Alt-r / Ctrl-T bindings

## Reference

- [API Reference](api-reference.md) — the `Record` Go library
- [Typed Reference](typed-reference.md) — the `ssql/typed` struct API
- [Expression Language](EXPRESSIONS.md) — expression syntax for `-if-expr`, `-set-expr` and `group-by -expr`
- [Troubleshooting](cli-troubleshooting.md) — inspecting a pipeline with ssql itself; common errors and what they mean

## Background

- [Performance, Measured](performance.md) — the DuckDB comparison and the typed/parallel numbers, with the pipelines that produce them
- [The Go Library, by Example](library-tour.md) — the `Record` API one capability at a time

## Using an LLM

- [Human Guide](ai-human-guide.md) — which prompt to paste, how, and how to check what comes back
- [CLI Pipeline Prompt](ai-cli-generation.md) — paste this into your LLM to have it write ssql pipelines
- [Go Code Prompt](ai-code-generation.md) — paste this into your LLM to have it write Go programs against the library

## For contributors

Design docs, proposals and decision records live in [doc/research](research/README.md), indexed by DFC number. Working conventions are in `claude/`; how these docs are validated is in [claude/doc-validation.md](../claude/doc-validation.md). `archive/` holds superseded docs kept for history; nothing there is current.
