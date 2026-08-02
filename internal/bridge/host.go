package bridge

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/charmbracelet/log"

	"github.com/StephenSHorton/suzuri/internal/applog"
)

// Host is the in-GUI side of the bridge: loopback HTTP + snapshot store.
type Host struct {
	mu     sync.RWMutex
	snap   Snapshot
	submit func(tabID int, line string) error // must be UI-safe (posted by UI layer)
	srv    *http.Server
	ep     Endpoint
}

// NewHost creates a bridge host. submit may be nil until BindSubmit.
func NewHost() *Host {
	return &Host{}
}

// BindSubmit sets the UI-thread submit implementation.
func (h *Host) BindSubmit(fn func(tabID int, line string) error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.submit = fn
}


// Publish replaces the latest snapshot (call from UI thread after meaningful updates).
func (h *Host) Publish(s Snapshot) {
	s.PID = os.Getpid()
	s.UpdatedAt = time.Now()
	h.mu.Lock()
	h.snap = s
	h.mu.Unlock()
}

// Snapshot returns a copy of the latest published state.
func (h *Host) Snapshot() Snapshot {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.snap
}

// Start listens on 127.0.0.1:0, writes bridge.json, serves /v1/*.
func (h *Host) Start() (Endpoint, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return Endpoint{}, err
	}
	token, err := randomToken(16)
	if err != nil {
		_ = ln.Close()
		return Endpoint{}, err
	}
	ep := Endpoint{
		PID:   os.Getpid(),
		URL:   "http://" + ln.Addr().String(),
		Token: token,
	}
	if err := WriteEndpoint(ep); err != nil {
		_ = ln.Close()
		return Endpoint{}, err
	}
	h.ep = ep

	mux := http.NewServeMux()
	mux.HandleFunc("/v1/status", h.auth(h.handleStatus))
	mux.HandleFunc("/v1/diag", h.auth(h.handleDiag))
	mux.HandleFunc("/v1/snapshot", h.auth(h.handleSnapshot))
	mux.HandleFunc("/v1/submit", h.auth(h.handleSubmit))
	mux.HandleFunc("/v1/logs", h.auth(h.handleLogs))

	h.srv = &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}
	go func() {
		if err := h.srv.Serve(ln); err != nil && err != http.ErrServerClosed {
			log.Warn("bridge serve ended", "err", err)
		}
	}()
	log.Info("mcp bridge listening", "url", ep.URL, "endpoint", EndpointPath())
	return ep, nil
}

// Stop shuts down the loopback server and removes bridge.json.
func (h *Host) Stop() {
	if h.srv != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		_ = h.srv.Shutdown(ctx)
		cancel()
	}
	RemoveEndpoint()
}

func (h *Host) auth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		const p = "Bearer "
		ah := r.Header.Get("Authorization")
		if len(ah) < len(p) || ah[len(p):] != h.ep.Token {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next(w, r)
	}
}

func (h *Host) handleStatus(w http.ResponseWriter, r *http.Request) {
	s := h.Snapshot()
	writeJSON(w, Status{
		OK:     true,
		PID:    os.Getpid(),
		Tabs:   len(s.Tabs),
		Active: s.ActiveTab,
		Bridge: h.ep.URL,
	})
}

func (h *Host) handleDiag(w http.ResponseWriter, r *http.Request) {
	s := h.Snapshot()
	// Enrich with agent-oriented notes.
	notes := append([]string{}, s.Notes...)
	if len(s.Tabs) == 0 {
		notes = append(notes, "no tabs")
	} else {
		t := s.Tabs[0]
		for _, tb := range s.Tabs {
			if tb.ID == s.ActiveTab {
				t = tb
				break
			}
		}
		if t.Echo.Armed {
			notes = append(notes, "echo filter armed for: "+t.Echo.Cmd)
		}
		// Detect possible double-command display: block cmd also on a live line.
		for _, b := range t.Blocks {
			for _, live := range t.LiveLines {
				if live == b.Command {
					notes = append(notes, "possible dual command display: live line equals block without chevron: "+b.Command)
				}
			}
		}
	}
	s.Notes = notes
	writeJSON(w, s)
}

func (h *Host) handleSnapshot(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, h.Snapshot())
}

func (h *Host) handleLogs(w http.ResponseWriter, r *http.Request) {
	n := 150
	if q := r.URL.Query().Get("lines"); q != "" {
		var v int
		if _, err := fmt.Sscanf(q, "%d", &v); err == nil && v > 0 {
			n = v
		}
	}
	// Flush GUI-owned log handle so the tail includes the latest lines.
	applog.Sync()
	path, lines, err := applog.Tail(n)
	if err != nil {
		writeJSON(w, LogsResult{OK: false, Path: path, Error: err.Error()})
		return
	}
	writeJSON(w, LogsResult{OK: true, Path: path, Lines: lines, Count: len(lines)})
}

func (h *Host) handleSubmit(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	var req SubmitRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad json: "+err.Error(), http.StatusBadRequest)
		return
	}
	h.mu.RLock()
	fn := h.submit
	snap := h.snap
	h.mu.RUnlock()
	if fn == nil {
		writeJSON(w, SubmitResult{OK: false, Error: "submit not bound"})
		return
	}
	tabID := snap.ActiveTab
	if req.TabID != nil {
		tabID = *req.TabID
	}
	if err := fn(tabID, req.Line); err != nil {
		writeJSON(w, SubmitResult{OK: false, TabID: tabID, Line: req.Line, Error: err.Error()})
		return
	}
	writeJSON(w, SubmitResult{OK: true, TabID: tabID, Line: req.Line})
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(v)
}

func randomToken(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
