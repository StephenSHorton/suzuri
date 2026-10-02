//go:build windows

package ui

import (
	"testing"

	"github.com/StephenSHorton/suzuri/internal/config"
)

func TestNoticeScreenOriginMatchesAnchors(t *testing.T) {
	const left, top, right, bottom int32 = 10, 20, 1010, 820
	const pw, ph, inset int32 = 320, 80, 16
	cases := []struct {
		id   string
		x, y int32
	}{
		{config.NoticeBottomLeft, left + inset, bottom - ph - inset},
		{config.NoticeBottomRight, right - pw - inset, bottom - ph - inset},
		{config.NoticeTopLeft, left + inset, top + inset},
		{config.NoticeTopRight, right - pw - inset, top + inset},
		{config.NoticeCenter, left + (right-left-pw)/2, top + (bottom-top-ph)/2},
	}
	for _, c := range cases {
		x, y := noticeScreenOrigin(left, top, right, bottom, pw, ph, inset, int32(config.NoticePositionIndex(c.id)))
		if x != c.x || y != c.y {
			t.Fatalf("%s origin (%d,%d) want (%d,%d)", c.id, x, y, c.x, c.y)
		}
	}
}

func TestNoticeBitmapPointDoublesClient(t *testing.T) {
	x, y := noticeBitmapPoint(10, 12, 320, 144, 640, 288)
	if x != 20 || y != 24 {
		t.Fatalf("scaled (%d,%d) want (20,24)", x, y)
	}
}

func TestNoticePanelShowsAndHides(t *testing.T) {
	const w, h = 8, 8
	pix := make([]byte, w*h*4)
	for i := 0; i < len(pix); i += 4 {
		pix[i], pix[i+1], pix[i+2], pix[i+3] = 40, 180, 90, 220
	}
	presentNoticeImage(pix, w*4, w, h, 0)
	if !noticePanelUp() || noticeHwnd == 0 {
		t.Fatal("panel did not show")
	}
	presentNoticeImage(nil, 0, 0, 0, 0)
	if noticePanelUp() {
		t.Fatal("panel stayed up after an empty present")
	}
}

func TestPremulBGRASwapsAndScales(t *testing.T) {
	src := []byte{
		255, 0, 0, 255,
		0, 0, 255, 128,
	}
	got := premulBGRA(src, 8, 2, 1)
	if got[0] != 0 || got[1] != 0 || got[2] != 255 || got[3] != 255 {
		t.Fatalf("opaque red BGRA %v", got[:4])
	}
	// 255 * 128 / 255 = 128
	if got[4] != 128 || got[5] != 0 || got[6] != 0 || got[7] != 128 {
		t.Fatalf("half blue BGRA %v", got[4:8])
	}
}
