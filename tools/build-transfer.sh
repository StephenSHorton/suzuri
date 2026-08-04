#!/usr/bin/env bash
# Build the transfer engine and optionally copy it next to a local suzuri binary.
#
# Usage (repo root):
#   ./tools/build-transfer.sh              # release build only
#   ./tools/build-transfer.sh --dev        # debug build
#   ./tools/build-transfer.sh --copy ./suzuri
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
PROFILE=release
COPY_TO=""

while [[ $# -gt 0 ]]; do
  case "$1" in
    --dev) PROFILE=debug; shift ;;
    --copy) COPY_TO="$2"; shift 2 ;;
    -h|--help)
      sed -n '2,12p' "$0"
      exit 0
      ;;
    *) echo "unknown arg: $1" >&2; exit 1 ;;
  esac
done

cd "$ROOT"
echo "==> cargo build ($PROFILE) libs/transfer"
if [[ "$PROFILE" == "release" ]]; then
  cargo build --release --manifest-path libs/transfer/Cargo.toml -p hato-cli
  OUT="$ROOT/libs/transfer/target/release/suzuri-transfer"
else
  cargo build --manifest-path libs/transfer/Cargo.toml -p hato-cli
  OUT="$ROOT/libs/transfer/target/debug/suzuri-transfer"
fi

if [[ ! -f "$OUT" ]]; then
  # Windows
  if [[ -f "${OUT}.exe" ]]; then
    OUT="${OUT}.exe"
  else
    echo "error: binary not found at $OUT" >&2
    exit 1
  fi
fi

echo "==> built $OUT"
ls -la "$OUT"

if [[ -n "$COPY_TO" ]]; then
  dest_dir="$(cd "$(dirname "$COPY_TO")" && pwd)"
  base="$(basename "$OUT")"
  cp "$OUT" "$dest_dir/$base"
  chmod +x "$dest_dir/$base" 2>/dev/null || true
  echo "==> copied to $dest_dir/$base"
fi
