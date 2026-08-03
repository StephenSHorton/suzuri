package update

import (
	"runtime"
	"testing"
)

func TestPickReleaseAssetSkipsSetup(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("pickReleaseAsset selects by runtime.GOOS")
	}
	assets := []ghAsset{
		{Name: "SHA256SUMS", URL: "https://example/sums"},
		{Name: "suzuri-1.2.3-windows-amd64-setup.exe", URL: "https://example/setup"},
		{Name: "suzuri-1.2.3-windows-amd64.zip", URL: "https://example/zip"},
		{Name: "suzuri-1.2.3-windows-amd64.exe", URL: "https://example/exe"},
	}
	a, sums := pickReleaseAsset(assets)
	if sums != "https://example/sums" {
		t.Fatalf("sums=%q", sums)
	}
	if a.URL != "https://example/exe" {
		t.Fatalf("got %q want portable exe (not setup)", a.URL)
	}
	if a.Name != "suzuri-1.2.3-windows-amd64.exe" {
		t.Fatalf("name=%q", a.Name)
	}
}

func TestCmpSemver(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"1.0.0", "1.0.0", 0},
		{"1.0.1", "1.0.0", 1},
		{"1.0.0", "1.0.1", -1},
		{"1.2.0", "1.1.9", 1},
		{"2.0.0", "1.9.9", 1},
		{"1.0.0", "1.0.0-beta", 1},
		{"1.0.0-beta", "1.0.0", -1},
	}
	for _, tc := range cases {
		if got := cmpSemver(tc.a, tc.b); got != tc.want {
			t.Errorf("cmpSemver(%q,%q)=%d want %d", tc.a, tc.b, got, tc.want)
		}
	}
}

func TestIsNewer(t *testing.T) {
	if isNewer("v1.0.0", "dev") {
		t.Fatal("dev should not update")
	}
	if !isNewer("v1.1.0", "1.0.0") {
		t.Fatal("expected newer")
	}
	if isNewer("v1.0.0", "1.0.0") {
		t.Fatal("same version")
	}
}
