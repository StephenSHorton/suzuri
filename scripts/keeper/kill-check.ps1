# Done-when 2, 3 and 5 (card hearth#923) on a scratch debug build, the
# Windows twin of kill9-check.sh: a shell with a program running survives
# the app being ended with its whole process tree (as Task Manager's End
# task does), and the next start shows it with its screen and scrollback;
# rsz-keeper ls, screen and type work against the Windows hosts; closing a
# pane still ends its shell and the program in it.
#   cargo build -p suzuri -p rsz-keeper
#   powershell -ExecutionPolicy Bypass -File scripts\keeper\kill-check.ps1
. (Join-Path $PSScriptRoot "lib.ps1")
$App = Join-Path $Root "target\debug\suzuri.exe"
if (-not ((Test-Path $App) -and (Test-Path $Keeper))) { Fail "build first: cargo build -p suzuri -p rsz-keeper" }
$before = (Real-Stamp) -join "; "
try {
  Say "== scratch: $S (LOCALAPPDATA, RSZ_CONFIG_DIR and the keeper folder all in it)"
  $app1 = Start-App $App
  Start-Sleep 3
  $k = Kept | Select-Object -First 1
  if (-not $k -or $k.error) { Fail "no kept shell after start: $k" }
  $id = [int]$k.id; $hostPid = [int]$k.pid; $shell = [int]$k.shell_pid
  Say "app ${app1}: pane $id is kept by host $hostPid ($($k.exe)), shell $shell, version $($k.version)"
  $parent = Parent-Of $hostPid
  Say "host $hostPid's parent is $parent (the app is $app1): not in the app's process tree"
  if ($parent -eq $app1) { Fail "the host is the app's child" }

  Say "== rsz-keeper type: start a program in pane $id"
  # No double quotes: Windows PowerShell 5 mangles them on a native command line.
  & $Keeper --dir $KDir type $id 'echo ctl=$env:SUZURI_CONTROL_URL; 1..400 | ForEach-Object { echo scroll-$_ }; $i = 0; while ($true) { $i++; echo tick-$i; Start-Sleep 1 }\n'
  if ($LASTEXITCODE -ne 0) { Fail "rsz-keeper type failed" }
  Start-Sleep 5
  $t1 = Last-Tick (Screen $id)
  if (-not $t1) { Fail "the counter did not start: $(Screen $id)" }
  Say "rsz-keeper screen ${id}: running, $t1"
  Shot $app1 (Join-Path $Out "kill-before.png")

  Say "== end the app with its whole process tree (taskkill /F /T, as Task Manager's End task)"
  taskkill /F /T /PID $app1 | Out-Host
  Start-Sleep 3
  if (Alive $app1) { Fail "app still alive" }
  if (-not (Alive $hostPid)) { Fail "host $hostPid died with the app" }
  if (-not (Alive $shell)) { Fail "shell $shell died with the app" }
  $t2 = Last-Tick (Screen $id)
  Say "app gone; host $hostPid and shell $shell alive; the program went on: $t1 -> $t2"
  if ($t2 -eq $t1) { Fail "the counter stopped" }

  Say "== start the app again"
  $app2 = Start-App $App
  for ($i = 0; $i -lt 40 -and (Kept-One $id).viewers -ne 1; $i++) { Start-Sleep -Milliseconds 500 }
  if ((Kept-One $id).viewers -ne 1) { Fail "the new app did not attach to shell $id" }
  Start-Sleep 2
  $snap = (Api-Get "/v1/panes/${id}?lines=500") | ConvertTo-Json -Depth 8 -Compress
  $t3 = Last-Tick $snap
  $first = ([regex]::Match($snap, "scroll-\d+")).Value
  $oldest = ([regex]::Match((Screen $id 1000), "scroll-\d+")).Value
  if (-not $t3 -or -not $first) { Fail "screen or scrollback missing in the new app: $($snap.Substring(0, [Math]::Min(300, $snap.Length)))" }
  if ((Kept-One $id).shell_pid -ne $shell) { Fail "not the same shell" }
  Say "app $app2 shows pane ${id}: same host $((Kept-One $id).pid), same shell $shell; screen at $t3; its last 500 lines start at $first, and the host's history goes back to $oldest"
  $ctl = ([regex]::Match((Screen $id 1000), "ctl=(http\S+)")).Groups[1].Value
  Say "the kept shell's SUZURI_CONTROL_URL: $ctl; the new app's bridge: $((Bridge).url)"
  Shot $app2 (Join-Path $Out "kill-after.png")

  Say "== rsz-keeper ls"
  & $Keeper --dir $KDir ls | Out-Host

  Say "== close a pane: its shell ends"
  $b = [int](Api-Call "split" @{ pane_id = $id; axis = "right" }).pane_id
  Start-Sleep 3
  $kb = Kept-One $b
  if (-not $kb) { Fail "the split pane $b is not kept" }
  $bshell = [int]$kb.shell_pid; $bhost = [int]$kb.pid
  Api-Post "/v1/submit" @{ tab_id = $b; line = "ping -n 9999 127.0.0.1" } | Out-Null
  Start-Sleep 2
  $prog = Children-Of $bshell | Where-Object { $_.Name -ieq "PING.EXE" } | Select-Object -First 1
  if (-not $prog) { Fail "ping did not start in pane $b" }
  $prog = [int]$prog.ProcessId
  Say "pane ${b}: host $bhost, shell $bshell, running ping (pid $prog)"
  Api-Call "close" @{ pane_id = $b } | Out-Null
  Start-Sleep 3
  if (Alive $bshell) { Fail "closed pane's shell $bshell still alive" }
  if (Alive $bhost) { Fail "closed pane's host $bhost still alive" }
  if (Alive $prog) { Fail "its program $prog lives on" }
  if (Test-Path (Join-Path $KDir "$b.sock")) { Fail "socket left" }
  if (-not (Alive $shell)) { Fail "the other pane's shell died" }
  Say "closed pane ${b}: host $bhost, shell $bshell and ping $prog are gone; pane $id still runs"

  $after = (Real-Stamp) -join "; "
  if ($before -ne $after) { Fail "the installed suzuri's config changed: $before -> $after" }
  Say "installed suzuri's config untouched: $after"
  Say "PASS"
} finally {
  Cleanup
}
