#!/usr/bin/env bash
#
# Build the OPTIONAL classic-AirPlay/RAOP dependency (libraop). Only needed to
# build with `-tags airplay`; the default Cast+Bluetooth build does not use it.
#
# Produces libraop.a + libcross.a (the crosstools archive providing
# netsock_init / cross_ssl_load, which are not in libraop.a).
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
RAOP="$ROOT/third_party/libraop"
HOST=macos
ARCH="$(uname -m)"

echo ">> building libraop.a ($HOST/$ARCH)"
make -C "$RAOP" CC=clang HOST="$HOST" PLATFORM="$ARCH" STATIC=1 lib

LIBDIR="$RAOP/lib/$HOST/$ARCH"
CROSS="$RAOP/crosstools/src"
OPENSSL_INC="$RAOP/libopenssl/targets/$HOST/$ARCH/include"

echo ">> building libcross.a (crosstools)"
TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT
for f in cross_log cross_ssl cross_util cross_net platform; do
  clang -c -O2 -fPIC -DNDEBUG -D_GNU_SOURCE -DOPENSSL_SUPPRESS_DEPRECATED -DSSL_STATIC_LIB \
    -I"$CROSS" -I"$RAOP/dmap-parser" \
    -I"$RAOP/libmdns/targets/include/mdnssvc" -I"$RAOP/libmdns/targets/include/mdnssd" \
    -I"$OPENSSL_INC" -I"$RAOP/src" \
    "$CROSS/$f.c" -o "$TMP/$f.o"
done
ar rcs "$LIBDIR/libcross.a" "$TMP"/*.o

echo ">> done: $LIBDIR/{libraop.a,libcross.a}"
