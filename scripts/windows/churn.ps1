# rust-suzuri smoke test and churn on Windows. Keys only ever go to the app's
# window: every press first checks the foreground and stops if it can't get it.
#
#   .\scripts\windows\churn.ps1 -Shot smoke.png                 # 1 shell pane: typing, arrows, Ctrl+C, splits, focus, close, wheel
#   .\scripts\windows\churn.ps1 -Cmd <fakeagent.exe> -Minutes 4 # 8 streaming panes: resize + split/close/focus churn
#   .\scripts\windows\churn.ps1 -Cmd claude -ShotOnly -ShotDelay 20 -Shot claude8.png
#
# The Claude run starts real Claude Code sessions: each one loads your plugins
# (an Irori agent each, for example), so -ShotOnly closes the app right after
# the screenshot. Set $env:RSZ_CWD to a folder Claude already trusts.
param(
  [int]$Minutes = 3,
  [string]$Cmd = "",
  [int]$Panes = 0,
  [string]$Shot = "",
  [int]$ShotDelay = 8,
  [switch]$ShotOnly
)
$ErrorActionPreference = "Stop"
$R = Split-Path -Parent (Split-Path -Parent $PSScriptRoot)
. (Join-Path $PSScriptRoot "keys-lib.ps1")
if (-not ("W3" -as [type])) {
Add-Type -TypeDefinition @"
using System; using System.Runtime.InteropServices;
public static class W3 {
  [StructLayout(LayoutKind.Sequential)] struct MOUSEINPUT { public int dx; public int dy; public int mouseData; public uint dwFlags; public uint time; public IntPtr dwExtraInfo; }
  [StructLayout(LayoutKind.Explicit, Size = 40)] struct INPUT { [FieldOffset(0)] public uint type; [FieldOffset(8)] public MOUSEINPUT mi; }
  [DllImport("user32.dll")] static extern uint SendInput(uint n, INPUT[] i, int s);
  [DllImport("user32.dll")] public static extern bool MoveWindow(IntPtr h, int x, int y, int w, int hh, bool r);
  public static void Wheel(int d) { var i = new INPUT[1]; i[0].type = 0; i[0].mi.mouseData = d; i[0].mi.dwFlags = 0x0800; SendInput(1, i, Marshal.SizeOf(typeof(INPUT))); }
}
"@
}
if ($Cmd) { $env:RSZ_CMD = $Cmd } else { Remove-Item Env:RSZ_CMD -ErrorAction SilentlyContinue }
$env:RSZ_PANES = if ($Panes -gt 0) { "$Panes" } elseif ($Cmd) { "8" } else { "1" }
Remove-Item Env:RSZ_BENCH -ErrorAction SilentlyContinue
$p = Start-Process -FilePath (Join-Path $R "target\release\rust-suzuri.exe") -PassThru
Start-Sleep -Seconds 4
$p.Refresh(); $script:h = $p.MainWindowHandle; $script:tp = $p.Id
function TypeText([string]$t) {
  foreach ($ch in $t.ToCharArray()) {
    Guard
    [System.Windows.Forms.SendKeys]::SendWait(([string]$ch -replace '([+^%~(){}\[\]])', '{$1}'))
    Start-Sleep -Milliseconds 20
  }
}
$CTRL = 0x11; $SHIFT = 0x10; $ALT = 0x12
Focus
[W3]::MoveWindow($script:h, 100, 80, 1400, 900, $true) | Out-Null
Start-Sleep -Seconds 1
if ($ShotOnly) { Start-Sleep -Seconds $ShotDelay; Snap $Shot; Stop-Process -Id $p.Id; "shot $Shot"; return }

if (-not $Cmd) {
  TypeText "echo hello-rsz"; Press @() 0x0D; Start-Sleep -Milliseconds 500
  TypeText "abc"; Press @() 0x25; Press @() 0x25; TypeText "X"; Press @() 0x0D
  TypeText "ping -n 30 127.0.0.1"; Press @() 0x0D; Start-Sleep -Seconds 2; Press @($CTRL) 0x43; Start-Sleep -Milliseconds 500
  TypeText "1..300"; Press @() 0x0D; Start-Sleep -Seconds 1
  for ($i = 0; $i -lt 10; $i++) { [W3]::Wheel(120); Start-Sleep -Milliseconds 50 }
  if ($Shot) { Snap ($Shot -replace '\.png$', '-scroll.png') }
  for ($i = 0; $i -lt 10; $i++) { [W3]::Wheel(-120); Start-Sleep -Milliseconds 50 }
  Press @($CTRL, $SHIFT) 0x44; Start-Sleep -Milliseconds 800
  Press @($CTRL, $SHIFT) 0x45; Start-Sleep -Milliseconds 800
  Press @($ALT) 0x25; TypeText "echo left"; Press @() 0x0D
  Press @($ALT) 0x27; TypeText "echo right"; Press @() 0x0D
  Start-Sleep -Seconds 1
  if ($Shot) { Snap $Shot }
  Press @($CTRL, $SHIFT) 0x57; Start-Sleep -Milliseconds 800
  $p.Refresh(); "smoke alive=$(-not $p.HasExited)"
  Stop-Process -Id $p.Id -ErrorAction SilentlyContinue
  return
}

Start-Sleep -Seconds 3
if ($Shot) { Snap $Shot }
$end = (Get-Date).AddMinutes($Minutes); $n = 0
while ((Get-Date) -lt $end) {
  $w = 900 + ($n * 137) % 900; $hh = 600 + ($n * 91) % 500
  [W3]::MoveWindow($script:h, 100, 80, $w, $hh, $true) | Out-Null; Start-Sleep -Milliseconds 120
  if ($n % 5 -eq 0) { Press @($CTRL, $SHIFT) 0x44 }
  if ($n % 5 -eq 2) { Press @($CTRL, $SHIFT) 0x45 }
  if ($n % 5 -eq 4) { Press @($CTRL, $SHIFT) 0x57; Press @($CTRL, $SHIFT) 0x57 }
  if ($n % 3 -eq 1) { Press @($ALT) (0x25 + ($n % 4)) }
  $p.Refresh(); if ($p.HasExited) { "CRASH at iteration $n exit=$($p.ExitCode)"; return }
  $n++
}
$p.Refresh(); "churn iterations=$n alive=$(-not $p.HasExited) ws_mb=$([math]::Round($p.WorkingSet64 / 1MB, 1))"
if ($Shot) { Snap ($Shot -replace '\.png$', '-end.png') }
Stop-Process -Id $p.Id -ErrorAction SilentlyContinue
