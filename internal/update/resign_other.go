//go:build !darwin

package update

// resignMacExecutable is a no-op off macOS.
func resignMacExecutable(exePath string) {}

// patchAppInfoPlist is a no-op off macOS (darwin-only Info.plist heal).
func patchAppInfoPlist(exePath, version string) {}
