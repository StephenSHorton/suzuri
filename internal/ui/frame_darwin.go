//go:build darwin

package ui

/*
#cgo LDFLAGS: -framework AppKit -framework Foundation
void suzuri_round_main(void);
void suzuri_screen_cursor(int *x, int *y);
void suzuri_set_title_hits(int titleH, const int *rects, int n);
*/
import "C"
import "unsafe"

import "github.com/hajimehoshi/ebiten/v2"

func roundMainWindow() { C.suzuri_round_main() }

func setTitleHits(titleH int, rects []int) {
	if len(rects) == 0 {
		C.suzuri_set_title_hits(C.int(titleH), nil, 0)
		return
	}
	C.suzuri_set_title_hits(C.int(titleH), (*C.int)(unsafe.Pointer(&rects[0])), C.int(len(rects)/4))
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
