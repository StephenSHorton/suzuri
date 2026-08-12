//go:build windows

package appmeta

import "testing"

func TestIsPackaged_UnpackagedDevBinary(t *testing.T) {
	// Unit tests and local `go test` always run outside an MSIX identity.
	if IsPackaged() {
		t.Fatalf("expected unpackaged test process, got package %q", PackageFullName())
	}
	if PackageFullName() != "" {
		t.Fatalf("expected empty package name, got %q", PackageFullName())
	}
}
