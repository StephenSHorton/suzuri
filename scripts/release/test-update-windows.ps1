# Local update test on the PC: builds $Old and $New installers, serves $New's
# feed on 127.0.0.1, installs $Old silently (per user), starts it with
# RSZ_UPDATE_AUTO=1 and checks that it updated itself to $New. A throwaway
# updater key is used (and the http feed allowed) through a config overlay, so
# the real key never leaves the Mac. The test install is removed at the end
# unless -Keep.
#
#   powershell -File scripts\release\test-update-windows.ps1 [-Old 0.1.0] [-New 0.1.1] [-Port 8787] [-Keep]
param([string]$Old = "0.1.0", [string]$New = "0.1.1", [int]$Port = 8787, [switch]$Keep)
$ErrorActionPreference = "Stop"
$root = Resolve-Path (Join-Path $PSScriptRoot "..\..")
# One run at a time: a second run would uninstall and kill the first one's app.
$busy = Get-NetTCPConnection -LocalPort $Port -State Listen -ErrorAction SilentlyContinue
if ($busy -or (Get-Process rust-suzuri -ErrorAction SilentlyContinue)) {
  throw "port $Port is in use or rust-suzuri is running: another update test may be going; stop it first"
}
$T = Join-Path ([IO.Path]::GetTempPath()) ("rsz-uptest-" + [guid]::NewGuid().ToString("N").Substring(0, 8))
New-Item -ItemType Directory -Force $T | Out-Null
$key = Join-Path $T "test.key"

# --ci: no prompts, no password (PowerShell 5 drops an empty -p "" argument).
cargo tauri signer generate --ci -w $key | Out-Null
if (-not (Test-Path "$key.pub")) { throw "signer generate failed" }
$overlay = @{ plugins = @{ updater = @{
  pubkey = (Get-Content "$key.pub" -Raw).Trim()
  dangerousInsecureTransportProtocol = $true
  windows = @{ installMode = "quiet" } } } } | ConvertTo-Json -Depth 6 -Compress
$overlayFile = Join-Path $T "overlay.json"
# UTF-8 without a BOM: PowerShell 5's -Encoding utf8 writes one, and the
# updater's JSON parser rejects latest.json with a BOM.
$utf8 = New-Object Text.UTF8Encoding $false
[IO.File]::WriteAllText($overlayFile, $overlay, $utf8)

foreach ($v in @($Old, $New)) {
  & (Join-Path $PSScriptRoot "build-windows.ps1") -Version $v -NoUpload -ExtraConfig $overlayFile
}
$dist = Join-Path $root "dist\$New"
$newExe = "rust-suzuri_${New}_x64-setup.exe"
# "--password=" passes an empty password (PowerShell 5 drops a bare "").
cargo tauri signer sign -f $key "--password=" (Join-Path $dist $newExe) | Out-Null
if (-not (Test-Path (Join-Path $dist "$newExe.sig"))) { throw "signer sign failed" }
$sig = (Get-Content (Join-Path $dist "$newExe.sig") -Raw).Trim()
$entry = @{ signature = $sig; url = "http://127.0.0.1:$Port/$newExe" }
$feed = @{ version = $New; notes = "local test"; pub_date = (Get-Date).ToUniversalTime().ToString("yyyy-MM-ddTHH:mm:ssZ")
   platforms = @{ "windows-x86_64" = $entry; "windows-x86_64-nsis" = $entry } } |
  ConvertTo-Json -Depth 6
[IO.File]::WriteAllText((Join-Path $dist "latest.json"), $feed, $utf8)

# Feed server: a background job serving dist\$New.
$srv = Start-Job -ArgumentList $dist, $Port -ScriptBlock {
  param($dir, $port)
  $l = [Net.HttpListener]::new(); $l.Prefixes.Add("http://127.0.0.1:$port/"); $l.Start()
  while ($true) {
    $c = $l.GetContext(); $f = Join-Path $dir ($c.Request.Url.AbsolutePath.TrimStart("/"))
    if (Test-Path $f -PathType Leaf) { $b = [IO.File]::ReadAllBytes($f); $c.Response.ContentLength64 = $b.Length; $c.Response.OutputStream.Write($b, 0, $b.Length) }
    else { $c.Response.StatusCode = 404 }
    $c.Response.Close()
  }
}

function Installed {
  Get-ItemProperty "HKCU:\Software\Microsoft\Windows\CurrentVersion\Uninstall\*" -ErrorAction SilentlyContinue |
    Where-Object { $_.DisplayName -eq "rust-suzuri" } | Select-Object -First 1
}
$result = "FAIL"
try {
  $p = Start-Process (Join-Path $root "dist\$Old\rust-suzuri_${Old}_x64-setup.exe") -ArgumentList "/S" -PassThru -Wait
  $inst = Installed
  if (-not $inst) { throw "install of $Old not found in the registry" }
  $dir = $inst.InstallLocation.Trim('"')
  Write-Host "==> installed $($inst.DisplayVersion) at $dir, feed offers $New"
  foreach ($f in "rust-suzuri.exe", "conpty.dll", "OpenConsole.exe") {
    if (-not (Test-Path (Join-Path $dir $f))) { throw "missing $f in $dir" }
  }
  Write-Host "==> conpty.dll and OpenConsole.exe are next to rust-suzuri.exe"

  $env:RSZ_UPDATE_AUTO = "1"; $env:RSZ_UPDATE_URL = "http://127.0.0.1:$Port/latest.json"
  # Its own config dir, so the test app never takes over a running suzuri's
  # bridge.json / ai.json (the install location doesn't come from this).
  $realLocal = $env:LOCALAPPDATA
  $env:LOCALAPPDATA = Join-Path $T "cfg"; New-Item -ItemType Directory -Force $env:LOCALAPPDATA | Out-Null
  Start-Process (Join-Path $dir "rust-suzuri.exe") -RedirectStandardError (Join-Path $T "app.err") | Out-Null
  $env:LOCALAPPDATA = $realLocal
  Remove-Item Env:RSZ_UPDATE_AUTO, Env:RSZ_UPDATE_URL
  for ($i = 0; $i -lt 120; $i++) { if ((Installed).DisplayVersion -eq $New) { break }; Start-Sleep 1 }
  Start-Sleep 5
  Get-Content (Join-Path $T "app.err") -ErrorAction SilentlyContinue
  $now = (Installed).DisplayVersion
  $exeVer = (Get-Item (Join-Path $dir "rust-suzuri.exe")).VersionInfo.ProductVersion
  Write-Host "==> registry version $now, exe version $exeVer"
  if ($now -eq $New -and $exeVer -like "$New*") { $result = "PASS: $Old updated itself to $New" }
  else { $result = "FAIL: still $now / $exeVer" }
} finally {
  Stop-Job $srv; Remove-Job $srv
  if (-not $Keep) {
    Get-Process rust-suzuri -ErrorAction SilentlyContinue | Stop-Process -Force
    $inst = Installed
    if ($inst) { Start-Process $inst.UninstallString.Trim('"') -ArgumentList "/S" -Wait }
  }
  Remove-Item -Recurse -Force $T -ErrorAction SilentlyContinue
}
Write-Host $result
if ($result -notlike "PASS*") { exit 1 }
