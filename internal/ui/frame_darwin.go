//go:build darwin

package ui

/*
#cgo LDFLAGS: -framework AppKit -framework Foundation
void suzuri_round_main(void);
void suzuri_screen_cursor(int *x, int *y);
*/
import "C"

import "github.com/hajimehoshi/ebiten/v2"

func roundMainWindow() { C.suzuri_round_main() }

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
