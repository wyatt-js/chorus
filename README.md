# chorus

**Synchronized multi-device audio relay for macOS.**

`chorus` is a macOS CLI that captures your system audio and relays it to multiple
**Google Cast**, **AirPlay 2**, and **Bluetooth** devices at once — time-aligned.
macOS can natively stream to multiple AirPlay 2 speakers at once, but only within
that one ecosystem — it can't drive Google Cast at all, talks to just one
Bluetooth device, and gives you no way to mix AirPlay + Cast + Bluetooth or to
delay each independently. `chorus` fans one captured stream out across all three
and aligns them with per-device offsets so they line up instead of echoing.

<img width="541" height="150" alt="Screenshot 2026-06-22 at 11 02 15" src="https://github.com/user-attachments/assets/40fa8dbd-6475-474d-abe7-333078befb52" />

> **Status:** early development. AirPlay 2, Cast, and Bluetooth output all work,
> as does mic-based acoustic auto-calibration (the `s` key) — though the latter is
> not yet verified end-to-end on real multi-room hardware. Manual `--offset` stays
> as an override. See [Roadmap](#roadmap).

## How it works

1. **Capture** — the [audiotee](https://github.com/makeusabrew/audiotee) sidecar
   taps macOS system audio (Core Audio process taps) → raw PCM.
2. **Fan out** — a broadcaster tees the PCM to one goroutine per output, each with
   its own start delay.
3. **Send** — per protocol:
   - **Cast**: host a live WAV stream over HTTP and point the device at it (via
     [go-chromecast](https://github.com/vishen/go-chromecast)).
   - **AirPlay 2**: stream live PCM through the `airplayrelay` Rust sidecar, which
     wraps [airplay2-rs](https://github.com/jburnhams/airplay2-rs) (mDNS discovery
     + HomeKit pairing + timed PCM).
   - **Bluetooth**: render PCM to a paired device through the `chorusaudio` Swift
     helper (IOBluetooth + CoreAudio).
4. **Align** — each output is delayed by a per-device offset so audio reaches your
   ears simultaneously. Set offsets manually (`--offset`) or measure them
   automatically with the mic (the `s` key — see [Sync](#sync-mic-calibration)).

## Install

```sh
curl -fsSL https://raw.githubusercontent.com/wyatt-js/chorus/main/install.sh | bash
```

This installs `chorus`, `audiotee`, `chorusaudio`, and `airplayrelay` to
`/usr/local/bin` (or `~/.local/bin` if that isn't writable). Override with
`CHORUS_BIN_DIR=...` or pin a release with `CHORUS_VERSION=v0.1.0`.

**Requirements:** macOS 14.2+. The system-audio capture permission is granted on
first run (use Terminal.app if the prompt doesn't appear).

<details>
<summary>Build from source instead</summary>

Needs Go 1.25+, a Swift toolchain (Xcode CLI tools), and a Rust toolchain
([rustup.rs](https://rustup.rs), for the AirPlay 2 sidecar).

```sh
git clone https://github.com/wyatt-js/chorus
cd chorus
make deps     # builds sidecars: audiotee (capture) + chorusaudio (BT) + airplayrelay (AirPlay 2)
make build    # builds ./bin/chorus (pure Go, CGO_ENABLED=0)
make test     # optional
```

Maintainers cut a release by pushing a version tag (`git tag v0.1.0 && git push
--tags`); the `release` GitHub Actions workflow builds the universal bundle on a
macOS runner and publishes it, which is what `install.sh` downloads.
</details>

## Usage

<img width="605" height="279" alt="Screenshot 2026-06-22 at 12 54 56" src="https://github.com/user-attachments/assets/ee1accc6-9111-4b62-bc62-b91197dc7862" />

```sh
chorus play                                       # interactive picker (multi-select)
chorus play --cast "The Frame"                    # one TV
chorus play --airplay "HomePod" --bt "HW-S700D"   # an AirPlay speaker + a soundbar
chorus play --airplay "HomePod" --bt "HW-S700D" --offset HW-S700D=2s   # delay the soundbar
```

**Interactive picker** (`chorus play` with no device flags): scans for Cast,
AirPlay, and paired Bluetooth devices and lets you multi-select across all three
(↑/↓ move · enter/space toggle · select **Submit** + enter to confirm · q
cancel). Selecting a disconnected Bluetooth device connects it on the spot. While
playing, single keys stay live: **`m`** reopens the menu to add/drop devices
(unchanged ones keep playing), **`s`** runs mic sync (below), **`q`** quits.

**Flag form** (`--cast`/`--airplay`/`--bt`, each a repeatable name substring):
streams to a fixed set until Ctrl-C, no in-session menu. `--offset name=dur`
delays one device relative to the others. `--pin 1234` supplies a first-time
pairing PIN (saved afterward). Pair Bluetooth devices in macOS Settings first.

### Sync (mic calibration)

Press **`s`** while playing to measure each device's latency acoustically and
align them automatically — no manual `--offset` math. Because the mic is the
Mac's built-in input and your speakers are in different rooms, sync is
**user-paced**: it lists your active devices, you carry the laptop near one and
press its number, and chorus plays a short test chirp on *that device only*,
records it, and measures how long the sound took to arrive. Do this for each
device (in any order), then press **`a`** to align — chorus delays the faster
devices to match the slowest. **`r`** resets measurements, **`q`** goes back.

If a measurement reports it couldn't hear the tone, move the Mac closer to that
speaker and retry. Calibration briefly mutes the other devices while it measures.

## Roadmap

| Phase | Goal | Status |
|-------|------|--------|
| 0 | Capture → single output | **done** |
| 1 | Fan out to Cast + AirPlay 2 + Bluetooth; manual `--offset` | **done** |
| 2 | Mic auto-calibration — chirp + FFT → automatic per-device delay | **done**, hardware validation pending |
| 3 | Periodic re-sync against clock drift | stretch |

Phase 2 computes the offsets automatically (`--offset` stays as a manual
override): it plays a chirp on each device, records it on the Mac's mic, and
cross-correlates to recover the per-device latency. The plumbing it needed —
runtime offset retuning with no playback gap — also sets up Phase 3. **The hard
part that remains is clock drift** — a one-time offset is only correct at the
instant it's measured; independent device clocks drift over minutes, so long
sessions need periodic recalibration (Phase 3). The target metric is residual
inter-device offset: uncorrected ~250ms → **<15ms** after calibration.

## Architecture

```
audiotee ─► capture ─► broadcaster ─┬─► Cast      (live WAV over HTTP → go-chromecast)
                                    ├─► AirPlay   (airplayrelay Rust sidecar → airplay2-rs)
                                    └─► Bluetooth (chorusaudio Swift helper → CoreAudio)
```

The main binary is **pure Go** (CGO_ENABLED=0); platform and protocol access live
in separate sidecar processes — Swift for capture/CoreAudio (`audiotee`,
`chorusaudio`), Rust for AirPlay 2 (`airplayrelay`). Audio is 48kHz/16-bit/
stereo PCM throughout — macOS's native rate, so nothing resamples.

Sync (the `s` key) adds a measurement path on top: the broadcaster plays a chirp
on one output and silences the rest, `chorusaudio record` captures the Mac's mic,
and the pure-Go `internal/calibrate` package (hand-rolled FFT + matched-filter
cross-correlation) recovers each device's latency, which retunes the offsets live
with no playback gap.

## Vendored dependencies

Two upstream projects live under `third_party/` as **plain vendored files, not git
submodules** — so their local patches are tracked directly in this repo and a
plain `git clone` builds without any submodule init. A nested `third_party/go.mod`
keeps them out of the parent Go module, so `go build ./...` ignores them.

- **`third_party/airplay2-rs`** ([jburnhams/airplay2-rs](https://github.com/jburnhams/airplay2-rs),
  upstream rev `527884f`) — the AirPlay 2 sender, consumed by `airplayrelay` via a
  `path` dependency in `native/airplayrelay/Cargo.toml`. It's early-stage (v0.1)
  and doesn't work out of the box against strict modern receivers (Samsung TVs,
  HomePods), so it's patched locally:
  - **`OPTIONS` deferred until after authentication** — strict receivers `403` a
    cleartext `OPTIONS`, so it's sent over the encrypted channel instead.
  - **Fuller `SETUP` #2** — the buffered audio stream carries the full `streams`
    field set (`streamConnectionID` / `supportsDynamicStreamID` / `isMedia` / `sr`
    / `audioMode`) and drops the RAOP `Transport` header.
  - **ALAC codec** for the audio stream.
  - **PTP grandmaster-id fallback** — `SETRATEANCHORTIME`'s `networkTimeTimelineID`
    falls back to the PTP grandmaster id captured from the master's Announce, since
    Samsung omits `timingPeerInfo.ClockID`.
  - **No zero-pad PCM** (`streaming/pcm.rs`) — a short packet is completed by
    reading more from the source rather than splicing in silence; the old zero-pad
    produced an audible pop every few seconds as the device and local clocks drift.

  With these, the full handshake + live audio is confirmed end-to-end on a Samsung
  Neo QLED; HomePod's PIN/encryption path is still untested.

- **`third_party/audiotee`** ([makeusabrew/audiotee](https://github.com/makeusabrew/audiotee))
  — the system-audio capture sidecar (Core Audio process taps). Vendored
  unmodified; pinned here so the capture path builds reproducibly.

## Caveats

- **AirPlay 2 needs a patched dependency.** Sending goes through airplay2-rs
  (early-stage, v0.1), which doesn't work out of the box against strict modern
  receivers — so it's vendored and locally patched. See
  [Vendored dependencies](#vendored-dependencies) for the full list of patches.
- **Audio sync is still being hardened.** Mic auto-calibration (the `s` key) is
  implemented but not yet validated end-to-end on real multi-room hardware; the
  10ms offset granularity caps the best-case residual at ±5ms. Outputs also run on
  independent clocks, so a static offset drifts over long sessions (Phase 3).
- **Cast buffers several seconds** — align other outputs *to* it. The live
  WAV-over-HTTP path still wants more real-hardware validation (an ffmpeg→FLAC
  fallback is the planned alternative).
- **Bluetooth pairing is a manual macOS step** — pair once in System Settings,
  then the device appears in the picker; selecting a disconnected one connects it.

## License

MIT
