# The standalone keeper host, rsz-keeper-host.exe (card hearth#190), on its
# own on Windows: no Suzuri install used. Copies the host (with conpty.dll
# and OpenConsole.exe when they are beside it) and the rsz-keeper client
# into an empty folder, starts a shell the way Suzuri and Hearth do
# (`--keeper-host <dir> <id>`, JSON on stdin), types into it with
# rsz-keeper, reads its screen back, and closes it. The Windows twin of
# host-check.sh.
#   powershell -File scripts\keeper\host-check.ps1 [-HostExe X] [-KeeperExe Y]
param([string]$HostExe = "", [string]$KeeperExe = "")
$ErrorActionPreference = "Stop"
$root = Resolve-Path (Join-Path $PSScriptRoot "..\..")
if (-not $HostExe) { $HostExe = Join-Path $root "target\release\rsz-keeper-host.exe" }
if (-not $KeeperExe) { $KeeperExe = Join-Path $root "target\release\rsz-keeper.exe" }
function Fail([string]$m) { Write-Host "FAIL: $m"; exit 1 }
if (-not (Test-Path $HostExe)) { Fail "no host program at $HostExe" }
if (-not (Test-Path $KeeperExe)) { Fail "no rsz-keeper at $KeeperExe" }

# The temp folder, or when its path is too long for a socket (AF_UNIX
# takes under 104 bytes; a check runner's own temp folder can be longer)
# the system drive's root, as the keeper's short_test_dir does.
$t = Join-Path ([IO.Path]::GetTempPath()) "rszh-$PID"
if ((Join-Path $t "k\4294967295.sock").Length -ge 104) {
  $t = Join-Path "$env:SystemDrive\" "rszh-$PID"
}
$bin = Join-Path $t "bin"
$kd = Join-Path $t "k"
New-Item -ItemType Directory -Force $bin, $kd | Out-Null
$h = Join-Path $bin "rsz-keeper-host.exe"
$k = Join-Path $bin "rsz-keeper.exe"
Copy-Item $HostExe $h
Copy-Item $KeeperExe $k
foreach ($f in "conpty.dll", "OpenConsole.exe") {
  $src = Join-Path (Split-Path $HostExe) $f
  if (Test-Path $src) { Copy-Item $src $bin }
}
$hostPid = 0
try {
  Write-Host "== system: $([Environment]::OSVersion.VersionString), $env:PROCESSOR_ARCHITECTURE"
  Write-Host "== the host program, alone in $bin"
  Get-ChildItem $bin | ForEach-Object { Write-Host "   $($_.Name)  $($_.Length)" }
  Write-Host "   $(& $h --version)"
  $installed = Join-Path $env:LOCALAPPDATA "Programs\suzuri\suzuri.exe"
  if (Test-Path $installed) { Write-Host "   (Suzuri is installed at $installed; not used)" } else { Write-Host "   no Suzuri installed" }

  Write-Host "== start a kept shell: rsz-keeper-host --keeper-host $kd 1"
  $psi = New-Object Diagnostics.ProcessStartInfo $h
  $psi.Arguments = "--keeper-host `"$kd`" 1"
  $psi.UseShellExecute = $false
  $psi.RedirectStandardInput = $true
  $psi.RedirectStandardOutput = $true
  $psi.WorkingDirectory = $kd
  $p = [Diagnostics.Process]::Start($psi)
  $p.StandardInput.Write('{"cols":80,"rows":24,"command":["cmd.exe"],"version":"host-check"}')
  $p.StandardInput.Close()
  $line = $p.StandardOutput.ReadLineAsync()
  if (-not $line.Wait(15000)) { Fail "no ready line in 15 s" }
  $ready = $line.Result
  if (-not ($ready -like "ready*")) { Fail "host said: $ready" }
  Write-Host "   host said: $ready"
  $hostPid = [int]($ready -split " ")[1]

  Write-Host "== rsz-keeper ls (the host's hello)"
  $hello = (& $k --dir $kd ls) -join ""
  Write-Host "   $hello"
  $hv = $hello | ConvertFrom-Json
  if ($hv.exe -ne $h) { Fail "the hello names another program: $($hv.exe)" }

  Write-Host "== type into it and read its screen"
  & $k --dir $kd type 1 "echo kept-42 on %OS%`n" | Out-Null
  $ok = $false
  for ($i = 0; $i -lt 100 -and -not $ok; $i++) {
    $screen = (& $k --dir $kd screen 1) -join "`n"
    $ok = $screen -match "(?m)^kept-42 on Windows_NT"
    if (-not $ok) { Start-Sleep -Milliseconds 100 }
  }
  $screen -split "`n" | Where-Object { $_.Trim() } | ForEach-Object { Write-Host "   | $_" }
  if (-not $ok) { Fail "no kept-42 on the screen" }

  Write-Host "== close it: the shell and its host end"
  & $k --dir $kd close 1 | Out-Null
  for ($i = 0; $i -lt 50 -and (Get-Process -Id $hostPid -ErrorAction SilentlyContinue); $i++) { Start-Sleep -Milliseconds 100 }
  if (Get-Process -Id $hostPid -ErrorAction SilentlyContinue) { Fail "host $hostPid still runs" }
  if (Test-Path (Join-Path $kd "1.sock")) { Fail "socket left behind" }
  Write-Host "   host $hostPid ended, socket removed"
  Write-Host "PASS: rsz-keeper-host hosted a shell on its own"
} finally {
  if ($hostPid -and (Get-Process -Id $hostPid -ErrorAction SilentlyContinue)) { Stop-Process -Id $hostPid -Force }
  Start-Sleep -Milliseconds 300
  Remove-Item -Recurse -Force $t -ErrorAction SilentlyContinue
}
