//go:build windows

package ui

import (
	"fmt"
	"os"
	"os/exec"
	"time"
)

func presentNoticeImage(pix []byte, stride, width, height int) {}

func playWAV(b []byte) {}

func playWAVSync(b []byte) {
	if len(b) == 0 {
		return
	}
	f, err := os.CreateTemp("", "suzuri-notice-*.wav")
	if err != nil {
		return
	}
	name := f.Name()
	_, _ = f.Write(b)
	_ = f.Close()
	defer os.Remove(name)
	_ = exec.Command("powershell", "-NoProfile", "-Command",
		fmt.Sprintf("(New-Object System.Media.SoundPlayer %q).PlaySync()", name)).Run()
}

func focusNoticeHost() {}

func pumpUI(d time.Duration) { time.Sleep(d) }
