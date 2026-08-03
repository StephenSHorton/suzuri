package chrome

import "strings"

// appVersion is the running build (set from main via -ldflags / SetAppVersion).
var appVersion = "dev"

// SetAppVersion records the binary version for chrome UI (settings, etc.).
func SetAppVersion(v string) {
	v = strings.TrimSpace(v)
	if v == "" {
		v = "dev"
	}
	appVersion = strings.TrimPrefix(v, "v")
}

// AppVersion returns the display version (no leading "v").
func AppVersion() string {
	if appVersion == "" {
		return "dev"
	}
	return appVersion
}
