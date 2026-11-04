# Go suzuri on Windows 11 with the same load as bench-windows.ps1: 8 fakeagent
# panes, real keys from guardkeys.ps1, and suzuri's own 10 s telemetry line
# (input_p99 = key event to the end of the paint after it, oak's definition).
#
#   go build -ldflags "-H windowsgui -s -w -X main.version=dev" -o suzuri-bench.exe ./cmd/suzuri
#   .\scripts\windows\bench-go.ps1 -Suzuri C:\path\to\suzuri-bench.exe [-Seconds 40]
#
# Build suzuri as version "dev": any other version shows an "Update suzuri?"
# modal that swallows the keys. Needs a branch with the telemetry line
# (oak/hang-watchdog, dc176c5 or later).
#
# suzuri runs with LOCALAPPDATA pointed at a private folder: your config is
# copied in, then intro/ambient/matrix are turned off and a "Bench" profile
# runs fakeagent. Your real config, logs and workspace are not touched.
#
# Caveat: the split sequence below does not give a balanced 2x4 layout, and
# every Go pane streams (bench-windows.ps1 keeps one quiet probe pane).
param(
  [Parameter(Mandatory = $true)][string]$Suzuri,
  [int]$Seconds = 40
)
$ErrorActionPreference = "Stop"
$R = Split-Path -Parent (Split-Path -Parent $PSScriptRoot)
$Out = Join-Path $R ("bench\out\go-win-" + (Get-Date -Format "yyyyMMdd-HHmmss"))
$B = Join-Path $Out "localappdata"
New-Item -ItemType Directory -Force (Join-Path $B "suzuri") | Out-Null

$src = Join-Path $env:LOCALAPPDATA "suzuri\config.json"
$c = if (Test-Path $src) { Get-Content $src -Raw | ConvertFrom-Json } else { [pscustomobject]@{} }
$set = @{
  intro = "none"; shell_ambient = "none"; shell_matrix = $false; shell_logo = 0
  first_run_done = $true; active_profile = "Bench"
  window = [pscustomobject]@{ x = 100; y = 80; width = 1400; height = 900 }
  profiles = @([pscustomobject]@{ name = "Bench"; shell = (Join-Path $R "target\release\fakeagent.exe") })
}
foreach ($k in $set.Keys) { $c | Add-Member -NotePropertyName $k -NotePropertyValue $set[$k] -Force }
# No BOM: Go's JSON decoder rejects it.
[IO.File]::WriteAllText((Join-Path $B "suzuri\config.json"), ($c | ConvertTo-Json -Depth 8), (New-Object Text.UTF8Encoding $false))

$realLocal = $env:LOCALAPPDATA
$env:LOCALAPPDATA = $B
try {
  $p = Start-Process -FilePath $Suzuri -PassThru
} finally { $env:LOCALAPPDATA = $realLocal }
Start-Sleep -Seconds 5

. (Join-Path $PSScriptRoot "keys-lib.ps1")
$p.Refresh(); $script:h = $p.MainWindowHandle; $script:tp = $p.Id
Focus
$CTRL = 0x11; $SHIFT = 0x10; $ALT = 0x12
# 4 columns, then split each one down: 8 panes.
Press @($CTRL, $SHIFT) 0x44; Start-Sleep -Milliseconds 700
Press @($ALT) 0x25; Press @($CTRL, $SHIFT) 0x44; Start-Sleep -Milliseconds 700
Press @($ALT) 0x27; Press @($ALT) 0x27; Press @($CTRL, $SHIFT) 0x44; Start-Sleep -Milliseconds 700
for ($i = 0; $i -lt 4; $i++) { Press @($ALT) 0x25 }
for ($i = 0; $i -lt 4; $i++) { Press @($CTRL, $SHIFT) 0x45; Start-Sleep -Milliseconds 600; Press @($ALT) 0x26; Press @($ALT) 0x27 }
Start-Sleep -Seconds 12
Snap (Join-Path $Out "shot.png")

function Mem {
  $x = Get-Process -Id $p.Id
  [pscustomobject]@{
    private_mb = [math]::Round($x.PrivateMemorySize64 / 1MB, 1)
    working_set_mb = [math]::Round($x.WorkingSet64 / 1MB, 1)
    cpu_s = [math]::Round($x.TotalProcessorTime.TotalSeconds, 1)
  }
}
$m0 = Mem
& (Join-Path $PSScriptRoot "guardkeys.ps1") -Seconds $Seconds -TargetNames @($p.ProcessName)
$m1 = Mem
# Let the last 10 s telemetry window close.
Start-Sleep -Seconds 11
Stop-Process -Id $p.Id
@{ memory_start = $m0; memory_end = $m1 } | ConvertTo-Json -Depth 4 | Set-Content (Join-Path $Out "memory.json")
Select-String -Path (Join-Path $B "suzuri\suzuri.log") -Pattern "telemetry" | ForEach-Object { $_.Line } | Set-Content (Join-Path $Out "telemetry.log")
Get-Content (Join-Path $Out "memory.json")
Get-Content (Join-Path $Out "telemetry.log")
