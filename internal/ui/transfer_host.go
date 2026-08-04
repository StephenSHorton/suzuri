package ui

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"

	"github.com/charmbracelet/log"

	"github.com/StephenSHorton/suzuri/internal/chrome"
	"github.com/StephenSHorton/suzuri/internal/transfer"
)

// transferHost is the UI-facing callbacks for engine events (always posted to UI thread by caller).
type transferHost interface {
	postTransferStatus(chrome.TransferStatusMsg)
	postToast(string)
	copyText(string)
	defaultReceiveDir() string
}

// transferCtl manages at most one in-flight engine process.
type transferCtl struct {
	mu      sync.Mutex
	stopFn  context.CancelFunc
	gen     atomic.Uint64 // increments on each start; ignore stale events
}

var globalTransfer transferCtl

func (t *transferCtl) stop() {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.stopFn != nil {
		t.stopFn()
		t.stopFn = nil
	}
}

func (t *transferCtl) setStop(c context.CancelFunc) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.stopFn != nil {
		t.stopFn()
	}
	t.stopFn = c
}

func (t *transferCtl) clearStop() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.stopFn = nil
}

// startTransferSend serves path until cancel; posts status via host.
func startTransferSend(h transferHost, path string) {
	path = filepath.Clean(path)
	if st, err := os.Stat(path); err != nil || st == nil {
		h.postToast("send: path not found")
		h.postTransferStatus(chrome.TransferStatusMsg{
			Active:  true,
			Phase:   "error",
			Message: "no such file or folder",
		})
		return
	}

	gen := globalTransfer.gen.Add(1)
	ctx, cancel := context.WithCancel(context.Background())
	globalTransfer.setStop(cancel)

	h.postTransferStatus(chrome.TransferStatusMsg{
		Active:  true,
		Phase:   "preparing",
		Message: filepath.Base(path),
	})

	go func() {
		defer func() {
			globalTransfer.clearStop()
			cancel()
		}()

		c := &transfer.Client{
			OnEvent: func(ev transfer.Event) {
				if globalTransfer.gen.Load() != gen {
					return
				}
				switch ev.Event {
				case "ready":
					h.postTransferStatus(chrome.TransferStatusMsg{
						Active:  true,
						Phase:   "ready",
						Ticket:  ev.Ticket,
						Message: "share this ticket · keep suzuri open",
					})
					h.postToast("ticket ready — press c to copy")
				case "stopped":
					h.postTransferStatus(chrome.TransferStatusMsg{
						Active:  true,
						Phase:   "stopped",
						Message: "stopped serving",
					})
				case "error":
					h.postTransferStatus(chrome.TransferStatusMsg{
						Active:  true,
						Phase:   "error",
						Message: ev.Message,
					})
				}
			},
		}
		err := c.SendTicket(ctx, path, nil, false)
		if globalTransfer.gen.Load() != gen {
			return
		}
		if err != nil {
			log.Warn("transfer send ended", "err", err)
			h.postTransferStatus(chrome.TransferStatusMsg{
				Active:  true,
				Phase:   "error",
				Message: err.Error(),
			})
			return
		}
		// Clean cancel without error event already posted stopped.
		h.postTransferStatus(chrome.TransferStatusMsg{
			Active:  true,
			Phase:   "stopped",
			Message: "done",
		})
	}()
}

// startTransferReceive downloads ticket into dir.
func startTransferReceive(h transferHost, ticket, dir string) {
	ticket = trimTicket(ticket)
	if ticket == "" {
		h.postToast("receive: empty ticket")
		return
	}
	if dir == "" {
		dir = h.defaultReceiveDir()
	}
	_ = os.MkdirAll(dir, 0o755)

	gen := globalTransfer.gen.Add(1)
	ctx, cancel := context.WithCancel(context.Background())
	globalTransfer.setStop(cancel)

	h.postTransferStatus(chrome.TransferStatusMsg{
		Active:  true,
		Phase:   "receiving",
		Ticket:  ticket,
		Message: dir,
	})

	go func() {
		defer func() {
			globalTransfer.clearStop()
			cancel()
		}()

		c := &transfer.Client{
			OnEvent: func(ev transfer.Event) {
				if globalTransfer.gen.Load() != gen {
					return
				}
				switch ev.Event {
				case "receiving":
					h.postTransferStatus(chrome.TransferStatusMsg{
						Active:  true,
						Phase:   "receiving",
						Ticket:  ticket,
						Message: dir,
					})
				case "progress":
					var done, total uint64
					if ev.Done != nil {
						done = *ev.Done
					}
					if ev.Total != nil {
						total = *ev.Total
					}
					h.postTransferStatus(chrome.TransferStatusMsg{
						Active: true,
						Phase:  "progress",
						Ticket: ticket,
						Done:   done,
						Total:  total,
					})
				case "resumed":
					msg := "resumed"
					if ev.AlreadyHad != nil {
						msg = "resumed (partial already downloaded)"
					}
					h.postTransferStatus(chrome.TransferStatusMsg{
						Active:  true,
						Phase:   "progress",
						Ticket:  ticket,
						Message: msg,
					})
				case "done":
					var total uint64
					if ev.TotalBytes != nil {
						total = *ev.TotalBytes
					}
					out := dir
					if ev.OutDir != "" {
						out = ev.OutDir
					}
					h.postTransferStatus(chrome.TransferStatusMsg{
						Active:  true,
						Phase:   "done",
						Ticket:  ticket,
						Done:    total,
						Total:   total,
						Message: "saved to " + out,
					})
					h.postToast("transfer complete")
				case "error":
					h.postTransferStatus(chrome.TransferStatusMsg{
						Active:  true,
						Phase:   "error",
						Ticket:  ticket,
						Message: ev.Message,
					})
				}
			},
		}
		err := c.ReceiveTicket(ctx, ticket, dir)
		if globalTransfer.gen.Load() != gen {
			return
		}
		if err != nil {
			log.Warn("transfer receive ended", "err", err)
			h.postTransferStatus(chrome.TransferStatusMsg{
				Active:  true,
				Phase:   "error",
				Ticket:  ticket,
				Message: err.Error(),
			})
		}
	}()
}

func cancelTransfer() {
	globalTransfer.gen.Add(1) // invalidate in-flight event handlers
	globalTransfer.stop()
}

func trimTicket(s string) string {
	// Collapse whitespace from chat paste.
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == ' ' || c == '\t' || c == '\n' || c == '\r' {
			continue
		}
		out = append(out, c)
	}
	return string(out)
}

func defaultDownloadDir() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return "."
	}
	dl := filepath.Join(home, "Downloads")
	if st, err := os.Stat(dl); err == nil && st.IsDir() {
		return dl
	}
	return home
}
