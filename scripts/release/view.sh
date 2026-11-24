#!/usr/bin/env bash
# Releases suzuri-view (ui/view) for embedders such as Hearth and Irori: packs
# it with npm and puts the tarball on its own GitHub release, tagged
# view-v<version> (the version in ui/view/package.json) on this commit. Not
# an app release: publish.sh does those. A version is released once and never
# replaced, so bump ui/view/package.json for each new one. The first,
# suzuri-view 0.1.0, came out on term-v0.10.0.
#
#   scripts/release/view.sh [--no-upload]
#
# --no-upload  pack into dist/view and stop
#
# Needs: node and npm, `npm ci` done in ui/ (packing builds the Settings
# window and palette with its Vite: ui/view/build.mjs), gh logged in, a clean
# checkout of a pushed commit.
set -euo pipefail

UPLOAD=1
[[ "${1:-}" == "--no-upload" ]] && UPLOAD=0
REPO="${RSZ_RELEASE_REPO:-StephenSHorton/suzuri}"
ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
VERSION="$(cd "$ROOT/ui/view" && node -p 'require("./package.json").version')"
TAG="view-v$VERSION"
TGZ="suzuri-view-$VERSION.tgz"
OUT="$ROOT/dist/view"

rm -rf "$OUT" && mkdir -p "$OUT"
(cd "$ROOT/ui/view" && npm pack --silent --pack-destination "$OUT" >/dev/null)
[[ -f "$OUT/$TGZ" ]] || { echo "error: $TGZ was not packed" >&2; exit 1; }
(cd "$OUT" && shasum -a 256 "$TGZ" >SHA256SUMS)
tar -tzf "$OUT/$TGZ" | sed 's/^/  /'

[[ $UPLOAD == 1 ]] || { echo "==> packed $OUT/$TGZ (not uploaded)"; exit 0; }

[[ -z "$(git -C "$ROOT" status --porcelain)" ]] || { echo "error: the checkout has changes; release a clean commit" >&2; exit 1; }
HEAD_SHA="$(git -C "$ROOT" rev-parse HEAD)"
if gh release view "$TAG" -R "$REPO" >/dev/null 2>&1; then
  echo "error: $TAG is already released; bump ui/view/package.json" >&2
  exit 1
fi
NOTES="For embedders such as Hearth and Irori: the suzuri-view package (ui/view) and, on the same commit, the rsz-term crate (crates/term), so their frame FORMAT_VERSION matches. Not an app release.

suzuri-view/settings and suzuri-view/palette are suzuri's own Settings window and command palette, built for React + Astryx hosts (README, Settings window and command palette).

The asset $TGZ is \`npm pack\` of ui/view at this tag. It is uploaded once and never replaced."
gh release create "$TAG" -R "$REPO" --target "$HEAD_SHA" --title "suzuri-view $VERSION ($TAG)" --notes "$NOTES" \
  "$OUT/$TGZ" "$OUT/SHA256SUMS"
echo "==> published https://github.com/$REPO/releases/tag/$TAG"
