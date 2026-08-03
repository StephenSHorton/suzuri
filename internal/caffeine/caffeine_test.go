package caffeine

import (
	"testing"
	"time"
)

func TestToggleAndDeactivate(t *testing.T) {
	m := New()
	defer m.Close()
	if m.Active() {
		t.Fatal("start inactive")
	}
	if !m.Toggle() {
		t.Fatal("toggle on")
	}
	if !m.Active() {
		t.Fatal("should be active")
	}
	if lab := m.StripLabel(); lab != "∞" {
		t.Fatalf("label=%q want ∞", lab)
	}
	if m.Toggle() {
		t.Fatal("toggle off")
	}
	if m.Active() {
		t.Fatal("should be inactive")
	}
	if lab := m.StripLabel(); lab != "" {
		t.Fatalf("label=%q want empty", lab)
	}
}

func TestTimedExpiry(t *testing.T) {
	m := New()
	defer m.Close()
	if err := m.Activate(40 * time.Millisecond); err != nil {
		t.Fatal(err)
	}
	if !m.Active() {
		t.Fatal("active after Activate")
	}
	// Wait past timeout + timer callback.
	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		if m.Tick() || !m.Active() {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if m.Active() {
		t.Fatal("should expire")
	}
}

func TestStripLabelMinutes(t *testing.T) {
	m := New()
	defer m.Close()
	if err := m.Activate(5 * time.Minute); err != nil {
		t.Fatal(err)
	}
	lab := m.StripLabel()
	if lab == "" || lab == "∞" {
		t.Fatalf("timed label=%q", lab)
	}
	// Expect something like "5m" (allow 4m if clock skew mid-second).
	if lab != "5m" && lab != "4m" {
		t.Fatalf("label=%q", lab)
	}
}
