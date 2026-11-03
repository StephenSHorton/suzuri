# Releasing rust-suzuri

Releases are cut on our machines with local scripts, not CI. The Mac builds
and publishes; the PC builds the Windows installer and hands it over through
a draft release.

## Where releases go

This repo is moving into `StephenSHorton/suzuri` and replaces Go suzuri there,
so the release setup is aimed at that repo: normal `v<version>` tags, and
the update feed at
`https://github.com/StephenSHorton/suzuri/releases/latest/download/latest.json`
(`plugins.updater.endpoints` in `app/tauri.conf.json`).

Nothing is published before the move. Until then updates are tested from
local feeds (below). At the move:
- the first release's version must be above Go suzuri's last one (0.9.x);
- Go suzuri's `release.yml` goes away, since these scripts replace it;
- Go suzuri installs don't read `latest.json`, so moving them over is part
  of the takeover.

## What ships

| Platform | First install | Update payload |
|---|---|---|
| macOS (Apple silicon) | `rust-suzuri_<v>_aarch64.dmg`: signed with Developer ID, notarized and stapled | `rust-suzuri_<v>_aarch64.app.tar.gz` (the stapled app) |
| Windows x64 | `rust-suzuri_<v>_x64-setup.exe`: NSIS installer, per user (no admin), installs to `%LOCALAPPDATA%\<product name>` (Tauri's per-user default; Go suzuri uses `%LOCALAPPDATA%\Programs\suzuri`, so the two don't collide before the move) | the same setup.exe, run passively by the updater |

The Windows install puts OpenConsole's `conpty.dll` and `OpenConsole.exe` next
to `rust-suzuri.exe`. portable-pty loads that `conpty.dll` before the system
one. They come from Microsoft's `Microsoft.Windows.Console.ConPTY` NuGet package
(MIT), pinned by version and hash in `scripts/release/conpty.ps1`. Go suzuri
doesn't use MSIX or the Store, so neither does this. The Windows installer is
not Authenticode-signed (Go suzuri's isn't either), so SmartScreen may warn on
first install.

## Updates

The app checks the feed 5 s after start and every 6 hours. When there is a newer
version it shows "Install and restart" and installs only when clicked, as Go
suzuri does. Every download is checked against the updater public key in
`tauri.conf.json` (`packaging/updater.pub`), and a bad signature is refused.
Debug builds don't check.

The matching private key is `~/.tauri/suzuri-updater.key` on the Mac, with
a backup in `~/Backups/tauri-keys/`. It has no password. Without it no build
can update existing installs, so keep the backup. Only the Mac signs:
the PC's installer is signed by `publish.sh`.

Test knobs: `RSZ_UPDATE_URL` (another feed), `RSZ_UPDATE_AUTO=1` (install
without asking).

## One-time setup (Mac)

1. Developer ID certificate in the login keychain
   (`security find-identity -v -p codesigning`).
2. Notary credentials as a keychain profile, using an app-specific password
   from https://account.apple.com:
   `xcrun notarytool store-credentials suzuri-notary --apple-id <apple id> --team-id GP6632G333`
3. `cargo install tauri-cli --version '^2'`, and `gh auth login`.

On the PC: Rust, `cargo install tauri-cli --version '^2'`, `gh auth login`.

## Cutting a release

1. PC: `powershell -File scripts\release\build-windows.ps1 -Version 0.2.0`
   builds the installer and uploads it to the draft release `v0.2.0`.
2. Mac: `scripts/release/publish.sh 0.2.0 --notes "what changed"`
   - builds and signs the app (hardened runtime, `packaging/macos/entitlements.plist`)
   - notarizes and staples the app, then the DMG
   - signs the macOS tarball and the Windows installer with the updater key
   - writes `latest.json` and `SHA256SUMS`, uploads, and publishes it as the latest release (clients
     see it only once it is complete)

`--mac-only` skips Windows. `--no-upload` stops after building into
`dist/<version>`. `--no-notarize` and `--feed-base URL` are for local tests.

The version comes from the command line as a config overlay. Nothing in the
repo needs bumping.

## Local update tests

These scripts build two versions, serve the newer one's feed on 127.0.0.1, start
the older one with `RSZ_UPDATE_AUTO=1`, and check that it updated itself. They
use a throwaway updater key and allow an http feed through a config overlay, so
the real key isn't needed and nothing is published.

- Mac: `scripts/release/test-update-mac.sh [old] [new]`
- PC: `powershell -File scripts\release\test-update-windows.ps1 [-Old 0.1.0] [-New 0.1.1] [-Keep]`.
  It installs silently per user, checks that `conpty.dll` and
  `OpenConsole.exe` are next to the exe, and uninstalls at the end
  unless `-Keep` is given.

The updater refuses to run from a path that goes through a symlink (on macOS,
`/var` is one). Installs in /Applications are fine.
