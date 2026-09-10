# The Codelab From Scratch in a Mint Container

Reference: DFC126
Created: 2026-09-08
Last modified: 2026-09-10

[Back to Index](./README.md)

## 1. Why

The CLI codelab (DFC125) is gated: every block runs on every change,
through `codelab-run.sh`. That proves the *commands* work against a
freshly built binary on the developer's machine. It proves nothing
about the reader's first ten minutes — the machine with no Go, the
`go install` that switches toolchains, the PATH that does not include
`~/go/bin`, the completion that has never been sourced. Those steps are
the ones the runner marks `codelab: skip — run once by hand`, and "by
hand" on the author's box is not a test: the author's box already has
everything.

Ross's ask (2026-09-08): "spin up a mint lxd container and then do the
codelab from scratch to see what problems you hit." The first run found
a bug every go-install reader on Ubuntu 24.04 would have hit on their
first `generate go -run` (§4). That paid for the exercise; this DFC
makes it repeatable.

## 2. Method

`scripts/codelab-mint.sh [-k] [NAME]`:

1. `lxc launch ubuntu:24.04` — the image a reader is most likely to
   have: Go absent, tmux present (the cloud image ships it), `ubuntu`
   user with passwordless sudo. Nothing from the checkout goes in; the
   binary comes from the module proxy exactly as a reader's would.
2. Setup, **verbatim** from `doc/cli-codelab.md`, as the `ubuntu` user
   in a login shell: `sudo apt-get install -y golang-go`, `go install
   github.com/rosscartlidge/ssql/v4/cmd/ssql@latest`, the `~/.bashrc`
   PATH line, `ssql version`, `ssql codelab`, `tmux -V`, `eval "$(ssql
   -shell-init)"`. Timed, with the module-cache size and the toolchains
   it downloaded.
3. The interactive keys through a **real pty** (`codelab-mint-keys.py`,
   the same discipline as `TestFieldKeybindingPTY`): Tab on a command,
   Tab in a field slot (must insert `Use-Ctrl-O`), Ctrl-O for names and
   values, Alt-h on a flag. Inline mode — no tmux, since there is no
   terminal to draw a popup on; what is being tested is that the keys
   are bound and answer.
4. `./codelab-run.sh` — the runner the data ships with, which fetches
   the codelab tagged with the installed version and runs every block
   against the installed binary (DFC125 amended 2026-09-07).
5. The blocks the runner skips that CAN run unattended: `generate go
   -run`, twice, timed (the first run pays for the module fetch and,
   on an older Go, a toolchain).

`-k` keeps the container for poking (`lxc exec NAME -- su - ubuntu`);
otherwise it is deleted. `make codelab-mint` runs it. *Amended
2026-09-10:* the script also runs the signal-processing codelab
(`./codelab-run.sh signal` — the shipped runner learned the two names,
`cli` and `signal`, fetching the doc for the installed version), and
`-b BINARY` pushes a local build over the installed one after Setup, so
a pre-release binary and the runner it embeds can be exercised before
the tag exists (the install step still tests the published release).

It is not part of `go test` or `make doc-test`: it needs LXD, the
network, and ~5 minutes, and it tests the *published* release rather
than the checkout — run it after a release, or before one with a
locally pushed binary (§3, second run).

## 3. Runs

### 2026-09-08, v4.94.1 (first run, dev binary pushed in afterwards)

| step | result |
|---|---|
| `apt-get install golang-go` | 40 s → go1.22.2 |
| `go install …@latest` | 53 s; "requires go >= 1.26; switching to go1.26.8"; module cache 926 MB, 240 MB of it the toolchain |
| PATH line, `ssql version` | v4.94.1 |
| `ssql codelab` | 14 files, next-steps text |
| tmux | 3.4, preinstalled |
| completion | `complete -F _autocli_complete ssql`, 6 key bindings, Tab/Ctrl-O/Alt-h all answer through the pty |
| `./codelab-run.sh` | **40 passed, 1 FAILED** — §7 `generate go -run` |
| `generate go -run` | `go: download go1.23 for linux/amd64: toolchain not available` |
| `generate sql -run` | `exec: "duckdb": executable file not found in $PATH` (expected; block is skip-marked) |

### 2026-09-08, v4.94.2 (published), fresh container

`scripts/codelab-mint.sh -k`, ~4 minutes end to end, **PASS**:

| step | result |
|---|---|
| `apt-get install golang-go` | go1.22.2 |
| `go install …@latest` | 53 s; switched to go1.26.8; module cache 927 MB |
| `ssql version` | v4.94.2 (build d09c2a8c) |
| `ssql codelab`, tmux, `eval` | 14 files; tmux 3.4; completion registered, 6 key bindings |
| pty keys | Tab→command, Tab in field slot→`Use-Ctrl-O`, Ctrl-O→names and values, Alt-h→flag help: all five ok |
| `./codelab-run.sh` | **41 passed, 0 failed, 9 skipped** (of 50) — the §7 block now passes |
| `generate go -run` ×2 | 2 s, 1 s (the runner's §7 block had already paid the first-run cost; cold it is ~35 s) |


## 4. What the first run found

**`go 1.23` in the temp module.** `generate go -run` writes a temp
module and runs `go build`; its `go` line was the bare minor `go 1.23`.
An older Go honours the line by fetching a toolchain of that name —
and no toolchain is named `go1.23` (releases are `go1.23.0`…), so the
stock Ubuntu 24.04 Go 1.22 failed with "toolchain not available".
Verified in the container: `go 1.23` and `go 1.26` both fail this way;
`go 1.26.0` downloads (11 s); `go 1.26.8` — the toolchain `go install`
had already fetched — costs nothing. Fix (v4.94.2): the line is the
FULL version of the toolchain ssql was built with
(`generatedGoLine()`, from `runtime.Version()`), which for a go-install
user is exactly the cached one. The author's machine never showed it:
its Go is 1.26.1, above any bare minor the file could name.

**Unstated prerequisites.** §7 needs `go`, fetches the ssql module on
first run, and may fetch a toolchain; the Setup section offered the
prebuilt binary as a way to avoid installing Go without saying §7 would
then not work. Both now say so.

**Things that were fine and are worth knowing.** tmux is in the cloud
image. The `eval` line binds six keys in a non-login `bash -i`. The
`Use-Ctrl-O` reminder is what a reader sees on Tab, as the codelab now
says. `go install` also fetches `github.com/rosscartlidge/ssql
v1.22.0` — module-path resolution probing the v1 prefix while locating
`…/v4/cmd/ssql`; benign.

## 5. Lessons

- **A skipped block is an untested block.** The runner's `codelab:
  skip — run once by hand` markers are exactly the steps a reader is
  most likely to fail at, and the only way to test them is a machine
  that has not run them. A container is that machine.
- **The author's toolchain hides version bugs.** Anything that names a
  Go version — go.mod, generated go.mod, docs — is only tested by a Go
  OLDER than the author's. The mint image supplies one.
- **Test the published artifact, not the checkout.** The container
  pulled v4.94.1 from the proxy; a checkout-built binary would have
  passed the same `-run` because it never leaves the author's Go.

## 6. Not covered

tmux popup rendering (needs an interactive terminal), `ssql serve`,
`from ssh`/catalog over SSH (the rig, DFC-less `doc/research/ssh-test-environment.md`,
covers those), macOS and Windows, the prebuilt-binary path.
