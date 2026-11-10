# UI hangs (Windows)

A thread in suzuri (`rsz-hang-watch`, app/src/hang.rs) sends the main window
a no-op message every 500 ms. When the main (UI) thread has not answered for
3 s it:

1. logs `ui stall: main thread not answering for N s (at <time>)` in
   `suzuri.log`, followed by one `ui stall thread <id>: <name>` line per named
   thread (the main thread is marked `(ui)`, unnamed ones are listed by id);
2. writes a minidump of the whole process (every thread's stack, the memory
   the stacks point at, handles) next to the log:
   `%LOCALAPPDATA%\suzuri\logs\hang-<time>.dmp` (`$RSZ_CONFIG_DIR\logs` for a
   dev instance). Only the newest 3 are kept;
3. logs `ui stall ended after N s` if the window comes back.

At most one dump per stall and one per 10 minutes (later stalls in that
window are only logged). The dump is written by a second `suzuri.exe` started
as a hidden helper (`suzuri __hang-dump <pid> <path>`), so a frozen thread
holding the heap or loader lock can't stall the dump inside the app; the app
waits up to 30 s for it and never kills it. A hang that still pumps messages
(a nested modal loop) answers the probe and is not caught. macOS has no
watchdog.

## Reading a dump

Use WinDbg (Microsoft Store) or `cdb` from the Windows SDK debugging tools.
Stacks need the `suzuri.pdb` built with that exact `suzuri.exe`: release
builds keep line tables (`debug = 1` in Cargo.toml), so it is
`target\release\suzuri.pdb` of the build that made the installer, not part of
the installer itself. Put it on the symbol path, with Microsoft's symbol
server for system DLLs:

```
cdb -z hang-2026-11-10T153000Z.dmp -y "srv*C:\symbols*https://msdl.microsoft.com/download/symbols;C:\path\to\suzuri-pdb"
```

Then:

- `!analyze -v -hang` names the blocked thread and what it waits on.
- `~*k` prints every thread's stack. Thread 0 is usually the main thread;
  match ids against the `ui stall thread` lines in the log (`~` lists the
  ids, `~~[<hex id>]s` switches to one).
- `!locks` / `!handle <h> f` show what a waiting thread is blocked on.

What to look for on the main thread's stack: a `#[tauri::command]` (sync
commands run on the main thread) or event-loop callback that is waiting on a
lock (`parking_lot` park), a channel, the clipboard (`GetClipboardData`), or
WebView2 controller creation (`wait_with_pump`). Then find which other thread
holds that lock in `~*k`.
