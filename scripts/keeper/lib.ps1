# Shared by the Windows keeper checks (card hearth#923), the twin of lib.sh.
# Every run is a scratch suzuri: its own LOCALAPPDATA, RSZ_CONFIG_DIR and
# keeper folder under one short folder, and RSZ_ALLOW_MULTI=1, so it never
# shares bridge.json, ai.json, settings or kept shells with the installed
# suzuri, and never hands off to its window. Processes are stopped by pid,
# never by name.
$ErrorActionPreference = "Stop"
$Root = (Resolve-Path (Join-Path $PSScriptRoot "..\..")).Path
if (-not $env:RSZK_SCRATCH) { $env:RSZK_SCRATCH = "C:\rszk-$PID" }
$S = $env:RSZK_SCRATCH
foreach ($d in "local", "cfg") { New-Item -ItemType Directory -Force (Join-Path $S $d) | Out-Null }
$KDir = Join-Path $S "cfg\keeper"
$Keeper = Join-Path $Root "target\debug\rsz-keeper.exe"
$Out = if ($env:SHOTS) { $env:SHOTS } else { $S }
$script:AppPids = @()

function Say([string]$m) { Write-Host $m }
function Fail([string]$m) { Write-Host "FAIL: $m"; throw "FAIL: $m" }

# The installed suzuri's config: a check must leave it alone.
$RealCfg = Join-Path ([Environment]::GetFolderPath("LocalApplicationData")) "suzuri"
function Real-Stamp {
  foreach ($f in "bridge.json", "ai.json", "keeper", "bridge-last.json") {
    $p = Join-Path $RealCfg $f
    if (Test-Path $p) { "$f $((Get-Item $p).LastWriteTimeUtc.Ticks)" }
  }
}

function Scratch-Env {
  @{ LOCALAPPDATA = (Join-Path $S "local"); RSZ_CONFIG_DIR = (Join-Path $S "cfg"); RSZ_KEEPER_DIR = $KDir
     RSZ_BRIDGE = "1"; RSZ_ALLOW_MULTI = "1" }
}

# Start-App EXE [extra env]: starts it in the scratch env, returns its pid
# once it has written bridge.json.
function Start-App([string]$exe, [hashtable]$extra = @{}) {
  $bj = Join-Path $S "local\suzuri\bridge.json"
  Remove-Item $bj -ErrorAction SilentlyContinue
  $vars = (Scratch-Env) + $extra
  $saved = @{}
  foreach ($k in $vars.Keys) { $saved[$k] = [Environment]::GetEnvironmentVariable($k); [Environment]::SetEnvironmentVariable($k, $vars[$k]) }
  try {
    $p = Start-Process $exe -PassThru -RedirectStandardError (Join-Path $S "app-$([guid]::NewGuid().ToString('N').Substring(0,6)).err")
  } finally {
    foreach ($k in $saved.Keys) { [Environment]::SetEnvironmentVariable($k, $saved[$k]) }
  }
  $script:AppPids += $p.Id
  for ($i = 0; $i -lt 100 -and -not ((Test-Path $bj) -and (Get-Item $bj).Length -gt 0); $i++) { Start-Sleep -Milliseconds 200 }
  if (-not (Test-Path $bj)) { Fail "app $($p.Id) wrote no bridge.json" }
  $p.Id
}

function Bridge { Get-Content (Join-Path $S "local\suzuri\bridge.json") -Raw | ConvertFrom-Json }
function Api-Get([string]$path) { $b = Bridge; Invoke-RestMethod -Uri "$($b.url)$path" -Headers @{ Authorization = "Bearer $($b.token)" } }
function Api-Post([string]$path, $body) {
  $b = Bridge
  Invoke-RestMethod -Method Post -Uri "$($b.url)$path" -Headers @{ Authorization = "Bearer $($b.token)" } -ContentType "application/json" -Body ($body | ConvertTo-Json -Compress -Depth 5)
}
function Api-Call([string]$tool, $argsObj) { Api-Post "/v1/call" @{ tool = $tool; args = $argsObj } }

# The kept shells' hellos, as objects.
function Kept { & $Keeper --dir $KDir ls | Where-Object { $_ } | ForEach-Object { $_ | ConvertFrom-Json } }
function Kept-One([int]$id) { Kept | Where-Object { $_.id -eq $id } | Select-Object -First 1 }
function Screen([int]$id, [int]$lines = 0) { (& $Keeper --dir $KDir screen $id $lines) -join "`n" }
function Last-Tick([string]$text) { ([regex]::Matches($text, "tick-\d+") | Select-Object -Last 1).Value }
function Alive([int]$id) { [bool](Get-Process -Id $id -ErrorAction SilentlyContinue) }
function Parent-Of([int]$id) { (Get-CimInstance Win32_Process -Filter "ProcessId=$id").ParentProcessId }
function Children-Of([int]$id) { Get-CimInstance Win32_Process -Filter "ParentProcessId=$id" }

# Shot PID FILE: a picture of that app's window.
if (-not ("RszkWin" -as [type])) {
  Add-Type -AssemblyName System.Drawing
  Add-Type -TypeDefinition @"
using System; using System.Runtime.InteropServices;
public static class RszkWin {
  [StructLayout(LayoutKind.Sequential)] public struct RECT { public int L, T, R, B; }
  [DllImport("user32.dll")] public static extern bool GetWindowRect(IntPtr h, out RECT r);
  [DllImport("user32.dll")] public static extern bool SetForegroundWindow(IntPtr h);
  [DllImport("user32.dll")] public static extern bool ShowWindow(IntPtr h, int c);
  [DllImport("user32.dll")] public static extern void keybd_event(byte vk, byte scan, uint flags, IntPtr extra);
}
"@
}
function Shot([int]$id, [string]$path) {
  $h = (Get-Process -Id $id).MainWindowHandle
  if ($h -eq [IntPtr]::Zero) { Say "(no picture: no window for $id)"; return }
  [RszkWin]::keybd_event(0x12, 0, 0, [IntPtr]::Zero); [RszkWin]::keybd_event(0x12, 0, 2, [IntPtr]::Zero)
  [RszkWin]::ShowWindow($h, 9) | Out-Null; [RszkWin]::SetForegroundWindow($h) | Out-Null
  Start-Sleep -Milliseconds 1500
  $r = New-Object RszkWin+RECT; [RszkWin]::GetWindowRect($h, [ref]$r) | Out-Null
  $bmp = New-Object System.Drawing.Bitmap ($r.R - $r.L), ($r.B - $r.T)
  $g = [System.Drawing.Graphics]::FromImage($bmp); $g.CopyFromScreen($r.L, $r.T, 0, 0, $bmp.Size); $bmp.Save($path); $g.Dispose(); $bmp.Dispose()
  Say "picture: $path"
}

function Cleanup {
  foreach ($p in $script:AppPids) { Stop-Process -Id $p -Force -ErrorAction SilentlyContinue }
  if ((Test-Path $KDir) -and (Test-Path $Keeper)) {
    foreach ($k in (Kept)) { if ($k.id -and -not $k.error) { & $Keeper --dir $KDir close $k.id | Out-Null } }
  }
  Start-Sleep -Milliseconds 800
  if (-not $env:KEEP_SCRATCH) { Remove-Item -Recurse -Force $S -ErrorAction SilentlyContinue }
}
