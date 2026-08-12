# autocli-shell + autocli-ssh Proposal

Reference: DFC095
Created: 2026-05-12
Last modified: 2026-05-13

[Back to Index](./README.md)

**Status:** Phases A–D shipped (2026-05-12 / 2026-05-13). See *Status at end of week 20* below for the snapshot.
**Date:** 2026-05-12 (orig); 2026-05-13 (status update)
**Target:** autocli vX, ssql v4.44 (first consumer) — both shipped
**Prerequisites:** stable autocli completion engine (already present)
**Related:** `ssql-serve-proposal.md`, `cli-tools-design.md`, `distributed-ssh-processing.md`

## Status at end of week 20 (2026-05-13)

| Phase | Module | Version | Notes |
| --- | --- | --- | --- |
| A — engine split | `github.com/rosscartlidge/autocli/v4` | v4.5.0 | `Complete`, `ExecuteWith`, `Context` IO+State |
| B — shell driver | `github.com/rosscartlidge/autocli/shell` | v0.1.1 | readline loop, `:` builtins, tokeniser |
| C — SSH server | `github.com/rosscartlidge/autocli/ssh` | v0.1.0 | host key, authorized_keys, pubkey auth |
| D — first consumer | `github.com/rosscartlidge/ssql/v4` | v4.44.0 | `ssql serve` subcommand |

End-to-end smoke-tested against loopback with a small CSV — `status` / `schema` / `count` / `head [-n N] [-t]` all return expected output through `ssh` and across multi-command sessions. Race-detector clean except for one known third-party race (see "Open follow-ups" below).

**Total effort:** ~3 working days across A–D, under the ~4-day proposal estimate. Test-as-you-go discipline kept regressions zero between iterations.

### Deviations from the original proposal

- **Module path** for the shell sub-module is `github.com/rosscartlidge/autocli/shell` (not `.../v4/shell`). Go's tooling expects the filesystem layout to mirror the module path; since the sub-module lives at `shell/` (not `v4/shell/`), we dropped the `/v4` suffix. Parent stays `github.com/rosscartlidge/autocli/v4`.
- **Runtime `:set vi`/`:set emacs`** defers the actual readline switch until next session because `chzyer/readline.SetVimMode` races with its own input goroutine (library bug, not ours). Bookkeeping updates immediately so per-user prefs persistence works.
- **`ssql serve` v0.1** ships with `status` / `schema` / `count` / `head` only — no pipelines, no `let` variables. These are tracked under "Open follow-ups" rather than blocking the first release. The autocli-shell stack is the heavy lift; layering ssql-specific features on top is incremental.

### Open follow-ups (tracked, not blocking)

1. **`chzyer/readline.SetVimMode` race** — send upstream PR (~1–2 hrs investigation + patch + test). If accepted, drop the deferred-switch workaround. Library is mostly maintenance-mode; if the PR languishes, consider forking or switching to `reeflective/readline`. The user's question prompted this — the answer was "yes, worth a shot."
2. **SSH window-size propagation** — `autocli/ssh` accepts `window-change` requests but doesn't surface them to readline's `SetSize`. Lines wrap at readline's auto-detected width (typically 80). Modest plumbing change.
3. **Pipes in `autocli/shell`** — Position 2 (`io.Pipe` between stages, opt-in via `Options.EnablePipes`) is documented in the "Pipes and composition" section below. Not yet implemented; ~100 LOC plus tests. Required for `ssql serve` to support `from-loaded | where … | to table`.
4. **`let $name = pipeline`** — REPL-friendly replacement for bash process substitution. Documented in this proposal as v2-of-shell scope. Reserved grammar already.
5. **Schema-aware completion in `ssql serve`** — TAB on `head -if <TAB>` should complete loaded field names. Same `FieldCompleter` ssql commands use today, pointed at the in-memory dataset instead of a file. Small registration change.
6. **Position 3 (in-process composition)** — composable handlers returning `iter.Seq[Record]` / `Filter[Record,Record]`, the big one. Bigger lift in autocli; the right model for `ssql serve` querying when we want full Go speed rather than JSONL serialisation between stages. Defer until we have a concrete perf-motivated need.
7. **Per-user history dir on `ssql serve`** — `autocli/ssh.Options.HistoryDir` plumbing exists, `ssql serve` just needs to expose a CLI flag (`-history-dir`).

## TL;DR

Expose autocli's command tree + completion engine as embeddable
Go libraries so any Go service can offer:

1. **Local interactive shell** (`autocli/shell`) — REPL-style line
   editor with full autocli tab-completion, history, and arrow-key
   editing. Driven by a readline library inside the service process.

2. **SSH-accessible CLI** (`autocli/ssh`) — same shell, but exposed
   over an SSH listener. Operators log in with their normal ssh
   keys and get a router-style interactive console for status,
   config, and ad-hoc queries against the running service. The
   pattern Cisco/Juniper/Vault/etcd/Redis have used for decades,
   now available as a ~200-LOC drop-in for any Go service.

The work is mostly a refactor of autocli's existing bash-completion
machinery: split the completion *engine* (a pure function from
partial-line to suggestions) from the bash *protocol shim* that
exists today. Once split, plugging in a different driver (readline,
bubbletea, SSH+pty) is mechanical.

First consumer: `ssql serve` (per the existing serve proposal) —
a daemon holding a Parquet/Arrow dataset mmap'd, accepting ad-hoc
queries with schema-aware completion against the live data,
zero per-query startup cost.

Effort: ~4 days end-to-end. Phase A in autocli (engine split):
~1 day. Phase B (shell adapter): ~half day. Phase C (SSH adapter):
~1 day. Phase D (`ssql serve` integration as proof of value):
~1 day. Phase E (docs/tests): ~half day.

## Motivation

### Why an SSH-accessible service CLI is valuable

Long-running services need an out-of-band channel for operators
to inspect and adjust them without restarting. The two
established patterns are:

- **HTTP admin endpoint** (envoy `/admin`, prometheus `/metrics`,
  vault HTTP API). Great for scripts and dashboards. Awkward for
  humans: requires `curl` + JSON parsing + memorising endpoints.

- **Interactive CLI over SSH** (Cisco IOS, Juniper Junos, etcdctl
  in interactive mode, vault CLI, psql, redis-cli). Great for
  humans: tab-completion, history, structured prompts. The CLI
  is part of the service binary, not a separate tool; operators
  ssh in and get a coherent UX.

Many services offer both — a REST API for automation, an SSH-CLI
for humans. The SSH-CLI is dramatically more pleasant when you're
debugging at 3 AM and don't remember the exact JSON shape of an
admin endpoint.

### Why autocli is a good basis for this

The CLI work the user has already done in autocli — subcommand
hierarchies, typed flags, variadic args, field-completer
delegates, helpful error messages — *is exactly the surface area
a service operator needs*. Re-doing it from scratch for each
service is a waste; the value is in the completion engine, not
the bash invocation path.

### Why now for ssql

The `ssql-serve-proposal.md` describes a long-running daemon
mode where ssql holds a dataset in memory across queries. That
proposal currently sketches a browser UI over WebSocket. An SSH-
CLI is a complementary (or arguably better) interface for the
same daemon — power users get their shell-native UX, the daemon
needs no JavaScript or browser, and the SSH path doubles as the
remote-access story (no need for a tunnel + local browser).

## Architecture

Three layers, two new sub-packages of autocli:

```
┌──────────────────────────────────────────────────────────────┐
│  Service Go code                                             │
│  cli := autocli.New().Subcommand("status")....               │
│  state := &MyState{...}                                      │
│  autoclishell.Serve(cli, autoclishell.Options{State: state}) │  ← Layer 3a (local)
│  // OR                                                       │
│  autoclissh.Serve(cli, autoclissh.Options{Addr: ":2222",     │  ← Layer 3b (SSH)
│      AuthorizedKeys: "/etc/myservice/authorized_keys",       │
│      State: state})                                          │
└──────────────────────────────────────────────────────────────┘
                            │
                            ▼
┌──────────────────────────────────────────────────────────────┐
│  autocli/shell — readline-based REPL                         │  ← Layer 2
│  • TAB → autocli.Complete(line, pos)                         │
│  • Enter → autocli.Execute(args, ctx)                        │
│  • Ctrl-C → cancel current command (context cancellation)    │
│  • Ctrl-D → close session                                    │
└──────────────────────────────────────────────────────────────┘
                            │
                            ▼
┌──────────────────────────────────────────────────────────────┐
│  autocli — completion engine + dispatch (refactored)         │  ← Layer 1
│  • Complete(line string, pos int) ([]Completion, error)      │
│  • Execute(args []string, ctx Context) error                 │
│  • Context carries IO, State, cancellation                   │
│  • Same surface for bash-completion shim, shell, SSH         │
└──────────────────────────────────────────────────────────────┘
```

### Layer 1: autocli engine split

Today's autocli has the completion logic and the dispatch logic
*coupled* to `os.Args` / `os.Stdin` / `os.Stdout` / `os.Exit`. The
bash-completion code path (`-complete N`) reads `os.Args`,
computes suggestions, prints them, exits.

The refactor splits these into pure functions that the existing
bash path becomes a thin wrapper over:

```go
package autocli

// Completion is one suggestion the engine emits.
type Completion struct {
    Text    string // what the user gets if they accept it
    Display string // optional rendering (defaults to Text)
    Help    string // optional one-line tooltip
}

// Complete returns the completions for the partial command line at
// caret position pos. Side-effect free: callable from anywhere, any
// number of times, concurrently.
func (c *Command) Complete(line string, pos int) ([]Completion, error)

// Context is the per-invocation state passed to a handler. Replaces
// the existing hard-coded os.Stdout / os.Stderr / os.Exit pattern.
type Context struct {
    // Args are the parsed positional arguments after flag extraction.
    Args []string
    // GlobalFlags / LocalFlags as today.
    GlobalFlags map[string]any
    LocalFlags  map[string]any
    // IO for the handler. Defaults to os.Stdin / os.Stdout / os.Stderr;
    // shell/ssh adapters override with the session's streams.
    Stdin  io.Reader
    Stdout io.Writer
    Stderr io.Writer
    // Ctx propagates cancellation (Ctrl-C, session close).
    Ctx context.Context
    // State is the service-supplied per-instance value (live data,
    // config, mutable counters …). Type-asserted by the handler.
    State any
}

// Execute parses args (including flags), validates, and invokes the
// matching subcommand's handler with ctx. Returns the handler's error
// without touching os.Exit. The existing main() entrypoint becomes
// a wrapper around this.
func (c *Command) Execute(args []string, ctx Context) error
```

Migration of existing bash path:

```go
// Today:
func main() { mycli.Run() } // reads os.Args, dispatches, exits

// After refactor — internal Run() becomes:
func (c *Command) Run() {
    if isCompletionRequest(os.Args) {
        line, pos := parseCompletionArgs(os.Args)
        for _, comp := range c.Complete(line, pos) {
            fmt.Println(comp.Text)
        }
        return
    }
    err := c.Execute(os.Args[1:], Context{
        Stdin:  os.Stdin,
        Stdout: os.Stdout,
        Stderr: os.Stderr,
        Ctx:    contextFromSignals(),
    })
    if err != nil {
        fmt.Fprintln(os.Stderr, "Error:", err)
        os.Exit(1)
    }
}
```

Existing services using autocli today see no API break — `Run()`
keeps working. They opt in to the new `Complete()`/`Execute()` by
calling them directly.

### Layer 2: autocli/shell

A thin wrapper around a readline library (recommendation:
`github.com/chzyer/readline` — mature, well-maintained, supports
custom completers, MIT-licensed).

```go
package autoclishell

type Options struct {
    Prompt       string             // default: "> "
    HistoryFile  string             // empty = no history (see "Editor mode and persistence")
    EditingMode  EditingMode        // EditingEmacs (default) or EditingVi
    SessionDir   string             // for per-user history + prefs; required for SSH multi-user
    State        any                // passed through to Context
    Welcome      string             // banner printed on session start
    Goodbye      string             // banner printed on Ctrl-D
    OnError      func(error)        // optional logging hook
    Stdin        io.Reader          // default os.Stdin
    Stdout       io.Writer          // default os.Stdout
}

type EditingMode int

const (
    EditingEmacs EditingMode = iota
    EditingVi
)

// Serve runs an interactive shell loop until Ctrl-D or :exit.
func Serve(cli *autocli.Command, opts Options) error
```

Tab handler wires into `cli.Complete(line, pos)`:

```go
rl, _ := readline.NewEx(&readline.Config{
    Prompt: opts.Prompt,
    AutoComplete: completerFunc(func(line []rune, pos int) ([][]rune, int) {
        comps, _ := cli.Complete(string(line), pos)
        out := make([][]rune, len(comps))
        for i, c := range comps {
            out[i] = []rune(c.Text)
        }
        return out, pos
    }),
})
```

Enter handler tokenises with the same shell-quoting rules autocli
already uses (single/double quote, backslash) and calls
`cli.Execute(args, ctx)`.

Built-in shell-level commands (not autocli subcommands):

- `:help` — alias for `cli help` if defined, otherwise built-in usage
- `:exit` / `:quit` — close session
- `:history` — list session history
- `:!cmd` — re-run a previous command by index
- `:set vi` / `:set emacs` — switch editing mode for the rest of
  the session (persists to per-user prefs file)
- `:set` — show current settings (mode, history file, etc.)

These start with `:` so they can't collide with user-defined
subcommands.

#### Editor mode and persistence

`chzyer/readline` supports both emacs (default) and vi modes
natively (`Config.VimMode: true` selects vi at startup; runtime
`rl.SetVimMode(bool)` flips on the fly). Bindings in each mode
match GNU readline: emacs gets Ctrl-A/E/K/W/R/etc.; vi gets
normal/insert modes with hjkl/w/b/dd motions and `:` for ex
commands.

In an SSH session the operator's local `~/.inputrc` isn't reachable
(readline runs on the server, not the client), so the server has
to expose the choice. `autocli/shell` does this via:

1. **Initial mode** from `Options.EditingMode`. Service authors
   pick the default for their consumer base. Vi-leaning teams set
   `EditingVi`; broader services keep the `EditingEmacs` default.

2. **Per-user override at runtime** via `:set vi` / `:set emacs`.
   Takes effect immediately.

3. **Persistence per user**, alongside command history. When
   `Options.SessionDir` is set (typically `/var/lib/myservice/sessions`
   under SSH, `~/.config/myservice` for local shell), the shell
   reads `$SessionDir/$user/prefs.json` on connect and writes back
   on mode change. Schema:

   ```json
   {"editing_mode": "vi", "history_file": "history"}
   ```

   The history file lives in the same per-user directory, so each
   operator gets their own scrollback without leaking commands
   between users on a shared service.

Recommended default for `ssql serve`: **vi mode** (project author
preference; data folks tend to vi-lean and the muscle memory pairs
well with `ssql` being a CLI tool). Other autocli/shell consumers
keep emacs.

### Layer 3: autocli/ssh

Wraps `golang.org/x/crypto/ssh` to accept incoming connections,
authenticate them, and hand each one a shell session.

```go
package autoclissh

type Options struct {
    Addr          string                 // ":2222" or "127.0.0.1:2222"; see Port configuration below
    HostKeyPath   string                 // server's host key (auto-generated on first run if missing)
    AuthorizedKeys string                // path to ssh-style authorized_keys file
    AuthCallback  func(meta ConnMeta, key ssh.PublicKey) (allowed bool, user string, err error)
    Welcome       string                 // banner on connect
    State         any                    // passed through (per-connection or shared)
    StatePerConn  func(meta ConnMeta) any // override to make State per-session
    OnLogin       func(meta ConnMeta)     // audit hook
    OnLogout      func(meta ConnMeta)
    Logger        *slog.Logger
}

type ConnMeta struct {
    User       string
    RemoteAddr string
    SessionID  string
}

// Serve listens on opts.Addr and accepts SSH connections until ctx
// is cancelled. Each accepted connection runs autoclishell.Serve in
// a goroutine with session-scoped IO.
func Serve(ctx context.Context, cli *autocli.Command, opts Options) error
```

#### Port configuration

Multiple service instances on one host is a normal deployment, so
the port story has to be unambiguous and easy.

`Options.Addr` is a standard Go listen address — `":2222"` (all
interfaces, port 2222), `"127.0.0.1:2222"` (loopback only),
`":0"` (kernel-assigned ephemeral). The default if empty is
`":2222"`, chosen because:

- ≥ 1024 (no root needed)
- not a well-known service port
- matches the convention used by ad-hoc sshd-in-container deployments

**Recommended pattern for service binaries:**

```go
var flagAddr = flag.String("listen", ":2222", "SSH listen address")
// later:
addr := *flagAddr
if env := os.Getenv("MYSERVICE_LISTEN"); env != "" {
    addr = env // env overrides flag default but not explicit flag
}
autoclissh.Serve(ctx, cli, autoclissh.Options{Addr: addr, ...})
```

Three layers of override, in priority order: explicit flag > env
var > built-in default. Standard 12-factor shape; operators can
launch `MYSERVICE_LISTEN=:2223 myservice` for a second instance
without rebuilding.

**Auto-discovery (optional pattern):** services that want to be
findable by tooling can write `pid + addr` to a state file on
startup (e.g. `/var/run/myservice.info`) and remove it on shutdown.
The standard Go `net.Listener.Addr()` returns the resolved address
even for `":0"`, so this works for ephemeral ports too:

```go
ln, _ := net.Listen("tcp", *flagAddr)
os.WriteFile("/var/run/myservice.info",
    []byte(fmt.Sprintf("pid=%d addr=%s\n", os.Getpid(), ln.Addr())),
    0644)
autoclissh.ServeListener(ctx, cli, ln, opts) // alternate entrypoint
```

`ServeListener` is the variant that takes an already-bound
listener (so the caller controls the binding). `Serve` wraps it
with a `net.Listen` call for the simple case.

**Bind-to-loopback by default for the local-only case:** services
that should NEVER be reachable off-box can default to
`"127.0.0.1:2222"`. Same UX, no firewall surprise. Document this
clearly in each service's man page.

#### Authentication

Default: load `opts.AuthorizedKeys` (ssh-style file, one key per
line with optional comment) and accept connections whose pubkey
matches a listed key. Users who already have ssh access set up
need zero additional config.

Override: `opts.AuthCallback` for services that want LDAP /
OAuth-proxy / per-tenant policy / etc.

No password auth (deliberately — service CLIs should be key-only).

#### Host key management

`opts.HostKeyPath` defaults to e.g. `/var/lib/myservice/ssh_host_ed25519_key`.
On first start, if the file is absent, generate an ed25519 key
and persist it (`0600`). On subsequent starts, load it. Same
contract as a real sshd, so operators recognise the pattern and
can replace the key file when rotating.

#### Session flow

```
TCP accept
  ↓
SSH handshake (host key, kex, user auth via pubkey)
  ↓
SSH session channel open
  ↓
PTY request? Honour rows/cols, listen for window-changed events
  ↓
Spawn goroutine running autoclishell.Serve with:
  Stdin  = ssh.Channel
  Stdout = ssh.Channel
  State  = opts.State (or opts.StatePerConn(meta))
  Ctx    = derived from server Ctx + session Ctx
  ↓
Client disconnects → cancel session Ctx → handler unwinds → goroutine returns
```

#### Concurrency model

Multiple sessions = multiple goroutines, each with its own
`Context` value but the same `State` pointer. The service is
responsible for any synchronisation that state needs — autocli
doesn't impose locking. Standard Go discipline: use channels or
sync primitives in the handler if it mutates shared state.

For services that want isolated per-session state (e.g. one
loaded dataset per user), `opts.StatePerConn` lets the service
return a fresh state object per connection.

### Layer 4: Service-side handler

A handler looks the same as today's autocli handler, except it
reads `State` from the `Context`:

```go
cli := autocli.New().Subcommand("status").
    Description("show service status").
    Handler(func(ctx *autocli.Context) error {
        svc := ctx.State.(*MyService)
        fmt.Fprintf(ctx.Stdout, "uptime: %v\n", svc.Uptime())
        fmt.Fprintf(ctx.Stdout, "queries served: %d\n", svc.Queries.Load())
        return nil
    }).Done()
```

Output goes to `ctx.Stdout` (the SSH session, not os.Stdout) so
multi-session output doesn't interleave. Errors return up to
the shell loop, which prints them and re-prompts.

Long-running commands honour `ctx.Ctx`:

```go
Handler(func(ctx *autocli.Context) error {
    for row := range svc.Stream() {
        if err := ctx.Ctx.Err(); err != nil { return err }
        fmt.Fprintln(ctx.Stdout, row)
    }
    return nil
})
```

Ctrl-C in the SSH session cancels `ctx.Ctx`; the handler exits
gracefully and the shell re-prompts.

## Pipes and composition

A naïve embedded shell that runs one subcommand per line is fine
for operator consoles (`status`, `reload`, `config`) but useless
for ssql serve, where every interesting query is a pipeline.
`from` → `where` → `group-by` → `to table` has to compose somehow.

Three positions, layered from simplest to most powerful:

### Position 1 — no pipes (default)

Each input line is exactly one subcommand. `|` is a syntax error.
Sufficient for service-operator consoles (configure / inspect /
reload). This is the default for `autocli/shell` because it adds
zero surface area to autocli itself and keeps the simplest case
genuinely simple.

### Position 2 — process pipes via `io.Pipe()` (opt-in)

Set `Options.EnablePipes = true`. The shell tokeniser splits the
line on top-level `|` (respecting quotes), creates an `io.Pipe()`
between each adjacent pair of stages, spawns each stage in its
own goroutine with its `Context.Stdin`/`Context.Stdout` wired to
the pipe ends, and waits for all stages to finish.

```
user types:  from a.csv | where -if x gt 5 | to table
                  │             │              │
                  ▼             ▼              ▼
              goroutine     goroutine     goroutine
              ctx.Stdout─►io.Pipe()─►ctx.Stdin─►io.Pipe()─►ctx.Stdin
                                                           ctx.Stdout = session
```

Works for any subcommand that already reads JSONL on stdin and
writes JSONL on stdout — i.e. essentially all of ssql today.
Cost: each pipeline still serialises through JSONL between
stages, paying the marshalling cost in-process for no real
reason. ~100 LOC in `autocli/shell` to implement.

### Position 3 — in-process composition (recommended for ssql serve)

Subcommands return composable Go values instead of writing to
stdout:

- A **source** subcommand returns `iter.Seq[Record]`.
- A **transform** subcommand returns a `Filter[Record, Record]`
  (i.e. `func(iter.Seq[Record]) iter.Seq[Record]`).
- A **sink** subcommand consumes an `iter.Seq[Record]` and returns
  nothing.

The shell parser walks the stages, type-checks that sources only
appear first and sinks only appear last, composes the chain, and
drains it. No JSONL serialisation; the entire pipeline runs in one
goroutine with values flowing through `iter.Seq` channels.

This is essentially what the generated-Go path produces today —
we'd be exposing the same composition mechanism interactively.
The handler signature changes:

```go
// Today (print to stdout):
Handler(func(ctx *autocli.Context) error {
    rows := loadDataset(...)
    for r := range rows { fmt.Fprintln(ctx.Stdout, r) }
    return nil
})

// Position-3 composable handler:
Source(func(ctx *autocli.Context) (iter.Seq[ssql.Record], error) {
    return loadDataset(...), nil
})
```

`autocli.Source` / `autocli.Transform` / `autocli.Sink` are new
handler-registration verbs alongside `Handler`. A subcommand with a
plain `Handler` is non-composable and emits a "cannot pipe into/out
of X" error if used in a pipeline.

Bigger lift in autocli (~half day for the type-aware composer) but
the perf delta is the whole point of `ssql serve` — querying a
loaded dataset at full Go speed instead of marshalling through
JSONL on every stage.

### Choosing per consumer

- `autocli/shell` ships Positions 1 and 2 (default 1, flag-flip 2).
  Process-pipe support is generic and useful for any shell.
- `ssql serve` builds Position 3 on top of autocli's composable-
  handler primitives. Other services adopt it if they want full-
  speed in-process pipelines.

## Process substitution: `let` variables

Bash `<(cmd)` forks a process and gives the parent a path to that
subprocess's output. In-shell, there's no fork — and even if there
were, the consuming command would have to accept either a real
path or an `io.Reader` (a meaningful API change for every command
that takes a FILE arg).

Better fit for a REPL: **named intermediate results.**

```
> let recent = from events.csv | where -if date ge 2026-05-01
> let high = from prices.csv | where -if value gt 1000
> join -left $recent -right $high -on symbol | to table
```

`let NAME = pipeline` runs the pipeline lazily, stores the result
as a named handle in the session, and lets subsequent commands
reference it via `$NAME`. The handle is whatever the pipeline
yields: under Position 2 it's a buffered bytes.Reader, under
Position 3 it's a re-runnable `iter.Seq[Record]`.

Why this is better than `<(...)`:

- **Reusable.** `$recent` can appear in multiple later commands
  without re-running the source.
- **Inspectable.** `:vars` lists current handles, their schemas,
  their row counts. Operator can see what they've materialised.
- **Disposable.** `:unset recent` frees it.
- **Lazy if Position 3.** A handle is an `iter.Seq`; nothing runs
  until something drains it. Combine handles freely.
- **REPL-natural.** Variables are how every interactive shell
  composes intermediate results (psql `\gset`, Python REPL,
  Jupyter cells). Process substitution is a bash-shell quirk.

`let` ships in `autocli/shell` as a built-in `:`-prefix command,
same family as `:exit / :history / :vars / :unset`. Position-3
composable services get full lazy reuse; Position-2 services get
buffered reuse. Position-1 shells don't expose `let` at all (no
pipelines = no intermediates).

Deferred to v2 of `autocli/shell` to keep the first ship small.
The grammar reserves `$name` and `let` from day one so we don't
break compatibility later.

## End-to-end example: `ssql serve`

The promised use case. Service binary:

```go
package main

import (
    "context"
    "log/slog"
    "github.com/rosscartlidge/autocli/v4"
    "github.com/rosscartlidge/autocli/v4/ssh"
    "github.com/rosscartlidge/ssql/v4"
)

type Server struct {
    dataset *ssql.Dataset // mmap'd Parquet/Arrow
    started time.Time
}

func main() {
    srv := &Server{
        dataset: ssql.OpenDataset("/data/main.parquet"),
        started: time.Now(),
    }

    cli := autocli.New().
        Subcommand("status").Description("show server status").
        Handler(statusHandler).Done().

        Subcommand("schema").Description("show dataset schema").
        Handler(schemaHandler).Done().

        Subcommand("query").Description("run an SQL-style pipeline").
        Flag("PIPELINE").String().Variadic().Required().Done().
        Handler(queryHandler).Done().

        Subcommand("reload").Description("reload the dataset from disk").
        Handler(reloadHandler).Done().

        Build()

    ctx := contextFromSignals()
    err := autoclissh.Serve(ctx, cli, autoclissh.Options{
        Addr:           ":2222",
        HostKeyPath:    "/var/lib/ssql-serve/ssh_host_ed25519_key",
        AuthorizedKeys: "/etc/ssql-serve/authorized_keys",
        State:          srv,
        Welcome:        "ssql serve — type `help` for commands",
        Logger:         slog.Default(),
    })
    if err != nil { log.Fatal(err) }
}
```

Operator workflow:

```
$ ssh -p 2222 operator@dataserver
ssql serve — type `help` for commands
> stat<TAB>
status   show server status
> status
uptime: 3h12m
queries served: 142
dataset: /data/main.parquet (12.4 GB, 87M rows)
> schema
name      type
─────────────────
order_id  int64
customer  string
amount    float64
...
> query from-loaded | where -if amount gt 1000 | group-by customer -count n
... results streamed back ...
> query <TAB>     # field-from-schema completion against loaded data!
order_id   customer   amount   ...
> :exit
$
```

The field-completion on `query <TAB>` is exactly the autocli
field-completer the CLI already uses today — pointed at the
in-memory schema instead of a file on disk. That reuse is the
whole point.

## Failure modes and risks

### A handler panics

`autocli.Execute` recovers panics, logs them, and returns an
error to the shell loop. The session re-prompts; the service
keeps running. Other sessions are unaffected. (This is a real
behaviour change for the bash path too — we should panic-recover
there as well, since today a panic crashes the entire process
mid-pipeline.)

### A handler runs forever

The session can Ctrl-C (sends SIGINT → cancels ctx.Ctx →
handler should observe and return). If the handler ignores
cancellation, the goroutine leaks until process exit.
Documented as a handler-author obligation, same as any other
context-respecting Go API.

### Server shutdown with sessions still open

`Serve` accepts a `context.Context`; cancelling it stops
accepting new connections, then waits for in-flight sessions to
finish (with a configurable grace timeout). After timeout,
sessions are forcibly closed. Standard graceful-shutdown
pattern.

### Concurrent state mutation

The service author's problem. We document the contract clearly:
`State` is shared across sessions unless `StatePerConn` is
provided; the author must protect mutable state with sync
primitives.

### Auth misconfiguration

If `AuthorizedKeys` file is missing or unreadable on startup,
fail to bind (refuse to run with no auth). If a key is removed
from the file while the server runs, in-flight sessions
continue; new connections are rejected.

No "operator forgot to set up auth and started a publicly-
reachable service" footgun.

### SSH host key compromise

Same blast radius as a regular sshd host-key compromise — MITM
on this service's port. Document key rotation in the operator
guide. Auto-generated keys are 0600 in a dedicated directory.

## Out of scope for v1

- **Multiple privilege levels** (admin vs read-only). Flat
  model first; service authors who need this can implement it
  in their handlers using `ctx.State` to look up the
  authenticated user (passed in `ConnMeta.User`).
- **SCP / SFTP subsystem.** Service CLIs don't usually need
  file transfer; users who do can run a real sshd alongside.
- **Streaming output mid-command with re-prompting.** v1: a
  command runs to completion, then the prompt returns. Future:
  a `--watch` modifier that re-runs on an interval and refreshes
  in place (TUI-ish).
- **Local socket variant** (Unix domain socket instead of TCP+SSH).
  Useful for "no network exposure, just sudo and connect" but
  doesn't reuse autocli/ssh — it's a separate driver. Easy follow-up.
- **Colour / TUI panels.** v1 is plain text + readline. Anything
  fancier composes on top.
- **Bash export of an embedded shell session.** Users who want
  to script against a service should use a separate REST/RPC
  API; the SSH-CLI is for humans.

## Phased implementation

### Phase A — autocli engine split (~1 day) — ✅ SHIPPED v4.5.0

1. Introduce `autocli.Context` struct with `Stdin/Stdout/Stderr/Ctx/State`.
2. Refactor existing handlers to receive `*Context` instead of
   reading globals (today's `cf.Context` is similar; this is
   mostly renames + threading IO through).
3. Extract `Command.Complete(line, pos) ([]Completion, error)` —
   peel the existing bash-completion logic away from os.Args.
4. Extract `Command.Execute(args []string, ctx Context) error` —
   peel dispatch logic away from os.Exit.
5. Wrap the old `Run()` entrypoint around the new primitives;
   existing CLIs see zero break.

Tests: every existing autocli test keeps passing. New table tests
for `Complete()` against known partial lines.

### Phase B — autocli/shell (~half day) — ✅ SHIPPED shell/v0.1.1

1. New module `autocli/shell` (sub-package of autocli or a
   separate go module — separate keeps the readline dep
   optional).
2. Implement `Serve(cli, opts)` using `chzyer/readline`.
3. Tokeniser shared with the bash path (existing autocli
   shell-quoting logic).
4. Built-in `:exit / :help / :history` commands.

Tests: spin up a shell with a stdin reader, feed commands, assert
on stdout. Standard readline-loop testing pattern.

### Phase C — autocli/ssh (~1 day) — ✅ SHIPPED ssh/v0.1.0

1. New module `autocli/ssh`.
2. SSH server setup: host-key load-or-generate, authorized_keys
   parsing, pubkey-auth callback.
3. Session goroutine: PTY negotiation, channel IO, window-change
   event handling.
4. Call into `autoclishell.Serve` with session-scoped streams.
5. Graceful shutdown with grace timeout.

Tests: in-process integration test — spin up a server on
localhost, connect with `golang.org/x/crypto/ssh` client,
exchange commands, assert on output. ~150 LOC of test, covers
auth + happy-path + shutdown.

### Phase D — ssql serve integration (~1 day) — ✅ SHIPPED ssql v4.44.0 (v0.1 scope: status / schema / count / head)

1. New `cmd/ssql-serve/` (or add a `serve` subcommand to `ssql`).
2. Implement Server struct + handlers: status, schema, query,
   reload, stats.
3. Hook into autocli/ssh.
4. End-to-end demo: load Parquet, ssh in, run queries against
   the live dataset, observe sub-millisecond query latency
   (no startup cost).

### Phase E — docs/examples (~half day) — ✅ Partial: shell/README.md, shell/_example, ssh/README.md, ssh/_example shipped; CHANGELOG + journal-W20 updated

1. README for autocli/shell and autocli/ssh.
2. Example service in `examples/`.
3. Operator guide section: key management, audit logging, graceful
   shutdown, monitoring.

### Total: ~4 days

Phases A-C are autocli work and ship independently. Phase D
ships in ssql.

## Why this scope is right

- **Solves a real operational problem** for any long-running Go
  service. Useful far beyond ssql.
- **Reuses everything autocli already has.** The completion engine
  *is* the valuable IP; this proposal just removes the bash-shaped
  hole around it.
- **Minimal new surface area.** Two new sub-packages, ~500 LOC
  combined. No changes to the existing autocli API beyond making
  `Complete()` / `Execute()` callable from outside.
- **First consumer ready to land** (`ssql serve`) so we're not
  building infrastructure with no payload.
- **No infra dependencies.** stdlib + `golang.org/x/crypto/ssh` +
  `chzyer/readline`. All MIT/BSD-compatible.

## Open questions

1. **Sub-package or separate module?** `autocli/shell` and
   `autocli/ssh` as sub-packages keeps versioning trivial but
   adds readline + crypto/ssh to *every* autocli user's
   dependency tree. Recommend: keep them as separate Go modules
   in the same repo so users who don't need a shell skip the
   readline dep. (Same pattern as `golang.org/x/exp/...`.)

2. **Default port for SSH.** Cisco-style port `22` requires
   root; safer default is `2222`. Let operators override.

3. **History persistence — per-user or per-server?** Per-user
   (keyed by SSH login name) is more useful and harder. Per-
   server (single shared history) is trivial but leaks one
   user's commands to another. Recommend: per-user, stored in
   `$HISTORY_DIR/$USER`. Default `HISTORY_DIR` is the service's
   state dir.

4. **What's the deprecation story for the bash-completion shim?**
   None — it stays. Servers and CLI binaries coexist in the same
   binary; you can have both interfaces wired up against the
   same command tree.

5. **TLS in front of SSH?** No. SSH is already encrypted. If a
   user wants TLS-tunnelled SSH for some compliance reason,
   that's their reverse-proxy's job.

## See also

- `ssql-serve-proposal.md` — daemon mode for ssql, the natural
  first consumer.
- `cli-tools-design.md` — overall CLI architecture.
- `autocli-improvements.md` — recent autocli work.
- `distributed-ssh-processing.md` — ssh-pushdown design (existing
  use of `golang.org/x/crypto/ssh` patterns in ssql).
- [chzyer/readline](https://github.com/chzyer/readline) — recommended
  readline library.
- [golang.org/x/crypto/ssh](https://pkg.go.dev/golang.org/x/crypto/ssh) — SSH server library.
