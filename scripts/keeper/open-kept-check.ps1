# Card hearth#973 on a scratch debug build, the Windows twin of
# open-kept-check.sh: Suzuri shows a shell another program keeps (Hearth's
# Open in Suzuri) through the open_kept tool. A stand-in for Hearth's host
# runs in a folder of its own that only this user can enter (as a user's
# own %LOCALAPPDATA% is: no inherited entries; this user, SYSTEM and
# Administrators), outside Suzuri's keeper folder, with a viewer of its own
# watching (Hearth's window). Then:
#   1. GET /tools lists open_kept; a path that is not a socket, a .. path and
#      a socket in a folder other users may enter are refused (400)
#   2. open_kept attaches a second viewer in a new tab under a pane id of
#      Suzuri's own, and answers 200 {ok, pane_id}
#   3. closing that tab lets go: the shell, its host and Hearth's viewer go
#      on, and the shell still draws; so does ending Suzuri
#   cargo build -p suzuri -p rsz-keeper
#   powershell -ExecutionPolicy Bypass -File scripts\keeper\open-kept-check.ps1
. (Join-Path $PSScriptRoot "lib.ps1")
$App = Join-Path $Root "target\debug\suzuri.exe"
if (-not ((Test-Path $App) -and (Test-Path $Keeper))) { Fail "build first: cargo build -p suzuri -p rsz-keeper" }
$before = (Real-Stamp) -join "; "

# Hearth's keeper folder (its own, not Suzuri's) and its shell's id: 1, the
# same id as Suzuri's own first pane.
$HD = Join-Path $S "hearth"
$HID = 1
$Sock = Join-Path $HD "$HID.sock"
$Me = [Security.Principal.WindowsIdentity]::GetCurrent().User.Value

function HK([string]$field) { (& $Keeper --dir $HD ls | Where-Object { $_ } | ForEach-Object { $_ | ConvertFrom-Json } | Where-Object { $_.id -eq $HID } | Select-Object -First 1).$field }
function HScreen { (& $Keeper --dir $HD screen $HID 0) -join "`n" }
# Frames a fresh viewer of Hearth's shell sees in two seconds while text is typed.
function HDraws([string]$text) {
  $job = Start-Job { param($k, $d, $id) & $k --dir $d watch $id 2 } -ArgumentList $Keeper, $HD, $HID
  Start-Sleep -Milliseconds 500
  & $Keeper --dir $HD type $HID "echo $text\n" | Out-Null
  $out = Receive-Job $job -Wait -AutoRemoveJob
  [int](([regex]::Match(($out -join "`n"), "(\d+) frames")).Groups[1].Value)
}

# open_kept straight to the bridge: (status, body), 400s included.
function Open-Kept($argsObj) {
  $b = Bridge
  $body = @{ tool = "open_kept"; args = $argsObj } | ConvertTo-Json -Compress -Depth 5
  try {
    $r = Invoke-WebRequest -UseBasicParsing -DisableKeepAlive -TimeoutSec 30 -Method Post -Uri "$($b.url)/v1/call" -Headers @{ Authorization = "Bearer $($b.token)" } -ContentType "application/json" -Body $body
    return @([int]$r.StatusCode, $r.Content)
  } catch {
    $resp = $_.Exception.Response
    if (-not $resp) { throw }
    $text = (New-Object IO.StreamReader $resp.GetResponseStream()).ReadToEnd()
    return @([int]$resp.StatusCode, $text)
  }
}

if (-not ("RszkFront" -as [type])) {
  Add-Type -TypeDefinition @"
using System; using System.Runtime.InteropServices;
public static class RszkFront {
  [DllImport("user32.dll")] static extern IntPtr GetForegroundWindow();
  [DllImport("user32.dll")] static extern uint GetWindowThreadProcessId(IntPtr h, out uint pid);
  public static uint Pid() { uint p; GetWindowThreadProcessId(GetForegroundWindow(), out p); return p; }
}
"@
}

$watch = $null
try {
  Say "== a host for Hearth's shell, in $HD (not Suzuri's folder)"
  New-Item -ItemType Directory -Force $HD | Out-Null
  icacls $HD /inheritance:r /grant:r "*${Me}:(OI)(CI)F" "*S-1-5-18:(OI)(CI)F" "*S-1-5-32-544:(OI)(CI)F" /Q | Out-Null
  if ($LASTEXITCODE -ne 0) { Fail "icacls could not make $HD private" }
  Say "its access list:"; icacls $HD | Out-Host
  $cfg = Join-Path $S "host.json"
  Set-Content -Encoding ascii $cfg '{"cols":80,"rows":24,"version":"hearth-stand-in"}'
  # The host starts itself once more and that one stays (keeper.rs host::run):
  # its "ready" line is read from a file.
  $hostOut = Join-Path $S "host.out"
  Start-Process $App -ArgumentList "--keeper-host", $HD, $HID -NoNewWindow -RedirectStandardInput $cfg -RedirectStandardOutput $hostOut -RedirectStandardError (Join-Path $S "host.err") | Out-Null
  for ($i = 0; $i -lt 100 -and -not ((Test-Path $hostOut) -and (Get-Content $hostOut -Raw) -match "ready"); $i++) { Start-Sleep -Milliseconds 100 }
  $line = (Get-Content $hostOut -Raw)
  if ($line -notmatch "ready (\d+)") { Fail "host said: $line" }
  $hostPid = [int]$Matches[1]
  $shell = [int](HK shell_pid)
  Say "host $hostPid, shell $shell, socket $Sock"

  # Hearth's own window on it: a viewer that stays attached the whole run.
  $watchOut = Join-Path $S "hearth-viewer.out"
  $watch = Start-Process $Keeper -ArgumentList "--dir", $HD, "watch", $HID, 3600 -NoNewWindow -PassThru -RedirectStandardOutput $watchOut -RedirectStandardError (Join-Path $S "hearth-viewer.err")
  for ($i = 0; $i -lt 50 -and (HK viewers) -ne 1; $i++) { Start-Sleep -Milliseconds 100 }
  if ((HK viewers) -ne 1) { Fail "expected Hearth's viewer only, the host says $(HK viewers)" }

  Say "== a cold start: open_kept straight after Suzuri starts, as Hearth sends it"
  $app1 = Start-App $App
  $codes = @()
  for ($i = 0; $i -lt 200; $i++) {
    $r = Open-Kept @{ socket = $Sock; title = "from Hearth"; from = "hearth" }
    $codes += $r[0]
    if ($r[0] -eq 200) { break }
    if ($r[0] -ne 503) { Fail "before it was ready Suzuri answered $($r[0]) (Hearth retries only 503): $($r[1])" }
    Start-Sleep -Milliseconds 100
  }
  if ($r[0] -ne 200) { Fail "open_kept never answered 200" }
  Say "answers until ready: $(($codes | Group-Object | ForEach-Object { "$($_.Name) x$($_.Count)" }) -join '  ')"
  $cold = ($r[1] | ConvertFrom-Json).pane_id
  Start-Sleep 1
  if ((HK viewers) -ne 2) { Fail "cold start: expected two viewers, the host says $(HK viewers)" }
  Api-Call "close" @{ pane_id = $cold } | Out-Null
  Start-Sleep 1
  if ((HK viewers) -ne 1) { Fail "cold start: the tab's close did not let go" }
  Say "cold start: pane $cold shown, then its tab closed; the host is back to Hearth's viewer"

  Say "== 1. GET /tools (app $app1)"
  $tool = (Api-Get "/tools").tools | Where-Object { $_.name -eq "open_kept" }
  if (-not $tool) { Fail "/tools has no open_kept" }
  Say ($tool | ConvertTo-Json -Compress)
  $r = Open-Kept @{ socket = "C:\Windows\win.ini"; from = "hearth" }
  Say "a path that is not a keeper socket: $($r[0]) $($r[1])"
  if ($r[0] -ne 400) { Fail "a bad path was not refused" }
  $r = Open-Kept @{ socket = "$KDir\..\..\hearth\$HID.sock" }
  Say "a path with ..: $($r[0]) $($r[1])"
  if ($r[0] -ne 400) { Fail "a .. path was not refused" }
  icacls $HD /grant "*S-1-5-11:(RX)" /Q | Out-Null
  $r = Open-Kept @{ socket = $Sock }
  icacls $HD /remove:g "*S-1-5-11" /Q | Out-Null
  Say "its folder open to every signed-in user (Authenticated Users may read): $($r[0]) $($r[1])"
  if ($r[0] -ne 400) { Fail "a socket in an open folder was not refused" }
  $tabs0 = @((Api-Get "/v1/layout").windows[0].tabs).Count

  Say "== 2. open_kept {socket, title, from: hearth}"
  $r = Open-Kept @{ socket = $Sock; title = "from Hearth"; from = "hearth" }
  Say "answer: $($r[0]) $($r[1])"
  if ($r[0] -ne 200) { Fail "open_kept answered $($r[0])" }
  $ans = $r[1] | ConvertFrom-Json
  if (-not $ans.ok) { Fail "no ok" }
  $pane = [int]$ans.pane_id
  Say "foreground window's process after the 200: $([RszkFront]::Pid()) (Suzuri is $app1)"
  if ($pane -eq $HID) { Fail "the borrowed pane took the host's id $HID" }
  $tabs1 = @((Api-Get "/v1/layout").windows[0].tabs).Count
  Say "Suzuri's pane id for it: $pane (the host's id is $HID, Suzuri's own first pane is 1); tabs $tabs0 -> $tabs1"
  if ($tabs1 -le $tabs0) { Fail "no new tab" }
  $focus = (Api-Get "/v1/layout").focus_pane
  if ($focus -ne $pane) { Fail "the new pane does not have the keys ($focus)" }
  Start-Sleep 1
  if ((HK viewers) -ne 2) { Fail "expected two viewers, the host says $(HK viewers)" }
  Say "the host has $(HK viewers) viewers: Hearth's and Suzuri's"
  & $Keeper --dir $HD type $HID 'echo typed-in-hearth\n' | Out-Null
  Start-Sleep 2
  $snap = (Api-Get "/v1/panes/${pane}?lines=20") | ConvertTo-Json -Depth 8 -Compress
  if ($snap -notmatch "typed-in-hearth") { Fail "Suzuri does not show what Hearth typed" }
  Say "Suzuri's pane $pane shows typed-in-hearth"
  Api-Post "/v1/submit" @{ tab_id = $pane; line = "echo typed-in-suzuri" } | Out-Null
  Start-Sleep 2
  if ((HScreen) -notmatch "typed-in-suzuri") { Fail "the shell did not get what Suzuri typed" }
  Say "Hearth's shell shows typed-in-suzuri"
  Shot $app1 (Join-Path $Out "open-kept.png")

  Say "== 3. closing the tab in Suzuri lets go"
  Api-Call "close" @{ pane_id = $pane } | Out-Null
  Start-Sleep 2
  if (-not (Alive $hostPid)) { Fail "the host died when the tab closed" }
  if (-not (Alive $shell)) { Fail "the shell died when the tab closed" }
  if ((HK viewers) -ne 1) { Fail "expected Hearth's viewer only, the host says $(HK viewers)" }
  $f = HDraws "after-the-tab-closed"
  Start-Sleep 1
  if ((HScreen) -notmatch "after-the-tab-closed") { Fail "the shell stopped" }
  if ($f -le 0) { Fail "the shell no longer draws" }
  if ($watch.HasExited -or (Get-Content $watchOut -Raw) -match "closed") { Fail "Hearth's viewer lost the shell" }
  Say "host $hostPid and shell $shell alive, $(HK viewers) viewer (Hearth's) still attached; the shell still draws ($f frames for one line)"
  Say "Suzuri's panes now: $(((Api-Get '/v1/layout').windows[0].tabs | ForEach-Object { $_.panes } | ForEach-Object { $_.id }) -join ', ')"

  Say "== 3. ending Suzuri lets go too"
  $r = Open-Kept @{ socket = $Sock; from = "hearth" }
  if ($r[0] -ne 200) { Fail "not shown again: $($r[0]) $($r[1])" }
  Start-Sleep 1
  if ((HK viewers) -ne 2) { Fail "not shown again" }
  taskkill /F /T /PID $app1 | Out-Host
  Start-Sleep 2
  if (Alive $app1) { Fail "app still alive" }
  if (-not ((Alive $hostPid) -and (Alive $shell))) { Fail "the shell went with Suzuri" }
  if ((HK viewers) -ne 1) { Fail "expected Hearth's viewer only after Suzuri ended" }
  $f = HDraws "after-suzuri-ended"
  if ($f -le 0) { Fail "the shell no longer draws" }
  Say "Suzuri gone; host $hostPid and shell $shell alive, Hearth's viewer still attached, the shell still draws ($f frames)"

  Say "== the next start does not take it as Suzuri's own"
  $app2 = Start-App $App
  Start-Sleep 3
  if ((HK viewers) -ne 1) { Fail "the next start attached to Hearth's shell" }
  Say "panes after the restart: $(((Api-Get '/v1/layout').windows[0].tabs | ForEach-Object { $_.panes } | ForEach-Object { $_.id }) -join ', '); Hearth's host still has 1 viewer"

  $after = (Real-Stamp) -join "; "
  if ($before -ne $after) { Fail "the installed suzuri's config changed: $before -> $after" }
  Say "PASS"
} finally {
  & $Keeper --dir $HD close $HID 2>$null | Out-Null
  if ($watch) { Stop-Process -Id $watch.Id -Force -ErrorAction SilentlyContinue }
  Cleanup
}
