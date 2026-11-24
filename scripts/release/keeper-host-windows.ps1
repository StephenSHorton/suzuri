# Builds rsz-keeper-host for Windows x64 (card hearth#190) and uploads it to
# the draft release keeper-v<version> (the version in
# crates/keeper-host/Cargo.toml). Run on the PC, then on the Mac
# scripts/release/keeper-host.sh, which builds macOS and Linux and publishes.
#
#   powershell -File scripts\release\keeper-host-windows.ps1 [-NoUpload]
#
# The zip holds rsz-keeper-host.exe with OpenConsole's conpty.dll and
# OpenConsole.exe beside it (conpty.ps1, the same ones the app installs):
# the host loads that conpty.dll before the system one. Keep the three
# together. Before it uploads, scripts\keeper\host-check.ps1 runs a shell in
# the built host. Needs: Rust, Zig 0.17.0, gh (logged in) unless -NoUpload.
# Output: dist\keeper-host\<version>\rsz-keeper-host-<version>-x86_64-pc-windows-msvc.zip
param(
  [string]$Repo = $(if ($env:RSZ_RELEASE_REPO) { $env:RSZ_RELEASE_REPO } else { "StephenSHorton/suzuri" }),
  [switch]$NoUpload
)
$ErrorActionPreference = "Stop"
$root = Resolve-Path (Join-Path $PSScriptRoot "..\..")
Set-Location $root
$m = Select-String -Path "crates\keeper-host\Cargo.toml" -Pattern '^version = "(.+)"' | Select-Object -First 1
if (-not $m) { throw "no version in crates\keeper-host\Cargo.toml" }
$Version = $m.Matches[0].Groups[1].Value
$tag = "keeper-v$Version"
$name = "rsz-keeper-host"
$triple = "x86_64-pc-windows-msvc"

& (Join-Path $PSScriptRoot "conpty.ps1")

# Its own target folder: the stripped profile must not rebuild the app's.
$td = Join-Path $root "target\keeper-host"
$env:CARGO_PROFILE_RELEASE_DEBUG = "0"
$env:CARGO_PROFILE_RELEASE_STRIP = "symbols"
cargo build --release --locked --target-dir $td -p rsz-keeper-host -p rsz-keeper
if ($LASTEXITCODE -ne 0) { throw "cargo build failed: $LASTEXITCODE" }

$dist = Join-Path $root "dist\keeper-host\$Version"
$stage = Join-Path $dist $triple
Remove-Item -Recurse -Force $stage -ErrorAction SilentlyContinue
New-Item -ItemType Directory -Force $stage | Out-Null
Copy-Item (Join-Path $td "release\$name.exe") $stage
Copy-Item "packaging\windows\conpty\conpty.dll", "packaging\windows\conpty\OpenConsole.exe" $stage

powershell -NoProfile -File (Join-Path $root "scripts\keeper\host-check.ps1") -HostExe (Join-Path $stage "$name.exe") -KeeperExe (Join-Path $td "release\rsz-keeper.exe")
if ($LASTEXITCODE -ne 0) { throw "host-check failed" }

$zip = Join-Path $dist "$name-$Version-$triple.zip"
Remove-Item -Force $zip -ErrorAction SilentlyContinue
Compress-Archive -Path (Join-Path $stage "*") -DestinationPath $zip
Write-Host "==> $zip"

if ($NoUpload) { return }
# PowerShell 5.1 under "Stop" turns gh's stderr ("release not found") into
# a terminating error, so check for the draft with errors allowed.
$prev = $ErrorActionPreference
$ErrorActionPreference = "Continue"
$draft = gh release view $tag -R $Repo --json isDraft -q .isDraft 2>$null
$found = $LASTEXITCODE -eq 0
$ErrorActionPreference = $prev
if ($found -and $draft -ne "true") { throw "$tag is already released; bump crates\keeper-host\Cargo.toml" }
if (-not $found) {
  gh release create $tag -R $Repo --draft --latest=false --target main --title "rsz-keeper-host $Version ($tag)" --notes "rsz-keeper-host $Version"
  if ($LASTEXITCODE -ne 0) { throw "gh release create failed" }
}
gh release upload $tag $zip -R $Repo --clobber
if ($LASTEXITCODE -ne 0) { throw "gh release upload failed" }
Write-Host "==> uploaded $name-$Version-$triple.zip to $Repo $tag (draft). Next on the Mac: scripts/release/keeper-host.sh"
