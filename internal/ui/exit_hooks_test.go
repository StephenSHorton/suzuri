//go:build windows || darwin

package ui

import "testing"

func TestGlassAllowDWMRejectsNestedAndNCActivate(t *testing.T) {
	if ok, why := glassAllowDWM(2, wmActivate, true, false); ok || why != "nested-wndproc" {
		t.Fatalf("nested force apply: ok=%v why=%s", ok, why)
	}
	if ok, why := glassAllowDWM(1, wmNCActivate, true, false); ok || why != "ncactivate" {
		t.Fatalf("NCACTIVATE must never touch DWM: ok=%v why=%s", ok, why)
	}
	if ok, why := glassAllowDWM(1, wmActivate, false, true); ok || why != "unchanged" {
		t.Fatalf("unchanged skip: ok=%v why=%s", ok, why)
	}
	if ok, why := glassAllowDWM(1, wmActivate, true, true); !ok || why != "apply" {
		t.Fatalf("clean activate may force-refresh: ok=%v why=%s", ok, why)
	}
	if ok, why := glassAllowDWM(1, 0, false, false); !ok || why != "apply" {
		t.Fatalf("first apply: ok=%v why=%s", ok, why)
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
}
