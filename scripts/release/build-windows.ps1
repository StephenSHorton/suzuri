# Builds the Windows installer for a release and uploads it to the draft
# release on the releases repo. Run on the PC, then run
# scripts/release/publish.sh on the Mac: it signs the installer for the
# updater, writes latest.json and publishes. The updater key stays on the Mac.
#
#   powershell -File scripts\release\build-windows.ps1 -Version 0.10.1 [-NoUpload]
#
# Needs: Rust, cargo-tauri (cargo install tauri-cli --version ^2), gh (logged
# in) unless -NoUpload. Output: dist\<version>\suzuri_<version>_x64-setup.exe
param(
  [Parameter(Mandatory = $true)][string]$Version,
  [string]$Repo = $(if ($env:RSZ_RELEASE_REPO) { $env:RSZ_RELEASE_REPO } else { "StephenSHorton/suzuri" }),
  [switch]$NoUpload,
  # One more config overlay (a JSON file), e.g. the local update test's.
  [string]$ExtraConfig = ""
)
$ErrorActionPreference = "Stop"
# The Windows VERSIONINFO resource takes only x.y.z: a pre-release suffix
# fails deep in tauri-winres ("RC.EXE"), so refuse it up front.
if ($Version -notmatch '^\d+\.\d+\.\d+$') { throw "Version must be plain x.y.z on Windows (got $Version)" }
$root = Resolve-Path (Join-Path $PSScriptRoot "..\..")
Set-Location $root

& (Join-Path $PSScriptRoot "conpty.ps1")

# Version and the bundled conpty files come in as a config overlay, so dev
# builds never need the conpty files. Updater signing happens on the Mac.
$overlay = @{
  version = $Version
  bundle  = @{
    createUpdaterArtifacts = $false
    resources = @{
      "../packaging/windows/conpty/conpty.dll"      = "conpty.dll"
      "../packaging/windows/conpty/OpenConsole.exe" = "OpenConsole.exe"
    }
  }
} | ConvertTo-Json -Depth 5 -Compress
$overlayFile = Join-Path ([IO.Path]::GetTempPath()) "rsz-release-$Version.json"
# UTF-8 without a BOM (PowerShell 5's -Encoding utf8 writes one).
[IO.File]::WriteAllText($overlayFile, $overlay, (New-Object Text.UTF8Encoding $false))

Push-Location app
try {
  $cfg = @("--config", $overlayFile)
  if ($ExtraConfig) { $cfg += @("--config", (Resolve-Path $ExtraConfig).Path) }
  cargo tauri build --bundles nsis @cfg
  if ($LASTEXITCODE -ne 0) { throw "cargo tauri build failed: $LASTEXITCODE" }
} finally { Pop-Location }

$setup = Get-ChildItem "target\release\bundle\nsis\*_${Version}_x64-setup.exe" | Select-Object -First 1
if (-not $setup) { throw "no setup.exe for $Version under target\release\bundle\nsis" }
$dist = Join-Path $root "dist\$Version"
New-Item -ItemType Directory -Force $dist | Out-Null
$name = "suzuri_${Version}_x64-setup.exe"
Copy-Item $setup.FullName (Join-Path $dist $name) -Force
Write-Host "==> $dist\$name"

if ($NoUpload) { return }
$tag = "v$Version"
# PowerShell 5.1 under "Stop" turns gh's stderr ("release not found") into
# a terminating error, so check for the draft with errors allowed.
$prev = $ErrorActionPreference
$ErrorActionPreference = "Continue"
gh release view $tag -R $Repo *> $null
$found = $LASTEXITCODE -eq 0
$ErrorActionPreference = $prev
if (-not $found) {
  gh release create $tag -R $Repo --draft --target main --title "suzuri $Version" --notes "suzuri $Version"
  if ($LASTEXITCODE -ne 0) { throw "gh release create failed" }
}
gh release upload $tag (Join-Path $dist $name) -R $Repo --clobber
if ($LASTEXITCODE -ne 0) { throw "gh release upload failed" }
Write-Host "==> uploaded $name to $Repo $tag (draft). Next on the Mac: scripts/release/publish.sh $Version"
