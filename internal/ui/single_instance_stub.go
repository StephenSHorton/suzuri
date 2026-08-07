//go:build !windows

package ui

// EnsureSingleInstance is a no-op off Windows (macOS multi-window is fine).
func EnsureSingleInstance() bool { return true }
