# CLAUDE.md

Guidance for Claude Code (and humans) working in this repo. See `README.md` for
the user-facing project description.

## What this is

`chorus` — a macOS Go CLI that captures system audio and relays it
time-aligned to multiple **Google Cast** + **AirPlay 2** + **Bluetooth** devices,
with mic+chirp acoustic calibration (later) to auto-measure per-device latency.
Module: `github.com/wyattjs/chorus`. Binary: `chorus`.

The whole thing is **pure Go (CGO_ENABLED=0)**. Reaching Apple/AirPlay APIs is
done with separate sidecar processes (audiotee, chorusaudio, airplayrelay),
never cgo. **AirPlay 2** sending is delegated to the `airplayrelay` Rust sidecar
(wraps the pure-Rust [airplay2-rs](https://github.com/jburnhams/airplay2-rs)
crate: mDNS discovery + HomeKit pairing + live PCM). The old classic-AirPlay/RAOP
cgo path (libraop) has been removed — AirPlay 2 supersedes it.

## Layout

```
cmd/chorus/     # cobra CLI: main, play (interactive device picker in menu.go)
internal/discover/   # mDNS browse: Browse (_raop._tcp), BrowseCast (_googlecast._tcp)
internal/capture/    # audiotee sidecar wrapper -> raw PCM stream
internal/audio/      # shared PCM Format type (StereoCD = 44100/16/2)
internal/output/     # Output interface, Broadcaster (fan-out + per-output delay),
                     #   Cast (live WAV HTTP + go-chromecast),
                     #   AirPlay (airplayrelay sidecar), BT (chorusaudio helper)
internal/pipeline/   # wires capture -> broadcaster -> outputs (Run)
native/chorusaudio/ # Swift helper: `list` CoreAudio devices, `bt-list`/`bt-connect`
                     #   paired Bluetooth (IOBluetooth), `render` PCM to a CoreAudio device
native/airplayrelay/ # Rust sidecar (wraps airplay2-rs): `list` AirPlay 2 receivers +
                     #   `render` s16le PCM from stdin to one (HomeKit pairing persisted)
scripts/build_deps.sh
third_party/         # audiotee submodule (nested go.mod);
                     #   airplay2-rs (vendored at rev 527884f, locally patched)
```

Planned: `internal/calibrate/` (chirp + FFT, P2). Keep `main` thin — wiring only.

## Build / test / run

```sh
make deps                      # build sidecars: audiotee + chorusaudio + airplayrelay
make build                     # CGO_ENABLED=0 go build -o bin/chorus ./cmd/chorus
make test                      # go test ./...
go vet ./...                   # vet before committing
gofmt -l cmd internal          # must report no files (formatting gate)
```

- The build is pure Go (CGO_ENABLED=0); `go build/vet ./...` needs no cgo.
- **AirPlay 2** needs a Rust toolchain (`cargo`, from https://rustup.rs):
  `make deps` builds the `airplayrelay` sidecar via `cargo build --release`. The
  airplay2-rs crate is **vendored** under `third_party/airplay2-rs` (upstream rev
  527884f) and consumed via a `path` dep in `native/airplayrelay/Cargo.toml`,
  because it carries a local patch: `connect_internal` defers RTSP `OPTIONS`
  until after authentication so strict receivers (Samsung TVs, HomePods) that
  `403` a cleartext `OPTIONS` accept it over the encrypted channel.
- `third_party/` has a nested `go.mod` so the parent's `./...` ignores the
  vendored submodule(s).

## Data flow

```
audiotee (PCM s16le/44100/stereo) -> capture.Reader
   -> output.Broadcaster (tees chunks to each Output; per-output start delay = prepended silence)
       -> output.Cast:    live WAV stream over HTTP, go-chromecast Load(url, "audio/wav", detach)
       -> output.AirPlay: pipe PCM to `airplayrelay render --device <id>` (airplay2-rs stream_audio)
       -> output.BT:      pipe PCM to `chorusaudio render --device-uid <uid>` (AVAudioSourceNode -> AUHAL device)
```

`AirPlay` and `BT` are `Prestarter`s: their sidecar PIDs are excluded from the
capture tap so their own playback doesn't feed back into the capture.

A slow output drops chunks rather than stalling the others (see `pump`).

## Conventions

- **Go style:** `gofmt`/`go vet` clean. Errors wrapped with `fmt.Errorf("...: %w", err)`.
- **Concurrency:** one goroutine per output; channels for the audio pipeline;
  clean ctx-cancellation shutdown (no leaked goroutines or child processes).
- **Time/latency:** offsets are `time.Duration`; flags use units (`--offset name=2s`).
- **Sidecars over cgo:** prefer a small Swift/Rust/CLI sidecar (audiotee,
  chorusaudio, airplayrelay) to reach platform/protocol APIs, rather than
  cgo, unless cgo is unavoidable.
- **Don't reimplement protocols:** use go-chromecast for Cast and airplay2-rs
  (via airplayrelay) for AirPlay 2; don't hand-roll AirPlay/HomeKit crypto.

## Platform realities (don't fight these)

- Core Audio process taps require **macOS 14.2+** and the
  `NSAudioCaptureUsageDescription` permission (prompted on first capture; some
  terminals don't surface it — use Terminal.app).
- **Cast is pull-based**: the device fetches a URL from an HTTP server chorus
  hosts on the LAN IP. WAV/PCM is lowest-latency and needs no encoder; MP3 lags
  multiple seconds. Cast buffers seconds regardless — align other outputs to it.
- **AirPlay 2 output** = the `airplayrelay` sidecar scans (`_airplay._tcp`),
  HomeKit-pairs (creds persisted to `~/Library/Application Support/chorus/
  airplay/pairings.json`), and streams live PCM via airplay2-rs. First-time
  pairing may need a PIN (`--pin`); AirPlay buffers ~2s, so align other outputs
  to it. Apple TVs use transient pairing; HomePods require the encryption path.
- **Bluetooth output** = a normal CoreAudio device once *connected*. Pairing is a
  manual macOS step, but the `chorus play` picker lists paired audio devices via
  IOBluetooth (`chorusaudio bt-list`, with connect state) and *connecting* is what
  brings a device online as a CoreAudio output — selecting a disconnected device
  in the picker runs `chorusaudio bt-connect --address <addr>` (spinner shown),
  then resolves it to its CoreAudio UID by name. The helper renders to it by UID
  via AUHAL + AVAudioSourceNode.
- **Bluetooth reachability**: classic BT audio devices don't advertise presence,
  so `bt-list --reachable-timeout <sec>` pings each disconnected paired device
  with a baseband *name request* and only prints the ones that answer (powered on,
  in range). The controller serializes paging, so the scan budget scales with the
  number of absent devices (≈ per-device timeout × count). It can have false
  negatives (a slow-to-answer device gets hidden); connecting still happens at
  select-time, so the spinner there is the backstop.
- **Clock drift** is the core hard problem: independent clocks drift over minutes;
  a static offset is fine short-term, periodic recalibration is the real fix (P3).

## Success metric

Residual inter-device offset after calibration (target large-uncorrected → <15ms).
When touching the fan-out/offset path, preserve the ability to measure this.

## Notes for the assistant

- Verify a path exists before referencing it; the architecture pivoted from
  classic-AirPlay/RAOP (libraop cgo, now removed) to Cast + AirPlay 2 + Bluetooth,
  so older notes may be stale.
- **AirPlay 2 status:** on a Samsung Neo QLED the full patched handshake now
  completes end-to-end against real hardware: `GET /info` → transient SRP pairing
  (no PIN) → encrypted `OPTIONS` → `SETUP` #1/#2 → `SETPEERS` → PTP sync →
  `SETRATEANCHORTIME` → `RECORD` → live RTP audio (stable, no teardown). The local
  airplay2-rs patches that made this work (see `third_party/airplay2-rs`): OPTIONS
  after auth; buffered SETUP #2 carries the full `streams` field set
  (`streamConnectionID`/`supportsDynamicStreamID`/`isMedia`/`sr`/`audioMode`) and
  drops the RAOP `Transport` header; ALAC codec; and SETRATEANCHORTIME's
  `networkTimeTimelineID` falls back to the PTP grandmaster id (captured from the
  master's Announce) since Samsung omits `timingPeerInfo.ClockID`. Still to confirm
  by ear: actual audible output + sync quality (PTP offset logging looks off).
  HomePod's PIN/encryption path remains untested. airplay2-rs is early-stage (v0.1).
- **Unverified on hardware:** the live WAV-over-HTTP Cast path (ffmpeg→FLAC is the
  fallback).
- Ask before committing or pushing.
