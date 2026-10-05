//go:build !windows

package ui

// InstallProcessExitHooks is a no-op off Windows (AppKit / tests).
func InstallProcessExitHooks() {}
