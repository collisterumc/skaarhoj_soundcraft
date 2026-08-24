# skaarhoj_soundcraft

A SKAARHOJ **device core** that speaks the Soundcraft Ui-series WebSocket protocol, allowing
SKAARHOJ Blue Pill controllers (Quick Bar, Air Fly Pro, custom panels, etc.) to control a
Soundcraft Ui12 / Ui16 / Ui24R digital mixer with full bidirectional feedback.

> **Status:** The core is written and every v1 control has been verified against a real
> Ui16, both directly and through Reactor on a Blue Pill. What remains is hardening and
> packaging: the `.ipks` build and on-device install are blocked pending SKAARHOJ guidance
> on how a third party builds a package (see [TODO.md](TODO.md) milestone 10). Until then
> the core runs on any host Reactor can reach.
> See [TODO.md](TODO.md) for the roadmap and [IMPLEMENTATION.md](IMPLEMENTATION.md) for design details.

## What this core will do

The core is a Go binary built on the SKAARHOJ IBeam framework
([ibeam-corelib-go](https://github.com/SKAARHOJ/ibeam-corelib-go)). It runs on the Blue Pill
(or any host reachable by Reactor), connects to one or more Soundcraft Ui mixers over
WebSocket, and exposes mixer functions as IBeam parameters that Reactor can bind to panel
hardware (buttons, faders, encoders, displays).

### Planned controls (v1 scope)

| Function | Control | Feedback |
|---|---|---|
| **Snapshot restore** | Step up/down through the snapshots of the current show (previous/next triggers) | Currently loaded show/snapshot name tracked live (`var.currentShow` / `var.currentSnapshot`) for display |
| **Channel mute** | Mute / unmute any input or line-in channel (toggle) | Live mute state per channel, updated even when changed from another controller |
| **Channel fader level** | Set the main-mix fader level of any input or line-in channel; master fader included | Live fader position per channel, with dB display value |
| **USB 2-track recording** | Recording toggle (Ui12/Ui16/Ui24R) | Recording active + busy state shown on the button |
| **Multitrack recording** *(Ui24R only, stretch)* | Multitrack recording toggle | Recording state, busy flag, elapsed time |

### Feedback model

Every control is state-tracked. The Soundcraft mixer pushes `SETD`/`SETS` state messages to
all connected WebSocket clients whenever anything changes — whether the change came from
this core, the mixer's own web UI, or another controller. The core ingests that stream and
reports *current* values back to Reactor, so panel LEDs, motorized faders, and displays stay
accurate at all times. Assumed-state / confirmed-state semantics follow the standard IBeam
target/current model.

### Supported mixers

| Model | Inputs | Notes |
|---|---|---|
| Soundcraft Ui12 | 8 | No multitrack recording |
| Soundcraft Ui16 | 12 | No multitrack recording; **integration-test hardware for this project** |
| Soundcraft Ui24R | 24 | Multitrack recording, VCAs, matrix mode; supported from spec, untested on hardware |

## Building

Go 1.25 or newer. A plain `go build` produces a binary for the machine you are on:

```sh
go build ./...
```

The Blue Pill is arm64 and runs the binary without a C library, so cross-compile it
statically:

```sh
GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -o skaarhoj_soundcraft .
```

`file` should report `ELF 64-bit LSB executable, ARM aarch64, … statically linked`.

## Repository layout

| Path | Purpose |
|---|---|
| [README.md](README.md) | This file — project goals and overview |
| [TODO.md](TODO.md) | Human-readable milestone checklists with per-milestone success gates |
| [IMPLEMENTATION.md](IMPLEMENTATION.md) | Agent/developer-oriented design decisions, protocol reference, open questions |
| `reference/` | Local clones of upstream reference material (git-ignored, see below) |

### Reference material (`reference/`, not committed)

Everything under `reference/` is pulled/provided locally for research and is **excluded from
version control** (see [.gitignore](.gitignore)) because the SKAARHOJ repositories carry a
Commons Clause license and the manuals and device images are SKAARHOJ copyrighted material.

Because it is not committed, the directory must be **rebuilt from scratch** in any fresh
container. The snapshot this project was researched against (captured **2026-07-20**) is:

| `reference/` path | Source | Pinned ref / provenance |
|---|---|---|
| `soundcraft-ui/` | [fmalcher/soundcraft-ui](https://github.com/fmalcher/soundcraft-ui) (MIT) — de-facto Soundcraft Ui WebSocket protocol docs | commit `93db985` |
| `core-skaarhoj-template/` | [SKAARHOJ/core-skaarhoj-template](https://github.com/SKAARHOJ/core-skaarhoj-template) — official Go device-core template | commit `82f0913` |
| `ibeam-core-proto/` | [SKAARHOJ/ibeam-core-proto](https://github.com/SKAARHOJ/ibeam-core-proto) — gRPC/protobuf contract | commit `1791239` |
| `ibeam-corelib-go/` | [SKAARHOJ/ibeam-corelib-go](https://github.com/SKAARHOJ/ibeam-corelib-go) — IBeam server/parameter framework | commit `a0219ce` |
| `ibeam-lib-config/` | [SKAARHOJ/ibeam-lib-config](https://github.com/SKAARHOJ/ibeam-lib-config) — config schema library | commit `85f0ad1` |
| `manuals/*.pdf` | [SKAARHOJ/Support](https://github.com/SKAARHOJ/Support) `Manuals/` (master) — Reactor + Blue Pill + SKAARHOJ manuals | fetched from `master`; verify via checksums below |
| `site.md` | Provided by the project owner — endpoints and credentials for the owner's test installation | obtain from the owner; its contents are never committed or quoted in public docs |
| `backups/` | Configuration backups of the owner's SKAARHOJ controller, taken before changes | created on demand; never committed |
