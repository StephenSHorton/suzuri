package chromehost

import "testing"

func TestPreferChromeUIAlwaysNative(t *testing.T) {
	// Product is native-only; classic/ebiten env values are ignored.
	for _, v := range []string{"", "classic", "ebiten", "legacy", "chrome", "native"} {
		t.Setenv(EnvUI, v)
		if !PreferChromeUI() {
			t.Fatalf("PreferChromeUI with SUZURI_UI=%q: want true", v)
		}
	}
}
