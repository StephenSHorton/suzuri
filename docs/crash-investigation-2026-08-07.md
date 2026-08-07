# Crash investigation — 2026-08-07 (Windows transfer UX session)

## What the user saw

suzuri “crashed” repeatedly while agents were installing a local build and
working on transfer UX. Running instance felt unstable.

## Evidence

| Source | Finding |
|--------|---------|
| `%LOCALAPPDATA%\suzuri\suzuri-crash.txt` | Only `crash-output open pid=…` markers — **no panic stack** |
| `%LOCALAPPDATA%\suzuri\suzuri.log` | Full-file search: **no** `panic:`, `runtime error`, access-violation strings |
| `%LOCALAPPDATA%\suzuri\CrashDumps\` | Empty / no new dumps for this window |
| Trail log | Normal layout settle / ConPTY resize breadcrumbs; last lines are startup |
| Last clean session (pid=22604, 16:57–16:58) | `WM_CLOSE` → `WM_DESTROY` → **exiting cleanly** |

## Timeline (agent-induced)

1. Built `suzuri-transfer-ux.exe` (transfer copy UX + hide engine console).
2. Tried to overwrite `%LOCALAPPDATA%\Programs\suzuri\suzuri.exe` while it was locked.
3. **Force-stopped** install-dir processes (`Stop-Process -Force`) to unlock the binary.
4. Several rapid restarts (pids 3112, 5360, 6956, 22604…) — looks like crashes from the outside.
5. Later session exited via normal window close (WM_CLOSE), not a panic.

## Conclusion

**This session’s “crashes” were mostly process kills and clean closes**, not a
new Go panic from the transfer-copy changes (those changes were never successfully
installed over the release binary).

That does **not** mean Windows is fine overall:

- Historical work on this tree already targets hard ConPTY resize under load
  (`fix(windows): never force ConPTY resize under load; durable crash trail`).
- Multiple simultaneous `suzuri.exe` processes (install path + `projects\suzuri`)
  amplify chaos during replace/debug.
- CUI child `suzuri-transfer.exe` opening a **second console** (fixed in this PR)
  felt like “suzuri opened another terminal / crashed the UX” even when the host stayed up.

## Recommendations (shipped in this PR where possible)

1. **Never force-kill install-dir suzuri from agents** to replace the binary.
   Prefer side-by-side launch with `SUZURI_ALLOW_MULTI=1`, or close the app
   from the UI first, then copy.
2. **Single-instance GUI (Windows)** — second launch activates the existing
   window via `Local\SuzuriTerminalSingleInstance` mutex + `FindWindow`.
   Override with `SUZURI_ALLOW_MULTI=1`.
3. Ship transfer UX so serve no longer pops a console window, and ticket copy
   works with in-panel **Copied!** feedback.
4. Continue ConPTY/resize hardening when a real trail + dump points at a
   resize path under alt-screen load (already paint-only under hot I/O on master).
