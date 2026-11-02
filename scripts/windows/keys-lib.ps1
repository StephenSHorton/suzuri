Add-Type -AssemblyName System.Windows.Forms, System.Drawing
if (-not ("W2" -as [type])) {
Add-Type -TypeDefinition @"
using System; using System.Runtime.InteropServices;
public static class W2 {
  [StructLayout(LayoutKind.Sequential)] struct KEYBDINPUT { public ushort wVk; public ushort wScan; public uint dwFlags; public uint time; public IntPtr dwExtraInfo; }
  [StructLayout(LayoutKind.Explicit, Size = 40)] struct INPUT { [FieldOffset(0)] public uint type; [FieldOffset(8)] public KEYBDINPUT ki; }
  [DllImport("user32.dll")] static extern uint SendInput(uint n, INPUT[] i, int s);
  [DllImport("user32.dll")] public static extern IntPtr GetForegroundWindow();
  [DllImport("user32.dll")] public static extern uint GetWindowThreadProcessId(IntPtr h, out uint pid);
  [DllImport("user32.dll")] public static extern bool SetForegroundWindow(IntPtr h);
  [DllImport("user32.dll")] public static extern bool ShowWindow(IntPtr h, int c);
  [StructLayout(LayoutKind.Sequential)] public struct RECT { public int L, T, R, B; }
  [DllImport("user32.dll")] public static extern bool GetWindowRect(IntPtr h, out RECT r);
  public static void K(ushort vk, bool up) { var i = new INPUT[1]; i[0].type = 1; i[0].ki.wVk = vk; i[0].ki.dwFlags = up ? 2u : 0u; SendInput(1, i, Marshal.SizeOf(typeof(INPUT))); }
  public static uint FgPid() { uint p; GetWindowThreadProcessId(GetForegroundWindow(), out p); return p; }
}
"@
}
function Focus { [W2]::K(0x12,$false); [W2]::K(0x12,$true); [W2]::ShowWindow($script:h, 9) | Out-Null; [W2]::SetForegroundWindow($script:h) | Out-Null; Start-Sleep -Milliseconds 300 }
function Guard { if ([W2]::FgPid() -ne $script:tp) { Focus; if ([W2]::FgPid() -ne $script:tp) { throw "foreground is not the target; stopping" } } }
function Press([int[]]$mods, [int]$vk) { Guard; foreach ($m in $mods) { [W2]::K($m,$false) }; [W2]::K($vk,$false); Start-Sleep -Milliseconds 40; [W2]::K($vk,$true); foreach ($m in $mods) { [W2]::K($m,$true) }; Start-Sleep -Milliseconds 200 }
function Snap([string]$path) {
  $r = New-Object W2+RECT; [W2]::GetWindowRect($script:h, [ref]$r) | Out-Null
  $bmp = New-Object System.Drawing.Bitmap ($r.R-$r.L), ($r.B-$r.T)
  $g = [System.Drawing.Graphics]::FromImage($bmp); $g.CopyFromScreen($r.L, $r.T, 0, 0, $bmp.Size); $bmp.Save($path); $g.Dispose(); $bmp.Dispose()
}
