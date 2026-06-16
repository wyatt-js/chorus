# chrometooth-sync

**Synchronized multi-device audio relay with mic-based acoustic calibration.**

`chrometooth` is a macOS CLI that captures your system audio and relays it to
multiple **Google Cast** and **Bluetooth** devices at once — time-aligned.
Different protocols introduce wildly different latencies (Cast buffers seconds;
Bluetooth A2DP ~150–300ms), so playing the same stream to both produces an
audible echo. `chrometooth` delays each output independently so they line up — and
(later) measures the right delays automatically with a mic and a chirp.

> Status: early development. See [Roadmap](#roadmap) for what works today.

## Why

Play the same audio through your TV and a soundbar (or a speaker in another room)
without the two echoing against each other. macOS can natively send to one
AirPlay/Cast target at a time; `chrometooth` fans one captured stream out to several
heterogeneous devices and aligns them.

## How it works

1. **Capture** — the [audiotee](https://github.com/makeusabrew/audiotee) sidecar
   taps macOS system audio (Core Audio process taps) → raw PCM.
2. **Fan out** — a broadcaster tees the PCM to one goroutine per output, each with
   its own start delay.
3. **Send** — per output protocol:
   - **Cast**: host a live WAV stream over HTTP and point the device's default
     media receiver at it (via [go-chromecast](https://github.com/vishen/go-chromecast)).
   - **Bluetooth**: render PCM to the paired CoreAudio output device through a
     small Swift helper (`chrometoothaudio`).
4. **Align** — each output's stream is delayed by a per-device offset (manual
   `--offset` today; mic auto-calibration later) so audio reaches your ears
   from every device simultaneously.

> **AirPlay note:** classic-RAOP AirPlay sending exists behind a build tag
> (`-tags airplay`, via a cgo binding over libraop) but is parked — sending to
> modern AirPlay 2 devices needs pairing/FairPlay and is out of scope. Cast +
> Bluetooth cover the target hardware.

## Install

> Not yet published. For now, build from source.

**Requirements**

- macOS 14.2+ (14.4+ recommended) — required for Core Audio process taps
- Apple Silicon
- Go 1.25+, a Swift toolchain, and Xcode command-line tools
- The system audio capture permission (`NSAudioCaptureUsageDescription`), granted
  on first run; microphone access later (for calibration)

```sh
git clone --recurse-submodules https://github.com/<user>/chrometooth-sync
cd chrometooth-sync

make deps     # builds the Swift sidecars: audiotee (capture) + chrometoothaudio (output)
make build    # builds ./bin/chrometooth (pure Go)
make test     # optional: unit tests
```

`make deps` builds the audiotee submodule under `third_party/` and the
`chrometoothaudio` helper under `native/`. The main binary is pure Go. If you cloned
without `--recurse-submodules`, run `git submodule update --init --recursive`
first. (Classic-AirPlay support is optional: `make deps-airplay && make
build-airplay`.)

## Usage

```sh
chrometooth devices                                  # list Cast + Bluetooth/output devices
chrometooth play --cast "The Frame"                  # cast system audio to a TV
chrometooth play --cast "The Frame" --bt "HW-S700D"  # fan out to a TV and a soundbar
chrometooth play --cast "The Frame" --bt "HW-S700D" --offset HW-S700D=2s  # delay the soundbar
```

`--cast`/`--bt` take a name substring and are repeatable. `--offset name=dur`
delays one device relative to the others (manual alignment until calibration
lands). Pair a Bluetooth device in macOS Settings first so it shows in `devices`.
Press Ctrl-C to stop.

## Roadmap

| Phase | Goal | Status |
|-------|------|--------|
| 0 | Capture → single output (prove the pipe) | **done** |
| 1 | Multiple simultaneous outputs (Cast + Bluetooth); fan-out; manual `--offset` | **done (pending live verification)** |
| 2 | **Mic auto-calibration** — chirp + FFT cross-correlation → automatic per-device delay | planned |
| 3 | Periodic re-sync against clock drift | stretch |

**Today:** `chrometooth devices` lists Google Cast devices (mDNS) and CoreAudio
output devices (incl. paired Bluetooth). `chrometooth play --cast … --bt …` taps
system audio and fans it out to all selected devices at once, each delayable with
`--offset`. Audio is 44.1kHz/16-bit/stereo PCM throughout.

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
audiotee ─► capture ─► broadcaster ─┬─► Cast output  (live WAV over HTTP → go-chromecast)
                                    └─► BT output    (chrometoothaudio Swift helper → CoreAudio)
```

- **Go + [cobra](https://github.com/spf13/cobra)** — CLI, orchestration, fan-out.
- **[audiotee](https://github.com/makeusabrew/audiotee)** (Swift sidecar) — system-audio capture.
- **[go-chromecast](https://github.com/vishen/go-chromecast)** — the Cast protocol; chrometooth hosts the live WAV stream it points at.
- **`chrometoothaudio`** (Swift, in `native/`) — renders PCM to a chosen CoreAudio output device, and enumerates devices.
- **Broadcaster + goroutine-per-output** — fan-out and per-device delay.
- *(later)* **[gonum](https://gonum.org) / go-dsp** — FFT for chirp cross-correlation.

The Cast + Bluetooth path is **pure Go** (no cgo). Classic AirPlay lives behind
`-tags airplay` (cgo + libraop).

## Scope

**In scope:** capture Mac audio → fan out to Google Cast + Bluetooth → align (manual
now, mic-calibrated later).

**Out of scope:** AirPlay 2 sending (needs pairing/FairPlay; parked behind a build
tag, with [airplay2-rs](https://github.com/lmcgartland/airplay2-rs) as the
reference if revisited).

## Benchmark

The headline metric is residual inter-device offset after calibration:

> Uncorrected ~250ms → **<15ms** after mic calibration *(target; to be measured).*

## Notes & caveats

- Latency figures are approximate; Cast in particular buffers several seconds, so
  align other outputs *to* it rather than expecting it to be fast.
- Bluetooth pairing is a manual macOS step — pair the device in System Settings,
  then it appears in `chrometooth devices`.
- The live WAV-over-HTTP Cast path is the main thing to validate on real hardware;
  if a device rejects it, an ffmpeg→FLAC fallback is the planned alternative.
- libraop (the parked AirPlay path) has no clear OSS license — resolve before
  distributing with `-tags airplay`.

## License

MIT
