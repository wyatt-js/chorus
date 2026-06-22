# chorus

**Synchronized multi-device audio relay for macOS.**

`chorus` is a macOS CLI that captures your system audio and relays it to multiple
**Google Cast**, **AirPlay 2**, and **Bluetooth** devices at once — time-aligned.
macOS can natively send to one AirPlay/Cast target at a time; `chorus` fans one
captured stream out to several devices and delays each independently so they line
up instead of echoing.

> **Status:** early development. AirPlay 2, Cast, and Bluetooth output all work;
> alignment is manual (`--offset`) today, mic auto-calibration is planned. See
> [Roadmap](#roadmap).

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
4. **Align** — each output is delayed by a per-device offset (manual `--offset`
   now; mic calibration later) so audio reaches your ears simultaneously.

## Install

Not yet published — build from source.

**Requirements:** macOS 14.2+ (Apple Silicon), Go 1.25+, a Swift toolchain +
Xcode CLI tools, and a Rust toolchain ([rustup.rs](https://rustup.rs), for the
AirPlay 2 sidecar). The system-audio capture permission is granted on first run.

```sh
git clone --recurse-submodules https://github.com/<user>/chorus
cd chorus
make deps     # builds sidecars: audiotee (capture) + chorusaudio (BT) + airplayrelay (AirPlay 2)
make build    # builds ./bin/chorus (pure Go, CGO_ENABLED=0)
make test     # optional
```

If you cloned without `--recurse-submodules`, run `git submodule update --init
--recursive` first.

## Usage

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
(unchanged ones keep playing), **`q`** quits. (**`s`** — mic sync — is a
placeholder for Phase 2.)

**Flag form** (`--cast`/`--airplay`/`--bt`, each a repeatable name substring):
streams to a fixed set until Ctrl-C, no in-session menu. `--offset name=dur`
delays one device relative to the others. `--pin 1234` supplies a first-time
pairing PIN (saved afterward). Pair Bluetooth devices in macOS Settings first.

## Roadmap

| Phase | Goal | Status |
|-------|------|--------|
| 0 | Capture → single output | **done** |
| 1 | Fan out to Cast + AirPlay 2 + Bluetooth; manual `--offset` | **done** |
| 2 | Mic auto-calibration — chirp + FFT → automatic per-device delay | planned |
| 3 | Periodic re-sync against clock drift | stretch |

Phase 2 is the centerpiece: it computes the `--offset` values automatically
(`--offset` stays as a manual override). **The hard part is clock drift** — a
one-time offset is only correct at the instant it's measured; independent device
clocks drift over minutes, so long sessions need periodic recalibration (Phase
3). The target metric is residual inter-device offset: uncorrected ~250ms →
**<15ms** after calibration.

## Architecture

```
audiotee ─► capture ─► broadcaster ─┬─► Cast      (live WAV over HTTP → go-chromecast)
                                    ├─► AirPlay   (airplayrelay Rust sidecar → airplay2-rs)
                                    └─► Bluetooth (chorusaudio Swift helper → CoreAudio)
```

The main binary is **pure Go** (CGO_ENABLED=0); platform and protocol access live
in separate sidecar processes — Swift for capture/CoreAudio (`audiotee`,
`chorusaudio`), Rust for AirPlay 2 (`airplayrelay`). Audio is 44.1kHz/16-bit/
stereo PCM throughout.

## Caveats

- **AirPlay 2 targets modern receivers** (HomePod, Apple TV, recent speakers/TVs)
  via airplay2-rs — the old classic-RAOP path is gone. The full handshake + live
  audio is confirmed end-to-end on Samsung TVs; HomePod's PIN/encryption path is
  still untested. airplay2-rs is early-stage (v0.1), pinned to a rev in
  `native/airplayrelay/Cargo.toml`.
- **Audio sync is still being hardened.** Outputs run on independent clocks, and
  periodic pops under clock drift are a known open issue.
- **Cast buffers several seconds** — align other outputs *to* it. The live
  WAV-over-HTTP path still wants more real-hardware validation (an ffmpeg→FLAC
  fallback is the planned alternative).
- **Bluetooth pairing is a manual macOS step** — pair once in System Settings,
  then the device appears in the picker; selecting a disconnected one connects it.

## License

MIT
