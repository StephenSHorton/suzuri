//go:build !windows

package appmeta

// IsPackaged is always false outside Windows.
func IsPackaged() bool { return false }

// PackageFullName is always empty outside Windows.
func PackageFullName() string { return "" }
