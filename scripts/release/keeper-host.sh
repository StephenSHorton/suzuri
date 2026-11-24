#!/usr/bin/env bash
# Releases rsz-keeper-host (crates/keeper-host, card hearth#190): the shell
# keeper's host as its own small program, for apps that ship it without
# Suzuri (Hearth). Not an app release: it goes on its own GitHub release,
# tagged keeper-v<version> (the version in crates/keeper-host/Cargo.toml),
# never marked Latest, so the app's update feed (releases/latest) stays on
# the app. A version is released once and never replaced: bump it first.
#
#   PC:  powershell -File scripts\release\keeper-host-windows.ps1
#        (builds the Windows zip, uploads it to the draft keeper-v<version>)
#   Mac: scripts/release/keeper-host.sh
#        builds macOS (Apple silicon and Intel; signed with Developer ID,
#        hardened runtime, notarized) and Linux x64 (cross-built), checks each
#        one hosts a shell on its own (scripts/keeper/host-check.sh; Linux in
#        a bare Debian container), writes SHA256SUMS over every asset,
#        uploads and publishes the release.
#
# --no-upload    build into dist/keeper-host/<version> and stop
# --no-notarize  sign but don't send to Apple (local tries)
# --no-sign      neither sign nor notarize (local tries)
# --no-linux     skip the Linux build
# --mac-only     don't wait for the Windows zip on the draft
#
# Needs: the one-time Mac setup in docs/releasing.md (Developer ID, the
# notary profile, Zig 0.17.0, gh logged in), the x86_64-apple-darwin Rust
# and Linux Rust targets (rustup target add x86_64-apple-darwin
# x86_64-unknown-linux-gnu), cargo-zigbuild (cargo install cargo-zigbuild),
# and Docker to try the Linux build.
set -euo pipefail

UPLOAD=1 NOTARIZE=1 SIGN=1 LINUX=1 WINDOWS=1
for a in "$@"; do
  case "$a" in
    --no-upload) UPLOAD=0 ;;
    --no-notarize) NOTARIZE=0 ;;
    --no-sign) SIGN=0 NOTARIZE=0 ;;
    --no-linux) LINUX=0 ;;
    --mac-only) WINDOWS=0 ;;
    *) echo "unknown option: $a" >&2; exit 2 ;;
  esac
done
REPO="${RSZ_RELEASE_REPO:-StephenSHorton/suzuri}"
PROFILE="${RSZ_NOTARY_PROFILE:-irori-notary}"
ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
VERSION="$(sed -n 's/^version = "\(.*\)"/\1/p' "$ROOT/crates/keeper-host/Cargo.toml" | head -1)"
[[ -n "$VERSION" ]] || { echo "error: no version in crates/keeper-host/Cargo.toml" >&2; exit 1; }
TAG="keeper-v$VERSION"
NAME="rsz-keeper-host"
OUT="$ROOT/dist/keeper-host/$VERSION"
# Its own target folder: the stripped release profile below must not
# rebuild the app's.
TD="$ROOT/target/keeper-host"
export CARGO_PROFILE_RELEASE_DEBUG=0 CARGO_PROFILE_RELEASE_STRIP=symbols

if [[ $UPLOAD == 1 ]]; then
  [[ -z "$(git -C "$ROOT" status --porcelain)" ]] || { echo "error: the checkout has changes; release a clean commit" >&2; exit 1; }
  if gh release view "$TAG" -R "$REPO" --json isDraft -q .isDraft 2>/dev/null | grep -q false; then
    echo "error: $TAG is already released; bump crates/keeper-host/Cargo.toml" >&2
    exit 1
  fi
fi

rm -rf "$OUT" && mkdir -p "$OUT"
echo "==> $NAME $VERSION into $OUT"

if [[ $SIGN == 1 ]]; then
  IDENTITY="${APPLE_SIGNING_IDENTITY:-$(security find-identity -v -p codesigning | sed -n 's/.*"\(Developer ID Application: [^"]*\)".*/\1/p' | head -1)}"
  [[ -n "$IDENTITY" ]] || { echo "error: no Developer ID Application identity in the keychain" >&2; exit 1; }
  echo "==> signing as: $IDENTITY"
fi

# ---- macOS, both chips
MAC_BINS=()
for t in aarch64-apple-darwin x86_64-apple-darwin; do
  cargo build -q --release --locked --target "$t" --target-dir "$TD" -p rsz-keeper-host -p rsz-keeper --manifest-path "$ROOT/Cargo.toml"
  mkdir -p "$OUT/$t"
  cp "$TD/$t/release/$NAME" "$OUT/$t/$NAME"
  if [[ $SIGN == 1 ]]; then
    codesign --force --sign "$IDENTITY" --options runtime --timestamp "$OUT/$t/$NAME"
    codesign --verify --strict "$OUT/$t/$NAME"
  fi
  # A kept shell, typed into and read back, with nothing else installed.
  "$ROOT/scripts/keeper/host-check.sh" "$OUT/$t/$NAME" "$TD/$t/release/rsz-keeper" | tail -1
  MAC_BINS+=("$OUT/$t/$NAME")
done
if [[ $NOTARIZE == 1 ]]; then
  # A bare program can't be stapled; Gatekeeper finds the ticket online,
  # and an app that bundles it (Hearth) is notarized with it inside.
  (cd "$OUT" && zip -q notarize.zip aarch64-apple-darwin/$NAME x86_64-apple-darwin/$NAME)
  xcrun notarytool submit "$OUT/notarize.zip" --keychain-profile "$PROFILE" --wait --timeout "${RSZ_NOTARY_TIMEOUT:-90m}"
  rm "$OUT/notarize.zip"
fi
for t in aarch64-apple-darwin x86_64-apple-darwin; do
  tar -C "$OUT/$t" -czf "$OUT/$NAME-$VERSION-$t.tar.gz" "$NAME"
done

# ---- Linux x64, cross-built here with Zig as the linker (cargo-zigbuild),
# against glibc 2.31 and with Zig's libc++ linked in: it needs only glibc.
# (Building inside an amd64 container instead fails on Apple silicon: Zig
# crashes under Rosetta, "bss_size overflow".)
if [[ $LINUX == 1 ]]; then
  t=x86_64-unknown-linux-gnu
  mkdir -p "$OUT/$t"
  cargo zigbuild -q --release --locked --target "$t.2.31" --target-dir "$TD" -p rsz-keeper-host -p rsz-keeper --manifest-path "$ROOT/Cargo.toml"
  cp "$TD/$t/release/$NAME" "$OUT/$t/$NAME"
  # On a bare Debian 11 (glibc 2.31) with no compilers or dev libraries.
  docker run --rm --platform linux/amd64 -v "$ROOT/scripts/keeper":/check:ro -v "$OUT/$t":/bin-in:ro \
    -v "$TD/$t/release/rsz-keeper":/keeper-in/rsz-keeper:ro \
    debian:bullseye-slim sh /check/host-check.sh /bin-in/$NAME /keeper-in/rsz-keeper | tail -1
  tar -C "$OUT/$t" -czf "$OUT/$NAME-$VERSION-$t.tar.gz" "$NAME"
fi

# ---- Windows x64, from the PC's draft
if [[ $UPLOAD == 1 && $WINDOWS == 1 ]]; then
  WZ="$NAME-$VERSION-x86_64-pc-windows-msvc.zip"
  gh release download "$TAG" -R "$REPO" -p "$WZ" -D "$OUT" ||
    { echo "error: $WZ is not on the draft $TAG; run keeper-host-windows.ps1 on the PC first, or pass --mac-only" >&2; exit 1; }
fi

(cd "$OUT" && shasum -a 256 $NAME-$VERSION-* >SHA256SUMS && cat SHA256SUMS)
[[ $UPLOAD == 1 ]] || { echo "==> built into $OUT (not uploaded)"; exit 0; }

HEAD_SHA="$(git -C "$ROOT" rev-parse HEAD)"
NOTES="rsz-keeper-host $VERSION: suzuri's shell keeper host as its own program, for apps that keep shells without Suzuri installed (Hearth). Not an app release.

It takes the same arguments as \`suzuri --keeper-host <dir> <id>\` (config as JSON on stdin, prints \`ready <pid>\`) and speaks the same wire (crates/keeper, rsz-keeper). One archive per Rust target triple; the Windows zip has OpenConsole's conpty.dll and OpenConsole.exe beside the exe, which must stay together. macOS builds are signed with Developer ID and notarized. Linux needs only glibc 2.31 or newer.

Each asset is uploaded once and never replaced."
ASSETS=("$OUT"/$NAME-$VERSION-*.tar.gz "$OUT/SHA256SUMS")
if gh release view "$TAG" -R "$REPO" >/dev/null 2>&1; then
  gh release upload "$TAG" -R "$REPO" --clobber "${ASSETS[@]}"
  gh release edit "$TAG" -R "$REPO" --target "$HEAD_SHA" --title "rsz-keeper-host $VERSION ($TAG)" --notes "$NOTES" --draft=false --latest=false
else
  gh release create "$TAG" -R "$REPO" --target "$HEAD_SHA" --title "rsz-keeper-host $VERSION ($TAG)" --notes "$NOTES" --latest=false "${ASSETS[@]}"
fi
echo "==> published https://github.com/$REPO/releases/tag/$TAG"
