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
void suzuri_app_init(void);
int suzuri_is_main_thread(void);
void suzuri_focus_host(void);
void suzuri_notice_take_click(int *kind, int *x, int *y);
int suzuri_notice_hover(void);
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

func initNoticeApp() { C.suzuri_app_init() }

func onMainThread() bool { return C.suzuri_is_main_thread() == 1 }

func pumpUI(d time.Duration) {
	if d <= 0 {
		return
	}
	C.suzuri_pump(C.double(d.Seconds()))
	pollNoticeInput()
}

func pollNoticeInput() {
	var kind, x, y C.int
	C.suzuri_notice_take_click(&kind, &x, &y)
	noticeMu.Lock()
	noticeHover = C.suzuri_notice_hover() != 0
	noticeMu.Unlock()
	switch int(kind) {
	case 1, 3:
		onNoticeMouse(int(x), int(y), int(kind))
	}
}
