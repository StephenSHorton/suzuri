#!/usr/bin/env bash
# Build a suzuri.app bundle (and optional .dmg) from a compiled binary.
#
# Usage (from repo root after go build):
#   packaging/macos/build-app.sh \
#     --binary ./suzuri \
#     --version 0.9.29 \
#     --out dist
#
# Produces:
#   dist/suzuri.app
#   dist/suzuri-<version>-darwin-arm64.app.zip
#   dist/suzuri-<version>-darwin-arm64.dmg   (if hdiutil available)
set -euo pipefail

BINARY=""
VERSION="0.0.0-dev"
OUT_DIR="dist"
ARCH="$(uname -m)"
case "$ARCH" in
  arm64|aarch64) GOARCH="arm64" ;;
  x86_64) GOARCH="amd64" ;;
  *) GOARCH="$ARCH" ;;
esac

while [[ $# -gt 0 ]]; do
  case "$1" in
    --binary) BINARY="$2"; shift 2 ;;
    --version) VERSION="$2"; shift 2 ;;
    --out) OUT_DIR="$2"; shift 2 ;;
    --arch) GOARCH="$2"; shift 2 ;;
    -h|--help)
      sed -n '2,20p' "$0"
      exit 0
      ;;
    *) echo "unknown arg: $1" >&2; exit 1 ;;
  esac
done

if [[ -z "$BINARY" || ! -f "$BINARY" ]]; then
  echo "error: --binary path required and must exist" >&2
  exit 1
fi

ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
ICON_SRC="$ROOT/assets/icon"
mkdir -p "$OUT_DIR"
OUT_DIR="$(cd "$OUT_DIR" && pwd)"

APP_NAME="suzuri.app"
APP_PATH="$OUT_DIR/$APP_NAME"
CONTENTS="$APP_PATH/Contents"
MACOS_DIR="$CONTENTS/MacOS"
RES_DIR="$CONTENTS/Resources"

echo "==> assembling $APP_NAME (version $VERSION)"

rm -rf "$APP_PATH"
mkdir -p "$MACOS_DIR" "$RES_DIR"

# Executable
cp "$BINARY" "$MACOS_DIR/suzuri"
chmod +x "$MACOS_DIR/suzuri"

# Info.plist with version stamped
sed "s/VERSION_PLACEHOLDER/${VERSION//\//\\/}/g" "$SCRIPT_DIR/Info.plist" > "$CONTENTS/Info.plist"

# Build .icns from PNG set (sips + iconutil — macOS only)
ICONSET="$OUT_DIR/suzuri.iconset"
rm -rf "$ICONSET"
mkdir -p "$ICONSET"

# iconutil wants specifically named sizes.
copy_icon() {
  local src="$1" dest="$2"
  if [[ -f "$src" ]]; then
    cp "$src" "$ICONSET/$dest"
  fi
}

# Prefer pre-made PNGs when dimensions match; otherwise sips-resize from 512.
SRC512="$ICON_SRC/suzuri-512.png"
if [[ ! -f "$SRC512" ]]; then
  echo "error: missing $SRC512" >&2
  exit 1
fi

# Generate all required iconset entries from 512 master (crisp enough for dock).
for pair in \
  "16 icon_16x16.png" \
  "32 icon_16x16@2x.png" \
  "32 icon_32x32.png" \
  "64 icon_32x32@2x.png" \
  "128 icon_128x128.png" \
  "256 icon_128x128@2x.png" \
  "256 icon_256x256.png" \
  "512 icon_256x256@2x.png" \
  "512 icon_512x512.png" \
  "1024 icon_512x512@2x.png"
do
  set -- $pair
  size="$1"
  name="$2"
  # Use matching asset when available for quality.
  case "$size" in
    16) pref="$ICON_SRC/suzuri-16.png" ;;
    32) pref="$ICON_SRC/suzuri-32.png" ;;
    64) pref="$ICON_SRC/suzuri-64.png" ;;
    128) pref="$ICON_SRC/suzuri-128.png" ;;
    256) pref="$ICON_SRC/suzuri-256.png" ;;
    512) pref="$ICON_SRC/suzuri-512.png" ;;
    *) pref="" ;;
  esac
  if [[ -n "$pref" && -f "$pref" ]]; then
    # Ensure exact pixel size (asset may already match).
    sips -z "$size" "$size" "$pref" --out "$ICONSET/$name" >/dev/null
  else
    sips -z "$size" "$size" "$SRC512" --out "$ICONSET/$name" >/dev/null
  fi
done

# 1024 for @2x 512 — upscale from 512 if no larger source.
if [[ ! -f "$ICONSET/icon_512x512@2x.png" ]]; then
  sips -z 1024 1024 "$SRC512" --out "$ICONSET/icon_512x512@2x.png" >/dev/null
fi

iconutil -c icns "$ICONSET" -o "$RES_DIR/suzuri.icns"
rm -rf "$ICONSET"

# Code-sign the .app so TCC can attribute folder grants to a stable identity.
#
# Prefer a real signing identity when available (same cert across builds ⇒ macOS
# remembers Desktop/Documents/Downloads grants). Override with:
#   SUZURI_CODESIGN_IDENTITY="Developer ID Application: …"
#   SUZURI_CODESIGN_IDENTITY="Suzuri Local"   # self-signed cert in login keychain
# Default "-" is ad-hoc: fine for Gatekeeper-ish local runs, but every binary
# change gets a new CDHash and macOS re-prompts for protected folders.
#
# Do NOT enable hardened runtime for ad-hoc/local certs unless you also ship
# entitlements — it can break Metal/PTY hosts. Runtime flag is only for
# Developer ID / notarization identities.
BUNDLE_ID="com.stephenshorton.suzuri"
SIGN_ID="${SUZURI_CODESIGN_IDENTITY:--}"
if command -v codesign >/dev/null 2>&1; then
  echo "==> codesign identity=${SIGN_ID} id=${BUNDLE_ID}"
  SIGN_ARGS=(--force --sign "$SIGN_ID" --identifier "$BUNDLE_ID")
  if [[ "$SIGN_ID" != "-" && "$SIGN_ID" == Developer\ ID* ]]; then
    SIGN_ARGS+=(--options runtime)
  fi
  # Sign nested executable first, then the bundle (binds Info.plist / resources).
  codesign "${SIGN_ARGS[@]}" "$MACOS_DIR/suzuri" || true
  codesign "${SIGN_ARGS[@]}" --deep "$APP_PATH" || true
  codesign -dv --verbose=2 "$APP_PATH" 2>&1 | head -20 || true
fi

# Zip of the .app (Finder-friendly install: unzip → drag to Applications)
ZIP_NAME="suzuri-${VERSION}-darwin-${GOARCH}.app.zip"
(
  cd "$OUT_DIR"
  rm -f "$ZIP_NAME"
  # ditto preserves resource forks / code signature better than zip alone.
  if command -v ditto >/dev/null 2>&1; then
    ditto -c -k --keepParent "$APP_NAME" "$ZIP_NAME"
  else
    zip -9 -r "$ZIP_NAME" "$APP_NAME"
  fi
)
echo "==> $OUT_DIR/$ZIP_NAME"

# DMG: drag-to-Applications volume (best first-install UX)
DMG_NAME="suzuri-${VERSION}-darwin-${GOARCH}.dmg"
DMG_PATH="$OUT_DIR/$DMG_NAME"
if command -v hdiutil >/dev/null 2>&1; then
  STAGE="$OUT_DIR/dmg-stage"
  rm -rf "$STAGE" "$DMG_PATH"
  mkdir -p "$STAGE"
  # Copy app into stage (ditto for signatures)
  if command -v ditto >/dev/null 2>&1; then
    ditto "$APP_PATH" "$STAGE/$APP_NAME"
  else
    cp -R "$APP_PATH" "$STAGE/$APP_NAME"
  fi
  ln -s /Applications "$STAGE/Applications"
  hdiutil create -volname "suzuri" -srcfolder "$STAGE" -ov -format UDZO "$DMG_PATH" >/dev/null
  rm -rf "$STAGE"
  echo "==> $DMG_PATH"
else
  echo "==> hdiutil not available; skipped DMG"
fi

echo "==> done: $APP_PATH"
ls -la "$APP_PATH/Contents/MacOS/suzuri" "$RES_DIR/suzuri.icns" 2>/dev/null || true
