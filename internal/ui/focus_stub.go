//go:build !darwin

package ui

// reclaimWindowFocus is a no-op off macOS (Windows keeps focus on the HWND).
func reclaimWindowFocus() {}
