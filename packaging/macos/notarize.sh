#!/usr/bin/env bash
# Submit a signed .app or .dmg to Apple notary service and staple the ticket.
#
# Credentials (first match wins):
#   1. App Store Connect API key:
#        APPLE_API_KEY_ID, APPLE_API_ISSUER_ID, APPLE_API_KEY_P8  (path)
#        or APPLE_API_KEY_P8_BASE64
#   2. Apple ID + app-specific password:
#        APPLE_ID, APPLE_TEAM_ID, APPLE_APP_SPECIFIC_PASSWORD
#
# Usage:
#   packaging/macos/notarize.sh path/to/suzuri.app
#   packaging/macos/notarize.sh path/to/suzuri.dmg
set -euo pipefail

TARGET="${1:-}"
if [[ -z "$TARGET" || ! -e "$TARGET" ]]; then
  echo "usage: $0 <signed.app|signed.dmg>" >&2
  exit 2
fi

if ! command -v xcrun >/dev/null 2>&1; then
  echo "error: xcrun not found (need full Xcode CLT)" >&2
  exit 1
fi

TARGET="$(cd "$(dirname "$TARGET")" && pwd)/$(basename "$TARGET")"
WORK="$(mktemp -d "${TMPDIR:-/tmp}/suzuri-notary.XXXXXX")"
trap 'rm -rf "$WORK"' EXIT

# notarytool wants a file; zip .app if needed.
SUBMIT="$TARGET"
if [[ -d "$TARGET" && "$TARGET" == *.app ]]; then
  SUBMIT="$WORK/submit.zip"
  echo "==> zipping app for notary submit"
  ditto -c -k --keepParent "$TARGET" "$SUBMIT"
fi

AUTH_ARGS=()
if [[ -n "${APPLE_API_KEY_ID:-}" && -n "${APPLE_API_ISSUER_ID:-}" ]]; then
  KEY_FILE="${APPLE_API_KEY_P8:-}"
  if [[ -z "$KEY_FILE" && -n "${APPLE_API_KEY_P8_BASE64:-}" ]]; then
    KEY_FILE="$WORK/AuthKey.p8"
    echo "$APPLE_API_KEY_P8_BASE64" | base64 --decode >"$KEY_FILE"
  fi
  if [[ -z "$KEY_FILE" || ! -f "$KEY_FILE" ]]; then
    echo "error: set APPLE_API_KEY_P8 or APPLE_API_KEY_P8_BASE64" >&2
    exit 1
  fi
  AUTH_ARGS=(--key "$KEY_FILE" --key-id "$APPLE_API_KEY_ID" --issuer "$APPLE_API_ISSUER_ID")
  echo "==> notary auth: App Store Connect API key"
elif [[ -n "${APPLE_ID:-}" && -n "${APPLE_TEAM_ID:-}" && -n "${APPLE_APP_SPECIFIC_PASSWORD:-}" ]]; then
  AUTH_ARGS=(--apple-id "$APPLE_ID" --team-id "$APPLE_TEAM_ID" --password "$APPLE_APP_SPECIFIC_PASSWORD")
  echo "==> notary auth: Apple ID + app-specific password"
else
  echo "error: missing notarization credentials (see packaging/macos/SIGNING.md)" >&2
  exit 1
fi

echo "==> notarytool submit $(basename "$SUBMIT")"
# --wait blocks until Accepted/Invalid (can take a few minutes).
xcrun notarytool submit "$SUBMIT" "${AUTH_ARGS[@]}" --wait --timeout 30m

echo "==> staple $(basename "$TARGET")"
xcrun stapler staple "$TARGET"
xcrun stapler validate "$TARGET"

if [[ -d "$TARGET" && "$TARGET" == *.app ]]; then
  echo "==> spctl assess app"
  spctl -a -vv "$TARGET" 2>&1 || true
fi

echo "==> notarize OK: $TARGET"
