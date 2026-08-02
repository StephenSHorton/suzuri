// Package bridge connects a live suzuri GUI to a spawn-on-demand stdio MCP
// process over loopback HTTP. The GUI is user-owned; the MCP process is not a
// daemon — agents spawn it, it talks to the running window, then exits.
package bridge

import "time"

// Endpoint is written by the GUI and read by the MCP stdio process.
type Endpoint struct {
	PID   int    `json:"pid"`
	URL   string `json:"url"`   // e.g. http://127.0.0.1:PORT
	Token string `json:"token"` // bearer for loopback auth
}

// Snapshot is a structured view of the host for agents (not a screenshot).
type Snapshot struct {
	PID       int       `json:"pid"`
	UpdatedAt time.Time `json:"updated_at"`
	Cols      int       `json:"cols"`
	Rows      int       `json:"rows"`
	ActiveTab int       `json:"active_tab"`
	Tabs      []TabSnap `json:"tabs"`
	Notes     []string  `json:"notes,omitempty"`
}

// TabSnap is one shell tab.
type TabSnap struct {
	ID        int      `json:"id"`
	Title     string   `json:"title"`
	Alive     bool     `json:"alive"`
	Shell     string   `json:"shell,omitempty"`
	Input     string   `json:"input"` // Warp bar draft
	AltScreen bool     `json:"alt_screen"` // full-screen TUI owns keyboard; bar hidden
	Echo      EchoStat `json:"echo"`
	LiveLines []string `json:"live_lines"` // effective (non-trailing-blank) live text
	Viewport  []string `json:"viewport"`  // what the user sees (history+live, text)
	Blocks    []Block  `json:"blocks"`    // recent command blocks
	History   []HLine  `json:"history_tail"`
	PtyTail   string   `json:"pty_tail"` // recent raw PTY bytes, Go-quoted
}

// EchoStat is the command-echo suppressor state.
type EchoStat struct {
	Armed bool   `json:"armed"`
	Cmd   string `json:"cmd,omitempty"`
	Phase int    `json:"phase"`
}

// Block is a host-injected command header in scrollback.
type Block struct {
	Command string `json:"command"`
}

// HLine is a scrollback history line with kind metadata.
type HLine struct {
	Text string `json:"text"`
	Kind string `json:"kind"` // normal | rule | cmd
}

// SubmitRequest is POST /v1/submit body.
type SubmitRequest struct {
	TabID *int   `json:"tab_id,omitempty"` // nil = active tab
	Line  string `json:"line"`
}

// SubmitResult is the response from submit.
type SubmitResult struct {
	OK    bool   `json:"ok"`
	TabID int    `json:"tab_id"`
	Line  string `json:"line"`
	Error string `json:"error,omitempty"`
}

// Status is a cheap liveness probe.
type Status struct {
	OK      bool   `json:"ok"`
	PID     int    `json:"pid"`
	Tabs    int    `json:"tabs"`
	Active  int    `json:"active_tab"`
	Bridge  string `json:"bridge"`
	Message string `json:"message,omitempty"`
}


// LogsResult is a tail of %LOCALAPPDATA%\suzuri\suzuri.log.
type LogsResult struct {
	OK    bool     `json:"ok"`
	Path  string   `json:"path"`
	Lines []string `json:"lines"`
	Count int      `json:"count"`
	Error string   `json:"error,omitempty"`
}
