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

// pendingPaste is an async alt-screen paste result drained on the UI thread.
type pendingPaste struct {
	payload      []byte
	toast        string
	preferSuperV bool // empty board: try Kitty Super+V first
	reclaimFocus bool // macOS: re-activate window after osascript paste
}

const maxPendingPaste = 8

func appendPendingPaste(dst []pendingPaste, p pendingPaste) []pendingPaste {
	dst = append(dst, p)
	if extra := len(dst) - maxPendingPaste; extra > 0 {
		dst = append([]pendingPaste(nil), dst[extra:]...)
	}
	return dst
}
