//go:build windows

package ui

import (
	"fmt"
	"os"
	"runtime"
	"strings"
	"time"
	"unsafe"

	"github.com/charmbracelet/log"
	"github.com/lxn/win"
	"golang.org/x/sys/windows"

	"github.com/StephenSHorton/suzuri/internal/applog"
)

// Hang watchdog and telemetry line (Windows handoff §3.2/§3.4, log part).
//
// The watchdog goroutine pings the UI thread every watchdogPing with a no-op
// sent message. SendMessageTimeout only returns once the UI thread pumps
// messages again, so no reply within watchdogStall means the UI thread is
// stuck inside one message (or not pumping at all). It then logs every
// goroutine's stack, the UI thread's current phase, the PTY queue depth and
// the process's GDI/USER handle counts, once per stall.
//
// It only observes: it never gates, drops or delays any work.

const (
	watchdogPing  = 500 * time.Millisecond
	watchdogStall = 2 * time.Second

	// wmSuzuriPing is the watchdog's no-op probe.
	wmSuzuriPing = win.WM_APP + 11
	// wmSuzuriDevStall sleeps the UI thread (dev trigger, see devStallFromEnv).
	wmSuzuriDevStall = win.WM_APP + 12

	smtoNormal = 0x0000

	grGDIObjects  = 0
	grUserObjects = 1
)

var (
	modUser32Watchdog       = windows.NewLazySystemDLL("user32.dll")
	procSendMessageTimeoutW = modUser32Watchdog.NewProc("SendMessageTimeoutW")
	procGetGuiResources     = modUser32Watchdog.NewProc("GetGuiResources")
	procGetMessageTime      = modUser32Watchdog.NewProc("GetMessageTime")
	procGetTickCount        = windows.NewLazySystemDLL("kernel32.dll").NewProc("GetTickCount")
)

// pingUIThread sends the no-op probe and reports whether the UI thread
// answered within timeout. On failure err is the Win32 error
// (ERROR_TIMEOUT when the thread is stuck, ERROR_INVALID_WINDOW_HANDLE when
// the window is gone).
func pingUIThread(hwnd win.HWND, timeout time.Duration) (ok bool, err error) {
	var result uintptr
	r, _, e := procSendMessageTimeoutW.Call(
		uintptr(hwnd), uintptr(wmSuzuriPing), 0, 0,
		smtoNormal, uintptr(timeout/time.Millisecond),
		uintptr(unsafe.Pointer(&result)),
	)
	if r != 0 {
		return true, nil
	}
	return false, e
}

// guiHandleCounts returns this process's GDI and USER object counts.
func guiHandleCounts() (gdi, user int) {
	proc := uintptr(windows.CurrentProcess())
	g, _, _ := procGetGuiResources.Call(proc, grGDIObjects)
	us, _, _ := procGetGuiResources.Call(proc, grUserObjects)
	return int(g), int(us)
}

// inputEventTime is when the message being handled was posted, from
// GetMessageTime (ms tick of the same clock as GetTickCount).
func inputEventTime(now time.Time) time.Time {
	r, _, _ := procGetMessageTime.Call()
	tick, _, _ := procGetTickCount.Call()
	// Both are 32-bit ms ticks; uint32 subtraction handles the 49.7-day wrap.
	age := time.Duration(uint32(tick)-uint32(r)) * time.Millisecond
	if age > 10*time.Second {
		return now
	}
	return now.Add(-age)
}

// startWatchdog launches the hang watchdog and the telemetry line. Both stop
// when the window dies.
func (u *winUI) startWatchdog() {
	if u == nil || u.hwnd == 0 || u.tel == nil {
		return
	}
	go u.watchdogLoop()
	go u.telemetryLoop()
	if d := devStallFromEnv(); d > 0 {
		go func() {
			time.Sleep(5 * time.Second)
			if u.alive.Load() && u.hwnd != 0 {
				log.Warn("dev stall: sleeping the UI thread", "for", d)
				win.PostMessage(u.hwnd, wmSuzuriDevStall, uintptr(d/time.Millisecond), 0)
			}
		}()
	}
}

func (u *winUI) watchdogLoop() {
	defer applog.Recover("watchdog", false)
	t := time.NewTicker(watchdogPing)
	defer t.Stop()
	var stallStart time.Time
	for range t.C {
		if !u.alive.Load() || u.hwnd == 0 {
			return
		}
		sent := time.Now()
		ok, err := pingUIThread(u.hwnd, watchdogStall)
		if ok {
			if !stallStart.IsZero() {
				log.Warn("ui stall ended", "stalled", time.Since(stallStart).Round(time.Millisecond))
				applog.Sync()
				stallStart = time.Time{}
			}
			continue
		}
		if !u.alive.Load() {
			return
		}
		if stallStart.IsZero() {
			stallStart = sent
			u.logStall(time.Since(sent), err)
		}
	}
}

// logStall writes the stall report: phase, queue depth, handles, stacks.
func (u *winUI) logStall(waited time.Duration, pingErr error) {
	now := time.Now()
	phase, inPhase := u.tel.currentPhase(now)
	panes, queued := paneTelemetrySnapshot(false)
	gdi, user := guiHandleCounts()
	log.Error("ui stall: UI thread did not answer the watchdog",
		"waited", waited.Round(time.Millisecond),
		"ping_err", pingErr,
		"phase", phase,
		"in_phase", inPhase.Round(time.Millisecond),
		"queue_bytes", queued,
		"panes", formatPanes(panes),
		"gdi", gdi,
		"user", user,
		"goroutines", runtime.NumGoroutine(),
		"stacks", allGoroutineStacks(),
	)
	applog.Sync()
}

func (u *winUI) telemetryLoop() {
	defer applog.Recover("telemetry", false)
	t := time.NewTicker(telemetryInterval)
	defer t.Stop()
	for range t.C {
		if !u.alive.Load() || u.hwnd == 0 {
			return
		}
		w := u.tel.takeWindow()
		w.panes, w.queued = paneTelemetrySnapshot(true)
		w.gdi, w.user = guiHandleCounts()
		w.goroutines = runtime.NumGoroutine()
		log.Info("telemetry",
			"frames", w.frames,
			"frame_p50", msDur(w.frameP50),
			"frame_p99", msDur(w.frameP99),
			"input_p99", msDur(w.inputP99),
			"inputs", w.inputN,
			"wakes", w.wakes,
			"bytes", formatPanes(w.panes),
			"queue_bytes", w.queued,
			"gdi", w.gdi,
			"user", w.user,
			"goroutines", w.goroutines,
		)
	}
}

// devStallFromEnv reads SUZURI_DEV_STALL (a Go duration such as "3s"): a
// dev-only trigger that sleeps the UI thread once, 5 s after start, so the
// watchdog's stall report can be checked. Unset or invalid means off.
func devStallFromEnv() time.Duration {
	v := strings.TrimSpace(os.Getenv("SUZURI_DEV_STALL"))
	if v == "" {
		return 0
	}
	d, err := time.ParseDuration(v)
	if err != nil || d <= 0 || d > time.Minute {
		log.Warn("SUZURI_DEV_STALL ignored", "value", v)
		return 0
	}
	return d
}

// telemetryPhase names a message for the stall report's "phase".
func telemetryPhase(msg uint32) string {
	switch msg {
	case win.WM_PAINT:
		return "paint"
	case wmSuzuriBytes:
		return "pty-drain"
	case wmSuzuriBlink:
		return "blink"
	case wmSuzuriClosed:
		return "pane-closed"
	case wmSuzuriMCP:
		return "mcp"
	case wmSuzuriLayoutSettle:
		return "layout-settle"
	case wmSuzuriSaveFinish:
		return "save-finish"
	case wmSuzuriOpenPalette:
		return "open-palette"
	case wmSuzuriToast:
		return "toast"
	case wmSuzuriUpdateOffer:
		return "update-offer"
	case wmSuzuriTransfer:
		return "transfer"
	case wmSuzuriDevStall:
		return "dev-stall"
	case win.WM_KEYDOWN, win.WM_SYSKEYDOWN, win.WM_CHAR, win.WM_SYSCHAR, win.WM_KEYUP, win.WM_SYSKEYUP:
		return "key"
	case win.WM_LBUTTONDOWN, win.WM_LBUTTONUP, win.WM_RBUTTONDOWN, win.WM_RBUTTONUP,
		win.WM_MBUTTONDOWN, win.WM_MBUTTONUP, win.WM_MOUSEMOVE, win.WM_MOUSEWHEEL:
		return "mouse"
	case win.WM_SIZE, win.WM_SIZING, win.WM_WINDOWPOSCHANGED, win.WM_ENTERSIZEMOVE, win.WM_EXITSIZEMOVE:
		return "size"
	case win.WM_TIMER:
		return "timer"
	}
	return fmt.Sprintf("msg 0x%04x", msg)
}

// isTelemetryInput reports messages that count toward input latency.
func isTelemetryInput(msg uint32) bool {
	switch msg {
	case win.WM_KEYDOWN, win.WM_SYSKEYDOWN, win.WM_CHAR, win.WM_SYSCHAR,
		win.WM_LBUTTONDOWN, win.WM_RBUTTONDOWN, win.WM_MBUTTONDOWN, win.WM_MOUSEWHEEL:
		return true
	}
	return false
}

// isTelemetryWake reports suzuri's own posted wake messages.
func isTelemetryWake(msg uint32) bool {
	return msg >= wmSuzuriBytes && msg <= wmSuzuriTransfer
}

// dispatchWithTelemetry wraps one WndProc call: phase for the stall report,
// wake and input counters, and frame timing for WM_PAINT.
func (u *winUI) dispatchWithTelemetry(hwnd win.HWND, msg uint32, wParam, lParam uintptr) uintptr {
	tel := u.tel
	if tel == nil {
		return u.handle(hwnd, msg, wParam, lParam)
	}
	switch msg {
	case wmSuzuriPing:
		return 1
	case wmSuzuriDevStall:
		prev := tel.enterPhase("dev-stall")
		time.Sleep(time.Duration(wParam) * time.Millisecond)
		tel.leavePhase(prev)
		log.Warn("dev stall: UI thread awake again")
		return 0
	}
	start := time.Now()
	if isTelemetryWake(msg) {
		tel.noteWake()
	}
	if isTelemetryInput(msg) {
		tel.noteInput(inputEventTime(start))
	}
	prev := tel.enterPhase(telemetryPhase(msg))
	defer func() {
		tel.leavePhase(prev)
		if msg == win.WM_PAINT {
			end := time.Now()
			tel.noteFrame(end.Sub(start), end)
		}
	}()
	return u.handle(hwnd, msg, wParam, lParam)
}
