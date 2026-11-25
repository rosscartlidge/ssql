# The Public FAQ in a Container

Reference: DFC171
Created: 2026-11-25
Last modified: 2026-11-26

[Back to Index](./README.md)

Ross asked (2026-11-25), after the security review of DFC169 §12.16:
would running the public FAQ instance in a container be useful? Would it
make its security simpler and safer, and how hard would it be?

**In short:**

- **Simpler: yes, clearly.** In an LXD container with no network card,
  the instance can reach exactly two things: Funnel's connections coming
  in, and Ollama going out. It sees no host files, and root inside it is
  an unprivileged uid on the host. Most of the hand-written hardening
  list becomes unnecessary, and so does the open TODO item on the
  service user's network reach.
- **Safer: somewhat, not as much as it looks.** The likeliest way out of
  the bubblewrap jail is a kernel bug, and a container shares the
  kernel. What it adds is a much smaller blast radius for every other
  kind of escape, and a design that is easy to check. Only a VM
  separates the kernel (§6).
- **How hard: half a day to a day.** A probe on the LXD rig (§3) showed
  that every moving part works. One setting is needed
  (`security.nesting=true`), and the FAQ's code needs no change.

The recommendation (§7) is to do it if the public instance is meant to
stay, and to add a small filter in front of Ollama either way.

## 1. Where the public instance stands

Today (DFC169 §12, §12.16) the public instance is a systemd **user**
unit of a dedicated `ssql-faq` user on the host. Its defences are:

1. **The validator.** Only ssql stages joined by `|`.
2. **The jail.** Each pipeline runs in a transient scope (256 tasks,
   1 GiB), under prlimit (30 s of CPU, 64 MiB files), then bubblewrap
   with every namespace unshared: no network, `/usr` read-only, only
   `/work` and `/tmp` writable.
3. **The unit's hardening.** `ProtectSystem=strict`, a private `/tmp`,
   `/home` and `/root` inaccessible, no new privileges, memory and task
   caps.
4. **The `ssql-faq` user itself.** It owns nothing of Ross's.

Two weaknesses are left:

- **After an escape, the service user has the host's whole network:**
  the LAN, the tailnet, the llama bridge on 127.0.0.1:8765, and Ollama's
  unauthenticated API, which can pull and delete models. This is the
  open TODO item from §12.16. `IPAddressDeny=` might close it, but it is
  unverified in a user unit. *Closed 2026-11-26 (DFC169 §12.17): an
  nftables rule keyed on the user's uid lets it reach only the Ollama
  filter (§5).*
- **The hardening is a list.** Each entry was found by testing:
  `ProtectKernelTunables` broke the jail's `/proc` (§12), and on the LAN
  unit, `ProtectSystem=strict` alone left `/home` writable (§12.16). A
  list like that is right only as far as it has been probed.

## 2. What a container changes

The design is an unprivileged LXD container, `ssql-faq-public`, with
**no network card**. It is reached through two LXD proxy devices:

```
Funnel ─▶ tailscaled ─▶ host 127.0.0.1:8485 ─[proxy "web"]─▶ container 127.0.0.1:8485  (ssql-faq -public)
container 127.0.0.1:11434 ─[proxy "ollama", bind=instance]─▶ host 127.0.0.1:11434 (Ollama, GPU)
```

A proxy device is a TCP relay that LXD runs in the host's network
namespace. The container needs no interface for it, so these two ports
are its whole network.

| Concern | Host user unit (now) | Container |
|---|---|---|
| Host files after an escape | Whatever the unit's list leaves visible (`/etc`, `/usr`, `/var`…) | None: its own root filesystem; Ross's files are not there at all |
| Network after an escape | The host's: LAN, tailnet, llama bridge, Ollama | Ollama only (its API, §5) |
| Identity after an escape | `ssql-faq`, a real host user | Root in the container is an unprivileged uid on the host (1000000+) |
| Hardening to get right | ~12 unit directives, each probed | Few: no NIC and two proxies; the container is the boundary |
| Kernel bugs | Exposed | Exposed (same kernel) |
| Recovery | Clean up a host user | Delete the container and rebuild from an image |
| Forwarded client address | Loopback-only trust (DFC169 §12) | The same: the proxy delivers from 127.0.0.1, `X-Forwarded-For` intact (§3) |

The middle rows are why it is *simpler*. "It has two ports and no files
of yours" can be checked with `lxc config show` and one probe. The
current list can only be checked entry by entry.

## 3. The probe (2026-11-25)

The probe was a throwaway copy of `ssql-node1` (Ubuntu 24.04, as on the
host), named `faq-probe-dfc171`. It ran on LXD 5.21.9 LTS and kernel
6.17, and was deleted afterwards. The rig's containers were not touched.

1. **No network card.** The profile's `eth0` was masked with a device
   of type `none`, leaving `lo` only. An outbound request failed.
2. **The jail failed until nesting was turned on.** The jail's exact
   command (systemd-run user scope, prlimit, bwrap with the same flags)
   was run as the unprivileged user `ubuntu`, with lingering enabled.
   It failed with `bwrap: Can't mount proc on /newroot/proc: Operation
   not permitted`. LXD overmounts parts of the container's `/proc` (with
   lxcfs), and the kernel refuses a fresh proc mount under locked
   overmounts. It is the same error the host unit hit with
   `ProtectKernelTunables` (DFC169 §12). With `security.nesting=true`
   the same command worked: `group-by dept` printed the right table,
   `/home` was invisible inside the jail, a TCP connect was refused (the
   jail's own empty network namespace), and process substitution found
   `/dev/fd/63`. Nesting loosens the container's AppArmor profile so
   that nested mounts work; it does not change the unprivileged uid
   mapping. It is how Docker runs inside LXD.
3. **The inbound proxy** ran from host `127.0.0.1:18485` (a test port)
   to container `127.0.0.1:8485`, with an echo server inside. The server
   saw `peer=127.0.0.1` and `X-Forwarded-For: 203.0.113.9` intact. So
   `publicOnly` and `clientKey` would work unchanged.
4. **The outbound proxy** (`bind=instance`) ran from container
   `127.0.0.1:11434` to host Ollama, and `/api/tags` answered. From
   inside, the LAN instance (192.168.4.132:8484) and the llama bridge
   (127.0.0.1:8765) were unreachable.
5. **Cost.** A jailed run took 36 ms in the container and 40 ms on the
   host, the same within noise. The idle container used 88 MiB.
   Ollama's GPU work stays on the host, so the model is not affected.

`bwrap` is not in the base image. The probe pushed the host's binary in;
a real build installs the `bubblewrap` package (§4).

## 4. How it would be built

`scripts/faq/install-public.sh` gains a container mode. Its `stage` step
is unchanged: the same app tree (slim ssql, the faq binary, docsearch,
the docs), built from HEAD.

1. **A base image, built once and again on updates** (`base`):
   - launch `ubuntu:24.04` with a NIC;
   - `apt install bubblewrap`, plus updates;
   - add a `faq` user with lingering;
   - publish the result as the local image `ssql-faq-base`;
   - delete the builder.

   The only network the instance's container ever has is during this
   step, in a separate container.
2. **Each install recreates the container from that image** (`install`):
   - delete `ssql-faq-public`;
   - `lxc init ssql-faq-base`, with `security.nesting=true`, `eth0`
     masked, and the two proxies;
   - add a disk device for the state directory (below);
   - push the app tree and the user unit, then start.

   Nothing survives from one install to the next but the log. A
   compromise is wiped by the next install, and drift can't build up.
3. **The log** lives in a host directory,
   `/var/lib/ssql-faq/state`, mounted into the container with
   `shift=true` (idmapped). The host path does not change, so
   `review.sh` and `status` keep working, and the log survives rebuilds.
   It is the one host path the container can write, and it holds only
   the log.
4. **The unit inside** keeps the current ExecStart. The hardening list
   can shrink to the resource caps (`MemoryMax`, `TasksMax`). The rest
   can stay as cheap defence in depth: it costs nothing, and the probe
   says nothing about whether each entry still works inside a container.
5. **The switch-over.** Stop the host user unit, freeing 127.0.0.1:8485,
   then start the container, whose proxy listens there. Funnel is
   unchanged, because it already points at 127.0.0.1:8485, so there is
   no `tailscale funnel` step. Rolling back is the reverse: stop the
   container, start the host unit.
6. **Checks:**
   - an `install-public.sh probe` that asserts, from inside, that
     there is no NIC, the LAN and the llama bridge are unreachable,
     Ollama answers, and a jailed pipeline runs;
   - `status` reports the container's state as well as `/api/health`.

**Effort.**

- About 150 lines of shell across `base`, `install`, `probe` and
  `status`.
- The base image takes a few minutes to build.
- The migration is one maintenance window, with the rollback above.
- Half a day to a day, including watching each probe fail on a wrong
  setup (a NIC left in, nesting off).
- No change to the FAQ's Go code.

**Running costs:**

- Updates mean rebuilding the base image and reinstalling, instead of
  the host's unattended upgrades. A monthly `base && install` reminder
  in TODO, or a timer, covers it.
- One more layer to debug: `lxc exec`, and the proxies.

## 5. What it does not fix

- **Kernel bugs.** The container and the jail share the host kernel,
  and a user-namespace or mount bug that escapes bubblewrap probably
  escapes the container too. Nesting gives slightly more mount surface.
  The defence stays the same: keep the kernel and bubblewrap updated.
  Only a VM changes this (§6).
- **Ollama's API.** The outbound proxy passes all of it, including
  `/api/pull`, `/api/delete` and `/api/create`. After an escape, someone
  could delete or replace models, or fill the disk with pulls. The fix
  works with or without a container: a small filtering proxy on the
  host. It would allow only `POST /api/chat`, `/api/embed`/`/api/embeddings`
  and `GET /api/tags`, for the two models the FAQ uses, and the
  container's `ollama` proxy would point at it instead of at Ollama.
  *Built 2026-11-26 (DFC169 §12.17), with two corrections. Without a
  container, a filter alone does nothing, because the service user can
  still connect to Ollama directly; it comes with a uid-keyed nftables
  rule. The calls it passes are fewer: `POST /api/chat` (the warm-up
  became an empty chat) and `POST /api/embeddings`.*
- **Abuse of the service itself** (questions, load, prompt injection).
  That is unchanged; the rate limits and the daily cap stay as they are.

## 6. Alternatives

- **An LXD VM** (`lxc launch --vm`) has its own kernel, which is the one
  thing a container cannot give. The FAQ needs no GPU, so a VM is
  possible. But it costs about 1 GiB of RAM and a boot. LXD proxy
  devices for VMs work only in NAT mode, which needs a NIC on a managed
  bridge, so egress would be confined with LXD network ACLs instead of
  having no NIC at all. That is more to get right than §4. Worth it only
  if the threat model grows to someone spending a kernel exploit on a
  laptop's FAQ.
- **A system unit with `User=ssql-faq` and `IPAddressDeny=any` /
  `IPAddressAllow=localhost`.** This closes the network part of the TODO
  item with a few lines. But localhost still includes the llama bridge
  and the LAN instance's loopback port, and the host's filesystem is
  still the starting point. The jail's `systemd-run --user` also needs
  the user's manager, so the system unit would have to start one or
  drop the scope. This is a smaller step, and it leaves the "list"
  problem as it is.
- **Docker or Podman.** They give the same isolation, but bubblewrap
  inside needs `--privileged` or seccomp and AppArmor relaxations, which
  undo much of the gain. LXD already runs on the rig, and the probe
  showed it works with only nesting enabled.

## 7. Recommendation

1. **Add the filtering Ollama proxy (§5) whatever else is decided.** It
   is small, and it removes the worst thing an escape could do today.
   *Done 2026-11-26, with the network rule it needs (DFC169 §12.17).*
2. **If the public instance is to stay, move it into the container
   (§4).** The gain is mostly simplicity: a boundary of two ports and no
   host files, rebuilt on every install, in place of a hardening list
   that is right only as far as it has been probed. The cost is half a
   day to a day, plus a monthly image rebuild.
3. **Don't do the VM** unless the threat model changes.
4. **Keep the kernel and bubblewrap updated** either way. It is the one
   risk none of these options removes, short of the VM.

## Open questions

- **Shifted mount.** Does `shift=true` on the state directory work with
  this kernel and storage pool? The probe did not try it. The fallback
  is `raw.idmap` for one uid, or `lxc file pull` in `review.sh`.
- **Rebuild cadence.** Monthly, or on each Ubuntu security notice for
  the kernel and bubblewrap? Only the userland is in the image; the
  kernel is the host's.
- **The LAN instance.** It runs as Ross, so containing it would protect
  more than the public one. But it is reachable only from the home
  network and the tailnet, and it needs the checkout for docsearch's
  embedding cache. Probably not worth it; a decision for Ross.
