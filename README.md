# chorus

**Synchronized multi-device audio relay with mic-based acoustic calibration.**

`chorus` is a macOS CLI that captures your system audio and relays it to
multiple **Google Cast**, **AirPlay 2**, and **Bluetooth** devices at once —
time-aligned. Different protocols introduce wildly different latencies (Cast
buffers seconds; AirPlay buffers ~2s; Bluetooth A2DP ~150–300ms), so playing the
same stream to all of them produces an audible echo. `chorus` delays each
output independently so they line up — and (later) measures the right delays
automatically with a mic and a chirp.

> Status: early development. See [Roadmap](#roadmap) for what works today.

## Why

Play the same audio through your TV and a soundbar (or a speaker in another room)
without the two echoing against each other. macOS can natively send to one
AirPlay/Cast target at a time; `chorus` fans one captured stream out to several
heterogeneous devices and aligns them.

## How it works

1. **Capture** — the [audiotee](https://github.com/makeusabrew/audiotee) sidecar
   taps macOS system audio (Core Audio process taps) → raw PCM.
2. **Fan out** — a broadcaster tees the PCM to one goroutine per output, each with
   its own start delay.
3. **Send** — per output protocol:
   - **Cast**: host a live WAV stream over HTTP and point the device's default
     media receiver at it (via [go-chromecast](https://github.com/vishen/go-chromecast)).
   - **AirPlay 2**: stream live PCM to the receiver through the `airplayrelay`
     Rust sidecar, which wraps [airplay2-rs](https://github.com/jburnhams/airplay2-rs)
     (mDNS discovery + HomeKit pairing + timed PCM).
   - **Bluetooth**: render PCM to the paired CoreAudio output device through a
     small Swift helper (`chorusaudio`).
4. **Align** — each output's stream is delayed by a per-device offset (manual
   `--offset` today; mic auto-calibration later) so audio reaches your ears
   from every device simultaneously.

> **AirPlay note:** this targets modern **AirPlay 2** receivers (HomePod, Apple
> TV, current third-party speakers) via the pure-Rust airplay2-rs crate. The old
> classic-RAOP path (cgo + libraop) has been removed. airplay2-rs is early-stage,
> so the streaming/pairing path is still being hardened on real hardware.

## Install

> Not yet published. For now, build from source.

**Requirements**

- macOS 14.2+ (14.4+ recommended) — required for Core Audio process taps
- Apple Silicon
- Go 1.25+, a Swift toolchain, and Xcode command-line tools
- A Rust toolchain (`cargo`, from [rustup.rs](https://rustup.rs)) — for the
  AirPlay 2 sidecar
- The system audio capture permission (`NSAudioCaptureUsageDescription`), granted
  on first run; microphone access later (for calibration)

```sh
git clone --recurse-submodules https://github.com/<user>/chorus
cd chorus

make deps     # builds sidecars: audiotee (capture) + chorusaudio (BT) + airplayrelay (AirPlay 2)
make build    # builds ./bin/chorus (pure Go)
make test     # optional: unit tests
```

`make deps` builds the audiotee submodule under `third_party/`, the
`chorusaudio` Swift helper, and the `airplayrelay` Rust sidecar under
`native/`. The main binary is pure Go (CGO_ENABLED=0). If you cloned without
`--recurse-submodules`, run `git submodule update --init --recursive` first.

## Usage

```sh
chorus play                                      # interactive picker: multi-select Cast/AirPlay/Bluetooth
chorus play --cast "The Frame"                   # cast system audio to a TV
chorus play --airplay "HomePod"                  # stream to an AirPlay 2 receiver
chorus play --airplay "HomePod" --bt "HW-S700D"  # fan out to an AirPlay speaker and a soundbar
chorus play --airplay "HomePod" --bt "HW-S700D" --offset HW-S700D=2s  # delay the soundbar
```

Run `chorus play` with no device flags in a terminal to open an interactive
picker that scans for and lets you multi-select Cast, AirPlay, and Bluetooth
devices (↑/↓ move · space toggle · enter confirm · q cancel). Otherwise,
`--cast`/`--airplay`/`--bt` take a name substring and are repeatable. `--offset
name=dur` delays one device relative to the others (manual alignment until
calibration lands). A receiver that requires a PIN the first time accepts
`--pin 1234`; the pairing is then saved for next time. Pair a Bluetooth device in
macOS Settings first so it shows in the picker. Press Ctrl-C to stop.

## Roadmap

| Phase | Goal | Status |
|-------|------|--------|
| 0 | Capture → single output (prove the pipe) | **done** |
| 1 | Multiple simultaneous outputs (Cast + AirPlay 2 + Bluetooth); fan-out; manual `--offset` | **done (pending live verification)** |
| 2 | **Mic auto-calibration** — chirp + FFT cross-correlation → automatic per-device delay | planned |
| 3 | Periodic re-sync against clock drift | stretch |

**Today:** `chorus play` opens an interactive picker that discovers Google Cast
devices (mDNS), AirPlay 2 receivers, and CoreAudio output devices (incl. paired
Bluetooth) and lets you multi-select across all three. `chorus play --cast …
--airplay … --bt …` skips the picker, taps system audio, and fans it out to all
selected devices at once, each delayable with `--offset`. Audio is
44.1kHz/16-bit/stereo PCM throughout.

Phase 2 is the centerpiece: it computes the `--offset` values automatically.
`--offset` stays as a manual override.

## The hard problem: clock drift

A one-time offset (mic-measured or manual) is only correct at the instant it's
measured. Cast and Bluetooth devices run on independent clocks, so over minutes
they drift back out of alignment.

- **Short sessions:** a static offset is fine.
- **Long sessions:** robustness requires periodic recalibration (Phase 3).

## Architecture

```
audiotee ─► capture ─► broadcaster ─┬─► Cast output     (live WAV over HTTP → go-chromecast)
                                    ├─► AirPlay output  (airplayrelay Rust sidecar → airplay2-rs)
                                    └─► BT output       (chorusaudio Swift helper → CoreAudio)
```

- **Go + [cobra](https://github.com/spf13/cobra)** — CLI, orchestration, fan-out.
- **[audiotee](https://github.com/makeusabrew/audiotee)** (Swift sidecar) — system-audio capture.
- **[go-chromecast](https://github.com/vishen/go-chromecast)** — the Cast protocol; chorus hosts the live WAV stream it points at.
- **`airplayrelay`** (Rust, in `native/`) — wraps [airplay2-rs](https://github.com/jburnhams/airplay2-rs) for AirPlay 2 discovery, HomeKit pairing, and live PCM streaming.
- **`chorusaudio`** (Swift, in `native/`) — renders PCM to a chosen CoreAudio output device, and enumerates devices.
- **Broadcaster + goroutine-per-output** — fan-out and per-device delay.
- *(later)* **[gonum](https://gonum.org) / go-dsp** — FFT for chirp cross-correlation.

The main binary is **pure Go** (CGO_ENABLED=0); platform/protocol access lives in
separate sidecar processes (Swift for capture/CoreAudio, Rust for AirPlay 2).

## Scope

**In scope:** capture Mac audio → fan out to Google Cast + AirPlay 2 + Bluetooth →
align (manual now, mic-calibrated later).

**Out of scope (for now):** airplay2-rs's native multi-room/NTP sync (we align
AirPlay via the same per-output `--offset` mechanism as the other outputs);
calibration (Phase 2).

## Benchmark

The headline metric is residual inter-device offset after calibration:

> Uncorrected ~250ms → **<15ms** after mic calibration *(target; to be measured).*

## Notes & caveats

- Latency figures are approximate; Cast in particular buffers several seconds, so
  align other outputs *to* it rather than expecting it to be fast.
- Bluetooth pairing is a manual macOS step — pair the device in System Settings,
  then it appears in the `chorus play` picker.
- The live WAV-over-HTTP Cast path is the main thing to validate on real hardware;
  if a device rejects it, an ffmpeg→FLAC fallback is the planned alternative.
- AirPlay 2 discovery (`airplayrelay list`) is confirmed; the streaming + pairing
  path still needs a real-hardware pass (PIN pairing, and HomePod's required
  audio encryption in particular). airplay2-rs is early-stage (v0.1), pinned to a
  rev in `native/airplayrelay/Cargo.toml`.

## License

MIT
