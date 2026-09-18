package host

import (
	"errors"
	"strings"
)

// ErrResizeBusy is returned when a platform PTY resize would race recent I/O
// or another native resize in this process. Callers leave UI sizes sticky and
// retry when quiet (never force mid-stream).
// Defined here (no OS build tag) so shared UI code can errors.Is against it.
var ErrResizeBusy = errors.New("pty resize busy: recent I/O")

// NativeResizeDenied is the ConPTY skip policy, extracted so it can be tested
// without a live console. Dual Grok split used to call ResizePseudoConsole twice
// on the UI thread; the process died with no Go panic and no WER dump.
//
// recentIO: this pane streamed within ioQuietNs.
// lastUnix/nowUnix: last successful native resize in the whole process (unix ns).
// gapNs: minimum spacing between any two native resizes.
func NativeResizeDenied(recentIO bool, lastUnix, nowUnix, gapNs int64) (denied bool, reason string) {
	if recentIO {
		return true, "recentIO"
	}
	if lastUnix != 0 && gapNs > 0 && nowUnix-lastUnix < gapNs {
		return true, "globalGap"
	}
	return false, ""
}

// stripNoColor drops NO_COLOR from a child env. Interactive PTYs must not
// inherit it from the host (Grok Build treats NO_COLOR as ColorLevel::None
// and paints every role as Reset — white-on-default).
func stripNoColor(env []string) []string {
	if len(env) == 0 {
		return env
	}
	out := make([]string, 0, len(env))
	for _, e := range env {
		k, _, _ := strings.Cut(e, "=")
		if strings.EqualFold(k, "NO_COLOR") {
			continue
		}
		out = append(out, e)
	}
	return out
}
