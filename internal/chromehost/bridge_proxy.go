package chromehost

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/charmbracelet/log"

	"github.com/StephenSHorton/suzuri/internal/bridge"
	"github.com/StephenSHorton/suzuri/internal/chrome"
	"github.com/StephenSHorton/suzuri/internal/config"
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

// ChromeStatus is the JSON shape written by native chrome (best-effort).
type ChromeStatus struct {
	PID         int    `json:"pid"`
	Tabs        int    `json:"tabs"`
	ActiveTab   int    `json:"active_tab"`
	ActiveTitle string `json:"active_title,omitempty"`
	Cols        int    `json:"cols,omitempty"`
	Rows        int    `json:"rows,omitempty"`
	Version     string `json:"version,omitempty"`
}

// ReadChromeStatus loads chrome_status.json if present.
func ReadChromeStatus() (ChromeStatus, error) {
	b, err := os.ReadFile(StatusPath())
	if err != nil {
		return ChromeStatus{}, err
	}
	var st ChromeStatus
	if err := json.Unmarshal(b, &st); err != nil {
		return ChromeStatus{}, err
	}
	return st, nil
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
		off := chrome.ApplyNotesDiskOp(string(req.Op), req.ID, req.Title, req.Body, req.SetActive)
		// Nudge open chrome UI if notes changed.
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
			FilePath:   req.FilePath,
			FileID:     req.FileID,
			Status:     req.Status,
			StatusNote: req.StatusNote,
		})
		// Soft-refresh open workspace panel (chrome polls mailbox).
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
	// Seed snapshot so status is OK before chrome_status.json exists.
	h.Publish(bridge.Snapshot{
		PID:       chromePID,
		ActiveTab: 0,
		Tabs: []bridge.TabSnap{{
			ID:    0,
			Title: "suzuri-chrome",
			Alive: true,
		}},
		Notes: []string{"ui=chrome", "bridge=chromehost proxy (disk notes/workspace + mailboxes)"},
	})
	return h, nil
}

func publishChromeStatus(h *bridge.Host, chromePID int) {
	st, err := ReadChromeStatus()
	tabs := 1
	active := 0
	title := "suzuri-chrome"
	cols, rows := 80, 24
	if err == nil {
		if st.Tabs > 0 {
			tabs = st.Tabs
		}
		active = st.ActiveTab
		if st.ActiveTitle != "" {
			title = st.ActiveTitle
		}
		if st.Cols > 0 {
			cols = st.Cols
		}
		if st.Rows > 0 {
			rows = st.Rows
		}
		if st.PID > 0 {
			chromePID = st.PID
		}
	}
	tabSnaps := make([]bridge.TabSnap, 0, tabs)
	for i := 0; i < tabs; i++ {
		t := bridge.TabSnap{ID: i, Alive: true}
		if i == active {
			t.Title = title
		} else {
			t.Title = fmt.Sprintf("tab %d", i)
		}
		tabSnaps = append(tabSnaps, t)
	}
	h.Publish(bridge.Snapshot{
		PID:       chromePID,
		Cols:      cols,
		Rows:      rows,
		ActiveTab: active,
		Tabs:      tabSnaps,
		Notes: []string{
			"ui=chrome",
			"bridge=chromehost proxy",
			"submit=chrome_submit mailbox",
			"workspace/notes=disk + refresh_workspace",
		},
	})
}

func notesBridgeFromDisk(off chrome.NotesBridgeResult) bridge.NotesResult {
	// Field-compatible shapes — JSON round-trip is the safest converter.
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
	t := time.NewTicker(500 * time.Millisecond)
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
