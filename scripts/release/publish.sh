#!/usr/bin/env bash
# Builds, signs and notarizes the macOS release of suzuri (the Rust + Tauri
# app; this repo moves into StephenSHorton/suzuri), signs the Windows installer
# the PC uploaded (scripts/release/build-windows.ps1), writes the updater feed
# (latest.json) and publishes the GitHub release. Local only, no CI.
#
#   scripts/release/publish.sh 0.1.1 [--notes "text"] [--mac-only]
#                                    [--no-notarize] [--feed-base URL] [--no-upload]
#
# --mac-only     publish without a Windows installer
# --no-notarize  sign but skip Apple's notary (local tests)
# --feed-base    URL the feed points at instead of the GitHub release
#                (local tests: serve dist/<version> and set RSZ_UPDATE_URL)
# --no-upload    build everything into dist/<version> and stop
#
# Needs on the Mac:
#   - the "Developer ID Application" identity in the login keychain
#   - notary credentials saved as a keychain profile (RSZ_NOTARY_PROFILE,
#     default suzuri-notary):
#       xcrun notarytool store-credentials suzuri-notary \
#         --apple-id <apple id> --team-id GP6632G333
#   - the updater key (RSZ_UPDATER_KEY, default ~/.tauri/suzuri-updater.key;
#     public half in packaging/updater.pub and tauri.conf.json)
#   - gh logged in, cargo-tauri
#
# RSZ_TAURI_CONFIG: one more config overlay (a JSON file), e.g. a local test
# that allows an http:// feed.
set -euo pipefail

VERSION="${1:-}"
[[ "$VERSION" =~ ^[0-9]+\.[0-9]+\.[0-9]+([-+][0-9A-Za-z.-]+)?$ ]] || {
  echo "usage: $0 <version> [--notes text] [--mac-only] [--no-notarize] [--feed-base URL] [--no-upload]" >&2
  exit 2
}
shift
NOTES="suzuri $VERSION"
MAC_ONLY=0 NOTARIZE=1 UPLOAD=1 FEED_BASE=""
while [[ $# -gt 0 ]]; do
  case "$1" in
    --notes) NOTES="$2"; shift 2 ;;
    --mac-only) MAC_ONLY=1; shift ;;
    --no-notarize) NOTARIZE=0; shift ;;
    --no-upload) UPLOAD=0; shift ;;
    --feed-base) FEED_BASE="${2%/}"; shift 2 ;;
    *) echo "unknown flag $1" >&2; exit 2 ;;
  esac
done

REPO="${RSZ_RELEASE_REPO:-StephenSHorton/suzuri}"
PROFILE="${RSZ_NOTARY_PROFILE:-suzuri-notary}"
KEY="${RSZ_UPDATER_KEY:-$HOME/.tauri/suzuri-updater.key}"
TAG="v$VERSION"
ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
DIST="$ROOT/dist/$VERSION"
[[ -n "$FEED_BASE" ]] || FEED_BASE="https://github.com/$REPO/releases/download/$TAG"

IDENTITY="${APPLE_SIGNING_IDENTITY:-$(security find-identity -v -p codesigning | sed -n 's/.*"\(Developer ID Application: [^"]*\)".*/\1/p' | head -1)}"
[[ -n "$IDENTITY" ]] || { echo "error: no Developer ID Application identity in the keychain" >&2; exit 1; }
[[ -f "$KEY" ]] || { echo "error: updater key not found: $KEY" >&2; exit 1; }
echo "==> suzuri $VERSION, signing as: $IDENTITY"

rm -rf "$DIST" && mkdir -p "$DIST"
WORK="$(mktemp -d "${TMPDIR:-/tmp}/rsz-release.XXXXXX")"
trap 'rm -rf "$WORK"' EXIT

notarize() { # <file.zip|file.dmg>
  echo "==> notarize $(basename "$1")"
  xcrun notarytool submit "$1" --keychain-profile "$PROFILE" --wait --timeout "${RSZ_NOTARY_TIMEOUT:-90m}"
}

sign_update() { # <file> -> prints the minisign signature
  TAURI_SIGNING_PRIVATE_KEY_PASSWORD="${TAURI_SIGNING_PRIVATE_KEY_PASSWORD:-}" \
    cargo tauri signer sign -f "$KEY" "$1" >/dev/null
  cat "$1.sig"
}

# 1. Build and sign the .app (Tauri applies hardened runtime + entitlements).
cat >"$WORK/overlay.json" <<EOF
{"version": "$VERSION", "bundle": {"createUpdaterArtifacts": false}}
EOF
(cd "$ROOT/app" && APPLE_SIGNING_IDENTITY="$IDENTITY" cargo tauri build --bundles app --config "$WORK/overlay.json" ${RSZ_TAURI_CONFIG:+--config "$RSZ_TAURI_CONFIG"})
APP="$ROOT/target/release/bundle/macos/suzuri.app"
codesign --verify --deep --strict "$APP"
BUILT="$(/usr/libexec/PlistBuddy -c 'Print :CFBundleShortVersionString' "$APP/Contents/Info.plist")"
[[ "$BUILT" == "$VERSION" ]] || { echo "error: built $BUILT, wanted $VERSION" >&2; exit 1; }
ditto "$APP" "$WORK/suzuri.app"
APP="$WORK/suzuri.app"

# 2. Notarize and staple the app.
if [[ $NOTARIZE == 1 ]]; then
  ditto -c -k --keepParent "$APP" "$WORK/submit.zip"
  notarize "$WORK/submit.zip"
  xcrun stapler staple "$APP"
  spctl -a -vv "$APP"
fi

# 3. DMG for first installs (drag to Applications), signed and notarized.
DMG="$DIST/suzuri_${VERSION}_aarch64.dmg"
mkdir -p "$WORK/dmg" && ditto "$APP" "$WORK/dmg/suzuri.app" && ln -s /Applications "$WORK/dmg/Applications"
hdiutil create -quiet -volname "suzuri $VERSION" -srcfolder "$WORK/dmg" -fs HFS+ -format UDZO "$DMG"
codesign --sign "$IDENTITY" --timestamp "$DMG"
if [[ $NOTARIZE == 1 ]]; then
  notarize "$DMG"
  xcrun stapler staple "$DMG"
fi

# 4. Updater payload: the stapled app as .tar.gz, signed with the updater key.
MAC_TGZ="suzuri_${VERSION}_aarch64.app.tar.gz"
COPYFILE_DISABLE=1 tar -czf "$DIST/$MAC_TGZ" -C "$WORK" suzuri.app
MAC_SIG="$(sign_update "$DIST/$MAC_TGZ")"

# 5. Windows installer from the draft release (built on the PC).
WIN_EXE="suzuri_${VERSION}_x64-setup.exe"
WIN_SIG=""
if [[ $MAC_ONLY == 0 ]]; then
  if [[ -f "$ROOT/dist/win/$WIN_EXE" ]]; then
    cp "$ROOT/dist/win/$WIN_EXE" "$DIST/"
  else
    gh release download "$TAG" -R "$REPO" -p "$WIN_EXE" -D "$DIST" --clobber
  fi
  WIN_SIG="$(sign_update "$DIST/$WIN_EXE")"
fi

# 6. The feed. Tauri's updater reads platforms["darwin-aarch64"] etc.
python3 - "$DIST/latest.json" "$VERSION" "$NOTES" "$FEED_BASE" "$MAC_TGZ" "$MAC_SIG" "$WIN_EXE" "$WIN_SIG" <<'PY'
import json, sys, datetime
out, version, notes, base, mac, mac_sig, win, win_sig = sys.argv[1:]
platforms = {"darwin-aarch64": {"signature": mac_sig, "url": f"{base}/{mac}"}}
if win_sig:
    w = {"signature": win_sig, "url": f"{base}/{win}"}
    platforms["windows-x86_64"] = w
    platforms["windows-x86_64-nsis"] = w
feed = {
    "version": version,
    "notes": notes,
    "pub_date": datetime.datetime.now(datetime.timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ"),
    "platforms": platforms,
}
json.dump(feed, open(out, "w"), indent=2)
PY
(cd "$DIST" && shopt -s nullglob && shasum -a 256 *.dmg *.tar.gz *.exe >SHA256SUMS)
ls -l "$DIST"

[[ $UPLOAD == 1 ]] || { echo "==> built in $DIST (not uploaded)"; exit 0; }

# 7. Publish: upload to the (draft) release, then make it the latest. The
# app's feed is releases/latest/download/latest.json, so clients see the new
# version only once the release is complete and published.
if ! gh release view "$TAG" -R "$REPO" >/dev/null 2>&1; then
  gh release create "$TAG" -R "$REPO" --draft --target main --title "suzuri $VERSION" --notes "$NOTES"
fi
gh release upload "$TAG" -R "$REPO" --clobber "$DIST"/*
gh release edit "$TAG" -R "$REPO" --draft=false --latest --notes "$NOTES"
echo "==> published https://github.com/$REPO/releases/tag/$TAG"
