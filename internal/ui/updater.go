package ui

import (
	"fmt"
	"sync"
	"sync/atomic"
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

	// One in-flight check per process (startup + palette share this).
	checkRunning atomic.Bool
	// Startup schedule only once even if loop()/create is re-entered.
	startupCheckOnce sync.Once
	// Session guards so we don't spam the same offer.
	offerMu        sync.Mutex
	offeredVersion string // last version we opened a confirm for
	laterVersion   string // user chose Later — don't re-offer this session
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

func peekPendingUpdate() *update.Info {
	pendingMu.Lock()
	defer pendingMu.Unlock()
	return pendingUpdate
}

// markUpdateLater records that the user dismissed an offer (Later / Esc).
func markUpdateLater() {
	pendingMu.Lock()
	var ver string
	if pendingUpdate != nil {
		ver = pendingUpdate.Version
	}
	pendingUpdate = nil
	pendingMu.Unlock()
	if ver == "" {
		return
	}
	offerMu.Lock()
	laterVersion = ver
	offerMu.Unlock()
	log.Info("update: user chose later", "version", ver)
}

// updateCheckHooks are UI callbacks (must be goroutine-safe where noted).
type updateCheckHooks struct {
	// toast posts a status line (UI-thread safe, e.g. postToast).
	toast func(string)
	// offerUpdate opens the confirm modal (must run on UI thread or post to it).
	offerUpdate func(version string)
	// quiet: if true, skip "checking…" toast (unused; startup always announces).
	quiet bool
}

// runUpdateCheck queries GitHub Releases. Never installs without offerUpdate → confirm.
// Concurrent calls are coalesced (one network check at a time).
func runUpdateCheck(h updateCheckHooks) {
	s := getUpdater()
	if s == nil {
		if h.toast != nil {
			h.toast("updates unavailable")
		}
		return
	}
	if !checkRunning.CompareAndSwap(false, true) {
		// Already checking / just finished race — don't spam another "checking…".
		log.Debug("update: check already in flight")
		return
	}
	if h.toast != nil && !h.quiet {
		h.toast("checking for updates…")
	}
	go func() {
		defer checkRunning.Store(false)

		// Dev builds never have a newer release.
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

		offerMu.Lock()
		later := laterVersion == info.Version
		already := offeredVersion == info.Version
		offerMu.Unlock()
		if later {
			log.Info("update: available but deferred (Later)", "version", info.Version)
			if h.toast != nil {
				h.toast(fmt.Sprintf("v%s available (deferred)", info.Version))
			}
			// Keep pending so palette check can re-offer if we clear later? User said Later — don't re-modal.
			return
		}
		if already && peekPendingUpdate() != nil {
			// Confirm already shown once this session for this version.
			log.Debug("update: offer already shown", "version", info.Version)
			if h.toast != nil {
				h.toast(fmt.Sprintf("v%s available", info.Version))
			}
			return
		}

		log.Info("update: available (awaiting confirm)", "version", info.Version)
		cp := *info
		setPendingUpdate(&cp)
		offerMu.Lock()
		offeredVersion = info.Version
		offerMu.Unlock()
		// Single toast + single modal (don't also toast "vX available" separately).
		if h.toast != nil {
			h.toast(fmt.Sprintf("update available: v%s", info.Version))
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
	offerMu.Lock()
	offeredVersion = ""
	laterVersion = ""
	offerMu.Unlock()
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
			setPendingUpdate(info)
			offerMu.Lock()
			offeredVersion = info.Version
			offerMu.Unlock()
		}
	}()
}

// scheduleStartupUpdateCheck runs at most once per process after the window is up.
func scheduleStartupUpdateCheck(toast func(string), offerUpdate func(version string)) {
	startupCheckOnce.Do(func() {
		go func() {
			// Let the first paint settle so the toast is visible.
			time.Sleep(900 * time.Millisecond)
			runUpdateCheck(updateCheckHooks{toast: toast, offerUpdate: offerUpdate})
		}()
	})
}
