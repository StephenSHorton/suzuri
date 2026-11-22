# Card hearth#1014 on a scratch debug build: Ctrl+C (0x03 typed into the
# shell) stops a running command in a kept shell on Windows, as it does in
# a plain pane. A host restarts itself detached in a new process group
# (keeper.rs win::spawn), which Windows starts with Ctrl+C ignored, and the
# shell under ConPTY inherited that; the host now turns it back on.
#   1. Suzuri's own kept shell (the app starts its host)
#   2. a shell Hearth keeps: `suzuri --keeper-host <dir> <id>` started
#      straight, in a folder of its own, as Hearth starts it
# Each: a loop runs, 0x03 goes in through the host's socket (as Suzuri's
# keys and Hearth's kept_write do, here rsz-keeper type), and the next line
# runs and prints how far the loop got.
#   cargo build -p suzuri -p rsz-keeper
#   powershell -ExecutionPolicy Bypass -File scripts\keeper\ctrlc-check.ps1
. (Join-Path $PSScriptRoot "lib.ps1")
$App = Join-Path $Root "target\debug\suzuri.exe"
if (-not ((Test-Path $App) -and (Test-Path $Keeper))) { Fail "build first: cargo build -p suzuri -p rsz-keeper" }
$before = (Real-Stamp) -join "; "
$CtrlC = [string][char]3

# Ctrl-C-Stops DIR ID WHO: the loop, Ctrl+C, then a line that must run.
function Ctrl-C-Stops([string]$dir, [int]$id, [string]$who) {
  $screen = { (& $Keeper --dir $dir screen $id 0) -join "`n" }
  # Wait until the shell answers a line (Suzuri's shells draw no prompt of
  # their own); ready-5 is the shell's output, not the typed text.
  & $Keeper --dir $dir type $id 'echo (''ready-'' + (2+3))\n' | Out-Null
  for ($i = 0; $i -lt 100 -and ((& $screen) -notmatch "(?m)^ready-5"); $i++) { Start-Sleep -Milliseconds 200 }
  if ((& $screen) -notmatch "(?m)^ready-5") { Fail "${who}: the shell does not answer: $(& $screen)" }
  Say "${who}: shell $id answers; starting the loop"
  & $Keeper --dir $dir type $id '$n=0; while ($true) { $n++; Start-Sleep -Milliseconds 200 }\n' | Out-Null
  Start-Sleep 2
  Say "${who}: typing Ctrl+C (0x03)"
  & $Keeper --dir $dir type $id $CtrlC | Out-Null
  Start-Sleep 1
  # Prints after-<n>: only the shell can write that, so it ran, after the loop.
  & $Keeper --dir $dir type $id 'echo (''after-'' + $n)\n' | Out-Null
  $seen = $null
  for ($i = 0; $i -lt 25 -and -not $seen; $i++) {
    Start-Sleep -Milliseconds 200
    $m = [regex]::Match((& $screen), "(?m)^after-(\d+)")
    if ($m.Success) { $seen = [int]$m.Groups[1].Value }
  }
  Say "${who}: its screen now:"; (& $screen) -split "`n" | Where-Object { $_.Trim() } | Select-Object -Last 6 | ForEach-Object { Say "  | $_" }
  if ($null -eq $seen) { Fail "${who}: Ctrl+C did not stop the loop: the next line never ran" }
  if ($seen -le 0) { Fail "${who}: the loop never ran (n = $seen)" }
  Say "${who}: Ctrl+C stopped the loop after $seen turns, and the next line ran"
}

$hostPid = $null
try {
  Say "== 1. Suzuri's own kept shell (scratch: $S)"
  $app1 = Start-App $App
  Start-Sleep 3
  $k = Kept | Select-Object -First 1
  if (-not $k -or $k.error) { Fail "no kept shell after start: $k" }
  Say "app ${app1}: pane $($k.id) kept by host $($k.pid), shell $($k.shell_pid)"
  Ctrl-C-Stops $KDir ([int]$k.id) "Suzuri's shell"

  Say "== 2. a shell Hearth keeps: suzuri --keeper-host started straight"
  $HD = Join-Path $S "hearth"
  New-Item -ItemType Directory -Force $HD | Out-Null
  $cfg = Join-Path $S "host.json"
  Set-Content -Encoding ascii $cfg '{"cols":100,"rows":30,"version":"hearth-stand-in"}'
  $hostOut = Join-Path $S "host.out"
  Start-Process $App -ArgumentList "--keeper-host", $HD, 1 -NoNewWindow -RedirectStandardInput $cfg -RedirectStandardOutput $hostOut -RedirectStandardError (Join-Path $S "host.err") | Out-Null
  for ($i = 0; $i -lt 100 -and -not ((Test-Path $hostOut) -and (Get-Content $hostOut -Raw) -match "ready"); $i++) { Start-Sleep -Milliseconds 100 }
  $line = (Get-Content $hostOut -Raw)
  if ($line -notmatch "ready (\d+)") { Fail "host said: $line" }
  $hostPid = [int]$Matches[1]
  Say "Hearth's stand-in host $hostPid in $HD"
  Ctrl-C-Stops $HD 1 "Hearth's shell"

  $after = (Real-Stamp) -join "; "
  if ($before -ne $after) { Fail "the installed suzuri's config changed: $before -> $after" }
  Say "PASS"
} finally {
  if ($HD -and (Test-Path $HD)) { & $Keeper --dir $HD close 1 2>$null | Out-Null }
  Cleanup
}
