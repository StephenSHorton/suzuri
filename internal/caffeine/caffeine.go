// Package caffeine keeps the machine awake while suzuri holds a power assertion.
// Inspired by the classic Caffeine menu-bar app (IOKit on macOS; Win32 on Windows).
package caffeine

import (
	"sync"
	"time"
)

// Manager owns the process-wide stay-awake state for one suzuri window.
type Manager struct {
	mu     sync.Mutex
	active bool
	until  time.Time // zero = indefinite while active
	timer  *time.Timer
	hold   platformHold
}

// platformHold is the OS power assertion handle.
type platformHold interface {
	acquire() error
	release()
}

// New returns an inactive manager.
func New() *Manager {
	return &Manager{hold: newPlatformHold()}
}

// Active reports whether stay-awake is currently on.
func (m *Manager) Active() bool {
	if m == nil {
		return false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.expireLocked(time.Now())
	return m.active
}

// Remaining returns time left when a timed activation is running.
// ok is false when inactive or indefinite.
func (m *Manager) Remaining() (d time.Duration, ok bool) {
	if m == nil {
		return 0, false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now()
	m.expireLocked(now)
	if !m.active || m.until.IsZero() {
		return 0, false
	}
	d = m.until.Sub(now)
	if d < 0 {
		d = 0
	}
	return d, true
}

// Activate turns stay-awake on. duration 0 means until Toggle/Deactivate.
func (m *Manager) Activate(duration time.Duration) error {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.activateLocked(duration)
}

// Deactivate releases the assertion (no-op if already off).
func (m *Manager) Deactivate() {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.deactivateLocked()
}

// Toggle flips indefinite stay-awake. Returns the new active state.
func (m *Manager) Toggle() bool {
	if m == nil {
		return false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.expireLocked(time.Now())
	if m.active {
		m.deactivateLocked()
		return false
	}
	_ = m.activateLocked(0)
	return m.active
}

// Close releases any assertion (call on window teardown).
func (m *Manager) Close() {
	m.Deactivate()
}

// Tick expires a timed activation. Returns true if state changed (became inactive).
func (m *Manager) Tick() (turnedOff bool) {
	if m == nil {
		return false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.active {
		return false
	}
	if m.until.IsZero() {
		return false
	}
	if time.Now().Before(m.until) {
		return false
	}
	m.deactivateLocked()
	return true
}

// StripLabel is a short right-strip caption: "" when off, "∞" indefinite, or "12m".
func (m *Manager) StripLabel() string {
	if m == nil {
		return ""
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now()
	m.expireLocked(now)
	if !m.active {
		return ""
	}
	if m.until.IsZero() {
		return "∞"
	}
	sec := int(m.until.Sub(now).Seconds() + 0.5)
	if sec < 0 {
		sec = 0
	}
	if sec >= 3600 {
		h := sec / 3600
		min := (sec % 3600) / 60
		if min == 0 {
			return formatInt(h) + "h"
		}
		return formatInt(h) + "h" + formatInt(min)
	}
	if sec >= 60 {
		return formatInt(sec/60) + "m"
	}
	return formatInt(sec) + "s"
}

func formatInt(n int) string {
	if n < 0 {
		n = 0
	}
	// tiny, allocation-free enough for strip paint
	if n < 10 {
		return string(rune('0' + n))
	}
	var b [12]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}

func (m *Manager) activateLocked(duration time.Duration) error {
	if m.timer != nil {
		m.timer.Stop()
		m.timer = nil
	}
	if !m.active {
		if err := m.hold.acquire(); err != nil {
			m.active = false
			m.until = time.Time{}
			return err
		}
		m.active = true
	}
	if duration > 0 {
		m.until = time.Now().Add(duration)
		// Timer is a backup; Tick() also expires from the UI loop.
		d := duration
		m.timer = time.AfterFunc(d, func() {
			m.mu.Lock()
			defer m.mu.Unlock()
			if m.active && !m.until.IsZero() && !time.Now().Before(m.until) {
				m.deactivateLocked()
			}
		})
	} else {
		m.until = time.Time{}
	}
	return nil
}

func (m *Manager) deactivateLocked() {
	if m.timer != nil {
		m.timer.Stop()
		m.timer = nil
	}
	if m.active {
		m.hold.release()
	}
	m.active = false
	m.until = time.Time{}
}

func (m *Manager) expireLocked(now time.Time) {
	if m.active && !m.until.IsZero() && !now.Before(m.until) {
		m.deactivateLocked()
	}
}
