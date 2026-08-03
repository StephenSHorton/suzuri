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
	down map[ebiten.Key]time.Time // first observed down
	last map[ebiten.Key]time.Time // last time we fired an action
}

const (
	keyRepeatInitialDelay = 400 * time.Millisecond
	keyRepeatInterval     = 33 * time.Millisecond // ~30/s after delay
)

func newKeyRepeat() *keyRepeat {
	return &keyRepeat{
		down: make(map[ebiten.Key]time.Time),
		last: make(map[ebiten.Key]time.Time),
	}
}

// fire returns true when the key action should run this frame (first press or
// a repeat tick while held).
func (k *keyRepeat) fire(key ebiten.Key, now time.Time) bool {
	if k == nil {
		return inpututil.IsKeyJustPressed(key)
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
		// Missed JustPressed (e.g. focus change) — treat as new hold.
		k.down[key] = now
		k.last[key] = now
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
