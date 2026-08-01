//go:build !windows && !darwin

package ui

import "errors"

// Run is only implemented on Windows and macOS.
func Run() error {
	return errors.New("suzuri UI supports Windows and macOS only")
}

// RegisterBundledFonts is a no-op off supported GUI platforms.
func RegisterBundledFonts() bool { return false }

// UnregisterBundledFonts is a no-op off supported GUI platforms.
func UnregisterBundledFonts() {}

// BundledFace is empty when no GUI host is built.
const BundledFace = ""
