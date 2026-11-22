# Card hearth#979: a bare Alt tap (or F10) in a focused suzuri must not stop
# it answering AI control calls. On a scratch debug build (scripts/keeper/
# lib.ps1), twice: with kept shells and with RSZ_KEEPER=0. Each run:
#   1. bring the window to the front, tap Alt alone, then time one
#      /v1/call layout (no retry: a 504 is the bug); the same for F10;
#   2. Alt+Shift+= must add a pane and Alt+Left must move the focus (Alt
#      shortcuts still work), and layout still answers in under 1 s after.
#   cargo build -p suzuri -p rsz-keeper
#   powershell -ExecutionPolicy Bypass -File scripts\windows\alt-tap-check.ps1
# Run against a build of the base branch, it should fail at step 1 (504).
. (Join-Path $PSScriptRoot "..\keeper\lib.ps1")
$App = Join-Path $Root "target\debug\suzuri.exe"
if (-not ((Test-Path $App) -and (Test-Path $Keeper))) { Fail "build first: cargo build -p suzuri -p rsz-keeper" }
if (-not ("AltTap" -as [type])) {
Add-Type -TypeDefinition @"
using System; using System.Runtime.InteropServices;
public static class AltTap {
  [StructLayout(LayoutKind.Sequential)] struct KEYBDINPUT { public ushort wVk; public ushort wScan; public uint dwFlags; public uint time; public IntPtr dwExtraInfo; }
  [StructLayout(LayoutKind.Explicit, Size = 40)] struct INPUT { [FieldOffset(0)] public uint type; [FieldOffset(8)] public KEYBDINPUT ki; }
  [DllImport("user32.dll")] static extern uint SendInput(uint n, INPUT[] i, int s);
  [DllImport("user32.dll")] public static extern IntPtr GetForegroundWindow();
  [DllImport("user32.dll")] public static extern uint GetWindowThreadProcessId(IntPtr h, out uint pid);
  [DllImport("user32.dll")] public static extern bool SetForegroundWindow(IntPtr h);
  [DllImport("user32.dll")] public static extern bool ShowWindow(IntPtr h, int c);
  public static void K(ushort vk, bool up) { var i = new INPUT[1]; i[0].type = 1; i[0].ki.wVk = vk; i[0].ki.dwFlags = up ? 2u : 0u; SendInput(1, i, Marshal.SizeOf(typeof(INPUT))); }
  public static uint FgPid() { uint p; GetWindowThreadProcessId(GetForegroundWindow(), out p); return p; }
}
"@
}
$VK_SHIFT = 0x10; $VK_ALT = 0x12; $VK_ESC = 0x1B; $VK_LEFT = 0x25; $VK_F10 = 0x79; $VK_EQUAL = 0xBB

function Tap([int[]]$keys) {
  foreach ($k in $keys) { [AltTap]::K($k, $false); Start-Sleep -Milliseconds 30 }
  [array]::Reverse($keys); foreach ($k in $keys) { [AltTap]::K($k, $true); Start-Sleep -Milliseconds 30 }
  Start-Sleep -Milliseconds 400
}

# One /v1/call layout, timed, with no retry: [ms, layout or $null, error].
function Timed-Layout {
  $b = Bridge
  $sw = [Diagnostics.Stopwatch]::StartNew()
  try {
    $r = Invoke-RestMethod -DisableKeepAlive -TimeoutSec 15 -Method Post -Uri "$($b.url)/v1/call" -Headers @{ Authorization = "Bearer $($b.token)" } -ContentType "application/json" -Body '{"tool":"layout","args":{}}'
    return @($sw.ElapsedMilliseconds, $r, "")
  } catch { return @($sw.ElapsedMilliseconds, $null, "$_") }
}
function Must-Answer([string]$what) {
  $ms, $r, $err = Timed-Layout
  Say ("  layout after {0}: {1} ms{2}" -f $what, $ms, $(if ($err) { " ($err)" } else { "" }))
  if ($err -or $ms -ge 1000) { [AltTap]::K($VK_ESC, $false); [AltTap]::K($VK_ESC, $true); Fail "layout after $what did not answer in under 1 s" }
  $r
}
function Active-Tab($l) { $l.windows[0].tabs | Where-Object { $_.active } | Select-Object -First 1 }

function Front([int]$appPid) {
  $h = (Get-Process -Id $appPid).MainWindowHandle
  [AltTap]::ShowWindow($h, 9) | Out-Null
  [AltTap]::SetForegroundWindow($h) | Out-Null
  Start-Sleep -Milliseconds 300
  if ([AltTap]::FgPid() -ne $appPid) {
    # Windows lets a process take the foreground after a key event; Esc
    # then ends any menu loop that the tap started (the base-branch bug).
    Tap @($VK_ALT); [AltTap]::SetForegroundWindow($h) | Out-Null; Start-Sleep -Milliseconds 300
    Tap @($VK_ESC); Start-Sleep -Milliseconds 300
  }
  if ([AltTap]::FgPid() -ne $appPid) { Fail "could not bring app $appPid to the front (foreground is pid $([AltTap]::FgPid()))" }
}

function Run([string]$label, [hashtable]$extra) {
  Say "== $label"
  $app = Start-App $App $extra
  Start-Sleep 3
  $l0 = Must-Answer "start"
  Front $app
  Must-Answer "coming to the front" | Out-Null

  Say "-- 1. Alt pressed and released alone"
  Tap @($VK_ALT)
  Must-Answer "an Alt tap" | Out-Null
  Must-Answer "an Alt tap, asked again" | Out-Null
  Say "-- 1b. F10 alone (the same menu request)"
  Tap @($VK_F10)
  Must-Answer "an F10 tap" | Out-Null

  Say "-- 3. Alt shortcuts"
  Front $app
  $before = Active-Tab (Must-Answer "the shortcut checks start")
  Tap @($VK_ALT, $VK_SHIFT, $VK_EQUAL)
  $split = Active-Tab (Must-Answer "Alt+Shift+=")
  Say "  panes in the tab: $(@($before.panes).Count) -> $(@($split.panes).Count); focus $($before.focus_pane) -> $($split.focus_pane)"
  if (@($split.panes).Count -ne @($before.panes).Count + 1) { Fail "Alt+Shift+= did not split the pane" }
  Tap @($VK_ALT, $VK_LEFT)
  $moved = Active-Tab (Must-Answer "Alt+Left")
  Say "  focus $($split.focus_pane) -> $($moved.focus_pane)"
  if ($moved.focus_pane -eq $split.focus_pane) { Fail "Alt+Left did not move the focus" }
  Tap @($VK_ALT)
  Must-Answer "one more Alt tap" | Out-Null
  Say "ok: $label"
  Stop-Process -Id $app -Force -ErrorAction SilentlyContinue
  Start-Sleep 1
}

$before = (Real-Stamp) -join "; "
try {
  Run "kept shells on" @{}
  Run "kept shells off (RSZ_KEEPER=0)" @{ RSZ_KEEPER = "0" }
  if (((Real-Stamp) -join "; ") -ne $before) { Fail "the installed suzuri's config changed" }
  Say "PASS: Alt and F10 taps leave AI control answering, with and without kept shells; Alt shortcuts work"
} finally {
  Cleanup
}
