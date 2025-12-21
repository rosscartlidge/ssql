# Go CLI Frameworks Comparison - December 2025

This document provides a comprehensive, factual comparison of the most popular Go CLI frameworks based on research conducted in December 2025.

## Summary Table

| Framework | GitHub Stars | Latest Release | Maintainers | Key Strength | Best For |
|-----------|--------------|----------------|-------------|--------------|----------|
| **spf13/cobra** | 42.7k | v1.10.2 (Dec 4, 2025) | 323+ contributors | Most popular, used by K8s/Docker | Complex enterprise CLIs with subcommands |
| **urfave/cli** | 23.8k | v3.6.1 (Nov 16, 2025) | 285 contributors | Simple, declarative API | Fast development, moderate complexity |
| **alecthomas/kong** | 2.9k | v1.0.0+ | 146 contributors | Struct-based, minimal boilerplate | Type-safe CLIs with clean code |
| **jessevdk/go-flags** | 2.7k | v1.6.1 (Jun 15, 2024) | 92 contributors | Reflection-based flag parsing | Simple flag parsing without framework |
| **peterbourgon/ff** | Not measured | v3.4.0+ | Not measured | Flags-first, config layering | 12-factor apps with env/config files |
| **posener/complete** | 941 | Active | Not measured | Bash completion library | Adding autocomplete to existing CLIs |
| **rosscartlidge/autocli** | ~0 | v4.3.3 (Dec 2025) | 1 | Data-aware completion, clauses | Data pipeline CLIs with field completion |

## Detailed Analysis

### 1. spf13/cobra - Industry Standard

**Repository:** https://github.com/spf13/cobra

**Stats:**
- GitHub Stars: 42,700+
- Latest Release: v1.10.2 (December 4, 2025)
- Contributors: 323+ (with 309 additional)
- Dependents: ~198,000 projects
- License: Apache 2.0
- Total Commits: 1,101 on main branch
- Created: September 3, 2013

**Description:**
A library providing a simple interface to create powerful modern CLI interfaces similar to git & go tools.

**Notable Users:**
Kubernetes, Hugo, Docker, GitHub CLI, OpenShift, Delve, CockroachDB, Istio, Linkerd, etcd, Moby, rkt, ProjectAtomic, Giant Swarm's gsctl, Nanobox/Nanopack, rclone, Pouch, Gardener, Prototool, mattermost-server

**Key Features:**
- Easy subcommand-based CLIs (e.g., `app server`, `app fetch`)
- Fully POSIX-compliant flags including short and long versions
- Intelligent suggestions (e.g., "app srver... did you mean app server?")
- Automatic help flag recognition (`-h`, `--help`)
- Automatically generated shell autocomplete (bash, zsh, fish, powershell)
- Automatically generated man pages
- Command aliases
- Optional code generation via cobra-cli tool
- Seamless integration with Viper for configuration management

**Strengths:**
- Most widely adopted - de facto standard for enterprise Go CLIs
- Comprehensive feature set out of the box
- Excellent documentation and community support
- Battle-tested in production by major projects
- Strong ecosystem integration (Viper for config)
- Regular maintenance and updates

**Weaknesses:**
- "More of a framework than a library" - can feel opinionated
- Optional code generation can feel heavyweight for simple CLIs
- Larger binary size compared to minimal alternatives
- Steeper learning curve for simple use cases

**When to Use:**
- Building complex CLIs with nested subcommands
- Enterprise applications requiring polish and reliability
- Projects that benefit from generated documentation
- When following industry best practices matters

---

### 2. urfave/cli - Simple and Fast

**Repository:** https://github.com/urfave/cli

**Stats:**
- GitHub Stars: 23,800+
- Latest Release: v3.6.1 (November 16, 2025)
- Contributors: 285
- Dependents: ~2,200 projects
- License: MIT
- Total Commits: 3,634 on main branch
- Releases: 164 total
- Open Issues: 50 | Pull Requests: 11

**Description:**
A declarative, simple, fast, and fun package for building command line tools in Go.

**Key Features:**
- Commands and subcommands with alias and prefix match support
- Automatically generated help system
- Dynamic shell completion and generation of markdown documentation
- Environment variable flag defaults
- Comma-delimited list for flag name aliases
- Minimal API surface - easy to understand
- No code generation required
- Binary size optimization option (removes ToMarkdown/ToMan for ~300-400 KB savings)

**Strengths:**
- Declarative, straightforward API
- No code generation - just write Go code
- Fast development iteration
- Good balance of features vs simplicity
- Active maintenance with frequent releases
- v3 is recommended for all new development

**Weaknesses:**
- Smaller ecosystem than Cobra
- "Run by unpaid volunteers" - community-driven, not corporate-backed
- Less polish in auto-generated documentation
- Fewer integrations with config libraries

**Maintenance Note:**
- v3 series: Active development, recommended for new projects
- v2 series: Security and bug fixes only (v2-maint branch)
- v1 series: Legacy, no longer maintained

**When to Use:**
- Building CLIs quickly without boilerplate
- Projects that value simplicity over features
- When you want full control without framework magic
- Small to medium complexity CLI applications

---

### 3. alecthomas/kong - Struct-Based Parsing

**Repository:** https://github.com/alecthomas/kong

**Stats:**
- GitHub Stars: 2,900+
- Latest Release: v1.0.0+ (stable)
- Contributors: 146 (80 primary + 66 additional)
- Dependents: ~4,900 projects
- License: MIT
- Total Commits: 460 on master branch
- Tags: 71

**Description:**
A command-line parser for Go that aims to support arbitrarily complex command-line structures with as little developer effort as possible. Command lines are expressed as Go types, with struct tags directing how the CLI is mapped.

**Key Features:**
- Define CLI structure using Go structs and tags
- Auto-generated `--help` flag for all applications
- Attach `Run(...) error` methods to leaf commands
- Use `kong.Parse()` to obtain `kong.Context`
- Minimal boilerplate - the struct IS the interface
- Type-safe by design
- Small binary size (4,324 KB vs Cobra's 4,352 KB)

**Strengths:**
- Extremely clean, declarative code
- Type safety enforced by Go's type system
- Minimal API surface - easy to learn
- "Stable for a long time" with 1.0 release
- Developers report: "Smaller, easier to grok, great interface design"
- Fast migration: "Swapped out urfave/cli for Kong in about 40 minutes - about 7 commands in total"

**Weaknesses:**
- Smaller community than Cobra/urfave
- Less extensive documentation
- Fewer examples for complex scenarios
- No built-in config file support (needs extensions like kong-toml)

**Developer Feedback:**
- "I've ditched cobra for alecthomas/kong for good."
- "Highly recommend it."

**When to Use:**
- When you want clean, type-safe code
- Projects valuing simplicity and maintainability
- When struct tags feel natural for your team
- Small to medium CLI applications

---

### 4. jessevdk/go-flags - Reflection-Based Parsing

**Repository:** https://github.com/jessevdk/go-flags

**Stats:**
- GitHub Stars: 2,700+
- Latest Release: v1.6.1 (June 15, 2024)
- Contributors: 92 (53 primary + 39 additional)
- Dependents: ~17,200 projects
- License: BSD-3-Clause
- Total Commits: 656 on main branch
- Releases: 10
- Open Issues: 49 | Pull Requests: 16

**Description:**
An extensive command line option parser similar to the Go built-in flag package but provides more options and uses reflection to provide a convenient and succinct way of specifying command line options.

**Key Features:**
- Options with short names (`-v`)
- Options with long names (`--verbose`)
- Options with and without arguments (bool vs. other types)
- Flags defined using Go's struct tags
- Support for nested commands/subcommands
- Callbacks for flag processing
- Multiple option groups

**Strengths:**
- More powerful than standard library `flag` package
- Uses reflection for convenience
- Good for small utilities that don't need a full framework
- Avoids Inversion of Control problems
- Developer feedback: "The first one I tried was go-flags and it rocks"
- Widely used (17.2k dependents)

**Weaknesses:**
- Last release mid-2024 (less frequent updates than Cobra/urfave)
- Reflection can make behavior less obvious
- No built-in subcommand help generation
- Less documentation than major frameworks

**When to Use:**
- Small utilities needing more than stdlib flags
- When you want to avoid framework complexity
- Projects already using struct tags extensively
- When Inversion of Control is a concern

---

### 5. peterbourgon/ff - Flags-First Configuration

**Repository:** https://github.com/peterbourgon/ff

**Stats:**
- GitHub Stars: Not measured in research
- Latest Release: v3.4.0+
- Active development
- License: Apache 2.0

**Description:**
A flags-first package for configuration that provides an opinionated way to populate a flag.FlagSet with configuration data from the environment. A declarative and lightweight alternative to Cobra, urfave/cli, or Kong.

**Key Features:**
- Configuration priority: CLI flags > env vars > config files
- Built-in parsers: JSON, Plain, and .env formats
- Environment variable support with prefix matching
- Subcommands via ffcli package
- Parent flag set support (child flagsets parse parent flags)
- Minimal API - focused on core mechanics only
- Works with standard library `flag.FlagSet`

**Configuration Priority Model:**
1. **Command-line args** (highest) - "user" configuration
2. **Environment variables** (medium) - "session" configuration
3. **Config files** (lowest) - "host" configuration

**Strengths:**
- Excellent for 12-factor apps
- Clean separation of configuration layers
- Works with stdlib flag package
- Minimal framework overhead
- Explicit configuration priority
- Good for microservices and cloud-native apps

**Weaknesses:**
- Less feature-rich than Cobra/urfave
- Smaller community and ecosystem
- Documentation assumes familiarity with Go patterns
- No auto-generated help beyond stdlib

**When to Use:**
- Building 12-factor applications
- Microservices needing env/config file support
- When you want minimal framework code
- Projects already using stdlib `flag` package

---

### 6. posener/complete - Autocomplete Library

**Repository:** https://github.com/posener/complete

**Stats:**
- GitHub Stars: 941
- Forks: 70
- Active development

**Description:**
Bash completion written in Go, plus bash completion for the `go` command itself. Not a full CLI framework, but a library specifically for adding autocompletion to CLIs.

**Key Features:**
- Add bash completion to any Go program
- Install completion: `COMP_INSTALL=1 my-cli`
- Uninstall completion: `COMP_UNINSTALL=1 my-cli`
- Used by mitchellh/cli for autocompletion
- Integration with Kong via kongplete package

**Strengths:**
- Focused, single-purpose library
- Easy to add to existing CLIs
- Works with multiple CLI frameworks
- Clean environment variable-based installation

**Weaknesses:**
- Not a full CLI framework - just completion
- Bash-specific (though other shells supported by frameworks)
- Requires separate integration work

**When to Use:**
- Adding completion to an existing CLI
- Building completion for custom frameworks
- When using a framework without built-in completion

---

### 7. rosscartlidge/autocli - Data-Aware Completion

**Repository:** https://github.com/rosscartlidge/autocli

**Stats:**
- GitHub Stars: ~0 (new project, single developer)
- Latest Release: v4.3.3 (December 2025)
- Contributors: 1 (author only)
- Dependents: 1 (ssql project)
- License: MIT
- Source Lines: ~5,700 (including tests)
- Releases: 21
- Zero Dependencies (stdlib only)

**Description:**
A CLI framework designed for data-processing pipelines with unique features around data-aware completion. Built to support clause-based parsing (Boolean logic with `+`/`-` separators) and field-level completion from data files.

**Key Features:**
- **Data-aware completion** - Complete with actual field names from CSV/JSON files
- **Field value completion** - Tab-complete with actual values from data columns
- **Clause-based grouping** - `+` for OR, within-clause for AND logic
- **Three-level flag scoping** - Root global, subcommand global, per-clause local
- **Fluent builder API** - Chainable configuration with `.Done()` returns
- **Multi-argument flags** - Per-argument types and completers
- **Nested subcommands** - Multi-level command hierarchies (git remote add style)
- **Auto-generated help** - `-help` and `-man` pages
- **Universal completion script** - Single shared function for all programs
- **Pipeline field caching** - `CacheFieldsFrom()` passes fields across pipeline stages

**Unique Differentiators:**

1. **FieldsFromFlag()** - Completers can read a data file and suggest field names:
   ```go
   Flag("-where").
       Arg("field").FieldsFromFlag("FILE").Done().  // Completes: name, age, status
       Arg("operator").Options("eq", "gt", "lt").Done().
       Arg("value").FieldValuesFrom("FILE", "field").Done().  // Completes: actual values!
   ```

2. **FieldValuesFrom()** - Completes with actual data values from columns:
   ```bash
   ssql where -where status <TAB>     # Shows: eq, gt, lt, ne...
   ssql where -where status eq <TAB>  # Shows: active, pending, archived (from data!)
   ```

3. **Clause-based parsing** - Native Boolean logic in command-line:
   ```bash
   # Within clause = AND, between clauses (+) = OR
   cmd -filter age gt 30 -filter status eq active + -filter role eq admin
   ```

4. **CacheFieldsFrom()** - Pipeline field propagation:
   ```bash
   # First command caches field names for downstream completion
   ssql from users.csv | ssql where -where <TAB>  # Completes from upstream file!
   ```

**Strengths:**
- **Truly unique feature set** - No other framework offers data-aware completion
- **Zero dependencies** - Pure Go stdlib
- **Designed for data pipelines** - Natural fit for CLI data processing tools
- **Comprehensive completion** - Fields AND values, not just static options
- **Clean fluent API** - Readable, chainable configuration

**Weaknesses:**
- **Minimal community** - Single developer, no external contributors
- **Limited adoption** - Only used by one project (ssql)
- **No ecosystem integration** - No Viper equivalent, no documentation generation
- **Not battle-tested** - Limited production use outside ssql
- **Single shell support** - Bash only (no zsh, fish, powershell)
- **Sparse documentation** - Comprehensive USAGE.md but limited examples
- **No config file support** - Flags and environment only
- **Niche use case** - Features most valuable for data pipeline CLIs

**Binary Size:**
- Example program: ~1.6 MB (library only, comparable to other frameworks)

**When to Use:**
- Building CLIs that work with data files (CSV, JSON, JSONL)
- When tab completion of field names would improve UX
- Creating Unix-style pipeline tools
- When clause-based Boolean logic is natural for your domain
- Building ssql-like data processing tools

**When NOT to Use:**
- General-purpose CLIs without data file context
- Projects requiring broad community support
- When zsh/fish/powershell completion is needed
- Enterprise projects requiring battle-tested frameworks
- When comprehensive documentation is essential

**Honest Assessment:**

autocli occupies a narrow niche that no other framework addresses: **data-aware CLI completion**. For its specific use case (data pipeline tools like ssql), it provides unique value that would require significant custom work to replicate with Cobra or urfave/cli.

However, for most Go CLI projects, **Cobra** or **urfave/cli** remain better choices due to:
- Larger community and more contributors
- Better documentation and examples
- Broader shell support (zsh, fish, powershell)
- Integration with config management (Viper)
- Production battle-testing at scale

**Comparison with Kong:**

Kong's struct-based approach is philosophically different from autocli's fluent builder:

| Aspect | Kong | autocli |
|--------|------|---------|
| Definition | Struct tags | Fluent builder |
| Type safety | Compile-time via structs | Runtime via context |
| Completion | Via posener/complete extension | Built-in, data-aware |
| Learning curve | Lower (just write structs) | Higher (builder pattern) |
| Community | 2.9k stars, 146 contributors | ~0 stars, 1 contributor |

**Comparison with Cobra:**

| Aspect | Cobra | autocli |
|--------|-------|---------|
| Industry adoption | De facto standard | Niche, single project |
| Completion | Shell-based | Data-aware from files |
| Config integration | Seamless Viper | None |
| Shell support | bash, zsh, fish, powershell | bash only |
| Documentation gen | Man pages, markdown | Help only |
| Boolean logic | Manual implementation | Native clause parsing |

---

## Other Notable Frameworks

### Minimal/Lightweight Options

- **mcli** - Minimal but very powerful CLI library for Go
- **pflag** - Drop-in replacement for Go's flag package, implementing POSIX/GNU-style --flags
- **mow.cli** - Go library for building CLI applications with sophisticated flag and argument parsing
- **teris-io/cli** - Simple and complete API for building command line interfaces
- **mitchellh/cli** - CLI framework by HashiCorp's founder, used by Terraform and Nomad
- **climate** - Autogenerates CLIs from structs/functions with nested subcommands and shell completion

### Interactive CLI Libraries (Prompts/Wizards)

- **survey** - Multi-select support, built-in validation, simplicity
- **promptui** - Polished templates, customizable prompts, spinners
- **go-prompt** - Tab completion, dynamic suggestions, REPL-style interactivity
- **Bubble Tea** - TUI framework (may be overkill for simple prompts)

---

## Decision Framework

### Choose **Cobra** if:
- Building complex enterprise CLIs
- Need comprehensive documentation generation
- Want industry-standard patterns
- Building for a large team or open source project
- Need extensive plugin/extension support

### Choose **urfave/cli** if:
- Want simple, declarative API
- Building CLIs quickly
- Value straightforward code over features
- Don't need extensive documentation generation
- Prefer community-driven tools

### Choose **Kong** if:
- Want type-safe, struct-based design
- Value clean, minimal code
- Building small-to-medium CLIs
- Like Go's type system and struct tags
- Want easy testing and mocking

### Choose **go-flags** if:
- Need more than stdlib flags but not a full framework
- Want reflection-based parsing
- Building small utilities
- Avoiding framework Inversion of Control
- Have simple subcommand needs

### Choose **ff** if:
- Building 12-factor apps
- Need env var + config file support
- Want minimal framework overhead
- Using stdlib flag package already
- Building microservices

### Choose **stdlib flag** + **posener/complete** if:
- Have very simple needs
- Want zero dependencies
- Just need completion for existing flag-based CLI

### Choose **autocli** if:
- Building data-processing pipeline CLIs
- Need tab completion of field names from data files
- Want completion of actual data values (not just static options)
- Clause-based Boolean logic fits your domain
- Creating tools similar to ssql, jq, or csvkit

### DON'T choose **autocli** if:
- Building general-purpose CLIs
- Need multi-shell support (zsh, fish, powershell)
- Require broad community support and documentation
- Want established, battle-tested framework
- Need Viper-style config management

---

## Binary Size Comparison

From benchmark data (Go 1.18.1 on Linux/amd64):

- **alecthomas/kong**: 4,324 KB (3,384 KB stripped)
- **spf13/cobra**: 4,352 KB (3,420 KB stripped)
- **urfave/cli**: 4,684 KB (3,684 KB stripped)
  - Can reduce by ~300-400 KB by disabling ToMarkdown/ToMan

Note: Binary size differences are relatively small (300 KB range). Choose based on features and API, not binary size.

---

## Maintenance Status (as of December 2025)

### Actively Maintained (Releases in last 6 months)
- **Cobra**: v1.10.2 (December 4, 2025) - Very active
- **urfave/cli**: v3.6.1 (November 16, 2025) - Very active
- **Kong**: v1.0+ stable - Active
- **ff**: v3.4.0+ - Active

### Stable Maintenance (Last release 6+ months ago)
- **go-flags**: v1.6.1 (June 15, 2024) - Still maintained, slower release cycle

All frameworks show signs of active maintenance through issue responses, PR merges, and community engagement.

---

## Ecosystem Integration

### Configuration Management
- **Cobra** → Seamless integration with **Viper**
- **ff** → Built-in config file support (JSON, Plain, .env)
- **Others** → Manual integration or third-party libraries

### Shell Completion
- **Cobra** → Built-in (bash, zsh, fish, powershell)
- **urfave/cli** → Built-in with custom completion methods
- **Kong** → Via kongplete package + posener/complete
- **Others** → Via posener/complete or manual

### Documentation Generation
- **Cobra** → Auto-generated man pages, markdown docs
- **urfave/cli** → Markdown generation (can disable for smaller binaries)
- **Others** → Minimal or none

---

## Migration Considerations

### From Cobra
- **To urfave/cli**: Less boilerplate, simpler API, lose documentation generation
- **To Kong**: Cleanest code, type-safe, smaller community

### From urfave/cli
- **To Cobra**: Gain documentation generation, larger ecosystem, more verbose
- **To Kong**: Cleaner syntax, report says "40 minutes for 7 commands"

### From stdlib flag
- **To go-flags**: Minimal change, add struct tags, gain subcommands
- **To ff**: Add config file/env support, keep flag.FlagSet usage
- **To Cobra/urfave**: Bigger jump, gain full framework features

---

## Key Trends in 2025

1. **Cobra dominates enterprise** - Used by Kubernetes, Docker, major projects
2. **Kong gaining popularity** - Developers switching from Cobra/urfave report positive experiences
3. **urfave/cli v3 active** - Strong community-driven development
4. **Minimal frameworks stable** - go-flags, ff serve niche needs well
5. **Type safety valued** - Kong's struct-based approach resonates with Go developers
6. **12-factor patterns** - ff's config layering matches cloud-native trends

---

## Common Patterns Across Frameworks

All major frameworks support:
- Subcommands (nested commands like `git remote add`)
- Short and long flags (`-v`, `--verbose`)
- Flag aliases
- Help generation
- Positional arguments

Most frameworks support:
- Environment variable flag defaults
- Custom flag types
- Flag validation
- Shell completion (natively or via libraries)

---

## Conclusion

**For most projects:** Start with **urfave/cli v3** (simple, fast) or **Cobra** (comprehensive, industry-standard).

**For type-safe, minimal code:** Use **Kong**.

**For 12-factor/cloud-native:** Use **ff**.

**For simple utilities:** Use **go-flags** or stdlib **flag**.

**For data pipeline CLIs:** Consider **autocli** if you need field/value completion from data files.

The Go CLI ecosystem is mature and well-maintained. All major frameworks are production-ready. Choose based on your team's preferences, project complexity, and long-term maintenance considerations.

**Note on autocli:** While it offers unique data-aware completion features, its single-developer status and limited adoption make it a specialized choice. For data pipeline tools specifically, the field completion capabilities provide genuine value not available elsewhere. For general CLI development, the established frameworks (Cobra, urfave/cli, Kong) remain safer choices.

---

## Sources

1. [Go Official - Command-line Interfaces (CLIs)](https://go.dev/solutions/clis)
2. [GitHub - spf13/cobra](https://github.com/spf13/cobra)
3. [GitHub - urfave/cli](https://github.com/urfave/cli)
4. [GitHub - alecthomas/kong](https://github.com/alecthomas/kong)
5. [GitHub - jessevdk/go-flags](https://github.com/jessevdk/go-flags)
6. [GitHub - peterbourgon/ff](https://github.com/peterbourgon/ff)
7. [GitHub - posener/complete](https://github.com/posener/complete)
8. [GitHub - Oursin/Go-CLI-Comparison](https://github.com/Oursin/Go-CLI-Comparison)
9. [GitHub - gschauer/go-cli-comparison](https://github.com/gschauer/go-cli-comparison)
10. [Matt Turner - Choosing a Go CLI Library](https://mt165.co.uk/blog/golang-cli-library/)
11. [Awesome Go - Standard CLI](https://awesome-go.com/standard-cli/)
12. [Evaluating GoLang CLI Packages - Medium](https://tjayrush.medium.com/evaluating-golang-cli-packages-2ae34bb79787)
13. [Daniel Michaels - Kong is an amazing CLI for Go apps](https://danielms.site/zet/2023/kong-is-an-amazing-cli-for-go-apps/)
14. [Daniel Michaels - How I write Golang CLI tools today (using Kong)](https://danielms.site/zet/2024/how-i-write-golang-cli-tools-today-using-kong/)
15. [Bytesizego - Generating A CLI Application with Cobra in Golang](https://www.bytesizego.com/blog/cobra-cli-golang)
16. [The Go Ecosystem in 2025 - JetBrains GoLand Blog](https://blog.jetbrains.com/go/2025/11/10/go-language-trends-ecosystem-2025/)

---

**Document Version:** 1.1 (added autocli comparison)
**Research Date:** December 21, 2025
**Researcher:** Claude (Anthropic)
**Location:** `/home/rossc/src/ssql/doc/research/go-cli-frameworks-comparison.md`
