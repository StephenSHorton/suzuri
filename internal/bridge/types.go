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

// NotesOp is the notes bank action for POST /v1/notes.
type NotesOp string

const (
	NotesOpList   NotesOp = "list"
	NotesOpGet    NotesOp = "get"
	NotesOpCreate NotesOp = "create"
	NotesOpUpdate NotesOp = "update"
	NotesOpDelete NotesOp = "delete"
)

// NotesRequest mutates or reads the suzuri notes bank (Ctrl+Shift+M).
type NotesRequest struct {
	Op        NotesOp `json:"op"`
	ID        string  `json:"id,omitempty"`         // empty = active note for get/update/delete
	Title     *string `json:"title,omitempty"`      // create: optional; update: nil=leave
	Body      *string `json:"body,omitempty"`       // create: optional; update: nil=leave
	SetActive bool    `json:"set_active,omitempty"` // after create/update, make this note active
}

// NoteItem is one note for agents (full body included).
type NoteItem struct {
	ID      string    `json:"id"`
	Title   string    `json:"title"` // display title (stored or derived)
	Body    string    `json:"body"`
	Updated time.Time `json:"updated"`
	Active  bool      `json:"active,omitempty"`
}

// NotesResult is the response from notes list/get/create/update/delete.
type NotesResult struct {
	OK    bool       `json:"ok"`
	Path  string     `json:"path,omitempty"`
	Note  *NoteItem  `json:"note,omitempty"`
	Notes []NoteItem `json:"notes,omitempty"`
	Count int        `json:"count,omitempty"`
	Error string     `json:"error,omitempty"`
}

// WorkspaceOp is the workspace action for POST /v1/workspace.
type WorkspaceOp string

const (
	WorkspaceOpStatus        WorkspaceOp = "status"
	WorkspaceOpJoin          WorkspaceOp = "join"
	WorkspaceOpLeave         WorkspaceOp = "leave"
	WorkspaceOpMembers       WorkspaceOp = "members"
	WorkspaceOpChannels      WorkspaceOp = "channels"
	WorkspaceOpChannelCreate WorkspaceOp = "channel_create"
	WorkspaceOpPost          WorkspaceOp = "post"
	WorkspaceOpHistory       WorkspaceOp = "history"
	WorkspaceOpUpload        WorkspaceOp = "upload"
	WorkspaceOpDownload      WorkspaceOp = "download"
)

// WorkspaceRequest mutates or reads the shared workspace (channels / messages).
type WorkspaceRequest struct {
	Op        WorkspaceOp `json:"op"`
	Channel   string      `json:"channel,omitempty"`
	Body      string      `json:"body,omitempty"`
	Name      string      `json:"name,omitempty"`
	Kind      string      `json:"kind,omitempty"` // human | agent
	MemberID  string      `json:"member_id,omitempty"`
	SessionID string      `json:"session_id,omitempty"`
	ReplyTo   string      `json:"reply_to,omitempty"`
	Topic     string      `json:"topic,omitempty"`
	Limit     int         `json:"limit,omitempty"`
	FilePath  string      `json:"file_path,omitempty"` // local source for upload
	FileID    string      `json:"file_id,omitempty"`   // stored file or message id for download
}

// WorkspaceMember is one human or agent in the workspace.
type WorkspaceMember struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Kind      string    `json:"kind"`
	SessionID string    `json:"session_id,omitempty"`
	JoinedAt  time.Time `json:"joined_at"`
	LastSeen  time.Time `json:"last_seen"`
}

// WorkspaceChannel is a named room.
type WorkspaceChannel struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"created_at"`
	Topic     string    `json:"topic,omitempty"`
}

// WorkspaceFile is an attachment on a message.
type WorkspaceFile struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Bytes   int64  `json:"bytes"`
	SHA256  string `json:"sha256,omitempty"`
	RelPath string `json:"rel_path"`
}

// WorkspaceMessage is one post.
type WorkspaceMessage struct {
	ID       string         `json:"id"`
	Channel  string         `json:"channel"`
	TS       time.Time      `json:"ts"`
	FromID   string         `json:"from_id"`
	FromName string         `json:"from_name"`
	FromKind string         `json:"from_kind"`
	Kind     string         `json:"kind"`
	Body     string         `json:"body"`
	ReplyTo  string         `json:"reply_to,omitempty"`
	File     *WorkspaceFile `json:"file,omitempty"`
}

// WorkspaceResult is the response from workspace ops.
type WorkspaceResult struct {
	OK        bool               `json:"ok"`
	Path      string             `json:"path,omitempty"`
	Error     string             `json:"error,omitempty"`
	Status    map[string]any     `json:"status,omitempty"`
	Member    *WorkspaceMember   `json:"member,omitempty"`
	Members   []WorkspaceMember  `json:"members,omitempty"`
	Channel   *WorkspaceChannel  `json:"channel,omitempty"`
	Channels  []WorkspaceChannel `json:"channels,omitempty"`
	Message   *WorkspaceMessage  `json:"message,omitempty"`
	Messages  []WorkspaceMessage `json:"messages,omitempty"`
	File      *WorkspaceFile     `json:"file,omitempty"`
	LocalPath string             `json:"local_path,omitempty"`
	Count     int                `json:"count,omitempty"`
}
