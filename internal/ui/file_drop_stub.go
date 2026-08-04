//go:build !windows

package ui

// Non-Windows: ebiten delivers DroppedFiles always; we only handle them when
// chrome.AcceptsFileDrop(). No OS accept toggle required.
