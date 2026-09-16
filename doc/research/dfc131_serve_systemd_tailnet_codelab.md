# A Read-Only Codelab Workspace on the Tailnet, as a systemd Service

Reference: DFC131
Created: 2026-09-16
Last modified: 2026-09-16

[Back to Index](./README.md)

Status: **parked design — nothing built.** Ross, 2026-09-16, after asking
how to start serve on the Tailscale address: "what do you think of having
this setup via systemd in readonly mode with the codelab data?" — "write
a doc to store for later". This records the design and the one fact it
has to be built around.

## 1. What it is

An always-on `ssql serve` on the machine's tailnet address, serving the
embedded codelab fixtures read-only, started by systemd and hardened by
it. Anyone on the tailnet, phone included, opens the workspace at
`http://<tailnet-ip>:8080` and gets the codelab's data with the grid,
the pipeline bar and completion — no install, no terminal. It is a demo
box and a GopherCon prop ("`systemctl enable`, then open it on your
phone"), not a replacement for codelab §4, which still has readers start
serve themselves.

The pieces already exist: `-listen-http tailscale:8080` resolves the
tailnet address (W34 addendum: IPv4 preferred, `fd7a:115c:a1e0::/48`
fallback, tokenless allowed on a tailnet bind with a loud notice
naming shared nodes); `-readonly` rejects pipelines that write (`tee`,
`to FMT FILE`, `generate -run/-build`); `ssql codelab DIR` writes the
fixtures that ship inside the binary.

## 2. The fact to design around

**`-dir` is a working directory, not a sandbox** — its own help says
"NOT a sandbox — see -token". A pipeline sent to serve can `from
/etc/passwd`, and `from ssh HOST` runs as the service user with that
user's keys. `-readonly` closes the write side only. The confinement
therefore has to come from systemd: an ephemeral unprivileged user, a
read-only filesystem, no home directories, and sockets limited to the
tailnet.

## 3. The unit

```ini
# /etc/systemd/system/ssql-codelab.service
[Unit]
Description=ssql codelab workspace on the tailnet (read-only)
After=network-online.target tailscaled.service
Wants=network-online.target tailscaled.service

[Service]
DynamicUser=yes
RuntimeDirectory=ssql-codelab
ExecStartPre=/usr/bin/ssql codelab /run/ssql-codelab
ExecStart=/usr/bin/ssql serve -listen-http tailscale:8080 -dir /run/ssql-codelab -readonly
Restart=on-failure
RestartSec=5
ProtectSystem=strict
ProtectHome=yes
PrivateTmp=yes
NoNewPrivileges=yes
IPAddressDeny=any
IPAddressAllow=100.64.0.0/10 fd7a:115c:a1e0::/48 localhost
MemoryMax=2G
CPUQuota=200%

[Install]
WantedBy=multi-user.target
```

Line by line:

- `ExecStartPre=ssql codelab /run/ssql-codelab` writes the embedded
  fixtures into the runtime directory on every start, so the data always
  matches the installed binary and nothing is checked into the box.
  `RuntimeDirectory` creates and owns it; it is the one writable path.
- `DynamicUser=yes` — an ephemeral, unprivileged account: no SSH keys,
  no home, no persistence. `from ssh` from inside a served pipeline has
  nothing to authenticate with.
- `ProtectSystem=strict` + `ProtectHome=yes` + `PrivateTmp=yes` +
  `NoNewPrivileges=yes` — the filesystem is read-only and home
  directories are invisible. This is what turns "not a sandbox" into
  one.
- `IPAddressDeny=any` with the tailnet CGNAT range, the Tailscale IPv6
  prefix and localhost allowed — both the listener and any outbound
  connection stay on the tailnet. Shared nodes are inside that boundary,
  which is exactly what the tokenless notice warns about; add `-token`
  (and thread it through share links, which already carry `?token=`) if
  the tailnet has nodes that should not see the workspace.
- `After=tailscaled.service` + `Restart=on-failure` + `RestartSec=5` —
  the boot race: if `tailscale0` has no address yet, `-listen-http
  tailscale:` fails loudly and systemd retries five seconds later until
  it does. (Check that the resolver's failure is an exit, not a bind to
  nothing — §5.)
- `MemoryMax` / `CPUQuota` — tailnet-only does not mean
  resource-limited; a heavy pipeline can burn a core. Sizes are a guess
  for a shared box; a dedicated one can drop them.

## 4. What to ship

1. `contrib/systemd/ssql-codelab.service` — the unit above, with a
   header comment pointing here.
2. The deb installs it under `/usr/lib/systemd/system/` **disabled**
   (`systemctl enable --now ssql-codelab` is the opt-in); go-install
   users copy it from the repo. Upgrading the deb restarts an enabled
   unit (`dh_installsystemd` default), which also re-runs `ssql codelab`
   so the data follows the binary.
3. A short page beside `tmux-for-ssql.md`: "Serve the codelab on your
   tailnet" — install, enable, open on a phone, what the tokenless
   notice means, when to add `-token`, how to serve your own data
   instead (change `-dir`, drop `ExecStartPre`, keep `-readonly` unless
   writes are wanted).
4. A smoke test on this machine: `systemctl start`, `curl
   http://<tailnet-ip>:8080/api/health`, one pipeline through
   `/api/execute`, then a `tee` that must be refused by `-readonly` and a
   `from /etc/hostname` that must be refused by the filesystem
   protection — the second one is the check that the hardening, not
   ssql, is doing the confining. Record the result in the page.
5. A codelab §4 sentence: "if the box serving the codelab is on your
   tailnet, this is already running — see the page."

Size: half a day, most of it the page and the smoke test.

## 5. Open questions

- Does `-listen-http tailscale:PORT` exit non-zero when no tailnet
  address exists (needed for `Restart=on-failure` to do its job), or
  does it fall through to something else? Verify before shipping.
- `IPAddressAllow` applies to the service's sockets; confirm the HTTP
  listener bound on the tailnet address is accepted and that the
  workspace's own outbound calls (none expected) are unaffected.
- Whether `ssql serve` should learn `-dir` confinement itself one day
  (refuse paths outside `-dir`). Out of scope here; systemd does it
  better and the help text is honest.

## 6. References

- `journal/2026-W34.md` — the tailscale serve addendum (resolution,
  tokenless rule, mobile layout).
- `doc/research/ssql-serve-proposal.md` — serve's design; §"Or with
  Tailscale" is this idea's origin.
- `doc/cli-codelab-serve.md` — the SSH operator console runbook.
- `doc/tmux-for-ssql.md` — the shape of the page to write.
