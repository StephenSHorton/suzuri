# Enable full WER LocalDumps for suzuri.exe (see docs/crash-dumps.md).
# Native AVs (ConPTY/GDI) never reach Go's suzuri-crash.txt; WER writes the dump.
# HKLM needs an elevated PowerShell. One line:
#   powershell -ExecutionPolicy Bypass -File packaging\windows\enable-wer-dumps.ps1
# Undo: Remove-Item 'HKLM:\SOFTWARE\Microsoft\Windows\Windows Error Reporting\LocalDumps\suzuri.exe'
$ErrorActionPreference = 'Stop'
$admin = ([Security.Principal.WindowsPrincipal][Security.Principal.WindowsIdentity]::GetCurrent()).IsInRole(
    [Security.Principal.WindowsBuiltInRole]::Administrator)
if (-not $admin) {
    Write-Error 'Run this from an elevated PowerShell (Run as administrator): it writes HKLM.'
}
# The folder suzuri reads at startup (%LOCALAPPDATA%\suzuri\CrashDumps). It is
# the current user's LOCALAPPDATA, so run elevated from the same account.
$dumpDir = Join-Path $env:LOCALAPPDATA 'suzuri\CrashDumps'
New-Item -ItemType Directory -Force -Path $dumpDir | Out-Null
$key = 'HKLM:\SOFTWARE\Microsoft\Windows\Windows Error Reporting\LocalDumps\suzuri.exe'
New-Item -Path $key -Force | Out-Null
Set-ItemProperty -Path $key -Name DumpFolder -Value $dumpDir -Type ExpandString
Set-ItemProperty -Path $key -Name DumpCount -Value 10 -Type DWord
Set-ItemProperty -Path $key -Name DumpType -Value 2 -Type DWord   # 2 = full dump
Write-Host "WER LocalDumps enabled -> $dumpDir"
Get-ItemProperty $key | Format-List DumpFolder, DumpCount, DumpType
