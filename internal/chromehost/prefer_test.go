package chromehost

import (
	"os"
	"testing"
)

func TestPreferChromeUIClassic(t *testing.T) {
	t.Setenv(EnvUI, "classic")
	if PreferChromeUI() {
		t.Fatal("classic should not prefer chrome")
	}
	t.Setenv(EnvUI, "ebiten")
	if PreferChromeUI() {
		t.Fatal("ebiten should not prefer chrome")
	}
}

func TestPreferChromeUIExplicit(t *testing.T) {
	t.Setenv(EnvUI, "chrome")
	// May still be true even if binary missing — explicit request.
	// PreferChromeUI only checks Resolve for empty env; chrome forces true.
	if !PreferChromeUI() {
		t.Fatal("SUZURI_UI=chrome should prefer chrome")
	}
	t.Setenv(EnvUI, "native")
	if !PreferChromeUI() {
		t.Fatal("SUZURI_UI=native should prefer chrome")
	}
}

func TestPreferChromeUIEmptyIsClassic(t *testing.T) {
	t.Setenv(EnvUI, "")
	if PreferChromeUI() {
		t.Fatal("empty SUZURI_UI should default to classic on this branch")
	}
	_ = SiblingChromeAvailable()
	_ = os.Getenv(EnvUI)
}
