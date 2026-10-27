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

// A program reading the PTY (Claude Code, Ink TUIs) can take one read of
// 64+ bytes with a CR inside as typed text: the CR becomes a newline and the
// line is never submitted. Text and Enter must be separate writes, and the
// text bracketed when the program asked for paste mode.
func TestSubmitWritesToProgram(t *testing.T) {
	line := strings.Repeat("x", 70) + "\nmore"
	got := submitWrites(line, true, true)
	if len(got) != 2 || string(got[0]) != "\x1b[200~"+strings.Repeat("x", 70)+"\rmore\x1b[201~" || string(got[1]) != "\r" {
		t.Fatalf("bracketed program submit %q", got)
	}
	got = submitWrites("hello", true, false)
	if len(got) != 2 || string(got[0]) != "hello" || string(got[1]) != "\r" {
		t.Fatalf("unbracketed program submit %q", got)
	}
	// The shell's line editor keeps the single write (echo filter, history).
	got = submitWrites("ls\nl", false, true)
	if len(got) != 1 || string(got[0]) != "ls\rl\r" {
		t.Fatalf("shell submit %q", got)
	}
}

func TestBracketedPasteEmpty(t *testing.T) {
	got := string(bracketedPaste(""))
	if got != "\x1b[200~\x1b[201~" {
		t.Fatalf("empty: %q", got)
	}
}
