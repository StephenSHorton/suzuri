//go:build windows

package ui

import (
	"testing"

	"github.com/lxn/win"

	"github.com/StephenSHorton/suzuri/internal/chrome"
)

func TestTitleStripHeightFormula(t *testing.T) {
	if got := titleStripHeightPx(18); got != 18+18/2 {
		t.Fatalf("titleStripHeightPx(18)=%d want %d", got, 18+18/2)
	}
	if got := titleStripHeightPx(20); got != 30 {
		t.Fatalf("titleStripHeightPx(20)=%d want 30", got)
	}
	if got := titleStripHeightPx(0); got != cellH+cellH/2 {
		t.Fatalf("fallback strip=%d want %d", got, cellH+cellH/2)
	}
	u := &winUI{metricH: 20}
	u.chrome.Frame = chrome.FrameWindows
	if got := u.chromePixelHeight(); got != 30 {
		t.Fatalf("windows chromePixelHeight=%d want 30", got)
	}
	u.chrome.Frame = chrome.FrameNative
	if got := u.chromePixelHeight(); got != 20 {
		t.Fatalf("native chromePixelHeight=%d want 20", got)
	}
}

func TestWinCaptionButtonsInsideStrip(t *testing.T) {
	const clientW, strip int32 = 800, 27
	btns := winCaptionButtons(clientW, strip)
	if btns[0].empty() || btns[1].empty() || btns[2].empty() {
		t.Fatalf("empty buttons %+v", btns)
	}
	if btns[2].R != clientW {
		t.Fatalf("close not flush right: %+v", btns[2])
	}
	if btns[0].L != clientW-3*captionButtonW {
		t.Fatalf("min origin=%d want %d", btns[0].L, clientW-3*captionButtonW)
	}
	for i, b := range btns {
		if b.T != 0 || b.B != strip {
			t.Fatalf("button %d not inside strip: %+v", i, b)
		}
		if b.R-b.L != captionButtonW {
			t.Fatalf("button %d width %d", i, b.R-b.L)
		}
		if b.L < 0 || b.R > clientW {
			t.Fatalf("button %d outside client: %+v", i, b)
		}
		if i > 0 && b.L != btns[i-1].R {
			t.Fatalf("button %d not flush to previous: %+v %+v", i, btns[i-1], b)
		}
	}
}

func TestTitleHitCornersAndButtons(t *testing.T) {
	if hitTopLeft != int(win.HTTOPLEFT) || hitTopRight != int(win.HTTOPRIGHT) ||
		hitBottomLeft != int(win.HTBOTTOMLEFT) || hitBottomRight != int(win.HTBOTTOMRIGHT) ||
		hitCaption != int(win.HTCAPTION) || hitClient != int(win.HTCLIENT) {
		t.Fatal("hit codes drifted from Win32")
	}
	const w, h, strip int32 = 800, 600, 27
	base := titleHitQuery{ClientW: w, ClientH: h, StripH: strip}
	corners := []struct {
		x, y int32
		want int
	}{
		{1, 1, hitTopLeft},
		{w - 2, 1, hitTopRight},
		{1, h - 2, hitBottomLeft},
		{w - 2, h - 2, hitBottomRight},
	}
	for _, c := range corners {
		q := base
		q.X, q.Y = c.x, c.y
		if got := hitTestTitleBar(q); got != c.want {
			t.Fatalf("corner (%d,%d)=%d want %d", c.x, c.y, got, c.want)
		}
	}
	btns := winCaptionButtons(w, strip)
	q := base
	q.Buttons = btns
	// Close button owns the top-right corner of the strip, including the resize band.
	q.X, q.Y = w-2, 1
	if got := hitTestTitleBar(q); got != hitClient {
		t.Fatalf("close top pixel=%d want HTCLIENT", got)
	}
	q.X, q.Y = btns[2].L+4, strip/2
	if got := hitTestTitleBar(q); got != hitClient {
		t.Fatalf("close center=%d want HTCLIENT", got)
	}
	if got := hitTestTitleBar(q); got == hitCaption {
		t.Fatal("button pixel mapped to HTCAPTION")
	}
	q.X, q.Y = btns[0].L+2, strip-2
	if got := hitTestTitleBar(q); got != hitClient {
		t.Fatalf("min button=%d want HTCLIENT", got)
	}
	// Empty strip (not brand, not a control) drags.
	q.X, q.Y = 200, 10
	q.Controls = nil
	if got := hitTestTitleBar(q); got != hitCaption {
		t.Fatalf("empty strip=%d want HTCAPTION", got)
	}
	// Tab / cup pixels are client, and still not caption.
	q.Controls = []pixRect{{L: 100, T: 0, R: 160, B: strip}}
	q.X, q.Y = 120, 12
	if got := hitTestTitleBar(q); got != hitClient {
		t.Fatalf("control=%d want HTCLIENT", got)
	}
	// Top resize band still wins over a tab, but not over a caption button.
	q.Y = 2
	if got := hitTestTitleBar(q); got != hitTop {
		t.Fatalf("tab in top band=%d want HTTOP", got)
	}
	q.X, q.Y = w-3, 2
	if got := hitTestTitleBar(q); got != hitClient {
		t.Fatalf("button in top band=%d want HTCLIENT", got)
	}
}
