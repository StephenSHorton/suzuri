package crashcapture

import (
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/log"
)

// DevCrashEnv names the dev-only crash trigger: SUZURI_DEV_CRASH=av makes a
// dev build die with a native access violation a few seconds after start
// (SUZURI_DEV_CRASH_AFTER=<seconds>, default 5). Release builds ignore it.
const DevCrashEnv = "SUZURI_DEV_CRASH"

// ArmDevCrash starts the dev-only crash timer when version is "dev" and
// SUZURI_DEV_CRASH=av. It returns whether it armed.
func ArmDevCrash(version string) bool {
	if version != "dev" || strings.ToLower(strings.TrimSpace(os.Getenv(DevCrashEnv))) != "av" {
		return false
	}
	after := 5 * time.Second
	if v := os.Getenv(DevCrashEnv + "_AFTER"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			after = time.Duration(n) * time.Second
		}
	}
	log.Warn("dev crash armed: native access violation", "after", after, "pid", os.Getpid())
	go func() {
		time.Sleep(after)
		log.Warn("dev crash: raising native access violation now", "pid", os.Getpid())
		nativeAccessViolation()
	}()
	return true
}
