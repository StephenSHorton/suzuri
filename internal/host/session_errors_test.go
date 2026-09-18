package host

import "testing"

func TestNativeResizeDeniedRecentIO(t *testing.T) {
	denied, reason := NativeResizeDenied(true, 0, 1, 400_000_000)
	if !denied || reason != "recentIO" {
		t.Fatalf("hot pane: denied=%v reason=%q", denied, reason)
	}
}

func TestNativeResizeDeniedGlobalGapBlocksSecondPane(t *testing.T) {
	const gap = int64(400_000_000)
	now := int64(1_000_000_000_000)
	// First pane in a dual-Grok settle: no prior native resize.
	if denied, _ := NativeResizeDenied(false, 0, now, gap); denied {
		t.Fatal("first pane must be allowed")
	}
	// Second pane 100ms later on the same UI turn.
	denied, reason := NativeResizeDenied(false, now, now+100_000_000, gap)
	if !denied || reason != "globalGap" {
		t.Fatalf("second pane: denied=%v reason=%q (want globalGap)", denied, reason)
	}
	// After the gap, retry is allowed (quiet blink settle).
	if denied, _ := NativeResizeDenied(false, now, now+gap, gap); denied {
		t.Fatal("retry after gap must be allowed")
	}
}

func TestNativeResizeDeniedAllowsFirstCall(t *testing.T) {
	if denied, reason := NativeResizeDenied(false, 0, 42, 400_000_000); denied {
		t.Fatalf("first native resize denied: %s", reason)
	}
}

func TestStripNoColor(t *testing.T) {
	in := []string{
		"PATH=/bin",
		"NO_COLOR=1",
		"COLORTERM=truecolor",
		"no_color=yes",
	}
	got := stripNoColor(in)
	for _, e := range got {
		if len(e) >= 8 && (e[:8] == "NO_COLOR" || e[:8] == "no_color") {
			t.Fatalf("NO_COLOR leaked: %v", got)
		}
	}
	if len(got) != 2 {
		t.Fatalf("got %v", got)
	}
}
