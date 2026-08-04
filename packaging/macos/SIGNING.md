# macOS code signing & notarization

Distribution builds should use a **Developer ID Application** certificate from your paid Apple Developer account, then **notarize** with Apple.

## One-time: create the certificate (your Mac)

1. Open [Apple Developer → Certificates](https://developer.apple.com/account/resources/certificates/list).
2. **+** → **Developer ID Application** → follow CSR flow (Keychain Access → Certificate Assistant → Request from CA).
3. Download the `.cer` and double-click to install into **login** keychain.
4. Confirm:
   ```bash
   security find-identity -v -p codesigning
   # expect: Developer ID Application: Your Name (TEAMID)
   ```

## One-time: export for GitHub Actions

```bash
# Export the Developer ID Application cert + private key as .p12
# Keychain Access → My Certificates → right-click cert → Export…
# Or:
# security export -t identities -f pkcs12 -k ~/Library/Keychains/login.keychain-db -o suzuri-dev-id.p12

# Base64 for a secret (no line breaks):
base64 -i suzuri-dev-id.p12 | pbcopy
```

### Notarization credentials (pick one)

**A. App-specific password (simple)**  
1. [appleid.apple.com](https://appleid.apple.com) → Sign-In → App-Specific Passwords → generate for “suzuri notary”.  
2. Store as secrets below.

**B. App Store Connect API key (preferred for CI)**  
1. [App Store Connect → Users and Access → Integrations → Team Keys](https://appstoreconnect.apple.com/access/integrations/api).  
2. Create key with **Developer** access; download `.p8` once.  
3. Note Key ID + Issuer ID.

## GitHub repository secrets

| Secret | Value |
|--------|--------|
| `MACOS_CERTIFICATE_P12_BASE64` | `base64` of the `.p12` |
| `MACOS_CERTIFICATE_PASSWORD` | Password you set on the `.p12` |
| `MACOS_CODESIGN_IDENTITY` | Full name, e.g. `Developer ID Application: Stephen Horton (XXXXXXXXXX)` |
| `APPLE_TEAM_ID` | 10-char Team ID |

**If using app-specific password:**

| Secret | Value |
|--------|--------|
| `APPLE_ID` | Your Apple ID email |
| `APPLE_APP_SPECIFIC_PASSWORD` | `xxxx-xxxx-xxxx-xxxx` |

**If using API key instead:**

| Secret | Value |
|--------|--------|
| `APPLE_API_KEY_ID` | Key ID |
| `APPLE_API_ISSUER_ID` | Issuer UUID |
| `APPLE_API_KEY_P8_BASE64` | `base64` of the `.p8` file |

Release CI signs + notarizes only when `MACOS_CERTIFICATE_P12_BASE64` is set. Without it, macOS builds stay ad-hoc (current behavior).

## Local signed build

```bash
export SUZURI_CODESIGN_IDENTITY="Developer ID Application: Your Name (TEAMID)"
# optional local notarization:
export APPLE_ID="you@example.com"
export APPLE_TEAM_ID="XXXXXXXXXX"
export APPLE_APP_SPECIFIC_PASSWORD="xxxx-xxxx-xxxx-xxxx"

go build -ldflags "-s -w -X main.version=dev" -o suzuri ./cmd/suzuri
./tools/build-transfer.sh
packaging/macos/build-app.sh \
  --binary ./suzuri \
  --transfer ./libs/transfer/target/release/suzuri-transfer \
  --version 0.0.0-dev \
  --out dist
```

## Verify

```bash
codesign -dv --verbose=4 dist/suzuri.app
spctl -a -vv dist/suzuri.app   # after notarization + staple
xcrun stapler validate dist/suzuri.app
```

## In-app updates

Release installers (`.app` / `.dmg`) are notarized. Portable in-app updates replace the Mach-O and re-sign with the previous identity when possible; a full notarized re-staple only happens on the next GitHub Release package. Users who install from the DMG/app.zip get Gatekeeper-clean first launch.
