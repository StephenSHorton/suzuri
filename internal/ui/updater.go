package ui

import (
	"sync"

	"github.com/charmbracelet/log"

	"github.com/StephenSHorton/suzuri/internal/update"
)

var (
	updaterMu sync.Mutex
	updater   *update.Service
)

// SetUpdater wires the GitHub Releases updater (called from main after Init).
func SetUpdater(s *update.Service) {
	updaterMu.Lock()
	updater = s
	updaterMu.Unlock()
}

func getUpdater() *update.Service {
	updaterMu.Lock()
	defer updaterMu.Unlock()
	return updater
}

// checkForUpdates runs a manual update check (palette / settings).
// Toasts progress on the active UI when possible.
func (u *winUI) checkForUpdates() {
	s := getUpdater()
	if s == nil {
		u.toast("updates unavailable")
		return
	}
	u.toast("checking for updates…")
	go func() {
		info, err := s.Check()
		if err != nil {
			log.Warn("manual update check failed", "err", err)
			// Best-effort toast from UI thread via PostMessage would be ideal;
			// log is enough if window is busy.
			return
		}
		if info == nil {
			log.Info("update: up to date", "version", s.Current())
			return
		}
		log.Info("update: applying", "version", info.Version)
		if err := s.DownloadAndApply(*info); err != nil {
			log.Warn("update apply failed", "err", err)
		}
	}()
}
