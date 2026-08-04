//go:build !darwin

package update

// resignMacExecutable is a no-op off macOS.
func resignMacExecutable(exePath string) {}
