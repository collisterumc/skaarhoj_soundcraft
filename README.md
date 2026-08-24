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

## Installation

Build from source. Go 1.25 or newer; a plain `go build` produces a binary for the machine
you are on:

```sh
go build -o skaarhoj_soundcraft .
```

The Blue Pill is arm64 and runs the binary without a C library, so cross-compile it
statically:

```sh
GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -o skaarhoj_soundcraft .
```

`file` should report `ELF 64-bit LSB executable, ARM aarch64, … statically linked`.

There is no installable package yet: building an `.ipks` for skaarOS needs information only
SKAARHOJ can supply, so on-device install is on hold (TODO.md milestone 10). In the
meantime the core runs on any host Reactor can reach, which is how it has been tested.

### Attaching the core to Reactor

Run the binary anywhere on the network. It serves IBeam gRPC on port **8502**. In Reactor,
add it by hand — a core that is not installed as a package announces nothing, so device
discovery will not find it:

> Add Device → Add Manually → **Remote or Unknown** → enter the host's IP address (port
> 8502 is implied) → Confirm

Reactor then pulls the whole parameter catalog, dimensions and labels included, straight
from the core over gRPC. The device shows as "configured by remote core".

One consequence of not being an installed package: Reactor's binding *authoring* helpers
(behavior recommendations, parameter info) resolve against an installed core's profile and
will report the core as not found. Bindings still work — they simply have to be written
rather than suggested.

## Configuration

On first start the core writes `skaarhoj_soundcraft.toml` next to itself, **in the process's
working directory**, and populates it with one inactive example device:

```toml
[[Devices]]
  Active = true
  Name = "Ui16"
  DeviceID = 1
  ModelID = 2
  Description = "Front of house"
  IP = "192.168.1.4"
```

| Field | Meaning |
|---|---|
| `Active` | The core only connects to a device when this is `true`. It defaults to `false`, so a fresh config connects to nothing. |
| `DeviceID` | Distinguishes devices in parameter references. The first device is `1`. |
| `ModelID` | `1` = Ui12, `2` = Ui16, `3` = Ui24R. Sets the channel count and gates the Ui24R-only multitrack parameters. |
| `IP` | The mixer's address. The core speaks plain WebSocket on port 80. |

Add a `[[Devices]]` block per mixer. Each gets its own `connection` parameter and its own
link, and a write to one never reaches another.

Reactor can also edit this configuration through the core's config schema once attached.

The core reconnects forever with a 2 s backoff, because mixer power-cycling is normal
operation in the installation this was built for. A power cut is a silent death — no TCP
FIN — so the link is declared dead by a read deadline about 5 s after the mixer stops
talking. While disconnected, writes are **discarded rather than queued**: nothing a
panel operator pressed against a dark mixer fires when it comes back.

## Parameters

Registered for every model unless noted. `<ch>` is a 1-based dimension index covering the
model's input channels followed by its line-in channels — 14 on a Ui16.

| Parameter | Type | Direction | Notes |
|---|---|---|---|
| `channel_mute` | Binary | read/write | Mute per channel |
| `channel_fader` | Floating 0–100 | read/write | Main-mix level per channel; the wire value is linear 0.0–1.0 |
| `channel_fader_db` | String | read-only | The same level as a dB reading, e.g. `-53.4 dB` |
| `channel_name` | String | read-only | The channel name as set on the mixer |
| `master_fader` | Floating 0–100 | read/write | Master level. The mixer exposes no master mute, so there is no such parameter |
| `master_fader_db` | String | read-only | Master level as a dB reading |
| `record_2track` | Binary | read/write | USB 2-track recording toggle, showing actual state |
| `record_busy` | Binary | read-only | Mixer-reported busy flag. Pulses for about 76 ms on stop and never fires on start, so it is not worth putting on a display |
| `snapshot_up` / `snapshot_down` | NoValue, Oneshot | write | Step to the adjacent snapshot in the current show, wrapping at both ends |
| `current_snapshot` | String | read-only | Name of the loaded snapshot |
| `connection` | ConnectionState | read-only | Mixer link state. Reactor blocks output while it is down |
| `record_multitrack`, `multitrack_busy`, `multitrack_time` | Binary / Binary / String | mixed | **Ui24R only.** Implemented from spec and untested — no Ui24R hardware |

The read-only string parameters exist to be shown next to the control they describe. On a
Blue Pill panel they do not need their own key: put a braced reference in a display field,
for example `{DC:skaarhoj_soundcraft/1/channel_fader_db/1}` in `Textline1`. Reactor does not
apply the `RecommendedParamFor*` hints on its own.

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
