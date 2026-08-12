package chromehost

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/charmbracelet/log"

	"github.com/StephenSHorton/suzuri/internal/config"
	"github.com/StephenSHorton/suzuri/internal/update"
)

// UpdateReqFile is chrome → host (`check` / `install` / `later`).
const UpdateReqFile = "update_req"

// UpdateEvtFile is host → chrome (`toast …` / `offer <version>`).
const UpdateEvtFile = "update_evt"

func updateReqPath() string { return filepath.Join(config.Dir(), UpdateReqFile) }
func updateEvtPath() string { return filepath.Join(config.Dir(), UpdateEvtFile) }

// applyGate lets RunCLI park after chrome exits because an update is replacing
// the host binary (DownloadAndApply calls os.Exit shortly after relaunch).
type applyGate struct {
	mu        sync.Mutex
	applying  bool
	finished  chan struct{}
	closeOnce sync.Once
}

func newApplyGate() *applyGate {
	return &applyGate{finished: make(chan struct{})}
}

func (g *applyGate) Begin() {
	g.mu.Lock()
	g.applying = true
	g.mu.Unlock()
}

func (g *applyGate) Applying() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.applying
}

func (g *applyGate) Finish() {
	g.closeOnce.Do(func() { close(g.finished) })
}

func (g *applyGate) Wait(d time.Duration) {
	select {
	case <-g.finished:
	case <-time.After(d):
	}
}

// updateBridge is the host half of the updater mailbox (classic
// internal/ui/updater.go). One in-flight check; confirm required before apply.
// svc is a GitHub *update.Service or a Store *update.StoreService.
type updateBridge struct {
	svc  update.Updater
	gate *applyGate

	pendingMu    sync.Mutex
	pending      *update.Info
	checkRunning atomic.Bool
	offerMu      sync.Mutex
	offered      string
	later        string
}

func newUpdateBridge(svc update.Updater, gate *applyGate) *updateBridge {
	return &updateBridge{svc: svc, gate: gate}
}

// RunUpdateBridge polls chrome's update_req mailbox, runs a startup check, and
// writes toast/offer events. stop is closed when the UI process exits without
// an in-flight apply.
func RunUpdateBridge(ctx context.Context, svc update.Updater, gate *applyGate, stop <-chan struct{}) {
	b := newUpdateBridge(svc, gate)
	_ = os.Remove(updateReqPath())
	_ = os.Remove(updateEvtPath())

	startup := time.NewTimer(900 * time.Millisecond)
	defer startup.Stop()
	tick := time.NewTicker(250 * time.Millisecond)
	defer tick.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-stop:
			return
		case <-startup.C:
			b.runCheck(false)
		case <-tick.C:
			b.drainReq()
		}
	}
}

func (b *updateBridge) drainReq() {
	path := updateReqPath()
	body, err := os.ReadFile(path)
	if err != nil || len(bytesTrim(body)) == 0 {
		return
	}
	_ = os.WriteFile(path, nil, 0o644)
	for _, line := range strings.Split(string(body), "\n") {
		switch strings.TrimSpace(line) {
		case "check":
			b.runCheck(false)
		case "install":
			b.applyPending()
		case "later":
			b.markLater()
		}
	}
}

func bytesTrim(b []byte) []byte {
	return []byte(strings.TrimSpace(string(b)))
}

func (b *updateBridge) runCheck(quiet bool) {
	if b.svc == nil {
		writeUpdateEvt("toast updates via Microsoft Store")
		return
	}
	if !b.checkRunning.CompareAndSwap(false, true) {
		log.Debug("update: check already in flight")
		return
	}
	if !quiet {
		writeUpdateEvt("toast checking for updates…")
	}
	go func() {
		defer b.checkRunning.Store(false)
		if b.svc.Current() == "" || b.svc.Current() == "dev" {
			log.Debug("update: skip check on dev build")
			writeUpdateEvt("toast dev build — auto-update off")
			return
		}
		info, err := b.svc.Check()
		if err != nil {
			log.Warn("update check failed", "err", err)
			writeUpdateEvt("toast update check failed")
			return
		}
		if info == nil {
			log.Info("update: up to date", "version", b.svc.Current())
			writeUpdateEvt(fmt.Sprintf("toast up to date (v%s)", b.svc.Current()))
			return
		}

		b.offerMu.Lock()
		later := b.later == info.Version
		already := b.offered == info.Version
		b.offerMu.Unlock()
		if later {
			log.Info("update: available but deferred (Later)", "version", info.Version)
			writeUpdateEvt(fmt.Sprintf("toast v%s available (deferred)", info.Version))
			return
		}
		if already && b.peekPending() != nil {
			writeUpdateEvt(fmt.Sprintf("toast v%s available", info.Version))
			return
		}

		log.Info("update: available (awaiting confirm)", "version", info.Version)
		cp := *info
		b.setPending(&cp)
		b.offerMu.Lock()
		b.offered = info.Version
		b.offerMu.Unlock()
		writeUpdateEvt(fmt.Sprintf("toast update available: v%s\noffer %s", info.Version, info.Version))
	}()
}

func (b *updateBridge) applyPending() {
	info := b.takePending()
	if b.svc == nil || info == nil {
		writeUpdateEvt("toast no update to install")
		return
	}
	b.offerMu.Lock()
	b.offered = ""
	b.later = ""
	b.offerMu.Unlock()
	writeUpdateEvt(fmt.Sprintf("toast installing v%s…", info.Version))
	go func() {
		log.Info("update: applying after confirm", "version", info.Version)
		if err := b.svc.DownloadAndApply(*info); err != nil {
			log.Warn("update apply failed", "err", err)
			if errors.Is(err, update.ErrStoreCanceled) {
				writeUpdateEvt("toast update canceled")
			} else {
				writeUpdateEvt("toast update install failed")
			}
			if b.gate != nil {
				b.gate.Finish()
			}
			b.setPending(info)
			b.offerMu.Lock()
			b.offered = info.Version
			b.offerMu.Unlock()
			return
		}
		// GitHub apply os.Exits on success. Store apply may return after
		// the package is staged and the app needs a restart.
		writeUpdateEvt(fmt.Sprintf("toast v%s installed — restart suzuri", info.Version))
		if b.gate != nil {
			b.gate.Finish()
		}
	}()
}

func (b *updateBridge) markLater() {
	info := b.takePending()
	if info == nil {
		return
	}
	b.offerMu.Lock()
	b.later = info.Version
	b.offerMu.Unlock()
	log.Info("update: user chose later", "version", info.Version)
}

func (b *updateBridge) setPending(info *update.Info) {
	b.pendingMu.Lock()
	b.pending = info
	b.pendingMu.Unlock()
}

func (b *updateBridge) takePending() *update.Info {
	b.pendingMu.Lock()
	defer b.pendingMu.Unlock()
	info := b.pending
	b.pending = nil
	return info
}

func (b *updateBridge) peekPending() *update.Info {
	b.pendingMu.Lock()
	defer b.pendingMu.Unlock()
	return b.pending
}

var updateEvtMu sync.Mutex

func writeUpdateEvt(body string) {
	updateEvtMu.Lock()
	defer updateEvtMu.Unlock()
	path := updateEvtPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		log.Debug("update: mkdir evt", "err", err)
		return
	}
	if !strings.HasSuffix(body, "\n") {
		body += "\n"
	}
	tmp := path + ".tmp"
	payload := []byte(body)
	if err := os.WriteFile(tmp, payload, 0o644); err != nil {
		log.Debug("update: write evt", "err", err)
		return
	}
	for i := 0; i < 8; i++ {
		_ = os.Remove(path)
		if err := os.Rename(tmp, path); err == nil {
			return
		}
		time.Sleep(time.Duration(4*(i+1)) * time.Millisecond)
	}
	_ = os.WriteFile(path, payload, 0o644)
	_ = os.Remove(tmp)
}

// parseUpdateReq is exported for tests (single verb).
func parseUpdateReq(line string) string {
	switch strings.TrimSpace(line) {
	case "check", "install", "later":
		return strings.TrimSpace(line)
	default:
		return ""
	}
}
