//go:build !windows

package update

// Check is unavailable off Windows.
func (s *StoreService) Check() (*Info, error) {
	return nil, errStoreUnavailable
}

// DownloadAndApply is unavailable off Windows.
func (s *StoreService) DownloadAndApply(Info) error {
	return errStoreUnavailable
}
