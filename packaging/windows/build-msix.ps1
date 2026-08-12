# Build a Store-ready (or dev-sideload) MSIX for suzuri.
#
# Prerequisites:
#   - Windows 10/11 SDK (makeappx.exe, optionally signtool.exe)
#   - Built suzuri.exe (windowsgui release binary)
#   - packaging/windows/msix/identity.json  (copy from identity.example.json)
#     OR use -IdentityDev for local sideload identity
#
# Usage (from repo root):
#   go build -ldflags "-H windowsgui -s -w -X main.version=0.9.116" -o suzuri.exe ./cmd/suzuri
#   cargo build --release --manifest-path chrome/Cargo.toml --bin suzuri-chrome
#   cargo build --release --manifest-path libs/transfer/Cargo.toml -p hato-cli
#   .\packaging\windows\build-msix.ps1 -Version 0.9.116 -Exe .\suzuri.exe `
#     -Transfer .\suzuri-transfer.exe -Chrome .\suzuri-chrome.exe -SignDev
#
# Store upload:
#   1. Create Partner Center app, reserve name "suzuri"
#   2. Copy Product identity -> packaging/windows/msix/identity.json
#   3. .\packaging\windows\build-msix.ps1 -Version X.Y.Z -Exe .\suzuri.exe
#   4. Upload the .msix in Partner Center (Store re-signs; no CA cert needed)
#
# See packaging/windows/STORE.md

[CmdletBinding()]
param(
  [Parameter(Mandatory = $true)]
  [string]$Version,

  [Parameter(Mandatory = $true)]
  [string]$Exe,

  # Sidecars next to suzuri.exe (ResolveBinary sibling lookup). Required for a
  # working Store build of current master (native chrome + transfer).
  [string]$Transfer = "",
  [string]$Chrome = "",

  [string]$OutDir = "dist\msix",

  [ValidateSet("x64", "arm64", "x86")]
  [string]$Arch = "x64",

  [string]$IdentityFile = "",

  # Use packaging/windows/msix/identity.dev.json (local testing only).
  [switch]$IdentityDev,

  # Create/install a self-signed cert matching identity publisher and sign the MSIX.
  # Required for local sideload. Not required for Store upload (Store re-signs).
  [switch]$SignDev,

  # Skip makeappx and only prepare the layout (for debugging).
  [switch]$LayoutOnly
)

$ErrorActionPreference = "Stop"
$Root = (Resolve-Path (Join-Path $PSScriptRoot "..\..")).Path
Set-Location $Root

function Find-SdkTool([string]$Name) {
  $kits = Join-Path ${env:ProgramFiles(x86)} "Windows Kits\10\bin"
  if (-not (Test-Path $kits)) {
    throw "Windows SDK not found under $kits. Install Windows 10/11 SDK (makeappx)."
  }
  $found = Get-ChildItem $kits -Recurse -Filter $Name -ErrorAction SilentlyContinue |
    Where-Object { $_.DirectoryName -match '\\x64$' } |
    Sort-Object FullName -Descending |
    Select-Object -First 1
  if (-not $found) {
    throw "Could not find $Name under $kits"
  }
  return $found.FullName
}

function ConvertTo-StoreVersion([string]$v) {
  # Store requires 4-part version; last component MUST be 0.
  $v = $v.TrimStart('v', 'V')
  $parts = $v.Split('.')
  if ($parts.Count -lt 1) { throw "invalid version: $v" }
  $nums = @()
  foreach ($p in $parts) {
    if ($p -notmatch '^\d+$') {
      # strip pre-release suffix like 0.9.65-beta
      if ($p -match '^(\d+)') { $p = $Matches[1] } else { throw "invalid version segment: $p" }
    }
    $n = [int]$p
    if ($n -lt 0 -or $n -gt 65535) { throw "version segment out of range: $n" }
    $nums += $n
  }
  while ($nums.Count -lt 3) { $nums += 0 }
  if ($nums.Count -gt 3) { $nums = $nums[0..2] }
  if ($nums[0] -eq 0) {
    # Store: first section cannot be 0 - bump to 1 for 0.x.y product versions.
    # Keep product meaning by encoding 0.9.65 -> 1.9.65.0 (document in STORE.md).
    Write-Warning "Store forbids major version 0; using 1.$($nums[1]).$($nums[2]).0 (product still $v)"
    $nums[0] = 1
  }
  return "{0}.{1}.{2}.0" -f $nums[0], $nums[1], $nums[2]
}

# --- identity ---
if ($IdentityDev) {
  $IdentityFile = Join-Path $Root "packaging\windows\msix\identity.dev.json"
} elseif (-not $IdentityFile) {
  $prod = Join-Path $Root "packaging\windows\msix\identity.json"
  $dev = Join-Path $Root "packaging\windows\msix\identity.dev.json"
  if (Test-Path $prod) { $IdentityFile = $prod }
  elseif (Test-Path $dev) {
    Write-Warning "identity.json missing; falling back to identity.dev.json (not for Store upload)"
    $IdentityFile = $dev
  } else {
    throw "No identity file. Copy packaging/windows/msix/identity.example.json -> identity.json (Partner Center values), or pass -IdentityDev."
  }
}
if (-not (Test-Path $IdentityFile)) { throw "Identity file not found: $IdentityFile" }
$identity = Get-Content $IdentityFile -Raw | ConvertFrom-Json
foreach ($req in @("packageName", "publisher", "publisherDisplayName", "displayName")) {
  if (-not $identity.$req) { throw "identity missing field: $req" }
}

if (-not (Test-Path $Exe)) { throw "Exe not found: $Exe" }
$ExePath = (Resolve-Path $Exe).Path

$storeVer = ConvertTo-StoreVersion $Version
$makeappx = Find-SdkTool "makeappx.exe"

# Resolve OutDir (relative -> under repo root; absolute OK)
if ([System.IO.Path]::IsPathRooted($OutDir)) {
  $outParent = $OutDir
} else {
  $outParent = Join-Path $Root $OutDir
}

# --- layout ---
$layout = Join-Path $outParent "layout"
if (Test-Path $layout) { Remove-Item $layout -Recurse -Force }
New-Item -ItemType Directory -Force -Path $layout | Out-Null
New-Item -ItemType Directory -Force -Path (Join-Path $layout "Images") | Out-Null

Copy-Item $ExePath (Join-Path $layout "suzuri.exe") -Force

function Copy-Sidecar([string]$src, [string]$destName) {
  if (-not $src) { return $false }
  if (-not (Test-Path $src)) { throw "sidecar not found: $src" }
  Copy-Item (Resolve-Path $src).Path (Join-Path $layout $destName) -Force
  return $true
}
$copiedTransfer = Copy-Sidecar $Transfer "suzuri-transfer.exe"
$copiedChrome = Copy-Sidecar $Chrome "suzuri-chrome.exe"
if (-not $copiedChrome) {
  Write-Warning "No -Chrome sidecar — Store/MSIX install will not find suzuri-chrome.exe next to the host"
}
if (-not $copiedTransfer) {
  Write-Warning "No -Transfer sidecar — P2P transfer will be missing from the Store package"
}

$imagesSrc = Join-Path $Root "packaging\windows\msix\Images"
Copy-Item (Join-Path $imagesSrc "*") (Join-Path $layout "Images") -Force

$template = Get-Content (Join-Path $Root "packaging\windows\msix\AppxManifest.xml.template") -Raw
$manifest = $template.
  Replace("{{PACKAGE_NAME}}", $identity.packageName).
  Replace("{{PUBLISHER}}", $identity.publisher).
  Replace("{{PUBLISHER_DISPLAY_NAME}}", $identity.publisherDisplayName).
  Replace("{{DISPLAY_NAME}}", $identity.displayName).
  Replace("{{VERSION}}", $storeVer).
  Replace("{{ARCHITECTURE}}", $Arch)
$manifestPath = Join-Path $layout "AppxManifest.xml"
# UTF-8 without BOM - makeappx is picky about BOM on some SDK builds
$utf8NoBom = New-Object System.Text.UTF8Encoding $false
[System.IO.File]::WriteAllText($manifestPath, $manifest, $utf8NoBom)

Write-Host "Layout ready: $layout"
Write-Host "  Identity: $($identity.packageName)"
Write-Host "  Publisher: $($identity.publisher)"
Write-Host "  Version: $storeVer ($Arch)"

if ($LayoutOnly) {
  Write-Host "LayoutOnly - skip pack. Register with: Add-AppxPackage -Register `"$manifestPath`""
  return
}

# --- pack ---
New-Item -ItemType Directory -Force -Path $outParent | Out-Null
$msixName = "suzuri-$Version-windows-$Arch.msix"
$msixPath = Join-Path $outParent $msixName
if (Test-Path $msixPath) { Remove-Item $msixPath -Force }

& $makeappx pack /d $layout /p $msixPath /o
if ($LASTEXITCODE -ne 0) { throw "makeappx failed: $LASTEXITCODE" }
Write-Host "Packed: $msixPath"

# --- optional dev sign ---
if ($SignDev) {
  $signtool = Find-SdkTool "signtool.exe"
  $certSubject = $identity.publisher
  if ($certSubject -notmatch '^CN=') {
    throw "publisher must look like CN=... for signing; got: $certSubject"
  }

  $existing = Get-ChildItem Cert:\CurrentUser\My -CodeSigningCert -ErrorAction SilentlyContinue |
    Where-Object { $_.Subject -eq $certSubject -and $_.NotAfter -gt (Get-Date) } |
    Select-Object -First 1
  if (-not $existing) {
    Write-Host "Creating self-signed code signing cert: $certSubject"
    $existing = New-SelfSignedCertificate `
      -Type CodeSigningCert `
      -Subject $certSubject `
      -CertStoreLocation Cert:\CurrentUser\My `
      -NotAfter (Get-Date).AddYears(2) `
      -KeyExportPolicy Exportable
    # Trust for local install (user Trusted People is enough for sideload with -AllowUnsigned sometimes;
    # for signed packages, publisher cert must be trusted).
    $rootStore = New-Object System.Security.Cryptography.X509Certificates.X509Store("Root", "CurrentUser")
    $rootStore.Open("ReadWrite")
    try {
      $rootStore.Add($existing)
      Write-Host "Installed cert into CurrentUser\Root (dev only; remove later if desired)"
    } finally {
      $rootStore.Close()
    }
  }

  & $signtool sign /fd SHA256 /a /n ($certSubject -replace '^CN=', '') /tr http://timestamp.digicert.com /td SHA256 $msixPath
  if ($LASTEXITCODE -ne 0) {
    Write-Warning "Timestamped sign failed; retrying without timestamp"
    & $signtool sign /fd SHA256 /a /n ($certSubject -replace '^CN=', '') $msixPath
    if ($LASTEXITCODE -ne 0) { throw "signtool failed: $LASTEXITCODE" }
  }
  Write-Host "Signed (dev): $msixPath"
  Write-Host "Install: Add-AppxPackage -Path `"$msixPath`""
} else {
  Write-Host ""
  Write-Host "Unsigned package ready for Partner Center upload (Store re-signs)."
  Write-Host "For local install test, re-run with -SignDev -IdentityDev."
}

Get-Item $msixPath | Format-List Name, Length, FullName
