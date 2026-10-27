//go:build windows || darwin

package ui

import "strings"

// bracketedPaste frames text for terminals that enable DECSET 2004.
// Grok always has bracketed paste on and routes Event::Paste through the
// image / drop-path classifier.
func bracketedPaste(text string) []byte {
	// Normalize newlines the way host terminals do inside paste brackets.
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\n", "\r")
	return []byte("\x1b[200~" + text + "\x1b[201~")
}

// framePaste brackets text only when the app has enabled DECSET 2004.
func framePaste(text string, bracket bool) []byte {
	if bracket {
		return bracketedPaste(text)
	}
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\n", "\r")
	return []byte(text)
}

// submitWrites is the PTY writes for a submitted line (Warp bar / MCP).
//
// The shell gets one write: line + CR. A foreground program gets the text
// and Enter as two writes, the text bracketed when it enabled DECSET 2004.
// Claude Code's input reader keeps a CR inside a read of 64+ bytes as
// typed text, so "long line\r" in one write lands as a draft with a
// trailing newline and is never submitted.
func submitWrites(payload string, program, bracket bool) [][]byte {
	if !program {
		payload = strings.ReplaceAll(payload, "\n", "\r")
		return [][]byte{[]byte(payload + "\r")}
	}
	return [][]byte{framePaste(payload, bracket), {'\r'}}
}

// pendingPaste is an async alt-screen paste result drained on the UI thread.
type pendingPaste struct {
	payload      []byte
	toast        string
	preferSuperV bool // empty board: try Kitty Super+V first
	reclaimFocus bool // macOS: re-activate window after osascript paste
}
