# Guarded sendkeys: only presses keys while the foreground window belongs to
# process $TargetPid (or one of $TargetNames); stops on the first mismatch.
param(
  [int]$Seconds = 40,
  [int]$IntervalMs = 100,
  [int]$HoldMs = 40,
  [string[]]$TargetNames = @("rust-suzuri"),
  [switch]$FocusFirst
)
Add-Type -TypeDefinition @"
using System;
using System.Runtime.InteropServices;
public static class GK {
  [StructLayout(LayoutKind.Sequential)] struct KEYBDINPUT { public ushort wVk; public ushort wScan; public uint dwFlags; public uint time; public IntPtr dwExtraInfo; }
  [StructLayout(LayoutKind.Explicit, Size = 40)] struct INPUT { [FieldOffset(0)] public uint type; [FieldOffset(8)] public KEYBDINPUT ki; }
  [DllImport("user32.dll", SetLastError = true)] static extern uint SendInput(uint n, INPUT[] inputs, int size);
  [DllImport("user32.dll")] public static extern IntPtr GetForegroundWindow();
  [DllImport("user32.dll")] public static extern uint GetWindowThreadProcessId(IntPtr h, out uint pid);
  [DllImport("user32.dll")] public static extern bool SetForegroundWindow(IntPtr h);
  [DllImport("user32.dll")] public static extern bool ShowWindow(IntPtr h, int c);
  public static void Send(ushort vk, bool up) {
    var i = new INPUT[1]; i[0].type = 1; i[0].ki.wVk = vk; i[0].ki.dwFlags = up ? 2u : 0u;
    SendInput(1, i, Marshal.SizeOf(typeof(INPUT)));
  }
  public static uint FgPid() { uint p; GetWindowThreadProcessId(GetForegroundWindow(), out p); return p; }
}
"@
function Ok {
  $fp = [GK]::FgPid()
  $pr = Get-Process -Id $fp -ErrorAction SilentlyContinue
  return ($pr -and ($TargetNames -contains $pr.ProcessName))
}
if ($FocusFirst) {
  $t = Get-Process -Name $TargetNames -ErrorAction SilentlyContinue | Where-Object { $_.MainWindowHandle -ne 0 } | Select-Object -First 1
  if ($t) {
    # ALT tap lets SetForegroundWindow succeed from a background process.
    [GK]::Send(0x12, $false); [GK]::Send(0x12, $true)
    [GK]::ShowWindow($t.MainWindowHandle, 9) | Out-Null
    [GK]::SetForegroundWindow($t.MainWindowHandle) | Out-Null
    Start-Sleep -Milliseconds 500
  }
}
if (-not (Ok)) { Write-Output "ABORT: foreground is not $TargetNames (pid $([GK]::FgPid()))"; exit 2 }
$VK_X = 0x58; $VK_BACK = 0x08
$end = (Get-Date).AddSeconds($Seconds); $n = 0
while ((Get-Date) -lt $end) {
  if (-not (Ok)) { Write-Output "ABORT after $n presses: foreground changed"; exit 3 }
  $vk = if ($n % 2 -eq 0) { $VK_X } else { $VK_BACK }
  [GK]::Send($vk, $false); Start-Sleep -Milliseconds $HoldMs; [GK]::Send($vk, $true)
  Start-Sleep -Milliseconds ([Math]::Max(1, $IntervalMs - $HoldMs)); $n++
}
Write-Output "sent $n presses"
