# Enable full minidumps for suzuri.exe.
# Native AVs (ConPTY/GDI) never hit Go's suzuri-crash.txt. HKLM needs elevation.
# powershell -ExecutionPolicy Bypass -File packaging/windows/enable-wer-dumps.ps1
$ErrorActionPreference = 'Stop'
$dumpDir = Join-Path $env:LOCALAPPDATA 'suzuri\CrashDumps'
New-Item -ItemType Directory -Force -Path $dumpDir | Out-Null
$key = 'HKLM:\SOFTWARE\Microsoft\Windows\Windows Error Reporting\LocalDumps\suzuri.exe'
New-Item -Path $key -Force | Out-Null
Set-ItemProperty -Path $key -Name DumpFolder -Value $dumpDir -Type ExpandString
Set-ItemProperty -Path $key -Name DumpCount -Value 10 -Type DWord
Set-ItemProperty -Path $key -Name DumpType -Value 2 -Type DWord
Write-Host "WER LocalDumps enabled -> $dumpDir"
Get-ItemProperty $key | Format-List DumpFolder, DumpCount, DumpType
