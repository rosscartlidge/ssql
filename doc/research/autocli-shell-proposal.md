# autocli-shell + autocli-ssh Proposal

**Status:** Proposal
**Date:** 2026-05-12
**Target:** autocli vX, ssql v4.44 (first consumer)
**Prerequisites:** stable autocli completion engine (already present)
**Related:** `ssql-serve-proposal.md`, `cli-tools-design.md`, `distributed-ssh-processing.md`

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
    HistoryFile  string             // empty = no history
    State        any                // passed through to Context
    Welcome      string             // banner printed on session start
    Goodbye      string             // banner printed on Ctrl-D
    OnError      func(error)        // optional logging hook
    Stdin        io.Reader          // default os.Stdin
    Stdout       io.Writer          // default os.Stdout
}

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

These start with `:` so they can't collide with user-defined
subcommands.

### Layer 3: autocli/ssh

Wraps `golang.org/x/crypto/ssh` to accept incoming connections,
authenticate them, and hand each one a shell session.

```go
package autoclissh

type Options struct {
    Addr          string                 // ":2222"
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

### Phase A — autocli engine split (~1 day)

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

### Phase B — autocli/shell (~half day)

1. New module `autocli/shell` (sub-package of autocli or a
   separate go module — separate keeps the readline dep
   optional).
2. Implement `Serve(cli, opts)` using `chzyer/readline`.
3. Tokeniser shared with the bash path (existing autocli
   shell-quoting logic).
4. Built-in `:exit / :help / :history` commands.

Tests: spin up a shell with a stdin reader, feed commands, assert
on stdout. Standard readline-loop testing pattern.

### Phase C — autocli/ssh (~1 day)

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

### Phase D — ssql serve integration (~1 day)

1. New `cmd/ssql-serve/` (or add a `serve` subcommand to `ssql`).
2. Implement Server struct + handlers: status, schema, query,
   reload, stats.
3. Hook into autocli/ssh.
4. End-to-end demo: load Parquet, ssh in, run queries against
   the live dataset, observe sub-millisecond query latency
   (no startup cost).

### Phase E — docs/examples (~half day)

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
