# Browser-Based Linux for ssql Playground

**Date:** 2026-03-21
**Status:** Research
**Goal:** Evaluate options for running real bash (with pipes, process substitution, tab completion) in the browser

## Context

The current WASM playground runs the ssql CLI directly in WebAssembly with a JS pipeline orchestrator simulating bash. This works well for demos but can't provide tab completion or real shell features. Running actual Linux/bash in the browser would give the full experience.

## Option 1: WebVM (CheerpX)

**Website:** https://webvm.io/
**GitHub:** https://github.com/leaningtech/webvm
**License:** CheerpX is proprietary (free for personal/educational use, commercial license required). The WebVM frontend is Apache 2.0.

**What it is:** A full x86 Debian environment running entirely client-side in the browser. CheerpX is a JIT compiler that translates x86 instructions to WebAssembly on the fly, plus a Linux syscall emulator and virtual block filesystem.

**What you get:**
- Real bash with pipes, `<(...)`, environment variables, everything
- Tab completion works (real readline)
- `apt install` works (real Debian package manager)
- Networking via Tailscale WebSocket bridge
- Persistent filesystem (IndexedDB-backed)
- Desktop environment support (Xorg, full GUI possible)

**How to use for ssql:**
1. Fork the WebVM GitHub template
2. Build a custom Debian image with ssql pre-installed and completion configured
3. Deploy to GitHub Pages (they provide a GitHub Actions workflow)
4. Users land on a terminal with ssql ready to use

**Pros:**
- Most polished and fastest of the three options (x86→WASM JIT, not emulation)
- Pre-built GitHub Pages deployment workflow
- Large community, active development
- Networking means SSH demos could potentially work

**Cons:**
- CheerpX is proprietary — free for personal use but commercial license needed if ssql itself is used commercially
- ~50-100MB initial download (Debian base image)
- 10-20 second boot time
- No purpose-built UI (just a terminal)
- License terms may conflict with open-source goals

**Effort:** Low (fork template, customize image, deploy). ~1 day.

## Option 2: v86

**Website:** https://copy.sh/v86/
**GitHub:** https://github.com/copy/v86
**License:** BSD 2-Clause (fully open source)

**What it is:** An x86 PC emulator with x86-to-WASM JIT. Emulates a full PC including BIOS, hardware, etc. Runs real operating systems — Alpine Linux, Arch Linux, FreeBSD, etc.

**What you get:**
- Real Linux kernel, real bash, real everything
- Fully open source, no license concerns
- Can build custom images from Dockerfiles (Alpine)
- Embeddable in any webpage via libv86.js

**How to use for ssql:**
1. Build a custom Alpine Linux image with ssql, bash, and completion pre-configured
2. Embed v86 in a page with the custom image
3. Host on GitHub Pages (images are typically 10-50MB)

**Pros:**
- Fully open source (BSD license)
- Well-established project (10+ years)
- Custom image building is documented
- Lightweight base (Alpine ~10MB)

**Cons:**
- Slower than CheerpX (emulation vs JIT)
- Limited to i386 (32-bit) — need to cross-compile ssql for linux/386
- Boot time 15-30 seconds
- Less polished than WebVM
- No built-in networking
- Memory hungry (emulating full PC hardware)

**Effort:** Medium. Build custom Alpine image, cross-compile ssql for linux/386, configure bash completion, embed v86. ~2-3 days.

**Cross-compilation note:** ssql would need `GOARCH=386 GOOS=linux go build`. This should work since ssql is pure Go (no CGO), but needs testing — the Arrow/Parquet libraries may have issues on 32-bit.

## Option 3: container2wasm

**Website:** https://github.com/nicjansma/container2wasm (fork) / https://github.com/nicjansma/container2wasm
**Original:** https://github.com/nicjansma/container2wasm
**License:** Apache 2.0

**What it is:** Converts Docker container images to WASM images that run in the browser. Uses a CPU emulator (riscv or x86) compiled to WASM, boots Linux kernel, runs the container's filesystem.

**How to use for ssql:**
```dockerfile
FROM alpine:latest
RUN apk add bash
COPY ssql /usr/local/bin/ssql
RUN echo 'eval "$(ssql -completion-script)"' >> /root/.bashrc
```
Convert with `container2wasm` tool, serve the output.

**Pros:**
- Familiar Docker workflow
- Fully open source
- Any Docker image works in theory
- Supports both x86 and RISC-V emulation

**Cons:**
- Slower than both CheerpX and v86 (double emulation layer)
- Large output files (100-200MB)
- Less mature than v86 or WebVM
- Boot time 30-60 seconds
- Limited documentation

**Effort:** Medium. Write Dockerfile, convert, host. ~2 days. But debugging performance/compatibility issues could take longer.

## Option 4: Keep Both (Recommended)

The current WASM playground and a real-Linux option serve different audiences:

| Audience | Best option |
|---|---|
| First-time visitor, wants to see what ssql does | Current WASM playground (fast, clean UI, guided examples) |
| Developer evaluating ssql, wants to try real commands | WebVM or v86 (real bash, tab completion, pipes) |
| Conference demo / presentation | Current WASM playground (predictable, buttons, no boot time) |

**Recommendation:** Keep the current playground as the primary "try it" experience. Add a "Full Terminal" link that opens a WebVM/v86 instance for users who want the real thing.

## Comparison

| Feature | Current playground | WebVM (CheerpX) | v86 | container2wasm |
|---|---|---|---|---|
| License | MIT (ours) | Proprietary | BSD | Apache 2.0 |
| Load time | ~5s (12MB) | ~15s (50-100MB) | ~20s (10-50MB) | ~40s (100-200MB) |
| Real bash | No (JS simulation) | Yes | Yes | Yes |
| Tab completion | No | Yes | Yes | Yes |
| Pipes / `<(...)` | Simulated | Real | Real | Real |
| SSH networking | No | Yes (Tailscale) | No | No |
| Custom UI | Yes (buttons, examples) | No (terminal only) | No (terminal only) | No (terminal only) |
| 64-bit support | N/A (WASM) | Yes (x86_64 via CheerpX) | No (i386 only) | Depends on emulator |
| Open source | Yes | Frontend only | Yes | Yes |
| Maintenance | Low | Low (fork template) | Medium (custom image) | Medium (Docker + convert) |

## Next Steps

1. **Quick spike:** Fork WebVM template, build a custom image with ssql, test locally. ~2 hours to determine if it's viable.
2. **If CheerpX license is OK:** Deploy alongside current playground. Add "Full Terminal" button.
3. **If license is a concern:** Try v86 with Alpine. Test ssql cross-compiled to linux/386.
4. **Either way:** Keep current WASM playground as the primary demo — it's faster, cleaner, and purpose-built.
