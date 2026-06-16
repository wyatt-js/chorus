#!/usr/bin/env bash
#
# Build chorus's native sidecars (macOS):
#   - audiotee          : system-audio capture (Core Audio taps)          [submodule]
#   - chorusaudio  : CoreAudio render + device list (Bluetooth out)   [native/]
#   - airplayrelay      : AirPlay 2 discovery + PCM streaming (Rust)       [native/]
#
# The main binary is pure Go (CGO_ENABLED=0); these sidecars are separate
# processes it spawns (CLAUDE.md: "sidecars over cgo").
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
AUDIOTEE="$ROOT/third_party/audiotee"
CHORUSAUDIO="$ROOT/native/chorusaudio"
AIRPLAYRELAY="$ROOT/native/airplayrelay"

echo ">> building audiotee (capture sidecar)"
swift build -c release --package-path "$AUDIOTEE"

echo ">> building chorusaudio (CoreAudio output helper)"
swift build -c release --package-path "$CHORUSAUDIO"

echo ">> building airplayrelay (AirPlay 2 sidecar)"
if ! command -v cargo >/dev/null 2>&1; then
  echo "!! cargo (Rust toolchain) not found. Install it from https://rustup.rs," >&2
  echo "!! then re-run 'make deps'. AirPlay 2 output needs this sidecar." >&2
  exit 1
fi
cargo build --release --manifest-path "$AIRPLAYRELAY/Cargo.toml"

echo ">> done:"
echo "   $AUDIOTEE/.build/release/audiotee"
echo "   $CHORUSAUDIO/.build/release/chorusaudio"
echo "   $AIRPLAYRELAY/target/release/airplayrelay"
