# Documentation

See also: **[Research & design docs →](research/README.md)**

## Learning Path

Do the codelabs in this order — each assumes the one before it. Every
code block in every codelab is executed against the current source on
`make doc-test` — CLI codelabs by `scripts/codelab-run.sh`, Go codelabs by
`scripts/codelab-go-run.sh` (DFC125) — so what you read is what happens.

1. [CLI Codelab](cli-codelab.md) — start here: ten minutes to useful, then the sophisticated features one at a time
2. [Signal Processing](cli-signal-processing.md) — optional CLI branch for time series: FFT, convolution, spectrogram, GPU
3. [SSH Operator Console](cli-codelab-serve.md) — optional runbook for `ssql serve` when a box holds the data
4. [Getting Started Guide (Go)](codelab-intro.md) — the `Record` library the CLI is built on; read it once you have seen `generate go` output in the CLI codelab
5. [Typed Codelab (Go)](typed-codelab.md) — the `ssql/typed` struct API for hot pipelines; what `SSQL_MODE=typed` emits

## Reference

- [API Reference](api-reference.md) — complete Go library documentation
- [Typed Reference](typed-reference.md) — `ssql/typed` high-performance struct-based API (15× faster, 34× less memory than Record)
- [Expression Language](EXPRESSIONS.md) — expression syntax for `-if-expr` and `-set-expr`
- [Debugging Pipelines](cli-debugging.md) — debug with jq, inspect data, profile performance
- [Troubleshooting](cli-troubleshooting.md) — common issues and quick solutions
- [Signal Processing](cli-signal-processing.md) — GPU-accelerated FFT, convolution, spectrogram

## AI Code Generation

- [Human Guide](ai-human-guide.md) — using AI assistants to generate ssql code
- [LLM Prompt](ai-code-generation.md) — copy this prompt into your LLM for code generation
- [CLI Pipeline Prompt](ai-cli-generation.md) — LLM prompt for generating CLI pipelines
- [Prompt Engineering](AI-PROMPT-README.md) — maintaining the AI prompt system
- [Prompt Fix Requests](ai-fix-request.md) — template for requesting prompt improvements
- [Prompt Improvements](ai-prompt-improvements.md) — analysis of prompt evolution
- [Test Cases](ai-test-cases.md) — validation test cases for generated code
- [Test Results](ai-test-results.md) — latest test run results

## Internal

- [Documentation Validation](VALIDATION.md) — automated doc checking system (`make doc-check`)
- `archive/` — superseded docs kept for history (the pre-v4 Advanced Tutorial among them); nothing there is current
