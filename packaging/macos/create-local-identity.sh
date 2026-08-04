#!/usr/bin/env bash
# Create a *stable* self-signed code-signing identity for local suzuri builds.
#
# Why: macOS TCC (Desktop / Documents / Downloads prompts) remembers grants by
# code signature. Ad-hoc ("-") and linker-signed binaries get a new CDHash on
# every rebuild/update, so macOS asks again. A self-signed cert with a fixed
# common name gives a stable Team/cert identity so "Allow" sticks across
# rebuilds of *your* machine's builds.
#
# This does NOT replace Apple Developer ID / notarization for distribution.
# Gatekeeper will still warn strangers; it's for you, the developer.
#
# Usage:
#   packaging/macos/create-local-identity.sh
#   export SUZURI_CODESIGN_IDENTITY="Suzuri Local"
#   # then build-app.sh, or codesign your installed .app:
#   codesign --force --deep --sign "Suzuri Local" \
#     --identifier com.stephenshorton.suzuri /Applications/suzuri.app
set -euo pipefail

NAME="${SUZURI_LOCAL_IDENTITY_NAME:-Suzuri Local}"

if security find-identity -v -p codesigning 2>/dev/null | grep -Fq "$NAME"; then
  echo "OK: codesigning identity already exists: $NAME"
  security find-identity -v -p codesigning | grep -F "$NAME" || true
  echo
  echo "Use it with:"
  echo "  export SUZURI_CODESIGN_IDENTITY=\"$NAME\""
  exit 0
fi

echo "==> creating self-signed codesigning certificate: $NAME"
# Certificate Assistant via CLI (macOS): generate a code-signing cert in login keychain.
# -T /usr/bin/codesign allows codesign without unlocking UI every time (still may prompt once).
TMP="$(mktemp -t suzuri-codesign.XXXXXX)"
trap 'rm -f "$TMP"' EXIT

cat >"$TMP" <<EOF
[ req ]
default_bits       = 2048
distinguished_name = md
prompt             = no
x509_extensions    = ext

[ md ]
CN = $NAME
O  = Suzuri Local Signing
C  = US

[ ext ]
basicConstraints       = critical,CA:false
keyUsage               = critical,digitalSignature
extendedKeyUsage       = critical,codeSigning
EOF

PEM="$(mktemp -t suzuri-codesign-pem.XXXXXX)"
KEY="$(mktemp -t suzuri-codesign-key.XXXXXX)"
P12="$(mktemp -t suzuri-codesign-p12.XXXXXX)"
trap 'rm -f "$TMP" "$PEM" "$KEY" "$P12"' EXIT

openssl req -x509 -newkey rsa:2048 -keyout "$KEY" -out "$PEM" \
  -days 3650 -nodes -config "$TMP" >/dev/null 2>&1

# Import into login keychain as a codesigning identity (empty passphrase on p12 for local use).
PASS="suzuri-local"
openssl pkcs12 -export -inkey "$KEY" -in "$PEM" -out "$P12" \
  -name "$NAME" -passout "pass:$PASS" >/dev/null 2>&1

security import "$P12" -k ~/Library/Keychains/login.keychain-db \
  -P "$PASS" -T /usr/bin/codesign -T /usr/bin/security >/dev/null

# Allow codesign to use the key without UI prompt (best-effort; may need one ACL approve).
security set-key-partition-list -S apple-tool:,apple:,codesign: -s -k "" \
  ~/Library/Keychains/login.keychain-db >/dev/null 2>&1 || true

echo "==> installed. Available codesigning identities:"
security find-identity -v -p codesigning | grep -F "$NAME" || security find-identity -v -p codesigning

echo
echo "Next:"
echo "  export SUZURI_CODESIGN_IDENTITY=\"$NAME\""
echo "  # optional: add that export to ~/.zshrc"
echo "  codesign --force --deep --sign \"\$SUZURI_CODESIGN_IDENTITY\" \\"
echo "    --identifier com.stephenshorton.suzuri /Applications/suzuri.app"
