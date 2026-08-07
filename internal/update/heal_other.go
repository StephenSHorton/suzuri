//go:build !darwin

package update

// HealMacAppBundle is a no-op off macOS.
func HealMacAppBundle(version string) {}
