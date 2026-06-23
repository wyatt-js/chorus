#!/usr/bin/env bash
#
# chorus installer — downloads the latest prebuilt universal macOS bundle and
# installs the chorus CLI plus its three sidecars (audiotee, chorusaudio,
# airplayrelay) onto your PATH. No build-from-source, no toolchains.
#
# Usage:
#   curl -fsSL https://raw.githubusercontent.com/wyatt-js/chorus/main/install.sh | bash
#
# Env overrides:
#   CHORUS_VERSION   tag to install (default: latest release)
#   CHORUS_BIN_DIR   install dir (default: /usr/local/bin, falls back to ~/.local/bin)
set -euo pipefail

REPO="wyatt-js/chorus"
ASSET="chorus-macos-universal.tar.gz"

err()  { printf '\033[31merror:\033[0m %s\n' "$*" >&2; }
info() { printf '\033[36m>>\033[0m %s\n' "$*" >&2; }

# --- preflight -------------------------------------------------------------
[ "$(uname -s)" = "Darwin" ] || { err "chorus is macOS-only (this is $(uname -s))."; exit 1; }
for cmd in curl tar shasum; do
  command -v "$cmd" >/dev/null 2>&1 || { err "missing required command: $cmd"; exit 1; }
done

# --- resolve version -------------------------------------------------------
VERSION="${CHORUS_VERSION:-}"
if [ -z "$VERSION" ]; then
  info "resolving latest release"
  VERSION="$(curl -fsSL "https://api.github.com/repos/$REPO/releases/latest" \
    | grep -m1 '"tag_name"' | cut -d'"' -f4)"
fi
[ -n "$VERSION" ] || { err "could not determine a release to install (set CHORUS_VERSION)."; exit 1; }
info "installing chorus $VERSION"

BASE="https://github.com/$REPO/releases/download/$VERSION"

# --- download + verify -----------------------------------------------------
TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

info "downloading $ASSET"
curl -fsSL "$BASE/$ASSET" -o "$TMP/$ASSET"
if curl -fsSL "$BASE/$ASSET.sha256" -o "$TMP/$ASSET.sha256" 2>/dev/null; then
  info "verifying checksum"
  ( cd "$TMP" && shasum -a 256 -c "$ASSET.sha256" >/dev/null ) \
    || { err "checksum verification failed"; exit 1; }
fi

tar -xzf "$TMP/$ASSET" -C "$TMP"
SRC="$TMP/chorus-macos-universal"
[ -d "$SRC" ] || { err "unexpected archive layout"; exit 1; }

# --- pick install dir ------------------------------------------------------
BIN_DIR="${CHORUS_BIN_DIR:-/usr/local/bin}"
SUDO=""
if [ ! -d "$BIN_DIR" ]; then
  if ! mkdir -p "$BIN_DIR" 2>/dev/null; then
    if [ "$BIN_DIR" = "/usr/local/bin" ]; then
      BIN_DIR="$HOME/.local/bin"; mkdir -p "$BIN_DIR"
    else
      err "cannot create $BIN_DIR"; exit 1
    fi
  fi
fi
if [ ! -w "$BIN_DIR" ]; then
  if command -v sudo >/dev/null 2>&1; then
    info "installing to $BIN_DIR (needs sudo)"
    SUDO="sudo"
  elif [ "$BIN_DIR" = "/usr/local/bin" ]; then
    BIN_DIR="$HOME/.local/bin"; mkdir -p "$BIN_DIR"
  else
    err "$BIN_DIR is not writable"; exit 1
  fi
fi

# --- install ---------------------------------------------------------------
info "installing to $BIN_DIR"
for b in chorus audiotee chorusaudio airplayrelay; do
  $SUDO install -m 0755 "$SRC/$b" "$BIN_DIR/$b"
  # curl downloads carry no quarantine flag, but strip it defensively.
  $SUDO xattr -d com.apple.quarantine "$BIN_DIR/$b" 2>/dev/null || true
done

info "installed: chorus audiotee chorusaudio airplayrelay"

# --- PATH guidance ---------------------------------------------------------
case ":$PATH:" in
  *":$BIN_DIR:"*) : ;;
  *)
    info "NOTE: $BIN_DIR is not on your PATH. Add it, e.g.:"
    printf '\n  echo '\''export PATH="%s:$PATH"'\'' >> ~/.zshrc && exec zsh\n\n' "$BIN_DIR" >&2
    ;;
esac

info "done — run: chorus play"
