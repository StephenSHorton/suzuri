package ui

import (
	"fmt"
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

// runUpdateCheck is the shared manual update path (palette).
// toast must be safe from a background goroutine (e.g. winUI.postToast).
func runUpdateCheck(toast func(string)) {
	s := getUpdater()
	if s == nil {
		if toast != nil {
			toast("updates unavailable")
		}
		return
	}
	if toast != nil {
		toast("checking for updates…")
	}
	go func() {
		info, err := s.Check()
		if err != nil {
			log.Warn("manual update check failed", "err", err)
			if toast != nil {
				toast("update check failed")
			}
			return
		}
		if info == nil {
			log.Info("update: up to date", "version", s.Current())
			if toast != nil {
				toast(fmt.Sprintf("up to date (v%s)", s.Current()))
			}
			return
		}
		log.Info("update: applying", "version", info.Version)
		if toast != nil {
			toast(fmt.Sprintf("installing v%s…", info.Version))
		}
		if err := s.DownloadAndApply(*info); err != nil {
			log.Warn("update apply failed", "err", err)
			if toast != nil {
				toast("update install failed")
			}
		}
	}()
}
