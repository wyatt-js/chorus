# airtooth-sync

**Synchronized multi-device audio relay with mic-based acoustic calibration.**

`airtooth` is a macOS CLI that captures your system audio and relays it to
multiple AirPlay and Bluetooth speakers at once — time-aligned. Different
wireless protocols introduce wildly different latencies (AirPlay 2 buffers
~1–2s, Bluetooth A2DP ~150–300ms), so playing the same stream to both produces
an audible echo. `airtooth` measures each device's true latency with a mic and a
chirp, then delay-aligns every output so they play in sync.

> Status: early development. See [Roadmap](#roadmap) for what works today.

## Why

Play music through your good AirPlay speaker in one room and a Bluetooth speaker
in another without the two echoing against each other. Existing solutions either
lock you to a single protocol (AirPlay-only) or require manual trial-and-error
offset tuning. `airtooth` automates the alignment acoustically.

## How it works

1. **Capture** — macOS Core Audio process taps grab the system audio stream.
2. **Fan out** — one goroutine and ring buffer per output device; the stream is
   relayed concurrently to every AirPlay/Bluetooth target.
3. **Calibrate** — `airtooth` emits a chirp on one device at a time, records it
   through the mic, and uses FFT cross-correlation to compute time-of-arrival.
   The per-device delay needed to align all outputs falls out of that.
4. **Align** — each output's ring buffer is delayed by its measured offset so the
   audio reaches your ears from all speakers simultaneously.

## Install

> Not yet published. For now, build from source.

**Requirements**

- macOS 14.2+ (14.4+ recommended) — required for Core Audio process taps
- Go 1.22+
- Microphone access (for calibration) and the system audio capture permission
  (`NSAudioCaptureUsageDescription`)

```sh
git clone https://github.com/<user>/airtooth-sync
cd airtooth-sync
go build ./cmd/airtooth
```

## Usage

```sh
airtooth devices                 # list available AirPlay + Bluetooth outputs
airtooth play                    # capture system audio and fan out to all
airtooth calibrate --chirp       # run mic-based acoustic sync
airtooth play --offset bt=120ms  # manual per-device delay override
```

Typical flow: run `airtooth devices` to see what's available, `airtooth
calibrate --chirp` once your speakers are placed, then `airtooth play`.

## Roadmap

| Phase | Goal | Status |
|-------|------|--------|
| 0 | Capture macOS system audio → single AirPlay output (prove the pipe) | planned |
| 1 | Add a second (Bluetooth) output; per-device ring buffers; manual `--offset` | planned |
| 2 | **Mic auto-calibration** — chirp + FFT cross-correlation → automatic per-device delay | planned |
| 3 | Periodic re-sync against clock drift; optional iPhone-as-source (stretch) | stretch |

Phase 2 is the centerpiece. `--offset` remains available as a manual override
even after auto-calibration lands.

## The hard problem: clock drift

A one-time offset (mic-measured or manual) is only correct at the instant it's
measured. AirPlay and Bluetooth devices run on independent clocks, so over
minutes they drift back out of alignment. AirPlay 2 maintains PTP timing
*within* AirPlay, but Bluetooth doesn't share that clock.

- **Short sessions:** a static offset is fine.
- **Long sessions:** robustness requires periodic recalibration (Phase 3).

## Architecture

- **Go + [cobra](https://github.com/spf13/cobra)** — CLI and orchestration.
- **cgo or a small Swift sidecar** — to reach the Obj-C/Swift Core Audio tap API
  from Go (likely wrapping a sample like `audiotee` / `insidegui/AudioCap`).
- **[gonum](https://gonum.org) / go-dsp** — FFT for chirp cross-correlation.
- **An existing AirPlay/RAOP sender library** — the AirPlay crypto is *not*
  reimplemented here.
- **Ring buffers + goroutine-per-output** — the fan-out and per-device delay.

GC pauses are sub-millisecond and the pipeline is buffered anyway, so Go's
runtime isn't a problem; the real friction is cgo glue and real-time buffer
management.

## Scope

**In scope (v1):** capture Mac audio → sync → fan out to AirPlay + Bluetooth →
mic calibration.

**Out of scope (v1):** receiving AirPlay *from* an iPhone (acting as an AirPlay 2
receiver). That's mostly reverse-engineering and integration rather than original
work — a stretch goal at most.

## Benchmark

The headline metric is residual inter-device offset after calibration:

> Uncorrected ~250ms → **<15ms** after mic calibration *(target; to be measured).*

## Notes & caveats

- Latency figures above are approximate ballparks and codec-dependent.
- No mature Go binding for Core Audio taps currently exists — expect to write
  glue or wrap a Swift sample.
- `goplay2` (a Go AirPlay 2 *receiver*) exists but is ~2023-stale; only relevant
  to the iPhone-source stretch goal. Treat as a black box.

## License

TBD.
