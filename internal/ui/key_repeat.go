//go:build windows || darwin

package ui

import (
	"time"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/hajimehoshi/ebiten/v2/inpututil"
)

// keyRepeat tracks held keys for OS-like auto-repeat (ebiten only reports
// IsKeyJustPressed once per physical press).
type keyRepeat struct {
	down     map[ebiten.Key]time.Time // first observed down
	last     map[ebiten.Key]time.Time // last time we fired an action
	suppress map[ebiten.Key]bool      // held across a mode change; ignore until release
}

const (
	keyRepeatInitialDelay = 400 * time.Millisecond
	keyRepeatInterval     = 33 * time.Millisecond // ~30/s after delay
)

func newKeyRepeat() *keyRepeat {
	return &keyRepeat{
		down:     make(map[ebiten.Key]time.Time),
		last:     make(map[ebiten.Key]time.Time),
		suppress: make(map[ebiten.Key]bool),
	}
}

// suppressUntilRelease drops this key until it is released. The Warp bar
// consumes Enter, then the program takes the keyboard while that key is
// still down. Without this, the next frame treats the held Enter as a new
// press and answers a waiting prompt with a blank line.
func (k *keyRepeat) suppressUntilRelease(key ebiten.Key) {
	if k == nil {
		return
	}
	if k.suppress == nil {
		k.suppress = make(map[ebiten.Key]bool)
	}
	k.suppress[key] = true
	now := time.Now()
	k.down[key] = now
	k.last[key] = now
}

// fire returns true when the key action should run this frame (first press or
// a repeat tick while held).
func (k *keyRepeat) fire(key ebiten.Key, now time.Time) bool {
	if k == nil {
		return inpututil.IsKeyJustPressed(key)
	}
	if k.suppress[key] {
		if !ebiten.IsKeyPressed(key) {
			delete(k.suppress, key)
			delete(k.down, key)
			delete(k.last, key)
		}
		return false
	}
	if !ebiten.IsKeyPressed(key) {
		delete(k.down, key)
		delete(k.last, key)
		return false
	}
	if inpututil.IsKeyJustPressed(key) {
		k.down[key] = now
		k.last[key] = now
		return true
	}
	downAt, ok := k.down[key]
	if !ok {
		// Missed JustPressed (focus change). Arrows still fire so a held
		// key keeps moving. Enter does not: the bar may have just consumed
		// that press, and a second one answers a waiting prompt.
		k.down[key] = now
		k.last[key] = now
		if key == ebiten.KeyEnter || key == ebiten.KeyKPEnter {
			return false
		}
		return true
	}
	if now.Sub(downAt) < keyRepeatInitialDelay {
		return false
	}
	if now.Sub(k.last[key]) < keyRepeatInterval {
		return false
	}
	k.last[key] = now
	return true
}

// modAlt is Option (macOS) / Alt — either side.
func modAlt() bool {
	return ebiten.IsKeyPressed(ebiten.KeyAlt) ||
		ebiten.IsKeyPressed(ebiten.KeyAltLeft) ||
		ebiten.IsKeyPressed(ebiten.KeyAltRight)
}

// modMeta is Command (macOS) / Windows key.
func modMeta() bool {
	return ebiten.IsKeyPressed(ebiten.KeyMeta) ||
		ebiten.IsKeyPressed(ebiten.KeyMetaLeft) ||
		ebiten.IsKeyPressed(ebiten.KeyMetaRight)
}

// modControl is physical Control (not Command).
func modControl() bool {
	return ebiten.IsKeyPressed(ebiten.KeyControl) ||
		ebiten.IsKeyPressed(ebiten.KeyControlLeft) ||
		ebiten.IsKeyPressed(ebiten.KeyControlRight)
}

// modShift is either shift key.
func modShift() bool {
	return ebiten.IsKeyPressed(ebiten.KeyShift) ||
		ebiten.IsKeyPressed(ebiten.KeyShiftLeft) ||
		ebiten.IsKeyPressed(ebiten.KeyShiftRight)
}
