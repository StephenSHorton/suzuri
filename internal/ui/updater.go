package ui

import (
	"fmt"
	"sync"
	"time"

	"github.com/charmbracelet/log"

	"github.com/StephenSHorton/suzuri/internal/chrome"
	"github.com/StephenSHorton/suzuri/internal/update"
)

var (
	updaterMu sync.Mutex
	updater   *update.Service

	pendingMu     sync.Mutex
	pendingUpdate *update.Info
)

// SetUpdater wires the GitHub Releases updater (called from main after Init).
func SetUpdater(s *update.Service) {
	updaterMu.Lock()
	updater = s
	updaterMu.Unlock()
}

// SetAppVersion records the build version for settings chrome and toasts.
func SetAppVersion(v string) {
	chrome.SetAppVersion(v)
}

func getUpdater() *update.Service {
	updaterMu.Lock()
	defer updaterMu.Unlock()
	return updater
}

func setPendingUpdate(info *update.Info) {
	pendingMu.Lock()
	pendingUpdate = info
	pendingMu.Unlock()
}

func takePendingUpdate() *update.Info {
	pendingMu.Lock()
	defer pendingMu.Unlock()
	info := pendingUpdate
	pendingUpdate = nil
	return info
}

// updateCheckHooks are UI callbacks (must be goroutine-safe where noted).
type updateCheckHooks struct {
	// toast posts a status line (UI-thread safe, e.g. postToast).
	toast func(string)
	// offerUpdate opens the confirm modal (must run on UI thread or post to it).
	offerUpdate func(version string)
}

// runUpdateCheck queries GitHub Releases. Never installs without offerUpdate → confirm.
// toast: "checking…" then result toasts. If a newer build exists, offerUpdate is
// called with the version string (host opens OpenConfirmUpdateMsg).
func runUpdateCheck(h updateCheckHooks) {
	s := getUpdater()
	if s == nil {
		if h.toast != nil {
			h.toast("updates unavailable")
		}
		return
	}
	if h.toast != nil {
		h.toast("checking for updates…")
	}
	go func() {
		// Dev builds never have a newer release — still toast so startup is clear.
		if s.Current() == "" || s.Current() == "dev" {
			log.Debug("update: skip check on dev build")
			if h.toast != nil {
				h.toast("dev build — auto-update off")
			}
			return
		}
		info, err := s.Check()
		if err != nil {
			log.Warn("update check failed", "err", err)
			if h.toast != nil {
				h.toast("update check failed")
			}
			return
		}
		if info == nil {
			log.Info("update: up to date", "version", s.Current())
			if h.toast != nil {
				h.toast(fmt.Sprintf("up to date (v%s)", s.Current()))
			}
			return
		}
		log.Info("update: available (awaiting confirm)", "version", info.Version)
		cp := *info
		setPendingUpdate(&cp)
		if h.toast != nil {
			h.toast(fmt.Sprintf("v%s available", info.Version))
		}
		if h.offerUpdate != nil {
			h.offerUpdate(info.Version)
		}
	}()
}

// applyPendingUpdate installs the update the user confirmed (UI thread starts go).
func applyPendingUpdate(toast func(string)) {
	s := getUpdater()
	info := takePendingUpdate()
	if s == nil || info == nil {
		if toast != nil {
			toast("no update to install")
		}
		return
	}
	if toast != nil {
		toast(fmt.Sprintf("installing v%s…", info.Version))
	}
	go func() {
		log.Info("update: applying after confirm", "version", info.Version)
		if err := s.DownloadAndApply(*info); err != nil {
			log.Warn("update apply failed", "err", err)
			if toast != nil {
				toast("update install failed")
			}
			// Put it back so they can try again from the palette.
			setPendingUpdate(info)
		}
	}()
}

// scheduleStartupUpdateCheck toasts "checking…" after the window is up, then
// offers a confirm modal if a newer release exists (never silent install).
func scheduleStartupUpdateCheck(toast func(string), offerUpdate func(version string)) {
	go func() {
		// Let the first paint + intro settle so the toast is visible.
		time.Sleep(900 * time.Millisecond)
		runUpdateCheck(updateCheckHooks{toast: toast, offerUpdate: offerUpdate})
	}()
}
