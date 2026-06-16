# CLAUDE.md

Guidance for Claude Code (and humans) working in this repo. See `README.md` for
the user-facing project description.

## What this is

`airtooth` — a macOS Go CLI that captures system audio and relays it
time-aligned to multiple **Google Cast** + **Bluetooth** devices, with mic+chirp
acoustic calibration (later) to auto-measure per-device latency. Module:
`github.com/wyattjs/airtooth-sync`. Binary: `airtooth`.

The Cast + Bluetooth path is **pure Go (no cgo)**. Classic-AirPlay/RAOP sending
is parked behind `-tags airplay` (cgo + libraop) and is not built by default.

## Layout

```
cmd/airtooth/        # cobra CLI: main, devices, play
internal/discover/   # mDNS browse: Browse (_raop._tcp), BrowseCast (_googlecast._tcp)
internal/capture/    # audiotee sidecar wrapper -> raw PCM stream
internal/audio/      # shared PCM Format type (StereoCD = 44100/16/2)
internal/output/     # Output interface, Broadcaster (fan-out + per-output delay),
                     #   Cast (live WAV HTTP + go-chromecast), BT (airtoothaudio helper)
internal/pipeline/   # wires capture -> broadcaster -> outputs (Run)
internal/raop/       # PARKED: cgo libraop RAOP sender, //go:build airplay only
native/airtoothaudio/ # Swift helper: `list` devices + `render` PCM to a CoreAudio device
scripts/build_deps.sh, scripts/build_deps_airplay.sh
third_party/         # audiotee + libraop submodules (nested go.mod)
```

Planned: `internal/calibrate/` (chirp + FFT, P2). Keep `main` thin — wiring only.

## Build / test / run

```sh
make deps                      # build Swift sidecars: audiotee + airtoothaudio
make build                     # CGO_ENABLED=0 go build -o bin/airtooth ./cmd/airtooth
make test                      # go test ./...
go vet ./...                   # vet before committing
gofmt -l cmd internal          # must report no files (formatting gate)
```

- Default build is pure Go. `internal/raop` is excluded by the `airplay` build
  tag, so `go build/vet ./...` does not need cgo or libraop.
- AirPlay path (optional): `make deps-airplay && make build-airplay`
  (`go build -tags airplay`). Its cgo paths hardcode `macos/arm64`; libraop's
  log-level globals (`util_loglevel`, `raop_loglevel`) are defined in the cgo
  preamble since the app, not the library, must provide them.
- `third_party/` has a nested `go.mod` so the parent's `./...` ignores the
  vendored submodules (some contain unrelated Go source).

## Data flow

```
audiotee (PCM s16le/44100/stereo) -> capture.Reader
   -> output.Broadcaster (tees chunks to each Output; per-output start delay = prepended silence)
       -> output.Cast: live WAV stream over HTTP, go-chromecast Load(url, "audio/wav", detach)
       -> output.BT:   pipe PCM to `airtoothaudio render --device-uid <uid>` (AVAudioSourceNode -> AUHAL device)
```

A slow output drops chunks rather than stalling the others (see `pump`).

## Conventions

- **Go style:** `gofmt`/`go vet` clean. Errors wrapped with `fmt.Errorf("...: %w", err)`.
- **Concurrency:** one goroutine per output; channels for the audio pipeline;
  clean ctx-cancellation shutdown (no leaked goroutines or child processes).
- **Time/latency:** offsets are `time.Duration`; flags use units (`--offset name=2s`).
- **Sidecars over cgo:** prefer a small Swift/CLI sidecar (audiotee, airtoothaudio)
  to reach Apple audio APIs, rather than cgo, unless cgo is unavoidable.
- **Don't reimplement protocols:** use go-chromecast for Cast; don't hand-roll
  AirPlay crypto (that's why RAOP is parked, not rebuilt).

## Platform realities (don't fight these)

- Core Audio process taps require **macOS 14.2+** and the
  `NSAudioCaptureUsageDescription` permission (prompted on first capture; some
  terminals don't surface it — use Terminal.app).
- **Cast is pull-based**: the device fetches a URL from an HTTP server airtooth
  hosts on the LAN IP. WAV/PCM is lowest-latency and needs no encoder; MP3 lags
  multiple seconds. Cast buffers seconds regardless — align other outputs to it.
- **Bluetooth output** = a normal CoreAudio device once paired (manual macOS
  step). The Swift helper renders to it by UID via AUHAL + AVAudioSourceNode.
- **Clock drift** is the core hard problem: independent clocks drift over minutes;
  a static offset is fine short-term, periodic recalibration is the real fix (P3).

## Success metric

Residual inter-device offset after calibration (target large-uncorrected → <15ms).
When touching the fan-out/offset path, preserve the ability to measure this.

## Notes for the assistant

- Verify a path exists before referencing it; the architecture pivoted from
  AirPlay/RAOP to Cast+Bluetooth, so older notes may be stale.
- The live WAV-over-HTTP Cast path is the main unverified risk — confirm on real
  hardware; ffmpeg→FLAC is the documented fallback.
- Ask before committing or pushing.
