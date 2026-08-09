# Windows hard-crash stability report (suzuri)

**Date:** 2026-08-08 (log corpus through 2026-08-08T20:23)  
**Repo:** `C:\Users\4step\projects\suzuri`  
**Runtime data:** `C:\Users\4step\AppData\Local\suzuri`  
**Scope:** Windows-only native hard deaths (no Go panic; macOS never crashes)  
**Method:** Independent re-read of production logs + live tree sources; no claim left as memory-only.

---

## Executive summary

Suzuri on Windows still dies with a **native process kill**: no Go panic stack, no `exiting cleanly`, no `WM_DESTROY` trail, and no WER minidump in the configured dump folder. The historical kill surface is **`ResizePseudoConsole` concurrent with hot ConPTY I/O** under Grok alt-screen load (commits `512f631` / `587e5aa` deferred resize while hot; residual paths still race).

**Smoking gun (P0, production 0.9.104):** session `pid=21324` at `2026-08-08T11:36:30` logged:

```text
tab.resize tab=0 from=159x84 to=159x40 alt=true hotIO=true ok=false
conpty.Resize enter cols=159 rows=40 pid=20516
conpty.Resize leave cols=159 rows=40
```

That sequence proves the **shipped** binary computed `ok=false` (hot I/O) and **still called** `ResizePseudoConsole`. The current worktree `tab.resize` early-returns on `!conPtyResizeOK()` and would not emit that enter/leave pair after a deny. Host `Session.Resize` still has **no** hot-I/O gate and only serializes Resize vs Close via `resizeMu`; Read/Write stay unlocked against both Resize and Close.

**Severity overall: critical** for residual unclean deaths under Grok; **high** for observability gaps that prevent attribution after partial gate fixes.

| Priority | Verdict | Hypothesis |
|----------|---------|------------|
| **P0** | Confirmed | H1 — production gate was advisory: `ok=false` still resized ConPTY |
| **P0** | Confirmed | H2 — `resizeMu` does not cover Read/Write vs ResizePseudoConsole |
| **P0** | Confirmed (mechanism) | H3 — non-settle paths call `applyClientSize` → `pane.resize` without settle begin |
| **P0** | Confirmed | H12 — hard deaths leave open-only crash markers; CrashDumps empty; recover is Go-only |
| **P1** | Confirmed | H5 (residual), H6, H8 — quiet TOCTOU / sash thrash / thin conpty v0.1.4 |
| **P1** | Confirmed (narrow) | H10 sticky `lastCols` + settle clear TOCTOU under dual-pane (not sole AV root) |
| **P2** | Confirmed (doc only) | H11 force-settle comment drift vs paint-only max-wait |
| **Disputed / not primary** | Refuted as stated | H4 dual-pane primary recipe; H7 toast ±1 storm as crash root; H9 Close↔Read as hard-death class; H5 as “300ms false-quiet under bursts” |

**Worktree status (already applied locally, not necessarily shipped as 0.9.104):** `tab.resize` skips ConPTY when hot; max-wait is paint-only; sash/input busy → paint-only; `Session.Close` holds `resizeMu`. **Ship that build and close the residual races + dump gap.**

---

## Evidence from production logs

### Artifacts inspected

| Path | Role |
|------|------|
| `C:\Users\4step\AppData\Local\suzuri\suzuri.log` | Session starts, alt-screen, split, clean vs unclean ends |
| `C:\Users\4step\AppData\Local\suzuri\suzuri-trail.log` | Fsync breadcrumbs: settle, `tab.resize` ok/hot, `conpty.Resize` enter/leave |
| `C:\Users\4step\AppData\Local\suzuri\suzuri-crash.txt` | Intended panic/crash body — **open markers only** |
| `C:\Users\4step\AppData\Local\suzuri\CrashDumps\` | WER LocalDumps target — **empty** |
| `C:\Users\4step\AppData\Local\suzuri\enable-wer-dumps.ps1` | HKLM LocalDumps for `suzuri.exe` DumpType=2 |

### Aggregate unclean-death gap

Prior production accounting (full `suzuri.log` scan in the stability campaign): **~245 starts vs ~31 clean `exiting cleanly` lines** — roughly **~214 unclean** process ends. Recent 0.9.104 unclean PIDs with open-only crash markers: **21324, 16220, 23484**. Clean contrast: **pid=2304** ends with shell exit → `WM_DESTROY` → `exiting cleanly`.

### Session A — pid=21324, version=0.9.104 (dual-pane + Grok + gate miss)

**Start:** `2026-08-08T11:35:56` — `starting pid=21324 … version=0.9.104`  
**Crash marker:** `suzuri-crash.txt` open-only at same timestamp.

Timeline (log + trail aligned):

| Time (local) | Event |
|--------------|--------|
| 11:35:56 | Start, shell pid 20516, window restore 1278×1360 |
| 11:35:57 | Boot settles; toast “checking for updates…” rows=2; quiet ConPTY resizes 81↔80 |
| 11:36:06 | alt-screen t0 on; settle deferred (busy) |
| 11:36:09 | Quiet settle: `alt=true hot=false ok=true` + `conpty.Resize` 159×84 |
| 11:36:27 | **split down** leaves=2; new shell 23360; toast “split down” rows=2 |
| **11:36:30** | **`tab.resize` t0 159×84→159×40 `alt=true hotIO=true ok=false` then `conpty.Resize enter/leave` rows=40** (no adjacent `layout settle begin`) |
| 11:36:30 | t1 quiet resize 42→38 ok=true + ConPTY |
| 11:36:32 | `layout max-wait paint-only` dual: t0 alt+hot sz=159×40; t1 hot sz=159×38 |
| 11:36:33 | alt-screen t1 on |
| 11:36:36 | Dual-alt settle both `hot=false`; t1 ConPTY to 159×40 |
| 11:37:23 | Still alive; empty settle |
| 11:38:11 | Last log line: `WM_ACTIVATE active=0` |
| 12:03:56 | **Next process start pid=16220 — no `WM_DESTROY` / `exiting cleanly` for 21324** |

**Implications:**

1. Gate bypass is proven in production trail (H1).
2. This particular `conpty.Resize` completed (`leave` logged); death was **not instantaneous** at 11:36:30.
3. Dual-pane Grok is a **strong stress path**, not proven as the sole/primary unclean recipe (see H4 refutation).

### Session B — pid=16220, 0.9.104 (single-pane Grok alt, unclean)

- Start 12:03:56; alt-screen 12:04:01; quiet settle 12:04:04 (`alt=true hot=false ok=true` ConPTY to 159×84).
- No split. Last trail activity ~12:04:04. Next start 19:04:47 (pid=23484).
- **No clean exit.** Counterexample to “dual-pane only.”

### Session C — pid=23484, 0.9.104 (single-pane Grok alt, unclean)

- Start 19:04:47; alt-screen 19:18:16; quiet settle 19:18:18.
- No split. Next start 20:23:13 (pid=2304).
- **No clean exit.** Same single-pane pattern.

### Session D — pid=2304, 0.9.104 (clean control)

```text
shell process exited tab=0 code=0
pty read ended tab=0 err="The handle is invalid."
WM_DESTROY — tearing down
WM_QUIT — message loop exit
exiting cleanly pid=2304
```

Close under live Read produced a **soft** invalid-handle error and clean host exit — not a native AV.

### Crash file (entire content pattern)

Every line is of the form:

```text
--- crash-output open pid=<N> t=<timestamp> ---
```

Including unclean 21324 / 16220 / 23484 and clean 2304. **Zero panic stacks, zero SEH records.**

### Trail breadcrumb of interest (verbatim)

```text
2026-08-08T11:36:30-06:00 pid=21324 tab.resize tab=0 from=159x84 to=159x40 alt=true hotIO=true ok=false
2026-08-08T11:36:30-06:00 pid=21324 conpty.Resize enter cols=159 rows=40 pid=20516
2026-08-08T11:36:30-06:00 pid=21324 conpty.Resize leave cols=159 rows=40
2026-08-08T11:36:32-06:00 pid=21324 layout max-wait paint-only w=1278 h=1360 panes=t0:alive=true,alt=true,hot=true,sz=159x40;t1:alive=true,alt=false,hot=true,sz=159x38
```

Note: production trail still had rich `ok=` / enter-leave instrumentation. Current tree `tab.resize` logs only `Debug` on skip and no longer emits the old `tab.resize … ok=` trail line in the same form — ship instrumentation must be re-aligned.

---

## Confirmed root-cause hypotheses (P0 / P1)

### H1 — Production 0.9.104: `ok=false` still called ResizePseudoConsole — **P0, real**

**Evidence:** trail L419–421 above; `suzuri.log` start `version=0.9.104` for pid=21324; worktree `tab.go` 528–534 early-return.

```524:535:C:\Users\4step\projects\suzuri\internal\ui\tab.go
	// Windows: ResizePseudoConsole while a pane streams (Grok alt-screen) has
	// hard-killed the host with no Go panic. Skip ConPTY and leave lastCols/
	// lastRows unchanged so a later quiet layout settle retries the native call.
	// VT is already at the new size for software paint.
	if t.sess != nil {
		if !t.conPtyResizeOK() {
			log.Debug("tab.resize skip ConPTY (hot I/O)",
				"tab", t.id, "cols", cols, "rows", rows,
				"from", fmt.Sprintf("%dx%d", t.lastCols, t.lastRows),
				"alt", t.altScreen())
			return
		}
```

**Host still always resizes when called:**

```253:276:C:\Users\4step\projects\suzuri\internal\host\session_windows.go
// Resize updates the ConPTY dimensions.
// Serialized with Close: concurrent ResizePseudoConsole vs ClosePseudoConsole
// (or concurrent Resize) has hard-crashed the host process (no Go panic trail).
func (s *Session) Resize(cols, rows int) error {
	// ...
	s.resizeMu.Lock()
	defer s.resizeMu.Unlock()
	if s.cpty == nil {
		return nil
	}
	return s.cpty.Resize(cols, rows)
}
```

**Caveat:** leave was logged for the 11:36:30 call — gate-bypass is proven; that single call is not proven as the AV frame.

### H2 — Resize races concurrent ReadFile/WriteFile — **P0/P1, real**

| Path | Lock |
|------|------|
| `Session.Read` / `Write` | none |
| `Session.Resize` / `Close` | `resizeMu` |
| `noteIO` | after Read returns n>0 only |
| `writeLoop` | never notes I/O |

```237:251:C:\Users\4step\projects\suzuri\internal\host\session_windows.go
func (s *Session) Read(p []byte) (int, error) {
	if s == nil || s.cpty == nil {
		return 0, io.EOF
	}
	return s.cpty.Read(p)
}

func (s *Session) Write(p []byte) (int, error) {
	// ...
	return s.cpty.Write(p)
}
```

Module `github.com/UserExistsError/conpty@v0.1.4` maps Resize → bare `ResizePseudoConsole`, Read/Write → `ReadFile`/`WriteFile`, **no library mutex**. Comments in `session_windows.go` already document hard-crash (no Go panic) for concurrent Resize under I/O.

Quiet resizes routinely complete while readLoop is blocked in Read (trail shows many enter/leave under alt-screen when `hot=false`). Concurrent Resize is **not always fatal**; it is a structural native hazard class matching the crash signature.

### H3 — Non-settle geometry paths invoke `pane.resize` without settle begin — **P0 mechanism, P1 residual**

`applyClientSize` always loops leaves → `g.pane.resize` with no hot check of its own:

```3837:3842:C:\Users\4step\projects\suzuri\internal\ui\window_windows.go
		for _, g := range res.leaves {
			if g.pane == nil || !g.pane.alive.Load() {
				continue
			}
			g.pane.resize(g.cols, g.rows)
		}
```

Callers that can hit ConPTY without a settle begin log:

- `maybeResizeForInput` → busy? paint-only : `applyClientSize` (current tree gated).
- Sash `WM_MOUSEMOVE` → same pattern (current tree gated).
- Split/toast use `postLayoutSettle` (coalesced), not bare apply.

Trail 11:36:30 has **no** adjacent `layout settle begin` (unlike 11:36:09 / 11:36:36), matching a path that resized under hot I/O while production `ok=false` was not enforced.

### H5 — 300ms quiet gate is last-I/O sample, not in-flight barrier — **P1 residual race; not “false-quiet under bursts”**

```34:40:C:\Users\4step\projects\suzuri\internal\ui\ui_common.go
	// conPtyIOQuiet: do not ResizePseudoConsole while a pane has recent I/O.
	conPtyIOQuiet = 300 * time.Millisecond
	// layoutDeferMaxWait: after this, force settle even if I/O is still hot so
	// split/window resize reflows (avoids permanent letterbox under Grok).
	layoutDeferMaxWait = 1500 * time.Millisecond
```

```232:248:C:\Users\4step\projects\suzuri\internal\ui\tab.go
func (t *tab) conPtyResizeOK() bool {
	// ...
	return !paneHasRecentIO(t, conPtyIOQuiet)
}

func paneHasRecentIO(t *tab, quiet time.Duration) bool {
	// ...
	ns := t.lastIOUnixNano.Load()
	// ...
	return time.Since(time.Unix(0, ns)) < quiet
}
```

Trail correctly reports `hot=true` during stream (11:36:30, max-wait 11:36:32). Quiet resizes at 11:36:09 / 11:36:36 match ≥300ms silence by the metric — not mid-burst false quiet. Residual hazard: **check-then-use** (sample lastIO, then call Resize while a Read is still in-flight or a new chunk arrives). Not the primary smoking gun (that is H1 gate bypass).

### H6 — Sash drag can thrash ConPTY on quiet gaps — **P1/P2 design thrash**

```3049:3061:C:\Users\4step\projects\suzuri\internal\ui\window_windows.go
		if u.sashDrag != nil && (wParam&win.MK_LBUTTON) != 0 {
			applySashDrag(*u.sashDrag, px, py)
			if u.width > 0 && u.height > 0 {
				if u.anyPaneConPtyBusy() {
					u.markLayoutDeferred()
					u.relayoutActivePaintOnly()
				} else {
					u.applyClientSize(u.width, u.height)
				}
			}
```

LBUTTONUP already `postLayoutSettle`. Mid-drag ConPTY on quiet panes is avoidable thrash; under Grok/hot I/O path is paint-only. Trail pid=21324 159×84→40 is a **one-shot split reflow**, not a sash mousemove storm.

### H8 — conpty v0.1.4 is an unlocked kernel32 wrapper — **P1 enabling surface**

Verified module cache `conpty@v0.1.4/conpty.go`:

- `NewLazySystemDLL("kernel32")` Create/Resize/ClosePseudoConsole
- `CreatePseudoConsole(..., flags=0, …)` — no RESIZE_QUIRK / no sideloaded conpty.dll
- `ConPty` struct has **no mutex**
- `Close()`: `ClosePseudoConsole` then `CloseHandle` on all pipes with **no drain**
- Non-S_OK HRESULTs return as Go errors; **native AVs never surface**

Pinned in `go.mod`: `github.com/UserExistsError/conpty v0.1.4`.

### H10 — sticky lastCols after hot skip + clearLayoutDeferred — **P1 narrow TOCTOU**

Current policy intentionally leaves `lastCols`/`lastRows` sticky on hot skip so quiet settle retries ConPTY. `applyLayoutAfterSizeMove` clears deferred only after a quiet `applyClientSize`. Paint re-posts settle when `needResize && !layoutDeferred`. Dual-pane mid-apply noteIO can skip one leaf, clear deferred, and reopen settle/paint churn while VT and ConPTY diverge. Mitigated by busy short-circuit; residual dual-pane TOCTOU remains. **Not** proven as native-AV root (no dump).

### H11 — stale force-settle comments — **P2 doc hazard**

Comments in `ui_common.go` / `tab.go` still say “force settle after layoutDeferMaxWait” while hot. Implementation is paint-only:

```2008:2024:C:\Users\4step\projects\suzuri\internal\ui\window_windows.go
			// Max-wait under load: paint-only reflow (never ResizePseudoConsole
			// mid-stream — force=true hard-killed 0.9.82 under Grok).
			// ...
					u.relayoutActivePaintOnly()
```

Settle log uses literal `force=false`. Risk is **reintroduction**, not an active force path.

### H12 — unattributable native deaths — **P0 observability**

- `suzuri-crash.txt`: open markers only for all recent PIDs.
- `CrashDumps\`: empty despite `enable-wer-dumps.ps1` (DumpFolder + DumpType=2).
- `main` defer `recover` and `applog.Recover` only catch **Go panics** — never SEH/ConPTY AV.
- Unclean sessions: last log often mid-session (`WM_ACTIVATE active=0` or quiet settle); clean path always ends `exiting cleanly`.

Without dumps, residual AVs after gate fixes stay unattributable.

---

## Refuted / noise

### H4 — Dual-pane Grok + split is the *primary* unclean recipe — **refuted as primary**

**What is true:** pid=21324 ran alt t0 → split leaves=2 → hot ConPTY with gate miss → dual alt → unclean end (no WM_DESTROY).

**What is false:** dual-pane as primary recipe.

- pid=16220 and pid=23484 unclean after **single-pane** Grok alt only (no split).
- pid=21324 survived dual-alt settles and ~26 minutes of silence after last deactivate before the next process start — hot resize is not death-adjacent in wall-clock.
- No WER stack ties AV to dual-pane.

Dual-pane remains a **high-value stress / gate-miss lab**, not the sole death recipe.

### H5 as “300ms false-quiet window under alt-screen bursts” — **overstated**

Quiet samples are real ≥300ms gaps by metric; stream correctly reports hot. The stronger trail line is **true-hot gate bypass** (H1), opposite of false quiet.

### H7 — Toast RowCount ±1 storms as crash root — **secondary noise**

Mechanism real: `toast` / `clearToastIfDue` post settle when `chrome.RowCount` flips. Boot 81↔80 maps to update toast rows=2 under quiet I/O. At 11:36:30 split toast already `rows=2` (no re-trigger); half-pane 84→40 is **split settle**, not toast ±1.

### H9 — ClosePseudoConsole races Read as hard-death class — **refuted as crash root**

Lock asymmetry is real (Read/Wait unlocked; Close locks and nils cpty). Project comments and production show Close under live Read as **soft** invalid-handle then clean exit (pid=2304). Unclean 21324 has no Close/read-end trail — Resize/hot-I/O class, not teardown Close.

### Force-kill / single-instance noise

- No evidence that unclean ends are Task Manager kills as the *common* pattern (would look similar: no clean exit), but Grok/alt + ConPTY trail correlation + historical ResizePseudoConsole crashes dominate.
- Single-instance: next process starts freely after unclean ends; no “already running” lock explains deaths.
- Empty Application Error WER for recent sessions is consistent with hard kills that never produce dumps **or** with silent TerminateProcess — dumps + exit markers still required.

### Mac never crashes

POSIX path uses `creack/pty` + `pty.Setsize` under `resizeMu` (`session_unix.go`). Fatal surface is Windows ConPTY + custom GDI host only.

---

## Comparison to other terminals

| Practice | Peers (Windows Terminal, WezTerm, etc.) | Suzuri 0.9.104 / worktree |
|----------|----------------------------------------|---------------------------|
| Serialize ConPTY resize vs I/O | Typically serialize HPCON ops; often own conpty/OpenConsole stack | `resizeMu` only Resize+Close; Read/Write concurrent |
| Sideload conpty.dll / OpenConsole | Common for quirk flags / fixes | System kernel32 only via thin v0.1.4 |
| CreatePseudoConsole flags | RESIZE_QUIRK / inheritance flags where available | flags literal `0` |
| Close order | Drain pipes / cancel I/O before ClosePseudoConsole | ClosePseudoConsole then CloseHandle pipes, no drain |
| Mid-stream window resize under TUI | Debounce / paint-first / wait quiet | Policy evolving: paint-only max-wait + hot skip (worktree); production still resized on ok=false |
| Crash dumps | Often WER + crashpad | Script exists; dumps not materializing |

Suzuri’s failure mode (native death, no Go panic) matches **kernel ConPTY SEH under concurrent Resize/I/O**, not a pure Go logic panic. Peer practice argues for: (1) full HPCON serialization or I/O barrier, (2) drain-before-close, (3) optional modern conpty sideload, (4) working minidumps.

---

## Concrete fix plan (ordered, PR-sized)

### PR1 — Ship enforced hot gate + paint-only max-wait (release train) — **critical**

**Goal:** Never call `ResizePseudoConsole` when any affected pane is hot; never force ConPTY after `layoutDeferMaxWait`.

- Keep/verify `tab.resize` early-return on `!conPtyResizeOK()` (already in worktree).
- Keep max-wait → `relayoutActivePaintOnly` only (already in worktree).
- Keep sash / `maybeResizeForInput` busy → paint-only (already in worktree).
- Align comments (H11): remove “force settle while hot” wording in `ui_common.go` / `tab.go`.
- Add regression test: when sess mock + hot, `tab.resize` must not call Resize (extend `resize_policy_test.go`; may need small interface seam).
- **Ship as 0.9.105+** and confirm trail never shows `ok=false` followed by `conpty.Resize enter`.

### PR2 — Host-level hard gate + I/O barrier — **critical**

**Goal:** Defense in depth so UI bugs cannot reach `ResizePseudoConsole` under load.

1. **In-flight I/O counter** on `Session`: `BeginRead`/`EndRead` (and Write if needed); `Resize` waits or fails closed while counter > 0.
2. Optionally hold a shared `ioMu` or extend `resizeMu` so Resize waits for zero in-flight Read/Write (with timeout → skip + return err, never force).
3. Trail: log `session.Resize skip reason=inflight|closed` with pid/cols/rows.
4. Do **not** block forever under Grok stream — prefer skip + UI deferred settle (letterbox-free via VT-only paint).

### PR3 — ConPTY lifecycle hardening — **high**

1. Drain or cancel pending Read before `ClosePseudoConsole` (async cancel + join readLoop).
2. Evaluate forking/vendoring conpty or upgrading when a safer wrapper exists; set CreatePseudoConsole flags if RESIZE_QUIRK is available for the OS build.
3. Keep Close under `resizeMu` (already done); document intentional waitLoop Close.

### PR4 — Reduce quiet-path ConPTY thrash — **high**

1. Sash drag: **always** paint-only mid-drag; ConPTY only on LBUTTONUP settle (even when quiet).
2. Toast RowCount: coalesce with existing settle; avoid ±1 ConPTY pairs during Grok warm-up when possible (defer chrome height until quiet).
3. `applyClientSize`: if any leaf would skip ConPTY (hot), do not `clearLayoutDeferred` until all leaves advanced `lastCols` (closes H10 TOCTOU).

### PR5 — Observability that survives native death — **high / P0 for diagnosis**

1. Verify elevated `enable-wer-dumps.ps1` applied; confirm `HKLM\…\LocalDumps\suzuri.exe` and that dumps appear after a deliberate AV (e.g. debug-only crash button).
2. Trail: durable **session exit marker** on every path — `exiting cleanly`, `exiting unclean reason=…`, and heartbeat / last-breadcrumb ring flushed with Sync.
3. Log trail line on **skip** ConPTY (not only Debug) so production trail matches policy: `tab.resize skip hotIO=true …` without enter.
4. Optional: VEH/WER custom filter or external watchdog writing last trail to a separate file.

### PR6 — Dual-pane + Grok soak + single-pane soak automation — **medium**

Automated soak (manual recipe scripted):

- Single-pane Grok alt + continuous stream + window resize / toast.
- Dual-pane both Grok alt + split + sash + max-wait.
- Assert: process alive N minutes; trail has zero `ok=false` + enter pairs; max-wait only paint-only.

### Explicitly do not reintroduce

- `force=true` ConPTY under load (0.9.82 kill).
- Permanent ban on alt-screen resize (letterbox regression).
- Unconditional clear of layoutDeferred after a partial hot apply.

---

## Repro recipes

### R1 — Production gate miss (0.9.104 binary)

1. Launch suzuri 0.9.104.
2. Run `grok` until alt-screen + streaming output.
3. Split down (second pane).
4. Immediately start `grok` in second pane / keep first pane streaming.
5. Observe trail: `hotIO=true ok=false` then `conpty.Resize enter` on half-height reflow.
6. Continue using dual Grok + focus/palette; session may survive minutes then vanish without clean exit.

### R2 — Worktree / stability1 expected behavior

1. Build with current tree (`tab.resize` skip + paint-only max-wait).
2. Same dual Grok + split under stream.
3. **Expect trail:** max-wait paint-only; Debug/Info skip ConPTY; **no** `conpty.Resize enter` while `hot=true`.
4. After stream quiets ≥300ms, settle should ConPTY-resize both panes to final geometry.

### R3 — Single-pane unclean (observed 16220 / 23484)

1. Start suzuri; run Grok alt-screen.
2. Idle / background after quiet alt settle.
3. Process may disappear later with no WM_DESTROY — **not** split-specific; keep single-pane soak in regression matrix.

### R4 — Clean control

1. Start suzuri; type `exit` in the only pane.
2. Expect: shell exit → invalid-handle read end → WM_DESTROY → `exiting cleanly`.

### R5 — Sash thrash (quiet)

1. Two quiet panes; drag sash rapidly.
2. Current quiet path may ConPTY every mousemove; after PR4 expect paint-only until mouse-up.

---

## Instrumentation gaps (WER dumps, trail exit markers)

| Gap | Current state | Needed |
|-----|---------------|--------|
| Panic/crash body | `suzuri-crash.txt` open markers only | Open + write last breadcrumbs; native dump path |
| WER LocalDumps | Script present; `CrashDumps\` empty | Confirm admin registry; force test dump; check `%LOCALAPPDATA%\CrashDumps` fallback |
| Go recover | Catches panics only | Document limitation; add VEH or external monitor |
| Session end marker | Clean path logs `exiting cleanly`; unclean has **none** | Trail + log: `session_end unclean last_event=…` from atexit / finalizer / external watcher |
| Trail skip vs enter | Production logged ok=false **and** enter; worktree Debug skip may not hit trail file | Always fsync trail on skip and enter/leave |
| In-flight Read flag | Only lastIO timestamp | Counter for true concurrent Resize guard |
| Start/exit ratio | ~245 vs ~31 historically | Dashboard metric: starts − clean exits per version |

**Until dumps work, every “fix landed” claim is trail-circumstantial.** Treat empty CrashDumps as a release blocker for stability claims.

---

## Hypothesis scoreboard (swarm merge)

| ID | Severity | Verdict | Priority |
|----|----------|---------|----------|
| H1 | critical | **Confirmed** — production ok=false still resized | P0 |
| H2 | critical | **Confirmed** — unlocked I/O vs Resize | P0 |
| H3 | critical | **Confirmed** mechanism; residual after UI gates | P0/P1 |
| H4 | critical | **Disputed** as *primary* recipe; dual-pane is stress, not sole death | — |
| H5 | high | **Partial** — residual check-then-use; not false-quiet under bursts | P1/P2 |
| H6 | high | **Confirmed** thrash design; hot path mitigated | P1/P2 |
| H7 | high | **Secondary** toast noise | P2 |
| H8 | high | **Confirmed** thin unlocked conpty | P1 |
| H9 | high | **Refuted** as hard-death class (soft error + clean exit) | — |
| H10 | high | **Partial** sticky lastCols TOCTOU | P1 |
| H11 | medium | **Confirmed** comment/code skew | P2 |
| H12 | high | **Confirmed** unattributable deaths | P0 |

---

## Top fixes (actionable)

1. **Ship PR1:** enforce `conPtyResizeOK` before every `sess.Resize`; never force ConPTY after max-wait.
2. **PR2:** Session in-flight Read/Write barrier; Resize fails closed under concurrent I/O.
3. **PR5:** Make WER dumps actually appear; trail session_end + skip breadcrumbs with fsync.
4. **PR4:** Sash paint-only until mouse-up; clear deferred only when all panes advanced lastCols.
5. **PR3:** Drain/cancel I/O before ClosePseudoConsole; evaluate conpty flags / vendor upgrade.
6. **Soak matrix:** single-pane + dual-pane Grok alt under continuous stream; assert no hot enter/leave pairs.
7. **Docs:** delete force-settle-while-hot language (H11) so 0.9.82 is never re-landed by comment cargo-cult.

---

## Sources (absolute paths)

- `C:\Users\4step\AppData\Local\suzuri\suzuri.log` (esp. L89946–90100)
- `C:\Users\4step\AppData\Local\suzuri\suzuri-trail.log` (esp. L400–488)
- `C:\Users\4step\AppData\Local\suzuri\suzuri-crash.txt`
- `C:\Users\4step\AppData\Local\suzuri\enable-wer-dumps.ps1`
- `C:\Users\4step\projects\suzuri\internal\ui\tab.go`
- `C:\Users\4step\projects\suzuri\internal\ui\ui_common.go`
- `C:\Users\4step\projects\suzuri\internal\ui\window_windows.go`
- `C:\Users\4step\projects\suzuri\internal\ui\split_host_windows.go`
- `C:\Users\4step\projects\suzuri\internal\host\session_windows.go`
- `C:\Users\4step\projects\suzuri\go.mod`
- `C:\Users\4step\go\pkg\mod\github.com\!user!exists!error\conpty@v0.1.4\conpty.go`
- `C:\Users\4step\projects\suzuri\cmd\suzuri\main.go`
- `C:\Users\4step\projects\suzuri\internal\applog\applog.go`
