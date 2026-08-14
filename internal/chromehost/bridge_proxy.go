package chromehost

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/charmbracelet/log"

	"github.com/StephenSHorton/suzuri/internal/bridge"
	"github.com/StephenSHorton/suzuri/internal/config"
	"github.com/StephenSHorton/suzuri/internal/notes"
	"github.com/StephenSHorton/suzuri/internal/workspace"
)

// StatusFile is written by suzuri-chrome under the product config dir so the
// Go host can publish an MCP bridge snapshot without embedding the GPU loop.
const StatusFile = "chrome_status.json"

// SubmitFile is the warp-submit mailbox (full line body, one submission).
const SubmitFile = "chrome_submit"

// StatusPath is `{config.Dir()}/chrome_status.json`.
func StatusPath() string {
	return filepath.Join(config.Dir(), StatusFile)
}

// SubmitPath is `{config.Dir()}/chrome_submit`.
func SubmitPath() string {
	return filepath.Join(config.Dir(), SubmitFile)
}

// ChromeStatus is the JSON shape written by native chrome (rich snapshot).
// Field names align with bridge.Snapshot / bridge.TabSnap where possible.
type ChromeStatus struct {
	PID       int              `json:"pid"`
	Version   string           `json:"version,omitempty"`
	Cols      int              `json:"cols,omitempty"`
	Rows      int              `json:"rows,omitempty"`
	ActiveTab int              `json:"active_tab"`
	Tabs      []bridge.TabSnap `json:"-"` // parsed from raw "tabs" (array or legacy count)
	Notes     []string         `json:"notes,omitempty"`
	// Legacy flat fields (wave 7 thin status) — still accepted.
	TabsCount   int    `json:"-"`
	ActiveTitle string `json:"active_title,omitempty"`
}

// chromeStatusWire is the on-disk JSON form. "tabs" may be an array (rich) or
// a number (wave-7 thin status).
type chromeStatusWire struct {
	PID         int             `json:"pid"`
	Version     string          `json:"version,omitempty"`
	Cols        int             `json:"cols,omitempty"`
	Rows        int             `json:"rows,omitempty"`
	ActiveTab   int             `json:"active_tab"`
	Tabs        json.RawMessage `json:"tabs,omitempty"`
	Notes       []string        `json:"notes,omitempty"`
	ActiveTitle string          `json:"active_title,omitempty"`
}

// ReadChromeStatus loads chrome_status.json if present.
func ReadChromeStatus() (ChromeStatus, error) {
	b, err := os.ReadFile(StatusPath())
	if err != nil {
		return ChromeStatus{}, err
	}
	return ParseChromeStatus(b)
}

// ParseChromeStatus unmarshals a chrome_status.json document.
func ParseChromeStatus(b []byte) (ChromeStatus, error) {
	var wire chromeStatusWire
	if err := json.Unmarshal(b, &wire); err != nil {
		return ChromeStatus{}, err
	}
	st := ChromeStatus{
		PID:         wire.PID,
		Version:     wire.Version,
		Cols:        wire.Cols,
		Rows:        wire.Rows,
		ActiveTab:   wire.ActiveTab,
		Notes:       wire.Notes,
		ActiveTitle: wire.ActiveTitle,
	}
	if len(wire.Tabs) > 0 {
		// Prefer array of tab snaps; fall back to integer tab count.
		var tabs []bridge.TabSnap
		if err := json.Unmarshal(wire.Tabs, &tabs); err == nil {
			st.Tabs = tabs
		} else {
			var n int
			if err2 := json.Unmarshal(wire.Tabs, &n); err2 == nil {
				st.TabsCount = n
			}
		}
	}
	return st, nil
}

// SnapshotFromChromeStatus maps chrome_status.json into a bridge.Snapshot.
func SnapshotFromChromeStatus(st ChromeStatus, fallbackPID int) bridge.Snapshot {
	pid := st.PID
	if pid <= 0 {
		pid = fallbackPID
	}
	cols, rows := st.Cols, st.Rows
	if cols <= 0 {
		cols = 80
	}
	if rows <= 0 {
		rows = 24
	}

	tabs := st.Tabs
	if len(tabs) == 0 {
		// Legacy thin status: synthesize one tab.
		n := st.TabsCount
		if n < 1 {
			n = 1
		}
		title := st.ActiveTitle
		if title == "" {
			title = "suzuri-chrome"
		}
		tabs = make([]bridge.TabSnap, 0, n)
		for i := 0; i < n; i++ {
			t := bridge.TabSnap{ID: i, Alive: true}
			if i == st.ActiveTab {
				t.Title = title
			} else {
				t.Title = fmt.Sprintf("tab %d", i)
			}
			tabs = append(tabs, t)
		}
	}

	// Ensure every tab has non-nil slices (JSON null → nil).
	for i := range tabs {
		if tabs[i].LiveLines == nil {
			tabs[i].LiveLines = []string{}
		}
		if tabs[i].Viewport == nil {
			tabs[i].Viewport = []string{}
		}
		if tabs[i].Blocks == nil {
			tabs[i].Blocks = []bridge.Block{}
		}
		if tabs[i].History == nil {
			tabs[i].History = []bridge.HLine{}
		}
	}

	notes := st.Notes
	if len(notes) == 0 {
		notes = []string{
			"ui=chrome",
			"bridge=chromehost proxy",
		}
	}
	// Tag version for agents.
	if st.Version != "" {
		notes = append(notes, "chrome_version="+st.Version)
	}

	return bridge.Snapshot{
		PID:       pid,
		Cols:      cols,
		Rows:      rows,
		ActiveTab: st.ActiveTab,
		Tabs:      tabs,
		Notes:     notes,
	}
}

// WriteSubmit writes a line for chrome to feed into the focused warp/PTY path.
func WriteSubmit(line string) error {
	line = trimSubmit(line)
	if line == "" {
		return fmt.Errorf("chromehost: empty submit line")
	}
	path := SubmitPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(line+"\n"), 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(path)
		if err2 := os.Rename(tmp, path); err2 != nil {
			_ = os.Remove(tmp)
			return err2
		}
	}
	return nil
}

func trimSubmit(s string) string {
	for len(s) > 0 && (s[0] == ' ' || s[0] == '\t' || s[0] == '\n' || s[0] == '\r') {
		s = s[1:]
	}
	for len(s) > 0 {
		c := s[len(s)-1]
		if c == ' ' || c == '\t' || c == '\n' || c == '\r' {
			s = s[:len(s)-1]
			continue
		}
		break
	}
	return s
}

// startChromeBridge starts a loopback MCP bridge bound to disk notes/workspace
// plus chrome mailboxes. Caller must Stop the host when chrome exits.
func startChromeBridge(chromePID int) (*bridge.Host, error) {
	h := bridge.NewHost()
	h.BindNotes(func(req bridge.NotesRequest) bridge.NotesResult {
		off := notes.ApplyNotesDiskOp(string(req.Op), req.ID, req.Title, req.Body, req.SetActive)
		switch req.Op {
		case bridge.NotesOpCreate, bridge.NotesOpUpdate, bridge.NotesOpDelete:
			_ = SendCommand(CmdOpenNotes)
		}
		return notesBridgeFromDisk(off)
	})
	h.BindWorkspace(func(req bridge.WorkspaceRequest) bridge.WorkspaceResult {
		r := workspace.Apply(nil, workspace.Request{
			Op:         workspace.Op(req.Op),
			Channel:    req.Channel,
			Body:       req.Body,
			Name:       req.Name,
			Kind:       req.Kind,
			MemberID:   req.MemberID,
			SessionID:  req.SessionID,
			ReplyTo:    req.ReplyTo,
			Topic:      req.Topic,
			Limit:      req.Limit,
			SinceID:    req.SinceID,
			AfterTS:    req.AfterTS,
			Since:      req.Since,
			Timeout:    req.Timeout,
			FilePath:   req.FilePath,
			FileID:     req.FileID,
			Status:     req.Status,
			StatusNote: req.StatusNote,
			Role:       req.Role,
		})
		_ = SendCommand(CmdRefreshWorkspace)
		return workspaceBridgeFromResult(r)
	})
	h.BindSubmit(func(tabID int, line string) error {
		_ = tabID // chrome focuses active tab; multi-tab submit later
		return WriteSubmit(line)
	})

	if _, err := h.Start(); err != nil {
		return nil, err
	}
	h.Publish(bridge.Snapshot{
		PID:       chromePID,
		ActiveTab: 0,
		Tabs: []bridge.TabSnap{{
			ID:    0,
			Title: "suzuri-chrome",
			Alive: true,
		}},
		Notes: []string{"ui=chrome", "bridge=chromehost proxy (waiting for chrome_status.json)"},
	})
	return h, nil
}

func publishChromeStatus(h *bridge.Host, chromePID int) {
	st, err := ReadChromeStatus()
	if err != nil {
		// Keep last good snapshot; only seed a minimal one if never published.
		return
	}
	h.Publish(SnapshotFromChromeStatus(st, chromePID))
}

func notesBridgeFromDisk(off notes.NotesBridgeResult) bridge.NotesResult {
	b, err := json.Marshal(off)
	if err != nil {
		return bridge.NotesResult{OK: false, Error: err.Error()}
	}
	var out bridge.NotesResult
	if err := json.Unmarshal(b, &out); err != nil {
		return bridge.NotesResult{OK: false, Error: err.Error()}
	}
	return out
}

func workspaceBridgeFromResult(r workspace.Result) bridge.WorkspaceResult {
	b, err := json.Marshal(r.ToMap())
	if err != nil {
		return bridge.WorkspaceResult{OK: false, Error: err.Error()}
	}
	var out bridge.WorkspaceResult
	if err := json.Unmarshal(b, &out); err != nil {
		return bridge.WorkspaceResult{OK: false, Error: err.Error()}
	}
	return out
}

// runStatusPublisher publishes chrome_status.json into the bridge until stop closes.
func runStatusPublisher(h *bridge.Host, chromePID int, stop <-chan struct{}) {
	t := time.NewTicker(400 * time.Millisecond)
	defer t.Stop()
	for {
		select {
		case <-stop:
			return
		case <-t.C:
			publishChromeStatus(h, chromePID)
		}
	}
}

// removeChromeStatus clears the status file on clean chrome exit (best-effort).
func removeChromeStatus() {
	_ = os.Remove(StatusPath())
	_ = os.Remove(SubmitPath())
}

func logBridgeStart(h *bridge.Host) {
	if h == nil {
		return
	}
	log.Info("chrome MCP bridge ready", "endpoint", bridge.EndpointPath())
}
