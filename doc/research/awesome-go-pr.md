# Awesome Go PR

Reference: DFC076
Created: 2026-04-08
Last modified: 2026-04-08

[Back to Index](./README.md)

Submit PR to: https://github.com/avelino/awesome-go

**Section:** Stream Processing

**Entry to add (alphabetical order, after `signals`):**

```markdown
- [ssql](https://github.com/rosscartlidge/ssql) - Stream processing CLI and Go library with composable iterators, pipeline optimizer, code generation (Go/SQL), and distributed SSH pushdown.
```

**PR Title:** Add ssql to Stream Processing

**PR Description:**

ssql is a stream processing CLI tool and Go library for CSV, JSON, Parquet, Arrow, and more.

**Why it belongs in Awesome Go:**

- Built on Go 1.23+ `iter.Seq[T]` iterators and generics
- Composable `Filter[T,U]` functions (`func(iter.Seq[T]) iter.Seq[U]`)
- CLI pipelines compile to standalone Go programs (`generate go`)
- Pipeline optimizer rewrites and pushes predicates into SSH connections
- MIT licensed, actively maintained, Homebrew and cross-platform binaries available
- Browser playground (WASM): https://rosscartlidge.github.io/ssql/playground.html

**Checklist (from their CONTRIBUTING.md):**

- [x] Not a duplicate
- [x] Has a useful README with examples
- [x] Has tests
- [x] Has a permissive license (MIT)
- [x] Actively maintained (commits within last month)
- [x] Not a commercial product
- [x] English documentation
