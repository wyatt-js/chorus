#!/usr/bin/env bash
#
# Build airtooth's native sidecars (macOS):
#   - audiotee       : system-audio capture (Core Audio taps)        [submodule]
#   - airtoothaudio  : CoreAudio render + device list (Bluetooth out) [native/]
#
# The main binary is pure Go (Google Cast + Bluetooth). The classic-AirPlay/RAOP
# path is optional and built separately by scripts/build_deps_airplay.sh
# (needed only for `go build -tags airplay`).
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
AUDIOTEE="$ROOT/third_party/audiotee"
airtoothAUDIO="$ROOT/native/airtoothaudio"

echo ">> building audiotee (capture sidecar)"
swift build -c release --package-path "$AUDIOTEE"

echo ">> building airtoothaudio (CoreAudio output helper)"
swift build -c release --package-path "$airtoothAUDIO"

echo ">> done:"
echo "   $AUDIOTEE/.build/release/audiotee"
echo "   $airtoothAUDIO/.build/release/airtoothaudio"
