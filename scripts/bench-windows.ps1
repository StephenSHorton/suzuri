# rust-suzuri on Windows 11: 8 streaming fakeagent panes, real keys from
# sendkeys.ps1, the app's own key-event-to-paint latency (oak's definition,
# the same one Go suzuri's telemetry line uses), plus memory of the app and
# its WebView2 processes.
#
#   cargo build --release -p rust-suzuri -p fakeagent
#   .\scripts\bench-windows.ps1 [-Seconds 40]
#
# Keep the window in front for the whole run: sendkeys.ps1 types into the
# foreground window.
param([int]$Seconds = 40)
$ErrorActionPreference = "Stop"
$R = Split-Path -Parent $PSScriptRoot
$Out = Join-Path $R ("bench\out\rsz-win-" + (Get-Date -Format "yyyyMMdd-HHmmss"))
New-Item -ItemType Directory -Force $Out | Out-Null
$lock = Join-Path $Out "quiet.lock"
$fa = Join-Path $R "target\release\fakeagent.exe"

$env:RSZ_PANES = "8"
$env:RSZ_CMD = "`"$fa`" --quiet-first `"$lock`""
$env:RSZ_BENCH = "1"
$env:RSZ_BENCH_PROBE = "external"
$env:RSZ_BENCH_SECS = "$Seconds"
$env:RSZ_BENCH_OUT = Join-Path $Out "report.json"
$p = Start-Process -FilePath (Join-Path $R "target\release\rust-suzuri.exe") -PassThru
# The app warms up for 15 s before it starts measuring.
Start-Sleep -Seconds 14

function Tree-Memory($rootId) {
  $all = Get-CimInstance Win32_Process
  $ids = @($rootId)
  do {
    $more = $all | Where-Object { $ids -contains $_.ParentProcessId -and $ids -notcontains $_.ProcessId } | ForEach-Object { $_.ProcessId }
    $ids += $more
  } while ($more)
  # The app plus WebView2; shells and fakeagent are the panes' own cost.
  $procs = Get-Process -Id $ids -ErrorAction SilentlyContinue | Where-Object { $_.ProcessName -in @("rust-suzuri", "msedgewebview2") }
  [pscustomobject]@{
    private_mb = [math]::Round(($procs | Measure-Object PrivateMemorySize64 -Sum).Sum / 1MB, 1)
    working_set_mb = [math]::Round(($procs | Measure-Object WorkingSet64 -Sum).Sum / 1MB, 1)
    cpu_s = [math]::Round((($procs | ForEach-Object { $_.TotalProcessorTime.TotalSeconds }) | Measure-Object -Sum).Sum, 1)
    processes = $procs.Count
  }
}
$m0 = Tree-Memory $p.Id
& (Join-Path $PSScriptRoot "sendkeys.ps1") -Seconds ($Seconds + 2)
$m1 = Tree-Memory $p.Id
Start-Sleep -Seconds 3
@{ memory_start = $m0; memory_end = $m1 } | ConvertTo-Json -Depth 4 | Set-Content (Join-Path $Out "memory.json")
Get-Content (Join-Path $Out "report.json")
Get-Content (Join-Path $Out "memory.json")
