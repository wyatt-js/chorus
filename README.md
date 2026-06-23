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

One command — downloads a prebuilt universal (Intel + Apple Silicon) bundle of
`chorus` and its three sidecars, no toolchains required:

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
`chorusaudio`), Rust for AirPlay 2 (`airplayrelay`). Audio is 48kHz/16-bit/
stereo PCM throughout — macOS's native rate, so nothing resamples.

## Caveats

- **AirPlay 2 needs a patched dependency.** Sending goes through airplay2-rs
  (early-stage, v0.1), which doesn't work out of the box against strict modern
  receivers — so it's **vendored under `third_party/airplay2-rs` and locally
  patched** (OPTIONS deferred until after auth, a fuller SETUP `streams` field
  set, ALAC, PTP grandmaster-id fallback, no-zero-pad PCM). With those patches the
  full handshake + live audio is confirmed end-to-end on Samsung TVs; HomePod's
  PIN/encryption path is still untested.
- **Audio sync is still being hardened.** Outputs run on independent clocks, and
  periodic pops under clock drift are a known open issue.
- **Cast buffers several seconds** — align other outputs *to* it. The live
  WAV-over-HTTP path still wants more real-hardware validation (an ffmpeg→FLAC
  fallback is the planned alternative).
- **Bluetooth pairing is a manual macOS step** — pair once in System Settings,
  then the device appears in the picker; selecting a disconnected one connects it.

## License

MIT
