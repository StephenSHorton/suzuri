#!/usr/bin/env bash
# Builds suzuri-transfer (hato, libs/transfer) and puts it where the release
# overlay's bundle.externalBin expects it: packaging/bin/suzuri-transfer-<triple>.
# Tauri then copies it next to the suzuri binary in the bundle and signs it, so
# transfers work with no Go suzuri installed (#330).
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
TRIPLE="$(rustc -vV | sed -n 's/^host: //p')"
cargo build --release --manifest-path "$ROOT/libs/transfer/Cargo.toml" -p hato-cli --bin suzuri-transfer
mkdir -p "$ROOT/packaging/bin"
cp "$ROOT/libs/transfer/target/release/suzuri-transfer" "$ROOT/packaging/bin/suzuri-transfer-$TRIPLE"
echo "==> packaging/bin/suzuri-transfer-$TRIPLE"
