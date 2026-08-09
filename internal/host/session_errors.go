package host

import "errors"

// ErrResizeBusy is returned when a platform PTY resize would race recent I/O.
// Callers leave UI sizes sticky and retry when quiet (never force mid-stream).
// Defined here (no OS build tag) so shared UI code can errors.Is against it.
var ErrResizeBusy = errors.New("pty resize busy: recent I/O")
