//go:build !windows

package winconsole

// AttachParent is a no-op off Windows.
func AttachParent() {}
