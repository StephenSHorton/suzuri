# Microsoft Store (MSIX) — suzuri

Free Authenticode for end users: ship **MSIX**, Microsoft **re-signs** after certification. No paid code-signing cert.

GitHub Releases (NSIS / portable `.exe`) stay as they are. This path is an **additional** channel.

## What you must do in a browser (Partner Center)

These steps need your Microsoft account — they cannot be automated from the repo.

### 1. Open a developer account

1. Go to [Partner Center](https://partner.microsoft.com/dashboard) / [Windows developer signup](https://developer.microsoft.com/microsoft-store/register).
2. Individual accounts: one-time registration fee may apply (Microsoft sets this; often ~\$19 USD historically — confirm current price). **Code signing itself stays free** for MSIX; the fee is for the publisher account, not a CA cert.
3. Complete identity verification if prompted.

### 2. Create the app and reserve the name

1. **Apps and games** → **New product** → **MSIX or PWA app**.
2. Reserve name: **suzuri** (or `suzuri terminal` if taken — then match `displayName` in identity).
3. Open the app → **Product management** → **Product identity**.
4. Copy:
   - **Package/Identity/Name** → `packageName`
   - **Package/Identity/Publisher** → `publisher` (exact `CN=…` string)
   - **Package/Properties/PublisherDisplayName** → `publisherDisplayName`

### 3. Put identity into the repo (local only)

```powershell
cd path\to\suzuri
copy packaging\windows\msix\identity.example.json packaging\windows\msix\identity.json
# edit identity.json with Partner Center values
```

`identity.json` is gitignored. Never commit real publisher CN if you prefer privacy; CI can use a secret file.

## Build the MSIX

### One-shot local (dev identity, Developer Mode)

```powershell
go test ./...
go build -ldflags "-H windowsgui -s -w -X main.version=0.9.116" -o suzuri.exe ./cmd/suzuri
cargo build --release --manifest-path chrome/Cargo.toml --bin suzuri-chrome
Copy-Item chrome\target\release\suzuri-chrome.exe .\suzuri-chrome.exe
cargo build --release --manifest-path libs/transfer/Cargo.toml -p hato-cli
Copy-Item libs\transfer\target\release\suzuri-transfer.exe .\suzuri-transfer.exe
.\packaging\windows\build-msix.ps1 -Version 0.9.116 -Exe .\suzuri.exe `
  -Transfer .\suzuri-transfer.exe -Chrome .\suzuri-chrome.exe -IdentityDev
# Fastest local test (needs Developer Mode / sideload enabled):
Add-AppxPackage -Register .\dist\msix\layout\AppxManifest.xml
```

Optional signed `.msix` install (`-SignDev`) needs the self-signed publisher cert trusted as a root (often requires elevating once to install into `LocalMachine\Root`). For Store you do **not** need this — upload the unsigned package.

### Store upload package (your Partner Center identity)

```powershell
go build -ldflags "-H windowsgui -s -w -X main.version=0.9.116" -o suzuri.exe ./cmd/suzuri
.\packaging\windows\build-msix.ps1 -Version 0.9.116 -Exe .\suzuri.exe `
  -Transfer .\suzuri-transfer.exe -Chrome .\suzuri-chrome.exe
# Upload dist\msix\suzuri-0.9.116-windows-x64.msix in Partner Center → Packages
```

**Do not** use `-SignDev` for Store upload. Partner Center accepts the package; the Store re-signs with Microsoft’s cert.

### Version numbering

| Product tag | MSIX `Identity@Version` |
| --- | --- |
| `v0.9.116` | `1.9.116.0` |

Store rules:

- Four parts: `major.minor.build.revision`
- **Revision must be `0`** (Store may rewrite it)
- **Major cannot be `0`** — the build script maps `0.x.y` → `1.x.y.0` and prints a warning

Keep tags as `v0.9.x`; only the package identity version is rewritten.

## Submit in Partner Center

1. **Start new submission**
2. **Packages** → upload `.msix`
3. Fill required Store listing fields:
   - Description, screenshots (min sizes per form), category (Developer tools / Productivity)
   - Privacy policy URL (required if the app can network — suzuri does for updates/transfer/MCP; use GitHub Pages or README section)
   - Age rating questionnaire
   - Support contact
4. **Submit** → certification (often hours to a few days)

### Screenshots

Capture from a running build (Start menu tile + main window with a shell). Store asks for specific resolutions; the form lists minima.

## Runtime differences (important)

| | GitHub portable / NSIS | Store MSIX |
| --- | --- | --- |
| Install path | `%LOCALAPPDATA%\Programs\suzuri` or anywhere | WindowsApps package folder (read-only) |
| Updates | In-app GitHub Releases | **Microsoft Store only** |
| Signing | Unsigned today (SignPath later) | Store re-signs |
| Config / logs | `%LOCALAPPDATA%\suzuri` | Same env vars; may be package-virtualized |

The app detects package identity (`GetCurrentPackageFullName`) and **disables GitHub auto-update** so Store builds do not try to overwrite themselves.

The MSIX layout must include **`suzuri.exe`**, **`suzuri-chrome.exe`**, and **`suzuri-transfer.exe`** as siblings — same as the NSIS/zip layout. The host finds chrome and transfer next to itself.

## Capabilities & certification notes

- Manifest declares **`runFullTrust`** (restricted). Expected for a real ConPTY terminal host. Certification may ask for justification — short answer: native Win32 window + ConPTY PTY requires full trust.
- No admin elevation required.
- Does not need a paid OV/EV cert for this channel.

## CI

Release workflow always builds an MSIX (dev identity unless secret `MSIX_IDENTITY_JSON` is set) and attaches `suzuri-*-windows-x64.msix` to the GitHub Release. Store submission remains a **manual Partner Center upload** of that asset (Start update → Packages → submit).

## Uninstall (sideload)

```powershell
Get-AppxPackage *suzuri* | Remove-AppxPackage
```

## Next (later)

- SignPath for GitHub Releases `.exe` / setup
- Optional Store listing polish (feature graphic, more locales)
