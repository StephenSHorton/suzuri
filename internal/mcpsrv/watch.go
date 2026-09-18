package mcpsrv

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/StephenSHorton/suzuri/internal/workspace"
)

const (
	watchPollInterval = 150 * time.Millisecond
	watchHeartbeat    = 20 * time.Second
)

// channelWatch binds this MCP process (one grok-fork conversation) to a
// workspace member and pushes mentions/assignments as MCP channel notifications.
type channelWatch struct {
	store  *workspace.Store
	notify func(method string, params any) error

	mu        sync.Mutex
	memberID  string
	cursorID  string
	lastTouch time.Time
	sizes     map[string]int64
}

func newChannelWatch(store *workspace.Store, notify func(method string, params any) error) *channelWatch {
	if store == nil {
		store = workspace.Default
	}
	return &channelWatch{
		store:  store,
		notify: notify,
		sizes:  map[string]int64{},
	}
}

// Bind this conversation to memberID. Existing history is not replayed.
func (w *channelWatch) Bind(memberID string) {
	if w == nil || memberID == "" {
		return
	}
	cursor := snapshotCursor(w.store)
	w.mu.Lock()
	w.memberID = memberID
	w.cursorID = cursor
	w.lastTouch = time.Now()
	w.mu.Unlock()
	_ = w.store.Touch(memberID)
}

func snapshotCursor(store *workspace.Store) string {
	chs, err := store.ListChannels()
	if err != nil {
		return ""
	}
	var best workspace.Message
	for _, ch := range chs {
		hist, err := store.HistorySince(ch.ID, 1, "", time.Time{})
		if err != nil || len(hist) == 0 {
			continue
		}
		m := hist[len(hist)-1]
		if best.ID == "" || m.TS.After(best.TS) || (m.TS.Equal(best.TS) && m.ID > best.ID) {
			best = m
		}
	}
	return best.ID
}

func (w *channelWatch) Unbind() {
	if w == nil {
		return
	}
	w.mu.Lock()
	w.memberID = ""
	w.cursorID = ""
	w.mu.Unlock()
}

func (w *channelWatch) MemberID() string {
	if w == nil {
		return ""
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.memberID
}

func (w *channelWatch) Loop(ctx context.Context) {
	tick := time.NewTicker(watchPollInterval)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			w.step()
		}
	}
}

func (w *channelWatch) step() {
	w.mu.Lock()
	memberID := w.memberID
	cursor := w.cursorID
	w.mu.Unlock()
	if memberID == "" {
		return
	}

	if !w.jsonlChanged() {
		w.heartbeat(memberID)
		return
	}

	list, err := w.store.Inbox(memberID, cursor, 50)
	if err != nil {
		return
	}
	for _, msg := range list {
		w.advance(msg.ID)
		if msg.FromID == memberID {
			continue
		}
		meta := map[string]string{
			"channel":    msg.Channel,
			"message_id": msg.ID,
			"from":       msg.FromName,
			"from_id":    msg.FromID,
			"member_id":  memberID,
		}
		if w.notify != nil {
			_ = w.notify(claudeChannelMethod, channelNotificationParams(msg.Body, meta))
		}
	}
	w.heartbeat(memberID)
}

func (w *channelWatch) advance(id string) {
	if id == "" {
		return
	}
	w.mu.Lock()
	w.cursorID = id
	w.mu.Unlock()
}

func (w *channelWatch) heartbeat(memberID string) {
	w.mu.Lock()
	due := time.Since(w.lastTouch) >= watchHeartbeat
	if due {
		w.lastTouch = time.Now()
	}
	w.mu.Unlock()
	if due {
		_ = w.store.Touch(memberID)
	}
}

func (w *channelWatch) jsonlChanged() bool {
	root := w.store.Root()
	chDir := filepath.Join(root, "channels")
	ents, err := os.ReadDir(chDir)
	if err != nil {
		return true
	}
	changed := false
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.sizes == nil {
		w.sizes = map[string]int64{}
	}
	seen := map[string]bool{}
	for _, e := range ents {
		if !e.IsDir() {
			continue
		}
		p := filepath.Join(chDir, e.Name(), "messages.jsonl")
		seen[p] = true
		info, err := os.Stat(p)
		var sz int64
		if err == nil {
			sz = info.Size()
		}
		if prev, ok := w.sizes[p]; !ok || prev != sz {
			changed = true
		}
		w.sizes[p] = sz
	}
	for p := range w.sizes {
		if !seen[p] {
			delete(w.sizes, p)
			changed = true
		}
	}
	return changed
}
