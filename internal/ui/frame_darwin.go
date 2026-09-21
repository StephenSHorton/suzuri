//go:build darwin

package ui

/*
#cgo LDFLAGS: -framework AppKit -framework Foundation
void suzuri_round_main(void);
void suzuri_screen_cursor(int *x, int *y);
void suzuri_set_title_hits(int titleH, const int *rects, int n);
*/
import "C"

import "github.com/hajimehoshi/ebiten/v2"

func roundMainWindow() { C.suzuri_round_main() }

func setTitleHits(titleH int, rects []int) {
	if len(rects) == 0 {
		C.suzuri_set_title_hits(C.int(titleH), nil, 0)
		return
	}
	// Go int is 8 bytes; the C side reads 4-byte ints. A raw pointer cast
	// scrambles every rectangle, so the whole strip looks empty and drags.
	buf := make([]C.int, len(rects))
	for i, v := range rects {
		buf[i] = C.int(v)
	}
	C.suzuri_set_title_hits(C.int(titleH), &buf[0], C.int(len(buf)/4))
}

func toggleFrameZoom() {
	if ebiten.IsWindowMaximized() {
		ebiten.RestoreWindow()
		return
	}
	ebiten.MaximizeWindow()
}

func screenCursor() (x, y int) {
	var sx, sy C.int
	C.suzuri_screen_cursor(&sx, &sy)
	return int(sx), int(sy)
}
