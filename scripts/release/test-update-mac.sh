#!/usr/bin/env bash
# Local update test on the Mac: builds signed $OLD and $NEW, serves $NEW's
# feed on 127.0.0.1, starts $OLD with RSZ_UPDATE_AUTO=1 and checks that it
# replaced itself with $NEW. A throwaway updater key is used (and the http
# feed allowed) through a config overlay, so the real key isn't needed.
#
#   scripts/release/test-update-mac.sh [old=0.1.0] [new=0.1.1] [port=8787]
set -euo pipefail
OLD="${1:-0.1.0}" NEW="${2:-0.1.1}" PORT="${3:-8787}"
ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
# Resolved: the updater refuses a path through a symlink (/var -> /private/var).
T="$(cd "$(mktemp -d "${TMPDIR:-/tmp}/rsz-uptest.XXXXXX")" && pwd -P)"
cleanup() { [[ -n "${SRV:-}" ]] && kill "$SRV" 2>/dev/null; pkill -f "$T/inst/" 2>/dev/null || true; rm -rf "$T"; }
trap cleanup EXIT

cargo tauri signer generate --ci -p "" -w "$T/test.key" >/dev/null
python3 - "$T" <<'PY'
import json, sys
t = sys.argv[1]
json.dump({"plugins": {"updater": {"pubkey": open(f"{t}/test.key.pub").read().strip(),
                                   "dangerousInsecureTransportProtocol": True}}},
          open(f"{t}/overlay.json", "w"))
PY
for v in "$OLD" "$NEW"; do
  RSZ_TAURI_CONFIG="$T/overlay.json" RSZ_UPDATER_KEY="$T/test.key" \
    "$ROOT/scripts/release/publish.sh" "$v" --mac-only --no-notarize --no-upload \
    --feed-base "http://127.0.0.1:$PORT" >"$T/build-$v.log" 2>&1 || { tail -20 "$T/build-$v.log"; exit 1; }
done

(cd "$ROOT/dist/$NEW" && exec python3 -m http.server "$PORT" --bind 127.0.0.1 >"$T/http.log" 2>&1) & SRV=$!
mkdir -p "$T/inst" && tar -xzf "$ROOT/dist/$OLD/"*.app.tar.gz -C "$T/inst"
APP="$T/inst/rust-suzuri.app"
ver() { /usr/libexec/PlistBuddy -c 'Print :CFBundleShortVersionString' "$APP/Contents/Info.plist"; }
echo "==> installed $(ver), feed offers $NEW"
RSZ_UPDATE_AUTO=1 RSZ_UPDATE_URL="http://127.0.0.1:$PORT/latest.json" "$APP/Contents/MacOS/rust-suzuri" >"$T/app.log" 2>&1 &
for _ in $(seq 60); do [[ "$(ver)" == "$NEW" ]] && break; sleep 1; done
sleep 3
cat "$T/app.log"
codesign --verify --deep --strict "$APP"
[[ "$(ver)" == "$NEW" ]] || { echo "FAIL: still $(ver)"; exit 1; }
pgrep -f "$APP/Contents/MacOS/rust-suzuri" >/dev/null || { echo "FAIL: app did not restart"; exit 1; }
echo "PASS: $OLD updated itself to $NEW, signature valid, app restarted"
