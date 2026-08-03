//go:build windows || darwin

package ui

import (
	"bytes"
	"testing"
)

func TestEncodeEnterPlain(t *testing.T) {
	got := encodeEnter(nil, false, false, false, false)
	if !bytes.Equal(got, []byte{'\r'}) {
		t.Fatalf("plain enter %q", got)
	}
}

func TestEncodeEnterShiftAlwaysCSIU(t *testing.T) {
	// Shift+Enter must not collapse to bare CR (Grok multiline).
	got := encodeEnter(nil, true, false, false, false)
	if !bytes.Equal(got, []byte("\x1b[13;2u")) {
		t.Fatalf("shift+enter %q want CSI 13;2u", got)
	}
}

func TestEncodeEnterAltLegacyFallback(t *testing.T) {
	got := encodeEnter(nil, false, true, false, false)
	if !bytes.Equal(got, []byte{0x1b, '\r'}) {
		t.Fatalf("alt+enter legacy %q", got)
	}
}

func TestEncodeEnterCmdSuper(t *testing.T) {
	// macOS Cmd+Enter → super modifier (bit 8 → mods 9).
	got := encodeEnter(nil, false, false, false, true)
	if !bytes.Equal(got, []byte("\x1b[13;9u")) {
		t.Fatalf("cmd+enter %q want CSI 13;9u", got)
	}
}

func TestEncodeEnterWithKittyFlags(t *testing.T) {
	var kk kittyKeyboard
	kk.push(kittyDisambiguate)
	got := encodeEnter(&kk, true, false, false, false)
	if !bytes.Equal(got, []byte("\x1b[13;2u")) {
		t.Fatalf("active shift+enter %q", got)
	}
	// Alt+Enter also CSI-u once protocol is active.
	got = encodeEnter(&kk, false, true, false, false)
	if !bytes.Equal(got, []byte("\x1b[13;3u")) {
		t.Fatalf("active alt+enter %q", got)
	}
}

func TestKittyQueryAndPush(t *testing.T) {
	var kk kittyKeyboard
	// Probe sequence Grok uses: CSI ? u then DA1.
	reply := kk.consumeHostQueries([]byte("\x1b[?u\x1b[c"))
	if !bytes.Contains(reply, []byte("\x1b[?0u")) {
		t.Fatalf("query reply missing flags: %q", reply)
	}
	if !bytes.Contains(reply, []byte("\x1b[?62;")) {
		t.Fatalf("DA1 reply missing: %q", reply)
	}
	// Push disambiguate.
	_ = kk.consumeHostQueries([]byte("\x1b[>1u"))
	if !kk.active() {
		t.Fatalf("flags=%d want disambiguate active", kk.flags)
	}
	// Pop restores previous.
	_ = kk.consumeHostQueries([]byte("\x1b[<u"))
	if kk.flags != 0 {
		t.Fatalf("after pop flags=%d", kk.flags)
	}
}

func TestKittySetMode(t *testing.T) {
	var kk kittyKeyboard
	_ = kk.consumeHostQueries([]byte("\x1b[=1;1u"))
	if kk.flags != 1 {
		t.Fatalf("set flags=%d", kk.flags)
	}
	_ = kk.consumeHostQueries([]byte("\x1b[=2;2u")) // OR in event types
	if kk.flags != 3 {
		t.Fatalf("or flags=%d want 3", kk.flags)
	}
	_ = kk.consumeHostQueries([]byte("\x1b[=1;3u")) // clear disambiguate
	if kk.flags != 2 {
		t.Fatalf("clear flags=%d want 2", kk.flags)
	}
}
