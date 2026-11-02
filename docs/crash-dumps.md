# Crash dumps (Windows)

When suzuri dies from a native fault (an access violation inside ConPTY or
GDI), Go's panic output stays empty. Windows Error Reporting (WER) can still
write a full memory dump, and the next launch tells you where it is.

## What suzuri does

- **WER sees native faults.** At start suzuri calls `debug.SetTraceback("wer")`.
  Without it, Go's last-chance exception handler prints the fault and exits
  with code 2, so WER never runs and `CrashDumps\` stays empty (the gap in
  `windows-stability-swarm-report.md`). With it, Go prints its traceback to
  `suzuri-crash.txt` and then re-raises the exception: WER writes the dump and
  the exit code is the original NTSTATUS (`0xC0000005` for an access
  violation). This is on in dev and release builds. On macOS, dev builds use
  `GOTRACEBACK=crash`.
- **Run marker.** Each GUI launch writes `%LOCALAPPDATA%\suzuri\runs\<pid>.json`
  (pid, start time, version). A clean exit removes it, including the
  updater's relaunch.
- **Next launch.** A marker whose process is gone means that run crashed.
  suzuri looks for `suzuri.exe.<pid>.dmp` in `%LOCALAPPDATA%\suzuri\CrashDumps`
  (then WER's default `%LOCALAPPDATA%\CrashDumps`), reads the exception code
  from the dump and:
  - appends one line to `%LOCALAPPDATA%\suzuri\suzuri-crashes.log`, e.g.
    `2026-11-02T15:40:12Z pid=21324 started=… version=0.9.105 exit=0xC0000005 ntstatus=STATUS_ACCESS_VIOLATION dump="C:\…\suzuri.exe.21324.dmp"`
  - logs the same in `suzuri.log` ("previous run ended without a clean exit")
  - shows a card: **suzuri closed unexpectedly** · *log + dump saved to
    `%LOCALAPPDATA%\suzuri\CrashDumps` · exit 0xC0000005 STATUS_ACCESS_VIOLATION ·
    click to open*. A click opens that folder; right-click dismisses it. The
    full dump path is in `suzuri-crashes.log`.

  Without a dump (WER not set up, or a kill from Task Manager) the exit code is
  `unknown` and the card says "log saved to …" with the data folder.

## Turn on dumps (once per machine)

From an elevated PowerShell (Run as administrator), signed in as the user who
runs suzuri:

```powershell
powershell -ExecutionPolicy Bypass -File packaging\windows\enable-wer-dumps.ps1
```

It sets `HKLM\SOFTWARE\Microsoft\Windows\Windows Error Reporting\LocalDumps\suzuri.exe`:
`DumpFolder=%LOCALAPPDATA%\suzuri\CrashDumps`, `DumpType=2` (full), `DumpCount=10`.
Check it with:

```powershell
Get-ItemProperty 'HKLM:\SOFTWARE\Microsoft\Windows\Windows Error Reporting\LocalDumps\suzuri.exe'
```

## Test it with a deliberate crash (dev builds only)

A dev build (version `dev`, i.e. built without `-X main.version`) crashes on
purpose when `SUZURI_DEV_CRASH=av`: a few seconds after start it writes through
a bad pointer inside `ntdll!memset`, a native access violation outside Go code,
like a ConPTY crash. Release builds ignore the variable.

```powershell
go build -o suzuri.exe ./cmd/suzuri
$env:SUZURI_DEV_CRASH = 'av'; $env:SUZURI_DEV_CRASH_AFTER = '5'; .\suzuri.exe
# window opens, dies ~5 s later
Remove-Item Env:SUZURI_DEV_CRASH; .\suzuri.exe
```

Expect `suzuri.exe.<pid>.dmp` in `%LOCALAPPDATA%\suzuri\CrashDumps`, a new
line in `suzuri-crashes.log` with `exit=0xC0000005`, and the card on the
second launch. The executable must be named `suzuri.exe`: LocalDumps matches
the image name.

## Open a dump

**Delve** (Go stacks, goroutines; needs the same binary that crashed):

```powershell
go install github.com/go-delve/delve/cmd/dlv@latest
dlv core .\suzuri.exe "$env:LOCALAPPDATA\suzuri\CrashDumps\suzuri.exe.<pid>.dmp"
(dlv) goroutines
(dlv) goroutine <n> bt
(dlv) threads
```

**WinDbg** (native frames: ConPTY, kernelbase, GDI). Install it with
`winget install Microsoft.WinDbg`, then File → Open dump file, or:

```powershell
windbgx -z "$env:LOCALAPPDATA\suzuri\CrashDumps\suzuri.exe.<pid>.dmp"
```

```text
.symfix; .reload
!analyze -v        exception code, faulting module and stack
.ecxr; k           stack of the faulting thread
~*k                all native threads
```

Keep the matching `suzuri.exe` next to the dump (release zips are on the
GitHub release page). WinDbg reads Go's DWARF poorly, so use Delve for Go
frames and WinDbg for the native side.
