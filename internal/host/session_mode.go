package host

import (
	"os"
	"strings"
	"sync/atomic"
)

// sessionActorOn selects the Windows ConPTY session actor (session_actor.go)
// for new sessions. Off by default until the A2c soak passes (card #253).
// It has no effect on other OSes.
var sessionActorOn atomic.Bool

// SetSessionActor turns the ConPTY session actor on or off for sessions
// started after the call (config "conpty_actor").
func SetSessionActor(on bool) { sessionActorOn.Store(on) }

// useSessionActor is the effective choice: SUZURI_CONPTY_ACTOR=1 or 0
// overrides the config, so a soak run can flip it without editing settings.
func useSessionActor() bool {
	switch strings.TrimSpace(os.Getenv("SUZURI_CONPTY_ACTOR")) {
	case "1", "true", "on":
		return true
	case "0", "false", "off":
		return false
	}
	return sessionActorOn.Load()
}
