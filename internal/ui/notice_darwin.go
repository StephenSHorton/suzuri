//go:build darwin

package ui

/*
#cgo LDFLAGS: -framework AppKit -framework Foundation
#include <stdlib.h>

void suzuri_notice_present(const void *pix, int stride, int width, int height);
void suzuri_notice_hide(void);
void suzuri_play_wav(const void *bytes, int len);
void suzuri_play_wav_sync(const void *bytes, int len);
void suzuri_pump(double seconds);
void suzuri_focus_host(void);
*/
import "C"
import (
	"time"
	"unsafe"
)

func presentNoticeImage(pix []byte, stride, width, height int) {
	if width < 1 || height < 1 || len(pix) == 0 {
		C.suzuri_notice_hide()
		return
	}
	C.suzuri_notice_present(unsafe.Pointer(&pix[0]), C.int(stride), C.int(width), C.int(height))
}

func playWAV(b []byte) {
	if len(b) == 0 {
		return
	}
	C.suzuri_play_wav(unsafe.Pointer(&b[0]), C.int(len(b)))
}

func playWAVSync(b []byte) {
	if len(b) == 0 {
		return
	}
	C.suzuri_play_wav_sync(unsafe.Pointer(&b[0]), C.int(len(b)))
}

func focusNoticeHost() { C.suzuri_focus_host() }

func pumpUI(d time.Duration) {
	if d <= 0 {
		return
	}
	C.suzuri_pump(C.double(d.Seconds()))
}

//export suzuriNoticeMouse
func suzuriNoticeMouse(x, y, kind C.int) {
	onNoticeMouse(int(x), int(y), int(kind))
}
