//go:build windows || darwin

package ui

import (
	"strings"
	"testing"
)

func TestGlassAllowDWMRejectsNestedAndNCActivate(t *testing.T) {
	if ok, why := glassAllowDWM(2, wmActivate, true, false, false); ok || why != "nested-wndproc" {
		t.Fatalf("nested force apply: ok=%v why=%s", ok, why)
	}
	if ok, why := glassAllowDWM(1, wmNCActivate, true, false, false); ok || why != "ncactivate" {
		t.Fatalf("NCACTIVATE must never touch DWM: ok=%v why=%s", ok, why)
	}
	if ok, why := glassAllowDWM(1, wmActivate, true, false, true); ok || why != "sizemove" {
		t.Fatalf("size/move must never touch DWM: ok=%v why=%s", ok, why)
	}
	if ok, why := glassAllowDWM(1, wmActivate, false, true, false); ok || why != "unchanged" {
		t.Fatalf("unchanged skip: ok=%v why=%s", ok, why)
	}
	if ok, why := glassAllowDWM(1, wmActivate, true, true, false); !ok || why != "apply" {
		t.Fatalf("clean activate may force-refresh: ok=%v why=%s", ok, why)
	}
	if ok, why := glassAllowDWM(1, 0, false, false, false); !ok || why != "apply" {
		t.Fatalf("first apply: ok=%v why=%s", ok, why)
	}
}

func TestGlassActivatePolicyDefersClickAndDrag(t *testing.T) {
	if post, def := glassActivatePolicy(waClickActive, false); post || !def {
		t.Fatalf("CLICKACTIVE must defer (impending title-bar drag): post=%v defer=%v", post, def)
	}
	if post, def := glassActivatePolicy(waActive, true); post || !def {
		t.Fatalf("size/move must defer: post=%v defer=%v", post, def)
	}
	if post, def := glassActivatePolicy(waClickActive, true); post || !def {
		t.Fatalf("CLICKACTIVE during drag must defer: post=%v defer=%v", post, def)
	}
	if post, def := glassActivatePolicy(waActive, false); !post || def {
		t.Fatalf("keyboard/alt-tab activate may post: post=%v defer=%v", post, def)
	}
	if post, def := glassActivatePolicy(waInactive, false); !post || def {
		t.Fatalf("deactivate may post (unfocused glass): post=%v defer=%v", post, def)
	}
	if glassMayPostRefresh(true) {
		t.Fatal("must not PostMessage glass refresh during size/move")
	}
	if !glassMayPostRefresh(false) {
		t.Fatal("after EXITSIZEMOVE, a posted refresh is safe")
	}
}

func TestWndProcShouldAbort(t *testing.T) {
	if wndProcShouldAbort(1) || wndProcShouldAbort(maxWndProcDepth) {
		t.Fatal("depth at the cap must still run DefWindowProc, not abort early")
	}
	if !wndProcShouldAbort(maxWndProcDepth + 1) {
		t.Fatal("past the cap must abort so the native stack cannot overflow")
	}
}

func TestFatalWinException(t *testing.T) {
	for _, code := range []uint32{0xC0000005, 0xC00000FD, 0xC0000374, 0xC0000409} {
		if !fatalWinException(code) {
			t.Fatalf("%#x should be fatal", code)
		}
	}
	for _, code := range []uint32{0x40010006, 0x406D1388, 0x80000003, 0xE06D7363} {
		if fatalWinException(code) {
			t.Fatalf("%#x is runtime/debug noise, not a host death", code)
		}
	}
	got := string(formatNativeException(0xC00000FD))
	if got != "native-exception code=0xC00000FD\n" {
		t.Fatalf("format %q", got)
	}
	detail := string(formatExceptionDetail(0xC0000005, 0x7FF123456789ABCD, "suzuri.exe"))
	if !strings.Contains(detail, "code=0xC0000005") ||
		!strings.Contains(detail, "addr=0x7FF123456789ABCD") ||
		!strings.Contains(detail, "module=suzuri.exe") {
		t.Fatalf("detail %q", detail)
	}
	av := string(formatAVInfo(0, 0x1234))
	if !strings.Contains(av, "av=read") || !strings.Contains(av, "fault=0x") {
		t.Fatalf("av %q", av)
	}
	ns := string(formatNativeFrames([]uintptr{0x1000, 0x2000}))
	if !strings.Contains(ns, "nstack=0x") || !strings.Contains(ns, "0000000000002000") {
		t.Fatalf("nstack %q", ns)
	}
}
