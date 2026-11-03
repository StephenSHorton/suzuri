# Fetches OpenConsole's conpty.dll + OpenConsole.exe (Microsoft's
# Microsoft.Windows.Console.ConPTY NuGet package, MIT) into
# packaging/windows/conpty/. The NSIS installer puts both next to
# rust-suzuri.exe, and portable-pty loads that conpty.dll before the
# system one. The package hash is pinned: bump both together.
param([string]$Arch = "x64")
$ErrorActionPreference = "Stop"
$Version = "1.25.260930003"
$Sha256 = "02B07B349AF66D801159BDF9E440D4A1CE78BB951F37FC8609731665AFDAE7EE"

$root = Resolve-Path (Join-Path $PSScriptRoot "..\..")
$out = Join-Path $root "packaging\windows\conpty"
$stamp = Join-Path $out "VERSION"
if ((Test-Path $stamp) -and ((Get-Content $stamp -Raw).Trim() -eq "$Version $Arch")) {
  Write-Host "conpty $Version ($Arch) already in place"
  return
}
$tmp = Join-Path ([IO.Path]::GetTempPath()) "rsz-conpty-$Version"
New-Item -ItemType Directory -Force $tmp | Out-Null
$pkg = Join-Path $tmp "conpty.zip"
$url = "https://api.nuget.org/v3-flatcontainer/microsoft.windows.console.conpty/$Version/microsoft.windows.console.conpty.$Version.nupkg"
Write-Host "==> $url"
Invoke-WebRequest -Uri $url -OutFile $pkg -UseBasicParsing
$got = (Get-FileHash $pkg -Algorithm SHA256).Hash
if ($got -ne $Sha256) { throw "conpty package hash mismatch: $got" }
Expand-Archive -Force $pkg (Join-Path $tmp "x")
New-Item -ItemType Directory -Force $out | Out-Null
Copy-Item (Join-Path $tmp "x\runtimes\win-$Arch\native\conpty.dll") $out -Force
Copy-Item (Join-Path $tmp "x\build\native\runtimes\$Arch\OpenConsole.exe") $out -Force
Set-Content $stamp "$Version $Arch"
Remove-Item -Recurse -Force $tmp
Get-ChildItem $out | Format-Table Name, Length
