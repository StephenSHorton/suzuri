package update

// Updater is the host-side update backend. GitHub Releases (*Service) and
// Microsoft Store (*StoreService) both implement it.
type Updater interface {
	Current() string
	Check() (*Info, error)
	DownloadAndApply(Info) error
}

var (
	_ Updater = (*Service)(nil)
	_ Updater = (*StoreService)(nil)
)
