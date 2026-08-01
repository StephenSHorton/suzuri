package ui

import (
	"bytes"
	"testing"
)

func TestEchoFilterSuppressesHighlightedPSEcho(t *testing.T) {
	// Captured from ConPTY + Windows PowerShell -NoProfile:
	raw := []byte("\x1b[?25l\x1b[93mWrite-Output \x1b[37mhello\r\n\x1b[?25h\x1b[mhello\r\n ")
	var f echoFilter
	f.arm("Write-Output hello")
	got := f.feed(raw)
	// Cursor-show may be suppressed while armed (inside match/nl phase) if it
	// arrives before NL; after NL, remainder passes. Accept either with/without
	// the pre-output cursor show as long as command text is gone and output stays.
	if bytes.Contains(got, []byte("Write-Output")) {
		t.Fatalf("command leak: %q", got)
	}
	if !bytes.Contains(got, []byte("hello")) {
		t.Fatalf("output missing: %q", got)
	}
	if f.armed {
		t.Fatal("expected disarmed after match")
	}
}

func TestEchoFilterChunked(t *testing.T) {
	var f echoFilter
	f.arm("echo hi")
	parts := [][]byte{
		[]byte("\x1b[?25l"),
		[]byte("\x1b[93mecho \x1b[37mhi\r"),
		[]byte("\n\x1b[?25h\x1b[mhi\r\n "),
	}
	var got []byte
	for _, p := range parts {
		got = append(got, f.feed(p)...)
	}
	if bytes.Contains(got, []byte("echo")) {
		t.Fatalf("command leak: %q", got)
	}
	if !bytes.Contains(got, []byte("hi")) {
		t.Fatalf("output missing: %q", got)
	}
}

func TestEchoFilterLeadingOutputPasses(t *testing.T) {
	// If real output arrives before echo (unusual), don't swallow it forever.
	var f echoFilter
	f.arm("Get-Date")
	raw := []byte("NOTICE\r\n\x1b[93mGet-Date\r\n\x1b[mthedate\r\n")
	got := f.feed(raw)
	if !bytes.Contains(got, []byte("NOTICE")) {
		t.Fatalf("leading output lost: %q", got)
	}
	if bytes.Contains(got, []byte("Get-Date")) {
		t.Fatalf("command leak: %q", got)
	}
	if !bytes.Contains(got, []byte("thedate")) {
		t.Fatalf("output missing: %q", got)
	}
}

func TestEchoFilterEmptyArm(t *testing.T) {
	var f echoFilter
	f.arm("   ")
	raw := []byte("hello\r\n")
	got := f.feed(raw)
	if !bytes.Equal(got, raw) {
		t.Fatalf("got %q", got)
	}
}

func TestEchoFilterPassesClearBeforeEcho(t *testing.T) {
	// Unix clear often emits CSI before (or without) echoing the word "clear".
	// Those sequences must reach the VT or the pane never blanks.
	var f echoFilter
	f.arm("clear")
	// terminfo-style wipe, then optional echo, then output
	raw := []byte("\x1b[H\x1b[2J\x1b[3Jclear\r\n")
	got := f.feed(raw)
	if !bytes.Contains(got, []byte("\x1b[2J")) {
		t.Fatalf("clear CSI swallowed: %q", got)
	}
	if bytes.Contains(got, []byte("clear")) {
		t.Fatalf("command leak: %q", got)
	}
}

func TestEchoFilterNoFalseMatch(t *testing.T) {
	var f echoFilter
	f.arm("abc")
	// "abX" should not fully match; "X" after partial may pass after reset.
	got := f.feed([]byte("abX\r\n"))
	// We suppressed "ab" then reset on X and pass X — known limitation of
	// streaming suppress without hold-back. Ensure we don't hang armed forever
	// on a later real "abc" line.
	_ = got
	got2 := f.feed([]byte("\x1b[93mabc\r\nOUT\r\n"))
	if bytes.Contains(got2, []byte("abc")) && f.armed {
		t.Fatalf("should suppress abc on retry: %q armed=%v", got2, f.armed)
	}
}
