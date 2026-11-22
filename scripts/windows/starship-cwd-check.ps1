# Card hearth#1031, the live check of hearth#1028 on Windows: a plain-shell
# pane (RSZ_NO_BAR=1, no suzuri hooks) running Windows PowerShell with
# starship in its profile is `cd`'d into work\alpha; after a hard kill of the
# app, its keeper hosts and shells (a reboot), the next start's shell is in
# work\alpha. That restored shell, cd'd to work\beta and killed again, comes
# back in work\beta.
#
# The app runs with RSZ_TRACE, so the reader's log shows whether ConPTY passed a folder report (OSC 7 / OSC 9;9) through: a
# kept shell's reader is its keeper host, logging to cfg\keeper\<pane id>.log.
#
# Stock starship sends no folder report; suzuri gives an unhooked PowerShell
# running starship the report from starship's own pre-prompt hook,
# Invoke-Starship-PreCommand (hearth#1031). -Report (none, osc99, osc7): a
# hook of the profile's own, which must still run (none: stock starship).
#
# Everything runs in a scratch folder (its own USERPROFILE, LOCALAPPDATA,
# RSZ_CONFIG_DIR and keeper folder), as scripts\windows\reboot-check.ps1 does.
#   cargo build -p suzuri -p rsz-keeper
#   powershell -NoProfile -ExecutionPolicy Bypass -File scripts\windows\starship-cwd-check.ps1 [-Report none|osc99|osc7] [-Keep]
param([string]$App = "", [ValidateSet("none", "osc99", "osc7")][string]$Report = "none", [switch]$Keep)
$ErrorActionPreference = "Stop"
# Starship's prompt is a U+276F: written as UTF-8, not the code page.
try { [Console]::OutputEncoding = [Text.Encoding]::UTF8 } catch { }
$R = Split-Path -Parent (Split-Path -Parent $PSScriptRoot)
if (-not $App) { $App = Join-Path $R "target\debug\suzuri.exe" }
$Keeper = Join-Path (Split-Path -Parent $App) "rsz-keeper.exe"
if (-not ((Test-Path $App) -and (Test-Path $Keeper))) { throw "build first: cargo build -p suzuri -p rsz-keeper" }
$Star = (Get-Command starship -ErrorAction SilentlyContinue).Source
if (-not $Star) { throw "starship is not installed" }
$S = Join-Path $env:TEMP ("rszs-" + $PID)
if ((Join-Path $S "cfg\keeper\4294967295.sock").Length -ge 100) { $S = Join-Path "$env:SystemDrive\" ("rszs-" + $PID) }
$KDir = Join-Path $S "cfg\keeper"
$Dir = Join-Path $S "work\alpha"
$Dir2 = Join-Path $S "work\beta"
foreach ($d in "home", "local", "cfg", "work\alpha", "work\beta") { New-Item -ItemType Directory -Force (Join-Path $S $d) | Out-Null }

function Say([string]$m) { Write-Host $m }
function Fail([string]$m) { Write-Host "FAIL: $m"; throw $m }

# The scratch home's PowerShell profile: starship, as on DESKTOP-STEPHEN.
$prof = "Invoke-Expression (&'$Star' init powershell)"
if ($Report -eq "osc99") {
  $prof = "function Invoke-Starship-PreCommand { `$l = `$executionContext.SessionState.Path.CurrentLocation; if (`$l.Provider.Name -eq 'FileSystem') { `$host.ui.Write(`"`$([char]27)]9;9;```"`$(`$l.ProviderPath)```"`$([char]27)\`") } }`n" + $prof
} elseif ($Report -eq "osc7") {
  $prof = "function Invoke-Starship-PreCommand { `$l = `$executionContext.SessionState.Path.CurrentLocation; if (`$l.Provider.Name -eq 'FileSystem') { `$host.ui.Write(`"`$([char]27)]7;file://`$env:COMPUTERNAME/`$(`$l.ProviderPath -replace '\\','/')`$([char]27)\`") } }`n" + $prof
}
foreach ($d in "Documents\PowerShell", "Documents\WindowsPowerShell") {
  New-Item -ItemType Directory -Force (Join-Path $S "home\$d") | Out-Null
  Set-Content -Encoding ASCII (Join-Path $S "home\$d\Microsoft.PowerShell_profile.ps1") $prof
}

$script:apps = @()
function StartApp {
  Remove-Item -Force -ErrorAction SilentlyContinue (Join-Path $S "local\suzuri\bridge.json")
  $keep = @{}
  $set = @{ USERPROFILE = "$S\home"; HOME = "$S\home"; LOCALAPPDATA = "$S\local"; RSZ_CONFIG_DIR = "$S\cfg"; RSZ_KEEPER_DIR = $KDir; RSZ_BRIDGE = "1"; RSZ_ALLOW_MULTI = "1"; RSZ_NO_BAR = "1"; RSZ_TRACE = "1" }
  foreach ($k in $set.Keys) { $keep[$k] = [Environment]::GetEnvironmentVariable($k); [Environment]::SetEnvironmentVariable($k, $set[$k]) }
  $n = $script:apps.Count + 1
  try { $p = Start-Process -FilePath $App -PassThru -RedirectStandardError (Join-Path $S "app-err-$n.log") -RedirectStandardOutput (Join-Path $S "app-out-$n.log") }
  finally { foreach ($k in $keep.Keys) { [Environment]::SetEnvironmentVariable($k, $keep[$k]) } }
  $script:apps += $p
  $bj = Join-Path $S "local\suzuri\bridge.json"
  for ($i = 0; $i -lt 100 -and -not (Test-Path $bj); $i++) { Start-Sleep -Milliseconds 200 }
  if (-not (Test-Path $bj)) { Fail "app $($p.Id) wrote no bridge.json" }
  $script:bridge = Get-Content $bj -Raw | ConvertFrom-Json
  $p
}
function Get_([string]$path) { Invoke-RestMethod -DisableKeepAlive -TimeoutSec 30 -Uri ($script:bridge.url + $path) -Headers @{ Authorization = "Bearer $($script:bridge.token)" } }
function Post_([string]$path, $body) { Invoke-RestMethod -DisableKeepAlive -TimeoutSec 30 -Method Post -Uri ($script:bridge.url + $path) -Headers @{ Authorization = "Bearer $($script:bridge.token)" } -ContentType "application/json; charset=utf-8" -Body ([Text.Encoding]::UTF8.GetBytes(($body | ConvertTo-Json -Depth 10 -Compress))) }
function Call([string]$tool, $a = @{}) { Post_ "/v1/call" @{ tool = $tool; args = $a } }
function Ready { for ($i = 0; $i -lt 60; $i++) { try { $l = Call layout; if (@($l.windows[0].tabs).Count) { return } } catch { }; Start-Sleep -Milliseconds 500 }; Fail "the window never answered" }
function Submit($id, [string]$line) { Post_ "/v1/submit" @{ tab_id = $id; line = $line } | Out-Null }
function Lines($id) { $t = Get_ "/v1/panes/$id`?lines=200"; @(@($t.tail) + @($t.live) | ForEach-Object { $_.TrimEnd() } | Where-Object { $_ }) }
function Tree([int]$root) {
  $all = Get-CimInstance Win32_Process | Select-Object ProcessId, ParentProcessId
  $out = @(); $todo = @($root)
  while ($todo.Count) { $x = $todo[0]; $todo = @($todo | Select-Object -Skip 1); foreach ($c in $all | Where-Object { $_.ParentProcessId -eq $x -and $_.ProcessId -ne $x }) { $out += [int]$c.ProcessId; $todo += [int]$c.ProcessId } }
  $out
}
function Kept { @(& $Keeper --dir $KDir ls | Where-Object { $_ } | ForEach-Object { $_ | ConvertFrom-Json } | Where-Object { $_.id -and -not $_.error }) }
function KillKept { foreach ($k in Kept) { $kids = Tree ([int]$k.pid); Stop-Process -Id ([int]$k.pid) -Force -ErrorAction SilentlyContinue; foreach ($c in $kids) { Stop-Process -Id $c -Force -ErrorAction SilentlyContinue } } }
# taskkill /F the app, everything under it, and every kept shell (a reboot).
function KillAll($p) {
  $kids = Tree $p.Id
  & taskkill /F /PID $p.Id | Out-Null
  foreach ($k in $kids) { Stop-Process -Id $k -Force -ErrorAction SilentlyContinue }
  KillKept
  Start-Sleep -Seconds 1
}
# The folder the pane's shell is in, asked of the shell itself.
function ShellLoc($id) {
  $f = Join-Path $S "loc-$id-$(Get-Random).txt"
  Submit $id "(Get-Location).ProviderPath | Set-Content -Encoding UTF8 '$f'"
  for ($i = 0; $i -lt 50 -and -not (Test-Path $f); $i++) { Start-Sleep -Milliseconds 200 }
  if (-not (Test-Path $f)) { Fail "pane $id never answered where it is" }
  (Get-Content $f -Raw).Trim()
}
# What the pane's reader saw (RSZ_TRACE): folder reports in the raw reads.
# A kept shell is read in its keeper host, which logs to cfg\keeper\<id>.log.
function Seen([int]$id) {
  $log = Get-Content -Raw -Encoding UTF8 (Join-Path $KDir "$id.log")
  $r99 = ([regex]::Matches($log, '\\u\{1b\}\]9;9;')).Count
  $r7 = ([regex]::Matches($log, '\\u\{1b\}\]7;file:')).Count
  $reads = ([regex]::Matches($log, 'rsz: read ')).Count
  "$reads raw reads, $r99 with OSC 9;9, $r7 with OSC 7"
}

try {
  $ver = (& $Star --version | Select-Object -First 1)
  Say "== $ver in the profile, plain-shell setting (RSZ_NO_BAR=1), folder report: $Report"
  $a1 = StartApp
  Ready
  Start-Sleep -Seconds 4
  $id = [int](Call layout).windows[0].tabs[0].panes[0].id
  Submit $id "cd '$Dir'"
  Start-Sleep -Seconds 3
  $here = ShellLoc $id
  if ($here -ne $Dir) { Fail "the shell did not cd: '$here'" }
  $prompt = (Lines $id | Select-Object -Last 1)
  Say "1. before the kill: the shell is in $here; its prompt line '$prompt'"
  $sj = Join-Path $S "cfg\restore\$($a1.Id)\session.json"
  $saved = ""
  for ($i = 0; $i -lt 30; $i++) {
    if (Test-Path $sj) { $saved = (Get-Content $sj -Raw | ConvertFrom-Json).tabs[0].tree.cwd; if ($saved -eq $Dir) { break } }
    Start-Sleep -Milliseconds 500
  }
  Say "2. the reader (RSZ_TRACE, cfg\keeper\$id.log): $(Seen $id)"
  Say "3. the pane's folder in the save: '$saved'"
  KillAll $a1
  Say "4. taskkill /F of app $($a1.Id), its shells and keeper hosts"
  $a2 = StartApp
  Ready
  Start-Sleep -Seconds 6
  $q = [int](Call layout).windows[0].tabs[0].panes[0].id
  $back = ShellLoc $q
  Say "5. after the restart: the restored shell is in $back"
  if ($back -ne $Dir) { KillAll $a2; Fail "the restored pane started in '$back', not '$Dir'" }
  # Round two: the restored shell (started with the old text's print) is
  # cd'd again and killed again.
  Submit $q "cd '$Dir2'"
  Start-Sleep -Seconds 3
  $sj = Join-Path $S "cfg\restore\$($a2.Id)\session.json"
  for ($i = 0; $i -lt 30; $i++) {
    if (Test-Path $sj) { $saved = (Get-Content $sj -Raw | ConvertFrom-Json).tabs[0].tree.cwd; if ($saved -eq $Dir2) { break } }
    Start-Sleep -Milliseconds 500
  }
  Say "6. the restored pane cd'd to $Dir2; saved as '$saved'; the reader: $(Seen $q)"
  KillAll $a2
  $a3 = StartApp
  Ready
  Start-Sleep -Seconds 6
  $q3 = [int](Call layout).windows[0].tabs[0].panes[0].id
  $back3 = ShellLoc $q3
  Say "7. after a second taskkill /F and restart: the shell is in $back3"
  KillAll $a3
  if ($back3 -ne $Dir2) { Fail "the twice-restored pane started in '$back3', not '$Dir2'" }
  Say "PASS"
}
finally {
  foreach ($p in $script:apps) { if (-not $p.HasExited) { $kids = Tree $p.Id; & taskkill /F /PID $p.Id 2>$null | Out-Null; foreach ($k in $kids) { Stop-Process -Id $k -Force -ErrorAction SilentlyContinue } } }
  try { KillKept } catch { }
  if (-not $Keep) { Start-Sleep -Milliseconds 800; Remove-Item -Recurse -Force -ErrorAction SilentlyContinue $S } else { Say "scratch folder kept: $S" }
}
