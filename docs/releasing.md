# Releasing suzuri

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
| macOS (Apple silicon) | `suzuri_<v>_aarch64.dmg`: signed with Developer ID, notarized and stapled | `suzuri_<v>_aarch64.app.tar.gz` (the stapled app) |
| Windows x64 | `suzuri_<v>_x64-setup.exe`: NSIS installer, per user (no admin), installs to `%LOCALAPPDATA%\Programs\suzuri`, replacing Go suzuri 0.9.x there (packaging/windows/installer-hooks.nsh: it keeps Tauri's reinstall page from running Go's Uninstall.exe, takes over the `Uninstall\suzuri` key, then removes Go's leftovers; `%LOCALAPPDATA%\suzuri`, the config folder, is never touched). Versions start at 0.10.0 so they sort above Go's 0.9.x | the same setup.exe, run passively by the updater |

suzuri-view (`ui/view`, the terminal view and the pane-tree rules other apps
embed) is released apart from the app, by `scripts/release/view.sh`: it packs
`suzuri-view-<v>.tgz` and puts it on its own release tagged `view-v<v>`, the
version in `ui/view/package.json`. Each version is released once and never
replaced, so bump that version first. 0.1.0 came out on `term-v0.10.0`.
Since 0.3.0 it also carries suzuri's Settings window and command palette
(`suzuri-view/settings`, `suzuri-view/palette`): `npm pack` runs the
package's `prepack` (`ui/view/build.mjs`), which compiles their JSX to
`ui/view/dist` with Vite from `ui/node_modules`, so run `npm ci` in `ui/`
first.

rsz-keeper-host (`crates/keeper-host`, hearth#190) is the shell keeper's
host as its own small program (4 to 6 MB stripped; no Tauri, no window), for
apps that keep shells without Suzuri installed: Hearth bundles it. It is the
same code as `suzuri --keeper-host`, takes the same arguments
(`rsz-keeper-host [--keeper-host] <dir> <id>`, config as JSON on stdin,
`ready <pid>` on stdout) and speaks the same wire (`crates/keeper`). It is
released apart from the app, on its own release tagged `keeper-v<v>`, the
version in `crates/keeper-host/Cargo.toml`; each version is released once and
never replaced, so bump that version first. Assets, one per Rust target
triple:

| Asset | Built | Notes |
|---|---|---|
| `rsz-keeper-host-<v>-aarch64-apple-darwin.tar.gz` | Mac | signed with Developer ID (hardened runtime) and notarized; a bare program can't be stapled |
| `rsz-keeper-host-<v>-x86_64-apple-darwin.tar.gz` | Mac, cross-built | the same |
| `rsz-keeper-host-<v>-x86_64-unknown-linux-gnu.tar.gz` | Mac, cross-built with `cargo zigbuild` against glibc 2.31 | needs only glibc 2.31+ (Zig's libc++ is linked in); tried in a bare `debian:bullseye-slim`. Not built in an amd64 container: Zig crashes under Rosetta there |
| `rsz-keeper-host-<v>-x86_64-pc-windows-msvc.zip` | PC | `rsz-keeper-host.exe` with `conpty.dll` and `OpenConsole.exe` beside it (`conpty.ps1`); keep the three together |

On Windows a running exe can't be replaced, so an app that ships it should
start hosts from a copy, as Suzuri does with `suzuri-keeper.exe`
(`app/src/keeper.rs`, `host_exe`). The release is never marked Latest
(`--latest=false`): the app's update feed is `releases/latest`, so only an
app release may hold Latest.

To cut one:
1. PC: `powershell -File scripts\release\keeper-host-windows.ps1` builds the
   zip, runs a shell in it (`scripts\keeper\host-check.ps1`) and uploads it
   to the draft `keeper-v<v>`.
2. Mac: `scripts/release/keeper-host.sh` builds both Mac chips and Linux,
   signs and notarizes the Mac ones, runs a shell in each with nothing else
   installed (`scripts/keeper/host-check.sh`; Linux in a bare
   `debian:bullseye-slim`), writes `SHA256SUMS` over all four, uploads and
   publishes. `--no-upload` stops after building into
   `dist/keeper-host/<v>`; `--no-notarize`, `--no-sign`, `--no-linux` and
   `--mac-only` are for local tries. Needs `rustup target add
   x86_64-apple-darwin x86_64-unknown-linux-gnu`, `cargo install
   cargo-zigbuild` and Docker, besides the setup below.

The Windows install puts OpenConsole's `conpty.dll` and `OpenConsole.exe` next
to `suzuri.exe`. portable-pty loads that `conpty.dll` before the system
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
2. Notary credentials as the keychain profile `irori-notary`, the one Irori's
   releases use (same Apple team; `publish.sh` uses it by default, or the one
   named in `RSZ_NOTARY_PROFILE`). Check it with
   `xcrun notarytool history --keychain-profile irori-notary`. If it is
   missing, make it with an app-specific password from https://account.apple.com:
   `xcrun notarytool store-credentials irori-notary --apple-id <apple id> --team-id GP6632G333`
3. `cargo install tauri-cli --version '^2'`, and `gh auth login`.
4. Zig, exactly 0.17.0, for libghostty-vt (README, Build). On the Mac it is
   `~/.local/zig/zig-aarch64-macos-0.17.0/zig`, linked as `~/.local/bin/zig`
   so it is on PATH (`zig version` must print `0.17.0`; `ZIG` can name
   another binary). Not Homebrew's: it ships 0.17.1, which the build refuses.

On the PC: Rust, `cargo install tauri-cli --version '^2'`, `gh auth login`,
and Zig 0.17.0 (the zip from https://ziglang.org/download/, on PATH or as
`ZIG`).

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
- PC: `powershell -File scripts\release\test-update-windows.ps1 [-Old 0.10.0] [-New 0.10.1] [-Keep]`.
  It installs silently per user, checks that `conpty.dll` and
  `OpenConsole.exe` are next to the exe, and uninstalls at the end
  unless `-Keep` is given.

The updater refuses to run from a path that goes through a symlink (on macOS,
`/var` is one). Installs in /Applications are fine.
