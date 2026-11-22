#!/bin/sh
# The keeper's tests on Linux, in Docker (card hearth#937): Debian with the
# Tauri libraries and Zig 0.17 (libghostty-vt), the workspace mounted, its
# own target folder in a Docker volume so it never mixes with the host's.
#   scripts/keeper/linux-test.sh            # cargo test -p suzuri --test keeper
#   scripts/keeper/linux-test.sh --ignored  # also the key-to-frame latency
set -eu
R=$(cd "$(dirname "$0")/../.." && pwd)
[ -d "$R/ui/dist" ] || { echo "build the UI first: (cd ui && npm ci && npm run build)" >&2; exit 2; }
EXTRA=""
[ "${1:-}" = --ignored ] && EXTRA="-- --include-ignored --nocapture"
exec docker run --rm -v "$R":/src -v rsz-linux-target:/target -v rsz-linux-cargo:/usr/local/cargo/registry \
  -v rsz-linux-cache:/root/.cache -e CARGO_TARGET_DIR=/target -e EXTRA="$EXTRA" -w /src rust:1.96-bookworm sh -c '
set -e
apt-get update -qq >/dev/null
apt-get install -y -qq xz-utils libwebkit2gtk-4.1-dev libgtk-3-dev libayatana-appindicator3-dev librsvg2-dev \
  libsoup-3.0-dev libjavascriptcoregtk-4.1-dev libssl-dev pkg-config >/dev/null 2>&1
A=$(uname -m)
curl -fsSL "https://ziglang.org/download/0.17.0/zig-$A-linux-0.17.0.tar.xz" | tar -xJ -C /opt
export ZIG=/opt/zig-$A-linux-0.17.0/zig
uname -srm; . /etc/os-release; echo "$PRETTY_NAME, zig $($ZIG version)"
cargo test -q -p suzuri --test keeper $EXTRA'
