package host

import (
	"errors"
	"strings"
)

// ErrResizeBusy is returned when a platform PTY resize would race recent I/O.
// Callers leave UI sizes sticky and retry when quiet (never force mid-stream).
// Defined here (no OS build tag) so shared UI code can errors.Is against it.
var ErrResizeBusy = errors.New("pty resize busy: recent I/O")

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
