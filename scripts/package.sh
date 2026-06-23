#!/usr/bin/env bash
#
# Build a distributable, universal (arm64 + x86_64) macOS bundle of chorus and
# its three sidecars, then tar it up for a GitHub Release.
#
# Output:
#   dist/chorus-macos-universal.tar.gz   (chorus, audiotee, chorusaudio, airplayrelay)
#   dist/chorus-macos-universal.tar.gz.sha256
#
# Used by .github/workflows/release.yml and runnable locally to test packaging.
# Requires: Go, a Swift toolchain (Xcode CLI tools), Rust (cargo + both targets).
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
AUDIOTEE="$ROOT/third_party/audiotee"
CHORUSAUDIO="$ROOT/native/chorusaudio"
AIRPLAYRELAY="$ROOT/native/airplayrelay"

DIST="$ROOT/dist"
STAGE="$DIST/chorus-macos-universal"
rm -rf "$DIST"
mkdir -p "$STAGE"

# --- chorus (Go) -----------------------------------------------------------
echo ">> building chorus (Go, universal)"
CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -o "$DIST/chorus.arm64" ./cmd/chorus
CGO_ENABLED=0 GOOS=darwin GOARCH=amd64 go build -o "$DIST/chorus.amd64" ./cmd/chorus
lipo -create -output "$STAGE/chorus" "$DIST/chorus.arm64" "$DIST/chorus.amd64"
rm -f "$DIST/chorus.arm64" "$DIST/chorus.amd64"

# --- audiotee + chorusaudio (Swift) ---------------------------------------
# Universal (--arch arm64 --arch x86_64) Swift builds require full Xcode (xcbuild).
# CI runs on a macOS runner that has it; fall back to a native-only build when
# only the Command Line Tools are installed so local packaging still works.
SWIFT_ARCH_ARGS=(--arch arm64 --arch x86_64)
if ! xcodebuild -version >/dev/null 2>&1; then
  echo "!! full Xcode not found — building Swift sidecars for $(uname -m) only" >&2
  echo "!! (install Xcode for a universal bundle; CI does this automatically)" >&2
  SWIFT_ARCH_ARGS=()
fi
build_swift() {
  local name="$1" pkg="$2"
  echo ">> building $name (Swift)"
  swift build -c release ${SWIFT_ARCH_ARGS[@]+"${SWIFT_ARCH_ARGS[@]}"} --package-path "$pkg"
  local bin
  bin="$(swift build -c release ${SWIFT_ARCH_ARGS[@]+"${SWIFT_ARCH_ARGS[@]}"} --package-path "$pkg" --show-bin-path)"
  cp "$bin/$name" "$STAGE/$name"
}
build_swift audiotee "$AUDIOTEE"
build_swift chorusaudio "$CHORUSAUDIO"

# --- airplayrelay (Rust) ---------------------------------------------------
echo ">> building airplayrelay (Rust, universal)"
if ! command -v cargo >/dev/null 2>&1; then
  echo "!! cargo (Rust toolchain) not found. Install it from https://rustup.rs." >&2
  exit 1
fi
rustup target add aarch64-apple-darwin x86_64-apple-darwin >/dev/null 2>&1 || true
cargo build --release --manifest-path "$AIRPLAYRELAY/Cargo.toml" --target aarch64-apple-darwin
cargo build --release --manifest-path "$AIRPLAYRELAY/Cargo.toml" --target x86_64-apple-darwin
lipo -create -output "$STAGE/airplayrelay" \
  "$AIRPLAYRELAY/target/aarch64-apple-darwin/release/airplayrelay" \
  "$AIRPLAYRELAY/target/x86_64-apple-darwin/release/airplayrelay"

# --- package ---------------------------------------------------------------
chmod +x "$STAGE"/*
echo ">> verifying universal binaries"
for b in chorus audiotee chorusaudio airplayrelay; do
  lipo -info "$STAGE/$b"
done

tar -czf "$DIST/chorus-macos-universal.tar.gz" -C "$DIST" chorus-macos-universal
( cd "$DIST" && shasum -a 256 chorus-macos-universal.tar.gz > chorus-macos-universal.tar.gz.sha256 )

echo ">> done:"
echo "   $DIST/chorus-macos-universal.tar.gz"
echo "   $DIST/chorus-macos-universal.tar.gz.sha256"
