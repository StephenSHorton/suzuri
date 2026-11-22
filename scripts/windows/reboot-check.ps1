# Cards hearth#950 and hearth#971, the Windows side of hearth#916, on a
# scratch debug build with kept shells (hearth#923):
#  - quitting suzuri with the window's close button leaves its shells
#    running in their keeper hosts; the next start shows those same shells
#    (same hosts, same shell pids, Claude Code and the dev server still
#    running in them), in the same tabs and splits, and restores nothing:
#    no restored mark, no resume offered;
#  - a hard kill of the app, every keeper host and every shell (what a
#    reboot does) brings back, on the next start, the same tabs and splits,
#    a fresh shell in each old folder, the old text above it marked
#    restored, and a pane that was running Claude Code offers
#    `claude --resume <id>` in its input bar without running it;
#  - `exit` in the last pane, and the next start opens one fresh pane;
#  - card hearth#975 (shells without the PowerShell hooks print the old text
#    themselves, hearth#972): with the plain-shell setting (RSZ_NO_BAR=1),
#    and with cmd.exe as the active profile, a hard kill brings back the
#    old text and the dim restored line above the new prompt, with
#    Japanese, a box line and a middle dot unbroken and no literal `[2m`;
#  - card hearth#1025: those two shells report no folder, yet the pane
#    that had `cd` into work\alpha (plain) or work\beta (cmd) is saved in
#    that folder and comes back in it after the hard kill.
#
# Claude Code is a stand-in claude.exe (compiled here from C#) that writes
# the session file the real one does (%USERPROFILE%\.claude\sessions\<pid>.json,
# pid = the shell's direct child, as the real claude.exe is); a second
# stand-in, devserver.exe, is a long-running program that is not Claude Code.
#
# Everything runs in a scratch folder: its own USERPROFILE/HOME, LOCALAPPDATA,
# RSZ_CONFIG_DIR and keeper folder, so the installed suzuri's files and kept
# shells are never touched.
#   cargo build -p suzuri -p rsz-keeper
#   powershell -NoProfile -ExecutionPolicy Bypass -File scripts\windows\reboot-check.ps1 [-Out <folder for pictures>] [-Keep] [-Cases kept,plain,cmd]
# -Cases: which parts to run (all by default): kept (the kept shells, the
# hard kill, exit), plain (the plain-shell setting), cmd (a cmd.exe profile).
param([string]$App = "", [string]$Out = "", [switch]$Keep, [string[]]$Cases = @("kept", "plain", "cmd"))
$ErrorActionPreference = "Stop"
# Its log names Japanese text and box lines: written as UTF-8, not the code page.
try { [Console]::OutputEncoding = [Text.Encoding]::UTF8 } catch { }
# -File hands "plain,cmd" over as one string.
$Cases = @($Cases | ForEach-Object { $_ -split "," } | ForEach-Object { $_.Trim() } | Where-Object { $_ })
$R = Split-Path -Parent (Split-Path -Parent $PSScriptRoot)
if (-not $App) { $App = Join-Path $R "target\debug\suzuri.exe" }
$Keeper = Join-Path (Split-Path -Parent $App) "rsz-keeper.exe"
if (-not ((Test-Path $App) -and (Test-Path $Keeper))) { throw "build first: cargo build -p suzuri -p rsz-keeper" }
# The keeper's sockets live in the scratch folder, and a Windows socket path
# is at most 108 bytes: under a long TEMP (as when Hearth runs this) the
# scratch folder goes at the system drive's root instead, as the keeper
# checks' does (scripts\keeper\lib.ps1).
$S = Join-Path $env:TEMP ("rszr-" + $PID)
if ((Join-Path $S "cfg\keeper\4294967295.sock").Length -ge 100) { $S = Join-Path "$env:SystemDrive\" ("rszr-" + $PID) }
if (-not $Out) { $Out = $S }
$SID = "7f3c2a10-5b6d-4e8f-9a01-23456789abcd"
$KDir = Join-Path $S "cfg\keeper"
foreach ($d in "home", "local", "cfg", "bin", "work\alpha", "work\beta", "work\gamma") { New-Item -ItemType Directory -Force (Join-Path $S $d) | Out-Null }
New-Item -ItemType Directory -Force $Out | Out-Null

function Say([string]$m) { Write-Host $m }
function Fail([string]$m) { Write-Host "FAIL: $m"; throw $m }

# The installed suzuri's files, to show they were not touched.
$realCfg = Join-Path $env:LOCALAPPDATA "suzuri"
function RealStamp {
  (@("config.json", "bridge.json", "ai.json", "restore", "keeper") | ForEach-Object {
    $p = Join-Path $realCfg $_
    if (Test-Path $p) { "$_ " + (Get-Item $p).LastWriteTimeUtc.Ticks }
  }) -join "; "
}
$before = RealStamp

# --- stand-ins
$src = @"
using System; using System.IO; using System.Diagnostics; using System.Threading;
public static class StandIn {
  public static void Main(string[] a) {
    string S = Environment.GetEnvironmentVariable("RSZR_SCRATCH");
    var me = Process.GetCurrentProcess();
    int pid = me.Id;
    string name = Path.GetFileNameWithoutExtension(me.MainModule.FileName);
    File.AppendAllText(Path.Combine(S, "stand.pids"), pid + "\n");
    string cwd = Environment.CurrentDirectory;
    if (name == "claude") {
      if (a.Length > 1 && a[0] == "--resume") File.AppendAllText(Path.Combine(S, "resumed.log"), "RESUMED " + a[1] + "\n");
      string d = Path.Combine(Environment.GetEnvironmentVariable("USERPROFILE"), ".claude", "sessions");
      Directory.CreateDirectory(d);
      File.WriteAllText(Path.Combine(d, pid + ".json"), "{\"pid\":" + pid + ",\"sessionId\":\"$SID\",\"cwd\":\"" + cwd.Replace("\\", "\\\\") + "\"}");
      Console.WriteLine("Claude Code (stand-in) in " + cwd + ", conversation $SID");
    } else {
      Console.WriteLine("dev server (stand-in) serving " + cwd);
    }
    Thread.Sleep(Timeout.Infinite);
  }
}
"@
Add-Type -TypeDefinition $src -OutputAssembly (Join-Path $S "bin\claude.exe") -OutputType ConsoleApplication
Copy-Item (Join-Path $S "bin\claude.exe") (Join-Path $S "bin\devserver.exe")

# --- window helpers
if (-not ("RszWin" -as [type])) {
Add-Type -AssemblyName System.Drawing
Add-Type -TypeDefinition @"
using System; using System.Runtime.InteropServices;
public static class RszWin {
  [DllImport("user32.dll")] public static extern bool PostMessage(IntPtr h, uint m, IntPtr w, IntPtr l);
  [StructLayout(LayoutKind.Sequential)] public struct RECT { public int L, T, R, B; }
  [DllImport("user32.dll")] public static extern bool GetWindowRect(IntPtr h, out RECT r);
  [DllImport("user32.dll")] public static extern bool PrintWindow(IntPtr h, IntPtr hdc, uint flags);
}
"@ -ReferencedAssemblies System.Drawing
}
function Hwnd($p) { for ($i = 0; $i -lt 50; $i++) { $p.Refresh(); if ($p.MainWindowHandle -ne 0) { return $p.MainWindowHandle }; Start-Sleep -Milliseconds 200 }; Fail "no window for $($p.Id)" }
# A picture of that window only, taken without focusing it or pressing a key.
# An earlier version tapped Alt to bring the window forward, and after it the
# control API stopped answering (probably the window's menu mode holding up
# the app's event loop; not confirmed).
function Shot($p, [string]$path) {
  $h = Hwnd $p
  $r = New-Object RszWin+RECT; [RszWin]::GetWindowRect($h, [ref]$r) | Out-Null
  $bmp = New-Object System.Drawing.Bitmap ($r.R - $r.L), ($r.B - $r.T)
  $g = [System.Drawing.Graphics]::FromImage($bmp); $dc = $g.GetHdc()
  # PW_RENDERFULLCONTENT: the webview's composited content too.
  [RszWin]::PrintWindow($h, $dc, 2) | Out-Null
  $g.ReleaseHdc($dc); $bmp.Save($path); $g.Dispose(); $bmp.Dispose()
  Say "picture: $path"
}

# --- the scratch app
$script:apps = @()
function StartApp($extra = @{}) {
  Remove-Item -Force -ErrorAction SilentlyContinue (Join-Path $S "local\suzuri\bridge.json")
  $keep = @{}
  $set = @{ USERPROFILE = "$S\home"; HOME = "$S\home"; LOCALAPPDATA = "$S\local"; RSZ_CONFIG_DIR = "$S\cfg"; RSZ_KEEPER_DIR = $KDir; RSZ_BRIDGE = "1"; RSZ_ALLOW_MULTI = "1"; RSZR_SCRATCH = $S; PATH = "$S\bin;$env:PATH" }
  foreach ($k in $extra.Keys) { $set[$k] = $extra[$k] }
  foreach ($k in $set.Keys) { $keep[$k] = [Environment]::GetEnvironmentVariable($k); [Environment]::SetEnvironmentVariable($k, $set[$k]) }
  try { $p = Start-Process -FilePath $App -PassThru -RedirectStandardError (Join-Path $S "app-err.log") -RedirectStandardOutput (Join-Path $S "app-out.log") }
  finally { foreach ($k in $keep.Keys) { [Environment]::SetEnvironmentVariable($k, $keep[$k]) } }
  $script:apps += $p
  $bj = Join-Path $S "local\suzuri\bridge.json"
  for ($i = 0; $i -lt 100 -and -not (Test-Path $bj); $i++) { Start-Sleep -Milliseconds 200 }
  if (-not (Test-Path $bj)) { Fail "app $($p.Id) wrote no bridge.json" }
  $script:bridge = Get-Content $bj -Raw | ConvertFrom-Json
  $p
}
# No keep-alive: a restarted app takes the same bridge port, and Windows
# PowerShell 5 would send the next request on the dead app's connection.
function Get_([string]$path) { Invoke-RestMethod -DisableKeepAlive -TimeoutSec 30 -Uri ($script:bridge.url + $path) -Headers @{ Authorization = "Bearer $($script:bridge.token)" } }
# The body as UTF-8 bytes: Windows PowerShell sends a string body as Latin-1.
function Post_([string]$path, $body) { Invoke-RestMethod -DisableKeepAlive -TimeoutSec 30 -Method Post -Uri ($script:bridge.url + $path) -Headers @{ Authorization = "Bearer $($script:bridge.token)" } -ContentType "application/json; charset=utf-8" -Body ([Text.Encoding]::UTF8.GetBytes(($body | ConvertTo-Json -Depth 10 -Compress))) }
function Call([string]$tool, $a = @{}) { Post_ "/v1/call" @{ tool = $tool; args = $a } }
# The window answers once its page has loaded (a debug build's first start,
# which copies the keeper host, can take a while).
function Ready { for ($i = 0; $i -lt 60; $i++) { try { $l = Call layout; if (@($l.windows[0].tabs).Count) { return } } catch { }; Start-Sleep -Milliseconds 500 }; Fail "the window never answered" }
function Submit($id, [string]$line) { Post_ "/v1/submit" @{ tab_id = $id; line = $line } | Out-Null }
function Text($id) { $t = Get_ "/v1/panes/$id`?lines=400"; ($t | ConvertTo-Json -Depth 10) }
function Leaves($n) { if ($null -ne $n.pane) { , $n.pane } else { (Leaves $n.a) + (Leaves $n.b) } }
# Processes under a pid (the shells and what they run).
function Tree([int]$root) {
  $all = Get-CimInstance Win32_Process | Select-Object ProcessId, ParentProcessId
  $out = @(); $todo = @($root)
  while ($todo.Count) { $x = $todo[0]; $todo = @($todo | Select-Object -Skip 1); foreach ($c in $all | Where-Object { $_.ParentProcessId -eq $x -and $_.ProcessId -ne $x }) { $out += [int]$c.ProcessId; $todo += [int]$c.ProcessId } }
  $out
}
# A pane's PATH comes from the registry (portable-pty builds a fresh Windows
# environment for each shell), not from the app: the stand-ins go on it in
# the pane, and the check stops unless `claude` there is the stand-in.
function UseStandIns($id) {
  $f = Join-Path $S "which-$id.txt"
  Submit $id "`$env:PATH = '$S\bin;' + `$env:PATH; (Get-Command claude).Source > '$f'"
  for ($i = 0; $i -lt 25 -and -not (Test-Path $f); $i++) { Start-Sleep -Milliseconds 200 }
  $w = if (Test-Path $f) { (Get-Content $f -Raw).Trim() } else { "" }
  if ($w -ne "$S\bin\claude.exe") { Fail "pane $id runs '$w' for claude, not the stand-in" }
  Remove-Item $f
}
function StandPids { $f = Join-Path $S "stand.pids"; if (Test-Path $f) { @(Get-Content $f | Where-Object { $_ }) } else { @() } }
function KillStandIns { foreach ($p in StandPids) { Stop-Process -Id ([int]$p) -Force -ErrorAction SilentlyContinue }; Remove-Item -Force -ErrorAction SilentlyContinue (Join-Path $S "stand.pids") }
function Sessions { Get-ChildItem (Join-Path $S "cfg\restore") -Directory -ErrorAction SilentlyContinue }

# The kept shells' hellos (rsz-keeper ls), as objects; the live ones only.
function Kept { @(& $Keeper --dir $KDir ls | Where-Object { $_ } | ForEach-Object { $_ | ConvertFrom-Json } | Where-Object { $_.id -and -not $_.error }) }
function KeptOne([int]$id) { Kept | Where-Object { $_.id -eq $id } | Select-Object -First 1 }
function Alive([int]$id) { [bool](Get-Process -Id $id -ErrorAction SilentlyContinue) }
# Kills every kept shell's host and what runs under it, by pid.
function KillKept {
  foreach ($k in Kept) {
    $kids = Tree ([int]$k.pid)
    Stop-Process -Id ([int]$k.pid) -Force -ErrorAction SilentlyContinue
    foreach ($c in $kids) { Stop-Process -Id $c -Force -ErrorAction SilentlyContinue }
  }
}

# The tabs and splits that were open: tab 1 split right [a | b], tab 2 [c],
# tab 1 in front. Returns the three panes.
# $cwds: each pane's folder as the first window showed it, else the folder
# its shell was cd'd into.
function CheckLayout([string]$label, $cwds = $null) {
  $L = Call layout
  $tabs = @($L.windows[0].tabs)
  if ($tabs.Count -ne 2) { Fail "${label}: 2 tabs, got $($tabs.Count): $(($tabs | % { $_.tree | ConvertTo-Json -Depth 8 -Compress }) -join ' ')" }
  $t1 = $tabs[0].tree
  if ($t1.axis -ne "right" -or $null -eq $t1.a.pane -or $null -eq $t1.b.pane) { Fail "${label}: tab 1 is a split right: $($t1 | ConvertTo-Json -Compress)" }
  if ($null -eq $tabs[1].tree.pane) { Fail "${label}: tab 2 is one pane" }
  if (-not $tabs[0].active) { Fail "${label}: tab 1 in front" }
  $panes = @{}; foreach ($t in $tabs) { foreach ($p in $t.panes) { $panes[[string]$p.id] = $p } }
  $a = $panes[[string]$t1.a.pane]; $b = $panes[[string]$t1.b.pane]; $c = $panes[[string]$tabs[1].tree.pane]
  Say "1. tabs and splits: tab 1 = split right [$($a.id) | $($b.id)], tab 2 = [$($c.id)], tab 1 in front"
  foreach ($x in @(@($a, "alpha"), @($b, "beta"), @($c, "gamma"))) {
    $want = if ($cwds) { $cwds[[int]$x[0].id] } else { Join-Path $S "work\$($x[1])" }
    if ($x[0].cwd -ne $want) { Fail "${label}: pane $($x[0].id) is in '$want': '$($x[0].cwd)'" }
  }
  @($a, $b, $c)
}

# Checks a window that came back by reattaching to the kept shells: the
# same shells, programs still running in them, nothing restored.
function CheckKept([string]$label, $was, $cwds) {
  $a, $b, $c = CheckLayout $label $cwds
  foreach ($x in @(@($a, $p1), @($b, $p2), @($c, $p3))) {
    if ([int]$x[0].id -ne $x[1]) { Fail "${label}: pane $($x[1]) not back in its place (found $($x[0].id))" }
    $k = KeptOne $x[1]
    if (-not $k) { Fail "${label}: pane $($x[1]) has no kept shell" }
    if ([int]$k.pid -ne $was[$x[1]].pid -or [int]$k.shell_pid -ne $was[$x[1]].shell_pid) { Fail "${label}: pane $($x[1]) is not the same shell: host $($k.pid) shell $($k.shell_pid), was $($was[$x[1]].pid) $($was[$x[1]].shell_pid)" }
    Say "2. pane $($x[1]): the same kept shell (host $($k.pid), shell $($k.shell_pid)) in $($x[0].cwd), $($k.viewers) viewer"
  }
  Say "3. input bars: '$($a.input)' '$($b.input)' '$($c.input)'"
  if ($a.input -or $b.input -or $c.input) { Fail "${label}: something offered in an input bar" }
  $ta = Text $a.id; $tb = Text $b.id; $tc = Text $c.id
  if ($ta -notmatch "alpha-line-300" -or $ta -notmatch "alpha-line-1\b") { Fail "${label}: pane $($a.id)'s own text is not there" }
  if ($tc -notmatch "dev server \(stand-in\) serving") { Fail "${label}: pane $($c.id)'s dev server text is not there" }
  if ($tb -notmatch "Claude Code \(stand-in\) in") { Fail "${label}: pane $($b.id)'s Claude Code text is not there" }
  foreach ($t in @(@($a.id, $ta), @($b.id, $tb), @($c.id, $tc))) {
    if ($t[1] -match "restored .{1,6} saved") { Fail "${label}: pane $($t[0]) shows the restored mark" }
  }
  if ($tb -match "Claude Code was running here") { Fail "${label}: pane $($b.id) offers a resume" }
  Say "4. the live text in each pane (alpha-line-1..300, Claude Code, the dev server), no restored mark, no resume offered"
  Start-Sleep -Seconds 2
  if (Test-Path (Join-Path $S "resumed.log")) { Fail "${label}: claude was run: $(Get-Content (Join-Path $S 'resumed.log'))" }
  $now = @(StandPids)
  if ($now.Count -ne 2) { Fail "${label}: programs started or ended: $now (was $script:standWas)" }
  foreach ($sp in $now) { if (-not (Alive ([int]$sp))) { Fail "${label}: program $sp ended" } }
  Say "4. Claude Code and the dev server still running ($($now -join ', ')); nothing new ran"
  @($a, $b, $c)
}

# Checks a window restored after a reboot against what was open. Returns
# the three panes.
function CheckRestored([string]$label) {
  $a, $b, $c = CheckLayout $label
  foreach ($x in $a, $b, $c) { Say "2. pane $($x.id): a new shell in $($x.cwd)" }
  Say "3. input bar of the Claude Code pane: '$($b.input)' | the others: '$($a.input)' '$($c.input)'"
  if ($b.input -ne "claude --resume $SID") { Fail "${label}: resume not offered: '$($b.input)'" }
  if ($a.input -or $c.input) { Fail "${label}: something offered in another pane" }
  $ta = Text $a.id
  if ($ta -notmatch "alpha-line-300") { Fail "${label}: old text not above pane $($a.id)" }
  if ($ta -notmatch "alpha-line-1\b") { Fail "${label}: old text not all there in pane $($a.id)" }
  if ($ta -notmatch "restored .{1,6} saved") { Fail "${label}: no restored mark in pane $($a.id)" }
  Say "2. pane $($a.id): old lines alpha-line-1..300 and the restored mark"
  $tc = Text $c.id
  if ($tc -notmatch "dev server \(stand-in\) serving") { Fail "${label}: old text not above pane $($c.id) (the tab not in front)" }
  if ($tc -notmatch "restored .{1,6} saved") { Fail "${label}: no restored mark in pane $($c.id)" }
  Say "2. pane $($c.id) (the tab not in front): its old text and the restored mark too"
  $tb = Text $b.id
  if ($tb -notmatch "Claude Code was running here") { Fail "${label}: no resume note in pane $($b.id)" }
  Start-Sleep -Seconds 2
  if (Test-Path (Join-Path $S "resumed.log")) { Fail "${label}: claude was run by itself: $(Get-Content (Join-Path $S 'resumed.log'))" }
  if ((StandPids).Count) { Fail "${label}: a program started by itself: $(StandPids)" }
  Say "3. nothing ran by itself: no Claude Code, no dev server, no resume"
  @($a, $b, $c)
}

# Hard-kills an app, everything under it and every kept shell (a reboot).
function KillAll($p) {
  $kids = Tree $p.Id
  Stop-Process -Id $p.Id -Force -ErrorAction SilentlyContinue
  foreach ($k in $kids) { Stop-Process -Id $k -Force -ErrorAction SilentlyContinue }
  KillKept
  Start-Sleep -Seconds 1
}

# Card hearth#975: one pane of a shell without the PowerShell hooks ($label),
# started with env $vars and the settings in $config, prints lines ($fill); after
# a hard kill the next start shows each of $want (whole lines, in order),
# then the restored line, dim (no literal [2m), and nothing of the old text
# below it; $prompt matches the new shell's prompt line.
# Then a cmd and a PowerShell started inside it ($nested, then exit from
# each) print nothing of it again.
# $dir (card hearth#1025): the folder $fill moves to. The save names it,
# though the shell reports no folder, and the new shell starts in it.
function RestoreCase([string]$label, $vars, $config, [string]$fill, [string[]]$want, [scriptblock]$prompt, [string]$picture, [string[]]$nested, [string]$dir) {
  Say "== $label"
  foreach ($d in Sessions) { Remove-Item -Recurse -Force $d.FullName }
  $cfgFile = Join-Path $S "cfg\config.json"
  Remove-Item -Force -ErrorAction SilentlyContinue $cfgFile
  if ($config) { [IO.File]::WriteAllText($cfgFile, ($config | ConvertTo-Json -Depth 5 -Compress)) }
  $a1 = StartApp $vars
  Ready
  Start-Sleep -Seconds 3
  $id = [int](Call layout).windows[0].tabs[0].panes[0].id
  Submit $id $fill
  Start-Sleep -Seconds 8
  $before = Get_ "/v1/panes/$id`?lines=400"
  $lines = @($before.tail) + @($before.live) | ForEach-Object { $_.TrimEnd() }
  foreach ($w in $want) { if ($lines -notcontains $w) { Fail "${label}: '$w' never shown before the kill: $(($lines | Select-Object -Last 15) -join ' | ')" } }
  if (-not (Test-Path (Join-Path $S "cfg\restore\$($a1.Id)\session.json"))) { Fail "${label}: no session saved" }
  Say "1. before the kill: $($want -join ' / ') on screen"
  if ($dir) {
    $saved = (Get-Content (Join-Path $S "cfg\restore\$($a1.Id)\session.json") -Raw | ConvertFrom-Json).tabs[0].tree.cwd
    if ($saved -ne $dir) { Fail "${label}: the pane's folder saved as '$saved', not '$dir'" }
    Say "1. folder saved for a reboot: $saved"
  }
  KillAll $a1
  $a2 = StartApp $vars
  Ready
  Start-Sleep -Seconds 6
  $q = (Call layout).windows[0].tabs[0].panes[0]
  $t = Get_ "/v1/panes/$($q.id)`?lines=400"
  $lines = @(@($t.tail) + @($t.live) | ForEach-Object { $_.TrimEnd() })
  $shown = ($lines | Where-Object { $_ } | Select-Object -Last 12) -join "`n"
  $at = -1
  foreach ($w in $want) {
    $i = [array]::IndexOf($lines, $w)
    if ($i -lt 0) { Fail "${label}: '$w' not back after the kill; the end:`n$shown" }
    if ($i -le $at) { Fail "${label}: '$w' out of order; the end:`n$shown" }
    $at = $i
  }
  $markRe = "^$([char]0x2500){3} restored $([char]0xB7) saved .+ $([char]0x2500){3}`$"
  $marks = @(0..($lines.Count - 1) | Where-Object { $lines[$_] -match $markRe })
  if ($marks.Count -ne 1) { Fail "${label}: $($marks.Count) restored lines, not one; the end:`n$shown" }
  if ($marks[0] -le $at) { Fail "${label}: the restored line is above the old text; the end:`n$shown" }
  if (($lines -join "`n") -match '\[2m|\[0m') { Fail "${label}: a literal [2m or [0m: the dim did not take; the end:`n$shown" }
  $below = @($lines[($marks[0] + 1)..($lines.Count - 1)] | Where-Object { $_ -and $want -contains $_ })
  if ($below.Count) { Fail "${label}: old text below the restored line: $below" }
  $last = @($lines | Where-Object { $_.Trim() }) | Select-Object -Last 1
  if (-not (& $prompt $last)) { Fail "${label}: no prompt under the restored line: '$last'; the end:`n$shown" }
  $prof = @(0..($lines.Count - 1) | Where-Object { $lines[$_] -match '^profile-ran-\d+$' })
  if ($label -match "plain") {
    if ($prof.Count -ne 2) { Fail "${label}: the scratch PowerShell profile ran $($prof.Count) times in the two starts, not 2; the end:`n$shown" }
    # The new start's line has the later time; the old text's is the first start's.
    $new, $old = $prof | Sort-Object { [long]($lines[$_] -replace '\D', '') } -Descending
    if ($new -gt $old) { Fail "${label}: the profile's output of the new start (line $new) is not above the old text (its first start's line $old); the end:`n$shown" }
    Say "2. the new start's profile line (line $new) comes before the old text (the first start's profile line, line $old) and the restored line (line $($marks[0]))"
  }
  Say "2. after the kill: $($want -join ' / ') back in order, then '$($lines[$marks[0]])' once, no literal [2m, then the prompt '$last'"
  if ($dir) {
    if ($q.cwd -ne $dir) { Fail "${label}: the new pane is in '$($q.cwd)', not '$dir'" }
    if (-not $last.Contains($dir)) { Fail "${label}: the new shell's prompt '$last' is not in '$dir'" }
    Say "2. the new shell starts in the old folder: the pane shows '$($q.cwd)', and the new shell's own prompt '$last'"
  }
  Shot $a2 (Join-Path $Out $picture)
  foreach ($n in $nested) { Submit $q.id $n; Start-Sleep -Seconds 3 }
  Submit $q.id "echo nested-done"
  foreach ($n in $nested) { Submit $q.id "exit"; Start-Sleep -Seconds 2 }
  $t = Get_ "/v1/panes/$($q.id)`?lines=600"
  $lines = @(@($t.tail) + @($t.live) | ForEach-Object { $_.TrimEnd() })
  if ($lines -notcontains "nested-done") { Fail "${label}: the nested shells never ran a line; the end:`n$(($lines | Where-Object { $_ } | Select-Object -Last 12) -join "`n")" }
  $n = @($lines | Where-Object { $_ -match $markRe }).Count
  $w = @($lines | Where-Object { $_ -eq $want[-1] }).Count
  if ($n -ne 1 -or $w -ne 1) { Fail "${label}: after a nested $($nested -join ' and '): $n restored lines and $w of '$($want[-1])', not one each" }
  Say "3. a nested $($nested -join ' and a nested ') printed nothing again (one restored line, one '$($want[-1])')"
  KillAll $a2
  foreach ($d in Sessions) { Remove-Item -Recurse -Force $d.FullName }
  Remove-Item -Force -ErrorAction SilentlyContinue $cfgFile
}

try {
  Say "== scratch: $S (keeper folder $KDir)"
  if ($Cases -contains "kept") {
  $app1 = StartApp
  Ready
  Start-Sleep -Seconds 2
  $L = Call layout
  $p1 = [int]$L.windows[0].tabs[0].panes[0].id
  if (-not $p1) { Fail "no pane after start" }
  UseStandIns $p1
  Submit $p1 "cd $S\work\alpha; 1..300 | % { `"alpha-line-`$_`" }"
  Start-Sleep -Seconds 1
  $p2 = [int](Call split @{ pane_id = $p1; axis = "right" }).pane_id
  Start-Sleep -Seconds 2
  UseStandIns $p2
  Submit $p2 "cd $S\work\beta; echo beta-before-claude; claude"
  $p3 = [int](Call new_tab).pane_id
  Start-Sleep -Seconds 2
  UseStandIns $p3
  Submit $p3 "cd $S\work\gamma; echo gamma-here; devserver"
  Call focus @{ pane_id = $p1 } | Out-Null
  Start-Sleep -Seconds 6
  Say "panes: $p1 | $p2 (Claude Code) in tab 1; $p3 (a dev server) in tab 2"
  $was = @{}
  foreach ($id in $p1, $p2, $p3) {
    $k = KeptOne $id
    if (-not $k) { Fail "pane $id is not a kept shell: $(Kept | ConvertTo-Json -Compress)" }
    $was[$id] = @{ pid = [int]$k.pid; shell_pid = [int]$k.shell_pid }
    if ((Tree $app1.Id) -contains [int]$k.pid) { Fail "pane ${id}'s host $($k.pid) is under the app" }
    Say "pane ${id}: kept by host $($k.pid), shell $($k.shell_pid), outside the app's process tree"
  }
  # Each pane's folder as this window shows it. A pane busy with a program
  # started on the same line as its cd (Claude Code here) shows the folder
  # of its last prompt; the saved session has the program's own folder.
  $cwds = @{}
  foreach ($t in @((Call layout).windows[0].tabs)) { foreach ($p in $t.panes) { $cwds[[int]$p.id] = $p.cwd } }
  Say "folders shown: $(($cwds.Keys | Sort-Object | % { "${_}: $($cwds[$_])" }) -join '; ')"
  $script:standWas = @(StandPids)
  if ($script:standWas.Count -ne 2) { Fail "Claude Code and the dev server did not both start: $($script:standWas)" }
  $RDIR = Join-Path $S "cfg\restore\$($app1.Id)"
  if (-not (Test-Path "$RDIR\session.json")) { Fail "no session saved in $RDIR" }
  $sess = Get-Content "$RDIR\session.json" -Raw | ConvertFrom-Json
  $tree = $sess.tabs[0].tree
  if ($sess.tabs.Count -ne 2) { Fail "saved $($sess.tabs.Count) tabs" }
  if ($tree.b.claude.session -ne $SID) { Fail "the Claude Code pane's conversation not saved: $($tree.b | ConvertTo-Json -Compress)" }
  if (-not ($tree.a.cwd -like "*\work\alpha")) { Fail "pane 1's folder not saved: '$($tree.a.cwd)'" }
  if (-not ($sess.tabs[1].tree.cwd -like "*\work\gamma")) { Fail "pane 3's folder not saved: '$($sess.tabs[1].tree.cwd)'" }
  Say "saved for a reboot: $(($sess.tabs | ConvertTo-Json -Depth 8 -Compress).Substring(0, 300))"
  Shot $app1 (Join-Path $Out "1-before-quit.png")

  Say "== quit with the window's close button: the shells stay running"
  [RszWin]::PostMessage((Hwnd $app1), 0x0010, [IntPtr]::Zero, [IntPtr]::Zero) | Out-Null
  if (-not $app1.WaitForExit(15000)) { Fail "the app did not quit on the close button" }
  Start-Sleep -Seconds 2
  foreach ($id in $p1, $p2, $p3) {
    if (-not (Alive $was[$id].pid) -or -not (Alive $was[$id].shell_pid)) { Fail "pane ${id}'s host or shell ended with the app" }
  }
  foreach ($sp in $script:standWas) { if (-not (Alive ([int]$sp))) { Fail "program $sp ended with the app" } }
  Say "1. the app quit; hosts, shells, Claude Code and the dev server all still run; kept: $((Kept | % { "$($_.id):$($_.viewers) viewers" }) -join ' ')"
  Say "   restore folders: $((Sessions | % Name) -join ' ')"

  Say "== start again: the live shells come back, nothing restored"
  $app2 = StartApp
  Ready
  for ($i = 0; $i -lt 40 -and @(Kept | Where-Object { $_.viewers -eq 1 }).Count -lt 3; $i++) { Start-Sleep -Milliseconds 500 }
  if (@(Kept | Where-Object { $_.viewers -eq 1 }).Count -lt 3) { Fail "the new app did not attach to all three shells: $(Kept | ConvertTo-Json -Compress)" }
  Start-Sleep -Seconds 3
  $null = CheckKept "after the close button" $was $cwds
  Shot $app2 (Join-Path $Out "2-after-restart.png")
  # The new window saves its own session (for a reboot from here).
  Start-Sleep -Seconds 4
  Say "   restore folders: $((Sessions | % Name) -join ' ')"

  Say "== reboot: the app, every keeper host and every shell killed at once"
  $kids = Tree $app2.Id
  Stop-Process -Id $app2.Id -Force
  foreach ($k in $kids) { Stop-Process -Id $k -Force -ErrorAction SilentlyContinue }
  KillKept
  KillStandIns
  Remove-Item -Force -ErrorAction SilentlyContinue "$S\home\.claude\sessions\*.json"
  Start-Sleep -Seconds 1
  foreach ($id in $p1, $p2, $p3) { if ((Alive $was[$id].pid) -or (Alive $was[$id].shell_pid)) { Fail "pane ${id}'s host or shell survived the kill" } }
  if ((Kept).Count) { Fail "kept shells left: $(Kept | ConvertTo-Json -Compress)" }
  # Windows reuses pids and gives no boot time: after a reboot a saved
  # session's pid can belong to an unrelated running process. Give the last
  # one the pid of this script (running, started at another time): it must
  # still come back.
  $R2 = Join-Path $S "cfg\restore\$($app2.Id)"
  if (-not (Test-Path "$R2\session.json")) { Fail "the second window saved no session" }
  Rename-Item $R2 $PID
  Say "nothing left running, no kept shell answers; restore folders: $((Sessions | % Name) -join ' ') (the last one renamed to this script's pid $PID)"
  $app3 = StartApp
  Ready
  # Each fresh shell prints its old text once it starts.
  Start-Sleep -Seconds 5
  $qa, $qb, $qc = CheckRestored "after a hard kill"
  Shot $app3 (Join-Path $Out "3-after-kill.png")

  Say "== the offered line, submitted in that pane, resumes the conversation"
  UseStandIns $qb.id
  Submit $qb.id "claude --resume $SID"
  Start-Sleep -Seconds 3
  if (-not ((Get-Content (Join-Path $S "resumed.log") -ErrorAction SilentlyContinue) -match "RESUMED $SID")) { Fail "the resume did not run" }
  Say "ran: $(Get-Content (Join-Path $S 'resumed.log'))"
  Remove-Item (Join-Path $S "resumed.log")
  KillStandIns
  Start-Sleep -Seconds 2

  Say "== exit in each pane; the last one ends the session"
  foreach ($q in $qc, $qa) { Submit $q.id "exit"; Start-Sleep -Seconds 2 }
  $n = @((Call layout).windows[0].tabs).Count
  Say "after exit in two panes: $n tab(s) left"
  Submit $qb.id "exit"
  if (-not $app3.WaitForExit(15000)) { Fail "the app stayed open after exit in its last pane" }
  Start-Sleep -Seconds 1
  $left = @(Sessions)
  if ($left.Count) { Fail "a session is left after exit in the last pane: $($left.Name)" }
  if ((Kept).Count) { Fail "a kept shell is left after exit in the last pane: $(Kept | ConvertTo-Json -Compress)" }
  Say "5. the app closed; no session and no kept shell is left"
  $app4 = StartApp
  Ready
  Start-Sleep -Seconds 3
  $tabs = @((Call layout).windows[0].tabs)
  $leaves = @(Leaves $tabs[0].tree)
  if ($tabs.Count -ne 1 -or $leaves.Count -ne 1) { Fail "not one fresh pane: $($tabs.Count) tabs" }
  $t = Text $leaves[0]
  if ($t -match "restored .{1,6} saved|alpha-line|Claude Code|dev server") { Fail "the fresh pane shows old text" }
  if ($tabs[0].panes[0].input) { Fail "the fresh pane offers '$($tabs[0].panes[0].input)'" }
  Say "5. the next start: one tab, one fresh pane, nothing restored or offered"
  Shot $app4 (Join-Path $Out "4-fresh-after-exit.png")
  KillAll $app4
  }

  # Japanese, a box line and a middle dot (nihongo no gyou, U+2500, U+00B7), from char
  # codes: Windows PowerShell reads this file as ANSI.
  $ja = (-join ([char[]](0x65E5, 0x672C, 0x8A9E, 0x306E, 0x884C))) + " $([char]0x2500) $([char]0xB7) done"
  if ($Cases -contains "plain") {
    # A PowerShell profile in the scratch home that prints a line: its
    # output comes before the old text, not under the restored line.
    foreach ($d in "Documents\PowerShell", "Documents\WindowsPowerShell") {
      New-Item -ItemType Directory -Force (Join-Path $S "home\$d") | Out-Null
      Set-Content -Encoding ASCII (Join-Path $S "home\$d\Microsoft.PowerShell_profile.ps1") "'profile-ran-' + [DateTime]::Now.Ticks"
    }
    RestoreCase "plain-shell setting (RSZ_NO_BAR=1): PowerShell with no hooks" @{ RSZ_NO_BAR = "1" } $null `
      "cd $S\work\alpha; 1..300 | % { `"plain-line-`$_`" }; 'plain $ja'" `
      @("plain-line-1", "plain-line-150", "plain-line-300", "plain $ja") `
      { param($l) $l -match '^PS .*>$' -or $l -match "$([char]0x276F)\s*`$" -or $l -match '>\s*$' } "5-plain-shell-restored.png" @("cmd.exe /d", "powershell -NoLogo -NoProfile") "$S\work\alpha"
  }
  if ($Cases -contains "cmd") {
    RestoreCase "a cmd.exe profile (Cmd, the active profile)" @{} @{ active_profile = "Cmd" } `
      "cd /d $S\work\beta & (for /L %i in (1,1,300) do @echo cmd-line-%i) & echo cmd $ja" `
      @("cmd-line-1", "cmd-line-150", "cmd-line-300", "cmd $ja") `
      { param($l) $l -match '^[A-Za-z]:\\.*>$' } "6-cmd-restored.png" @("cmd.exe /d", "powershell -NoLogo -NoProfile") "$S\work\beta"
  }

  $after = RealStamp
  if ($after -ne $before) { Fail "the installed suzuri's files changed: '$before' -> '$after'" }
  Say "6. scratch build ($App) with USERPROFILE, LOCALAPPDATA, RSZ_CONFIG_DIR and RSZ_KEEPER_DIR under $S; the installed suzuri's files are untouched: $(if ($before) { $before } else { 'none exist' })"
  Say "PASS"
}
finally {
  foreach ($p in $script:apps) { if (-not $p.HasExited) { $kids = Tree $p.Id; Stop-Process -Id $p.Id -Force -ErrorAction SilentlyContinue; foreach ($k in $kids) { Stop-Process -Id $k -Force -ErrorAction SilentlyContinue } } }
  try { KillKept } catch { }
  KillStandIns
  if (-not $Keep) { Start-Sleep -Milliseconds 800; Remove-Item -Recurse -Force -ErrorAction SilentlyContinue $S }
}
