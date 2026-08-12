package update

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// StoreProductID is the public Microsoft Store listing id for suzuri.
const StoreProductID = "9PJ735V6JKN3"

// errStoreUnavailable is returned when Store APIs cannot run (wrong OS,
// unpackaged process, or the Store service refused the call).
var errStoreUnavailable = errors.New("microsoft store updates require a packaged Windows install")

// ErrStoreCanceled means the user dismissed the Store update UI.
var ErrStoreCanceled = errors.New("store update canceled")

// StoreService checks and applies Microsoft Store package updates.
// Packaged installs must not use GitHub self-update: the install dir is
// immutable and the Store owns the signature.
type StoreService struct {
	current      string
	ownerPID     int
	OnApplyBegin func()
}

// NewStore returns a Store-backed updater for the running product version
// (e.g. "0.9.116"). On non-Windows, Check/DownloadAndApply always error.
func NewStore(current string) *StoreService {
	return &StoreService{current: strings.TrimPrefix(current, "v")}
}

// Current returns the running product version.
func (s *StoreService) Current() string { return s.current }

// SetOwnerPID records the UI process whose window should own Store dialogs.
func (s *StoreService) SetOwnerPID(pid int) { s.ownerPID = pid }

// DisplayVersion maps an MSIX identity version to the product tag users see.
// The Store forbids major 0, so packaging ships 0.x.y as 1.x.y.0.
func DisplayVersion(major, minor, build, revision uint16) string {
	if major == 1 && revision == 0 {
		return fmt.Sprintf("0.%d.%d", minor, build)
	}
	if revision == 0 {
		return fmt.Sprintf("%d.%d.%d", major, minor, build)
	}
	return fmt.Sprintf("%d.%d.%d.%d", major, minor, build, revision)
}

// ParseMSIXVersion parses "1.9.116.0" / "0.9.116" into identity parts.
func ParseMSIXVersion(s string) (major, minor, build, revision uint16, ok bool) {
	s = strings.TrimPrefix(strings.TrimSpace(s), "v")
	parts := strings.Split(s, ".")
	if len(parts) < 3 || len(parts) > 4 {
		return 0, 0, 0, 0, false
	}
	nums := make([]uint16, 4)
	for i, p := range parts {
		n, err := strconv.ParseUint(p, 10, 16)
		if err != nil {
			return 0, 0, 0, 0, false
		}
		nums[i] = uint16(n)
	}
	return nums[0], nums[1], nums[2], nums[3], true
}
