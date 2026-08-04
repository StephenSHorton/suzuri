//go:build windows || darwin

package ui

import (
	"strings"
	"testing"
)

func TestBracketedPaste(t *testing.T) {
	got := string(bracketedPaste("/tmp/x.png"))
	if !strings.HasPrefix(got, "\x1b[200~") || !strings.HasSuffix(got, "\x1b[201~") {
		t.Fatalf("framing: %q", got)
	}
	if !strings.Contains(got, "/tmp/x.png") {
		t.Fatalf("path missing: %q", got)
	}
	// newlines → CR inside paste
	got = string(bracketedPaste("a\nb"))
	if !strings.Contains(got, "a\rb") {
		t.Fatalf("newline normalize: %q", got)
	}
	// CRLF → CR
	got = string(bracketedPaste("a\r\nb"))
	if !strings.Contains(got, "a\rb") {
		t.Fatalf("crlf normalize: %q", got)
	}
}

func TestBracketedPasteEmpty(t *testing.T) {
	got := string(bracketedPaste(""))
	if got != "\x1b[200~\x1b[201~" {
		t.Fatalf("empty: %q", got)
	}
}
