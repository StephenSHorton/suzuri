# Releasing rust-suzuri

Releases are cut on our machines with local scripts, not CI. The Mac builds
and publishes; the PC builds the Windows installer and hands it over through
a draft release.

## Where releases go

To the public `StephenSHorton/suzuri` repo, which rust-suzuri will take over.
Until then its releases stay out of Go suzuri's way:

- They are **prereleases** tagged `rust-v<version>`. Go suzuri's updater reads
  `releases/latest`, which skips prereleases. A `v*` tag would also start Go
  suzuri's release workflow.
- The app's update feed is `latest.json` on the moving prerelease
  `rust-latest`:
  `https://github.com/StephenSHorton/suzuri/releases/download/rust-latest/latest.json`
  (`plugins.updater.endpoints` in `app/tauri.conf.json`).

At the takeover, point the endpoint at `releases/latest/download/latest.json`
and publish full releases.

## What ships

| Platform | First install | Update payload |
|---|---|---|
| macOS (Apple silicon) | `rust-suzuri_<v>_aarch64.dmg`: signed with Developer ID, notarized and stapled | `rust-suzuri_<v>_aarch64.app.tar.gz` (the stapled app) |
| Windows x64 | `rust-suzuri_<v>_x64-setup.exe`: NSIS installer, per user (no admin), installs to `%LOCALAPPDATA%\Programs` | the same setup.exe, run passively by the updater |

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

The matching private key is `~/.tauri/rust-suzuri-updater.key` on the Mac, with
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
   `xcrun notarytool store-credentials rust-suzuri-notary --apple-id <apple id> --team-id GP6632G333`
3. `cargo install tauri-cli --version '^2'`, and `gh auth login`.

On the PC: Rust, `cargo install tauri-cli --version '^2'`, `gh auth login`.

## Cutting a release

1. PC: `powershell -File scripts\release\build-windows.ps1 -Version 0.2.0`
   builds the installer and uploads it to the draft prerelease `rust-v0.2.0`.
2. Mac: `scripts/release/publish.sh 0.2.0 --notes "what changed"`
   - builds and signs the app (hardened runtime, `packaging/macos/entitlements.plist`)
   - notarizes and staples the app, then the DMG
   - signs the macOS tarball and the Windows installer with the updater key
   - writes `latest.json` and `SHA256SUMS`, uploads, publishes the prerelease,
     and replaces `latest.json` on `rust-latest` (last, so clients only ever
     see a complete release)

`--mac-only` skips Windows. `--no-upload` stops after building into
`dist/<version>`. `--no-notarize` and `--feed-base URL` are for local tests.

The version comes from the command line as a config overlay. Nothing in the
repo needs bumping.

## Local update test (Mac)

```sh
echo '{"plugins":{"updater":{"dangerousInsecureTransportProtocol":true}}}' > /tmp/insecure.json
for v in 0.1.0 0.1.1; do
  RSZ_TAURI_CONFIG=/tmp/insecure.json scripts/release/publish.sh $v \
    --mac-only --no-notarize --no-upload --feed-base http://127.0.0.1:8787
done
(cd dist/0.1.1 && python3 -m http.server 8787 --bind 127.0.0.1 &)
mkdir -p /tmp/rsz && tar -xzf dist/0.1.0/*.app.tar.gz -C /tmp/rsz
RSZ_UPDATE_URL=http://127.0.0.1:8787/latest.json /tmp/rsz/rust-suzuri.app/Contents/MacOS/rust-suzuri
```
