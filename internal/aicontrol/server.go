// Package aicontrol is the loopback HTTP plane grok-fork curls to split/focus
// panes. Not MCP. Binds 127.0.0.1:0; discovery is {config}/ai.json plus PTY env
// SUZURI_CONTROL_URL / SUZURI_CONTROL_TOKEN.
package aicontrol

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/StephenSHorton/suzuri/internal/config"
)

const (
	EnvURL   = "SUZURI_CONTROL_URL"
	EnvToken = "SUZURI_CONTROL_TOKEN"
	EnvHost  = "SUZURI"
	helpText = `# Suzuri AI control

Loopback HTTP for this suzuri window. Not MCP. Prefer curl.

Auth (required except GET /help and GET /tools):
  Authorization: Bearer $SUZURI_CONTROL_TOKEN
  or header X-Suzuri-Token: $SUZURI_CONTROL_TOKEN
  or query ?token=

Discovery:
  $SUZURI_CONTROL_URL  (injected into every pane PTY)
  $SUZURI=1            (injected; grok-fork sessions_open gates on this)
  {config_dir}/ai.json
  {config_dir}/ai/{pid}.json   (one file per window process)

Endpoints:
  GET  /help          this text
  GET  /tools         JSON tools + argument schemas
  GET  /v1/layout     windows, tabs, pane trees, titles, cwd, tail text
  GET  /v1/panes/:id  one pane (more scrollback)
  POST /v1/call       {"tool":"<name>","args":{...}}

Tools (POST /v1/call):
  layout                         snapshot
  pane        {pane_id, lines?}  content
  split       {pane_id?, axis: "right"|"down"}
  focus       {pane_id}
  move        {pane_id, target_pane_id, edge: "left"|"right"|"top"|"bottom"}
  move_to_tab {pane_id, tab_id?}  omit tab_id → extract to a new tab
  rotate / swap / grow / shrink / equalize  {pane_id?}
  close       {pane_id?}
  rename      {pane_id, title}
  new_tab
`
)

// Job is one UI-thread request from an HTTP handler.
type Job struct {
	Kind  Kind
	Pane  int
	Lines int
	Tool  string
	Args  map[string]any
	Reply chan Reply
}

type Kind int

const (
	KindLayout Kind = iota
	KindPane
	KindCall
)

// Reply is the HTTP body the UI thread produces.
type Reply struct {
	Status      int
	Body        []byte
	ContentType string
}

func JSONOK(v any) Reply {
	b, _ := json.Marshal(v)
	return Reply{Status: 200, Body: b, ContentType: "application/json; charset=utf-8"}
}

func JSONErr(status int, msg string) Reply {
	b, _ := json.Marshal(map[string]any{"error": msg})
	return Reply{Status: status, Body: b, ContentType: "application/json; charset=utf-8"}
}

// Server is the running listener + job queue.
type Server struct {
	URL   string
	Token string
	// Wake is called from the HTTP goroutine after a job is queued so the UI
	// thread drains it. Windows posts WM_APP; Darwin already drains every frame.
	Wake func()
	jobs chan Job
	ln   net.Listener
	srv  *http.Server
	pid  int
	stop atomic.Bool
}

var advertised struct {
	mu    sync.RWMutex
	url   string
	token string
}

// PtyEnv is injected into every new PTY. Empty if the server is not up.
func PtyEnv() []string {
	advertised.mu.RLock()
	defer advertised.mu.RUnlock()
	if advertised.url == "" {
		return nil
	}
	return []string{
		EnvURL + "=" + advertised.url,
		EnvToken + "=" + advertised.token,
		EnvHost + "=1",
	}
}

// Start binds loopback and writes discovery files.
func Start() (*Server, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	url := "http://" + ln.Addr().String()
	token := makeToken(url)
	pid := os.Getpid()
	s := &Server{
		URL:   url,
		Token: token,
		jobs:  make(chan Job, 16),
		ln:    ln,
		pid:   pid,
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/", s.serve)
	s.srv = &http.Server{Handler: mux}
	advertised.mu.Lock()
	advertised.url = url
	advertised.token = token
	advertised.mu.Unlock()
	writeDiscovery(url, token, pid)
	go func() { _ = s.srv.Serve(ln) }()
	return s, nil
}

// TryRecv is non-blocking.
func (s *Server) TryRecv() (Job, bool) {
	if s == nil {
		return Job{}, false
	}
	select {
	case j := <-s.jobs:
		return j, true
	default:
		return Job{}, false
	}
}

// Close stops the listener and removes discovery files for this pid.
func (s *Server) Close() {
	if s == nil {
		return
	}
	s.stop.Store(true)
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	_ = s.srv.Shutdown(ctx)
	_ = s.ln.Close()
	removeDiscovery(s.pid)
	advertised.mu.Lock()
	if advertised.url == s.URL {
		advertised.url = ""
		advertised.token = ""
	}
	advertised.mu.Unlock()
}

func (s *Server) serve(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	if path == "" {
		path = "/"
	}
	method := strings.ToUpper(r.Method)
	if method == "GET" && (path == "/" || path == "/help") {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = io.WriteString(w, helpText)
		return
	}
	if method == "GET" && path == "/tools" {
		writeJSON(w, 200, toolsJSON())
		return
	}
	if !s.tokenOK(r) {
		writeJSON(w, 401, map[string]any{"error": "unauthorized"})
		return
	}
	var job Job
	job.Reply = make(chan Reply, 1)
	switch {
	case method == "GET" && path == "/v1/layout":
		job.Kind = KindLayout
	case method == "GET" && strings.HasPrefix(path, "/v1/panes/"):
		id, err := strconv.Atoi(strings.TrimPrefix(path, "/v1/panes/"))
		if err != nil {
			writeJSON(w, 400, map[string]any{"error": "bad pane id"})
			return
		}
		lines := 80
		if q := r.URL.Query().Get("lines"); q != "" {
			if n, err := strconv.Atoi(q); err == nil && n > 0 {
				lines = n
			}
		}
		job.Kind = KindPane
		job.Pane = id
		job.Lines = lines
	case method == "POST" && path == "/v1/call":
		body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		if err != nil {
			writeJSON(w, 400, map[string]any{"error": "bad body"})
			return
		}
		var payload struct {
			Tool string         `json:"tool"`
			Args map[string]any `json:"args"`
		}
		if err := json.Unmarshal(body, &payload); err != nil {
			writeJSON(w, 400, map[string]any{"error": "json: " + err.Error()})
			return
		}
		if strings.TrimSpace(payload.Tool) == "" {
			writeJSON(w, 400, map[string]any{"error": "missing tool"})
			return
		}
		if payload.Args == nil {
			payload.Args = map[string]any{}
		}
		job.Kind = KindCall
		job.Tool = payload.Tool
		job.Args = payload.Args
	default:
		writeJSON(w, 404, map[string]any{"error": "not found", "see": "/help"})
		return
	}
	select {
	case s.jobs <- job:
		if wfn := s.Wake; wfn != nil {
			wfn()
		}
	default:
		writeJSON(w, 503, map[string]any{"error": "ui thread busy"})
		return
	}
	select {
	case rep := <-job.Reply:
		w.Header().Set("Content-Type", rep.ContentType)
		w.WriteHeader(rep.Status)
		_, _ = w.Write(rep.Body)
	case <-time.After(4 * time.Second):
		writeJSON(w, 504, map[string]any{"error": "ui timeout"})
	}
}

func (s *Server) tokenOK(r *http.Request) bool {
	if s.Token == "" {
		return false
	}
	if r.URL.Query().Get("token") == s.Token {
		return true
	}
	if r.Header.Get("X-Suzuri-Token") == s.Token {
		return true
	}
	a := strings.TrimSpace(r.Header.Get("Authorization"))
	if rest, ok := strings.CutPrefix(a, "Bearer "); ok && strings.TrimSpace(rest) == s.Token {
		return true
	}
	if rest, ok := strings.CutPrefix(a, "bearer "); ok && strings.TrimSpace(rest) == s.Token {
		return true
	}
	return false
}

func toolsJSON() map[string]any {
	return map[string]any{
		"tools": []map[string]any{
			{"name": "layout", "args": map[string]any{}, "doc": "Snapshot windows, tabs, pane trees, titles, cwd, tail text."},
			{"name": "pane", "args": map[string]any{"pane_id": "u64", "lines": "u32?"}, "doc": "One pane including more scrollback."},
			{"name": "split", "args": map[string]any{"pane_id": "u64?", "axis": "right|down"}, "doc": "Split a pane (default: focused)."},
			{"name": "focus", "args": map[string]any{"pane_id": "u64"}, "doc": "Focus a pane."},
			{"name": "move", "args": map[string]any{"pane_id": "u64", "target_pane_id": "u64", "edge": "left|right|top|bottom"}, "doc": "Re-dock pane_id onto an edge of target_pane_id."},
			{"name": "move_to_tab", "args": map[string]any{"pane_id": "u64", "tab_id": "u64?"}, "doc": "Move onto an existing tab, or extract to a new tab if tab_id omitted."},
			{"name": "rotate", "args": map[string]any{"pane_id": "u64?"}, "doc": "Rotate the split around the pane."},
			{"name": "swap", "args": map[string]any{"pane_id": "u64?"}, "doc": "Swap the two sides of the split."},
			{"name": "grow", "args": map[string]any{"pane_id": "u64?"}, "doc": "Grow the pane."},
			{"name": "shrink", "args": map[string]any{"pane_id": "u64?"}, "doc": "Shrink the pane."},
			{"name": "equalize", "args": map[string]any{"pane_id": "u64?"}, "doc": "50/50 the split."},
			{"name": "close", "args": map[string]any{"pane_id": "u64?"}, "doc": "Close pane (or its tab if last pane)."},
			{"name": "rename", "args": map[string]any{"pane_id": "u64", "title": "string"}, "doc": "Set the pane title."},
			{"name": "new_tab", "args": map[string]any{}, "doc": "Open a new tab with a shell."},
		},
	}
}

// ArgUint reads a JSON number or string id.
func ArgUint(args map[string]any, key string) (int, bool) {
	if args == nil {
		return 0, false
	}
	v, ok := args[key]
	if !ok || v == nil {
		return 0, false
	}
	switch n := v.(type) {
	case float64:
		if n < 0 {
			return 0, false
		}
		return int(n), true
	case json.Number:
		i, err := n.Int64()
		if err != nil || i < 0 {
			return 0, false
		}
		return int(i), true
	case string:
		i, err := strconv.Atoi(strings.TrimSpace(n))
		if err != nil || i < 0 {
			return 0, false
		}
		return i, true
	case int:
		if n < 0 {
			return 0, false
		}
		return n, true
	default:
		return 0, false
	}
}

func ArgString(args map[string]any, key string) (string, bool) {
	if args == nil {
		return "", false
	}
	v, ok := args[key]
	if !ok {
		return "", false
	}
	s, ok := v.(string)
	return s, ok
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func makeToken(url string) string {
	h := sha256.New()
	_, _ = fmt.Fprintf(h, "%d:%s:%d", os.Getpid(), url, time.Now().UnixNano())
	sum := h.Sum(nil)
	return hex.EncodeToString(sum[:16])
}

func writeDiscovery(url, token string, pid int) {
	body, _ := json.Marshal(map[string]any{
		"url": url, "token": token, "pid": pid,
		"help": "/help", "tools": "/tools",
	})
	dir := filepath.Join(config.Dir(), "ai")
	_ = os.MkdirAll(dir, 0o755)
	_ = os.WriteFile(filepath.Join(dir, fmt.Sprintf("%d.json", pid)), body, 0o600)
	_ = os.WriteFile(filepath.Join(config.Dir(), "ai.json"), body, 0o600)
}

func removeDiscovery(pid int) {
	dir := filepath.Join(config.Dir(), "ai")
	_ = os.Remove(filepath.Join(dir, fmt.Sprintf("%d.json", pid)))
	root := filepath.Join(config.Dir(), "ai.json")
	raw, err := os.ReadFile(root)
	if err != nil {
		return
	}
	if strings.Contains(string(raw), fmt.Sprintf(`"pid":%d`, pid)) ||
		strings.Contains(string(raw), fmt.Sprintf(`"pid": %d`, pid)) {
		_ = os.Remove(root)
	}
}
