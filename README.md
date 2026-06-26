# chorus

**Synchronized multi-device audio relay for macOS.**

`chorus` captures your Mac's system audio and relays it — time-aligned — to as
many **Google Cast**, **AirPlay 2**, and **Bluetooth** speakers as you want, all
at once. macOS can stream to multiple AirPlay 2 speakers on its own, but only
within that one ecosystem: it can't drive Google Cast at all, talks to just one
Bluetooth device, and gives you no way to mix AirPlay + Cast + Bluetooth or to
delay each independently. `chorus` fans one captured stream across all three and
aligns them — by ear, with the mic, or by hand — so they play in lockstep
instead of echoing room to room.

<img width="541" height="150" alt="chorus banner" src="https://github.com/user-attachments/assets/40fa8dbd-6475-474d-abe7-333078befb52" />

## Demo

<!-- TODO: drop the end-to-end GIF here (capture → fan-out → mic sync → in-sync playback). -->

_End-to-end demo GIF coming soon._

## Quick start

```sh
chorus play
```

That's the whole interface. `chorus play` scans your network and Bluetooth,
shows an interactive picker, and streams to everything you select — then keeps
single-key controls live so you can re-align, add/drop devices, or quit without
restarting. Everything below is detail on top of that one command.

## How it works

1. **Capture** — the [audiotee](https://github.com/makeusabrew/audiotee) sidecar
   taps macOS system audio (Core Audio process taps) → raw PCM.
2. **Fan out** — a broadcaster tees the PCM to one goroutine per output, each
   with its own start delay.
3. **Send** — per protocol:
   - **Cast**: host a live WAV stream over HTTP and point the device at it (via
     [go-chromecast](https://github.com/vishen/go-chromecast)).
   - **AirPlay 2**: stream live PCM through the `airplayrelay` Rust sidecar, which
     wraps [airplay2-rs](https://github.com/jburnhams/airplay2-rs) (mDNS discovery
     + HomeKit pairing + timed PCM).
   - **Bluetooth**: render PCM to a paired device through the `chorusaudio` Swift
     helper (IOBluetooth + CoreAudio).
4. **Align** — each output is delayed by a per-device offset so audio reaches
   your ears simultaneously. Measure offsets automatically with the mic
   (**`s`**), trim them by hand (**`d`**), or set them up front
   (`--offset name=dur`). See [Aligning devices](#aligning-devices).

## Install

```sh
curl -fsSL https://raw.githubusercontent.com/wyatt-js/chorus/main/install.sh | bash
```

This installs `chorus` and its sidecars (`audiotee`, `chorusaudio`,
`airplayrelay`) to `/usr/local/bin` (or `~/.local/bin` if that isn't writable).
Override with `CHORUS_BIN_DIR=...` or pin a release with `CHORUS_VERSION=v0.1.0`.

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

```sh
chorus play
```

<img width="605" height="279" alt="chorus play picker" src="https://github.com/user-attachments/assets/ee1accc6-9111-4b62-bc62-b91197dc7862" />

Run with no arguments for the **interactive picker**: `chorus` scans for Cast,
AirPlay, and paired Bluetooth devices and lets you multi-select across all three.

- **↑/↓** move · **enter/space** toggle · select **Submit** + enter to confirm ·
  **q** cancel
- Selecting a disconnected Bluetooth device connects it on the spot.

Once playing, these single keys stay live (you never have to restart):

| Key | Does |
|-----|------|
| **`m`** | reopen the menu to add/drop devices — unchanged ones keep playing |
| **`s`** | mic auto-sync — measure and align each device acoustically |
| **`d`** | delays — trim each device's offset by hand |
| **`q`** | quit |

### Skipping the picker

You can name devices up front and stream to a fixed set until you quit:

```sh
chorus play --cast "The Frame"                    # one TV
chorus play --airplay "HomePod" --bt "HW-S700D"   # an AirPlay speaker + a soundbar
```

`--cast` / `--airplay` / `--bt` each take a name substring and are repeatable.
The live keys above still work. First-time pairing for a receiver that needs a
PIN: add `--pin 1234` (saved afterward). Pair Bluetooth devices in macOS
Settings first.

## Aligning devices

Different speakers buffer for different amounts of time (Cast and AirPlay buffer
seconds; Bluetooth far less), so without alignment they echo. `chorus` gives you
three ways to line them up — all applied live, with no playback gap.

### Mic auto-sync (`s`) — recommended

Press **`s`** while playing and `chorus` measures each device's true latency
acoustically, then aligns them for you. Because the mic is the Mac's built-in
input and your speakers are in different rooms, sync is **user-paced**:

1. It lists your active devices.
2. Carry the laptop near one and press its **number** — `chorus` plays a short
   test chirp on *that device only*, records it on the mic, and measures how long
   the sound took to arrive.
3. Repeat for each device, in any order. After every measurement it re-aligns
   the measured devices automatically (delaying the faster ones to match the
   slowest). Once they're all measured the screen closes itself.

**`r`** resets and starts over · **`q`** closes. If a measurement reports it
couldn't hear the tone, move the Mac closer to that speaker and retry.
Calibration briefly mutes the other devices while it measures.

### Manual delays (`d`)

Press **`d`** for a live slider you tune by ear — handy for a quick nudge or when
you'd rather not get up. Each device shows a centered bar (left = earlier, right
= later, relative to the others):

- **↑/↓** select a device
- **←/→** trim ±10 ms · **`[` / `]`** trim ±250 ms · **`0`** recenter
- **`q`** done

### Up-front offsets (`--offset`)

If you already know the numbers, set them on the command line:

```sh
chorus play --airplay "HomePod" --bt "HW-S700D" --offset HW-S700D=2s
```

`--offset name=dur` delays one device relative to the others; it's repeatable.

## Architecture

```
audiotee ─► capture ─► broadcaster ─┬─► Cast      (live WAV over HTTP → go-chromecast)
                                    ├─► AirPlay   (airplayrelay Rust sidecar → airplay2-rs)
                                    └─► Bluetooth (chorusaudio Swift helper → CoreAudio)
```

The main binary is **pure Go** (CGO_ENABLED=0); platform and protocol access live
in separate sidecar processes — Swift for capture/CoreAudio (`audiotee`,
`chorusaudio`), Rust for AirPlay 2 (`airplayrelay`). Audio is
48 kHz / 16-bit / stereo PCM throughout — macOS's native rate, so nothing
resamples.

Mic auto-sync (the `s` key) adds a measurement path on top: the broadcaster plays
a chirp on one output and silences the rest, `chorusaudio record` captures the
Mac's mic, and the pure-Go `internal/calibrate` package (hand-rolled FFT +
matched-filter cross-correlation) recovers each device's latency and retunes the
offsets live — no playback gap.

## Vendored dependencies

Two upstream projects live under `third_party/` as **plain vendored files, not
git submodules** — so their local patches are tracked directly in this repo and a
plain `git clone` builds without any submodule init. A nested `third_party/go.mod`
keeps them out of the parent Go module, so `go build ./...` ignores them.

- **`third_party/airplay2-rs`** ([jburnhams/airplay2-rs](https://github.com/jburnhams/airplay2-rs),
  upstream rev `527884f`) — the AirPlay 2 sender, consumed by `airplayrelay` via a
  `path` dependency in `native/airplayrelay/Cargo.toml`. It's early-stage (v0.1)
  and doesn't pair with strict modern receivers (Samsung TVs, HomePods) out of the
  box, so it's patched locally:
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

  With these, the full handshake and live audio run end-to-end on a Samsung Neo
  QLED. HomePod's PIN/encryption path isn't tested yet.

- **`third_party/audiotee`** ([makeusabrew/audiotee](https://github.com/makeusabrew/audiotee))
  — the system-audio capture sidecar (Core Audio process taps). Vendored
  unmodified; pinned here so the capture path builds reproducibly.

## Notes & limitations

- **AirPlay 2 relies on a patched dependency.** Sending goes through airplay2-rs
  (early-stage, v0.1), which is vendored and locally patched to pair with strict
  modern receivers — see [Vendored dependencies](#vendored-dependencies).
- **Clock drift.** Independent devices run on independent clocks, so a static
  offset is only exact at the moment it's measured and drifts over long sessions.
  Re-run mic sync (**`s`**) or nudge with delays (**`d`**) to recorrect.
- **Offset granularity is 10 ms**, which caps the best-case residual at ±5 ms.
- **Cast buffers several seconds** — align the others *to* it.
- **Bluetooth pairing is a manual macOS step** — pair once in System Settings,
  then the device appears in the picker; selecting a disconnected one connects it.

## What's built

| Capability | |
|------------|---|
| Capture system audio → single output | ✅ |
| Fan out to Cast + AirPlay 2 + Bluetooth at once | ✅ |
| Per-device offsets — up-front (`--offset`) and hand-trimmed (`d`) | ✅ |
| Mic auto-calibration — chirp + FFT → automatic per-device delay (`s`) | ✅ |

Alignment is measured as residual inter-device offset: uncorrected ~250 ms →
**< 15 ms** after calibration.

## License

MIT
</content>
