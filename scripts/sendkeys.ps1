# Real key presses into the foreground window (SendInput), for the Windows
# latency bench: "x" then Backspace, every IntervalMs, each held HoldMs like a
# person's press (suzuri polls key state per tick and misses instant taps).
#
#   .\scripts\sendkeys.ps1 -Seconds 40 -IntervalMs 100 -HoldMs 40
param(
  [int]$Seconds = 40,
  [int]$IntervalMs = 100,
  [int]$HoldMs = 40
)
Add-Type -TypeDefinition @"
using System;
using System.Runtime.InteropServices;
public static class Keys {
  [StructLayout(LayoutKind.Sequential)] struct KEYBDINPUT { public ushort wVk; public ushort wScan; public uint dwFlags; public uint time; public IntPtr dwExtraInfo; }
  [StructLayout(LayoutKind.Explicit, Size = 40)] struct INPUT { [FieldOffset(0)] public uint type; [FieldOffset(8)] public KEYBDINPUT ki; }
  [DllImport("user32.dll", SetLastError = true)] static extern uint SendInput(uint n, INPUT[] inputs, int size);
  public static void Send(ushort vk, bool up) {
    var i = new INPUT[1];
    i[0].type = 1;
    i[0].ki.wVk = vk;
    i[0].ki.dwFlags = up ? 2u : 0u;
    SendInput(1, i, Marshal.SizeOf(typeof(INPUT)));
  }
}
"@
$VK_X = 0x58; $VK_BACK = 0x08
$end = (Get-Date).AddSeconds($Seconds)
$n = 0
while ((Get-Date) -lt $end) {
  $vk = if ($n % 2 -eq 0) { $VK_X } else { $VK_BACK }
  [Keys]::Send($vk, $false)
  Start-Sleep -Milliseconds $HoldMs
  [Keys]::Send($vk, $true)
  Start-Sleep -Milliseconds ([Math]::Max(1, $IntervalMs - $HoldMs))
  $n++
}
Write-Output "sent $n presses"
