# CLAUDE.md

Guidance for Claude Code (and humans) working in this repo. See `README.md` for
the user-facing project description.

## What this is

`airtooth` — a macOS Go CLI that captures system audio and relays it
time-aligned to multiple AirPlay + Bluetooth speakers, with mic+chirp acoustic
calibration to auto-measure per-device latency. Module:
`github.com/<user>/airtooth-sync`. Binary: `airtooth`.

## Layout (target)

```
cmd/airtooth/        # CLI entrypoint (cobra commands: devices, play, calibrate)
internal/capture/    # Core Audio process tap (cgo or Swift sidecar glue)
internal/output/     # per-device senders (AirPlay/RAOP, Bluetooth) + ring buffers
internal/calibrate/  # chirp generation, mic recording, FFT cross-correlation
internal/sync/       # delay/offset model, alignment logic
internal/audio/      # shared audio types (frames, formats, sample rate)
```

Keep `main` thin — wiring only. Logic lives in `internal/`.

## Build / test / run

```sh
go build ./cmd/airtooth        # build the binary
go test ./...                  # run tests
go vet ./...                   # vet before committing
gofmt -l .                     # must report no files (formatting gate)
```

If a Swift sidecar is used for the audio tap, document its build step here once
it exists.

## Conventions

- **Go style:** standard `gofmt`/`go vet` clean. Errors wrapped with `fmt.Errorf("...: %w", err)`.
- **Concurrency:** one goroutine + one ring buffer per output device. Guard
  shared state; prefer channels for the audio pipeline. Always have a clean
  shutdown path (context cancellation) — no leaked audio goroutines.
- **Real-time path:** avoid allocations in the per-frame hot loop; reuse buffers.
  GC is fine elsewhere.
- **Time/latency:** represent offsets as `time.Duration`. Be explicit about units
  in flags (e.g. `--offset bt=120ms`).
- **No reinventing crypto:** use an existing AirPlay/RAOP sender lib. Don't hand-roll
  the AirPlay handshake.

## Platform realities (don't fight these)

- Core Audio process taps require **macOS 14.2+ (14.4+ safer)** and the
  `NSAudioCaptureUsageDescription` permission. Capture will silently fail without
  the entitlement/permission.
- The tap API is Obj-C/Swift — reached from Go via **cgo or a Swift helper
  process**. cgo is where most friction lives; isolate it behind `internal/capture`.
- **Clock drift is the core hard problem:** AirPlay and BT run on independent
  clocks. A one-time offset drifts over minutes. Static offset is acceptable for
  short sessions; periodic recalibration is the real fix (Phase 3). Don't assume
  a single calibration holds forever.
- Latency ballparks: AirPlay 2 ~1–2s buffered, BT A2DP ~150–300ms (codec-dependent).

## Build phases (current focus)

- **P0:** capture system audio → single AirPlay output. Prove the pipe.
- **P1:** add a second (BT) output; per-device ring buffers; manual `--offset`.
- **P2 (centerpiece):** mic auto-calibration (chirp → FFT cross-correlation →
  per-device delay). Keep `--offset` as a manual override.
- **P3 (stretch):** periodic re-sync for drift; optional iPhone-as-source.

Out of scope for v1: iPhone acting as an AirPlay receiver/source.

## Success metric

Residual inter-device offset after calibration (target ~250ms → <15ms). When
touching the sync/calibration path, preserve the ability to measure this.

## Notes for the assistant

- The repo may be mostly empty early on — propose structure rather than assuming
  files exist; verify before referencing a path.
- Don't introduce a heavy DSP/audio dependency without flagging the tradeoff;
  prefer `gonum`/`go-dsp` for FFT.
- Ask before committing or pushing.
