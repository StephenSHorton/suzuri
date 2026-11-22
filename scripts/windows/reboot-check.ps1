# Card hearth#950, the Windows side of hearth#916, on a scratch debug build:
# after quitting suzuri with the window's close button (and again after a
# hard kill of the app and every shell, as a reboot does), the next start
# shows the same tabs and splits, a fresh shell in each old folder, the old
# text above it marked restored, and a pane that was running Claude Code
# offers `claude --resume <id>` in its input bar without running it. Then
# `exit` in the last pane, and the next start opens one fresh pane.
#
# Windows has no kept shells yet (until hearth#923), so every start with a
# saved session restores it.
#
# Claude Code is a stand-in claude.exe (compiled here from C#) that writes
# the session file the real one does (%USERPROFILE%\.claude\sessions\<pid>.json,
# pid = the shell's direct child, as the real claude.exe is); a second
# stand-in, devserver.exe, is a long-running program that is not Claude Code.
#
# Everything runs in a scratch folder: its own USERPROFILE/HOME, LOCALAPPDATA
# and RSZ_CONFIG_DIR, so the installed suzuri's files are never touched.
#   cargo build -p suzuri
#   powershell -NoProfile -ExecutionPolicy Bypass -File scripts\windows\reboot-check.ps1 [-Out <folder for pictures>] [-Keep]
param([string]$App = "", [string]$Out = "", [switch]$Keep)
$ErrorActionPreference = "Stop"
$R = Split-Path -Parent (Split-Path -Parent $PSScriptRoot)
if (-not $App) { $App = Join-Path $R "target\debug\suzuri.exe" }
if (-not (Test-Path $App)) { throw "build first: cargo build -p suzuri" }
$S = Join-Path $env:TEMP ("rszr-" + $PID)
if (-not $Out) { $Out = $S }
$SID = "7f3c2a10-5b6d-4e8f-9a01-23456789abcd"
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
function StartApp {
  Remove-Item -Force -ErrorAction SilentlyContinue (Join-Path $S "local\suzuri\bridge.json")
  $keep = @{}
  $set = @{ USERPROFILE = "$S\home"; HOME = "$S\home"; LOCALAPPDATA = "$S\local"; RSZ_CONFIG_DIR = "$S\cfg"; RSZ_BRIDGE = "1"; RSZ_ALLOW_MULTI = "1"; RSZR_SCRATCH = $S; PATH = "$S\bin;$env:PATH" }
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
function Get_([string]$path) { Invoke-RestMethod -Uri ($script:bridge.url + $path) -Headers @{ Authorization = "Bearer $($script:bridge.token)" } }
function Post_([string]$path, $body) { Invoke-RestMethod -Method Post -Uri ($script:bridge.url + $path) -Headers @{ Authorization = "Bearer $($script:bridge.token)" } -ContentType "application/json" -Body ($body | ConvertTo-Json -Depth 10 -Compress) }
function Call([string]$tool, $a = @{}) { Post_ "/v1/call" @{ tool = $tool; args = $a } }
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

# Checks a restored window against what was open. Returns the three panes.
function CheckRestored([string]$label) {
  $L = Call layout
  $tabs = @($L.windows[0].tabs)
  if ($tabs.Count -ne 2) { Fail "${label}: 2 tabs, got $($tabs.Count): $($L | ConvertTo-Json -Depth 12 -Compress)" }
  $t1 = $tabs[0].tree
  if ($t1.axis -ne "right" -or $null -eq $t1.a.pane -or $null -eq $t1.b.pane) { Fail "${label}: tab 1 is a split right: $($t1 | ConvertTo-Json -Compress)" }
  if ($null -eq $tabs[1].tree.pane) { Fail "${label}: tab 2 is one pane" }
  if (-not $tabs[0].active) { Fail "${label}: tab 1 in front" }
  $panes = @{}; foreach ($t in $tabs) { foreach ($p in $t.panes) { $panes[[string]$p.id] = $p } }
  $a = $panes[[string]$t1.a.pane]; $b = $panes[[string]$t1.b.pane]; $c = $panes[[string]$tabs[1].tree.pane]
  Say "1. tabs and splits: tab 1 = split right [$($a.id) | $($b.id)], tab 2 = [$($c.id)], tab 1 in front"
  foreach ($x in @(@($a, "alpha"), @($b, "beta"), @($c, "gamma"))) {
    if (-not ($x[0].cwd -like "*\work\$($x[1])")) { Fail "${label}: pane $($x[0].id) starts in $($x[1]): '$($x[0].cwd)'" }
    Say "2. pane $($x[0].id): a new shell in $($x[0].cwd)"
  }
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

try {
  Say "== scratch: $S"
  $app1 = StartApp
  Start-Sleep -Seconds 3
  $L = Call layout
  $p1 = $L.windows[0].tabs[0].panes[0].id
  if (-not $p1) { Fail "no pane after start" }
  UseStandIns $p1
  Submit $p1 "cd $S\work\alpha; 1..300 | % { `"alpha-line-`$_`" }"
  Start-Sleep -Seconds 1
  $p2 = (Call split @{ pane_id = $p1; axis = "right" }).pane_id
  Start-Sleep -Seconds 2
  UseStandIns $p2
  Submit $p2 "cd $S\work\beta; echo beta-before-claude; claude"
  $p3 = (Call new_tab).pane_id
  Start-Sleep -Seconds 2
  UseStandIns $p3
  Submit $p3 "cd $S\work\gamma; echo gamma-here; devserver"
  Call focus @{ pane_id = $p1 } | Out-Null
  Start-Sleep -Seconds 6
  Say "panes: $p1 | $p2 (Claude Code) in tab 1; $p3 (a dev server) in tab 2"
  $RDIR = Join-Path $S "cfg\restore\$($app1.Id)"
  if (-not (Test-Path "$RDIR\session.json")) { Fail "no session saved in $RDIR" }
  $sess = Get-Content "$RDIR\session.json" -Raw | ConvertFrom-Json
  $tree = $sess.tabs[0].tree
  if ($sess.tabs.Count -ne 2) { Fail "saved $($sess.tabs.Count) tabs" }
  if ($tree.b.claude.session -ne $SID) { Fail "the Claude Code pane's conversation not saved: $($tree.b | ConvertTo-Json -Compress)" }
  if (-not ($tree.a.cwd -like "*\work\alpha")) { Fail "pane 1's folder not saved: '$($tree.a.cwd)'" }
  if (-not ($sess.tabs[1].tree.cwd -like "*\work\gamma")) { Fail "pane 3's folder not saved: '$($sess.tabs[1].tree.cwd)'" }
  Say "saved: $(($sess.tabs | ConvertTo-Json -Depth 8 -Compress).Substring(0, 400))"
  Shot $app1 (Join-Path $Out "1-before-quit.png")

  Say "== quit with the window's close button"
  [RszWin]::PostMessage((Hwnd $app1), 0x0010, [IntPtr]::Zero, [IntPtr]::Zero) | Out-Null
  if (-not $app1.WaitForExit(15000)) { Fail "the app did not quit on the close button" }
  Start-Sleep -Seconds 1
  KillStandIns
  Remove-Item -Force -ErrorAction SilentlyContinue "$S\home\.claude\sessions\*.json"
  if (-not (Test-Path "$RDIR\session.json")) { Fail "quitting cleared the saved session" }
  Say "the app quit; its session is kept: $((Sessions | % Name) -join ' ')"
  # Windows reuses pids and gives no boot time: after a reboot a saved
  # session's pid can belong to an unrelated running process. Give this one
  # the pid of this script (running, started at another time): it must
  # still come back.
  Rename-Item $RDIR $PID
  Say "the saved session now names pid $PID (this script: running, not the app that saved it)"

  Say "== start again"
  $app2 = StartApp
  Start-Sleep -Seconds 6
  $pa, $pb, $pc = CheckRestored "after the close button"
  Shot $app2 (Join-Path $Out "2-after-restart.png")

  Say "== the offered line, submitted in that pane, resumes the conversation"
  UseStandIns $pb.id
  Submit $pb.id "claude --resume $SID"
  Start-Sleep -Seconds 3
  if (-not ((Get-Content (Join-Path $S "resumed.log") -ErrorAction SilentlyContinue) -match "RESUMED $SID")) { Fail "the resume did not run" }
  Say "ran: $(Get-Content (Join-Path $S 'resumed.log'))"
  Remove-Item (Join-Path $S "resumed.log")
  Start-Sleep -Seconds 4

  Say "== reboot: the app and every shell killed at once"
  $kids = Tree $app2.Id
  Stop-Process -Id $app2.Id -Force
  foreach ($k in $kids) { Stop-Process -Id $k -Force -ErrorAction SilentlyContinue }
  KillStandIns
  Remove-Item -Force -ErrorAction SilentlyContinue "$S\home\.claude\sessions\*.json"
  Start-Sleep -Seconds 1
  Say "nothing left running; restore folders: $((Sessions | % Name) -join ' ')"
  $app3 = StartApp
  Start-Sleep -Seconds 6
  $qa, $qb, $qc = CheckRestored "after a hard kill"
  Shot $app3 (Join-Path $Out "3-after-kill.png")

  Say "== exit in each pane; the last one ends the session"
  foreach ($q in $qc, $qa) { Submit $q.id "exit"; Start-Sleep -Seconds 2 }
  $n = @((Call layout).windows[0].tabs).Count
  Say "after exit in two panes: $n tab(s) left"
  Submit $qb.id "exit"
  if (-not $app3.WaitForExit(15000)) { Fail "the app stayed open after exit in its last pane" }
  Start-Sleep -Seconds 1
  $left = @(Sessions)
  if ($left.Count) { Fail "a session is left after exit in the last pane: $($left.Name)" }
  Say "4. the app closed and no session is left"
  $app4 = StartApp
  Start-Sleep -Seconds 5
  $tabs = @((Call layout).windows[0].tabs)
  $leaves = @(Leaves $tabs[0].tree)
  if ($tabs.Count -ne 1 -or $leaves.Count -ne 1) { Fail "not one fresh pane: $($tabs.Count) tabs" }
  $t = Text $leaves[0]
  if ($t -match "restored .{1,6} saved|alpha-line") { Fail "the fresh pane shows old text" }
  Say "4. the next start: one tab, one fresh pane, nothing restored"
  Shot $app4 (Join-Path $Out "4-fresh-after-exit.png")

  $after = RealStamp
  if ($after -ne $before) { Fail "the installed suzuri's files changed: '$before' -> '$after'" }
  Say "5. scratch build ($App) with USERPROFILE, LOCALAPPDATA and RSZ_CONFIG_DIR under $S; the installed suzuri's files are untouched: $(if ($before) { $before } else { 'none exist' })"
  Say "PASS"
}
finally {
  foreach ($p in $script:apps) { if (-not $p.HasExited) { $kids = Tree $p.Id; Stop-Process -Id $p.Id -Force -ErrorAction SilentlyContinue; foreach ($k in $kids) { Stop-Process -Id $k -Force -ErrorAction SilentlyContinue } } }
  KillStandIns
  if (-not $Keep) { Start-Sleep -Milliseconds 500; Remove-Item -Recurse -Force -ErrorAction SilentlyContinue $S }
}
