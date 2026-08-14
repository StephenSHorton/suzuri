package workspace

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"
)

// Op is a workspace operation for bridge/MCP.
type Op string

const (
	OpStatus        Op = "status"
	OpJoin          Op = "join"
	OpLeave         Op = "leave"
	OpMembers       Op = "members"
	OpChannels      Op = "channels"
	OpChannelCreate Op = "channel_create"
	OpChannelDelete Op = "channel_delete"
	OpPost          Op = "post"
	OpHistory       Op = "history"
	OpWait          Op = "wait"
	OpInbox         Op = "inbox"
	OpUpload        Op = "upload"
	OpDownload      Op = "download"
	OpSetStatus     Op = "set_status" // member availability (idle|working|waiting|blocked|away)
	OpClaimRole     Op = "claim_role" // exclusive role: pm|engine|content
	OpTaskCreate    Op = "task_create"
	OpTaskList      Op = "task_list"
	OpTaskClaim     Op = "task_claim"
	OpTaskAssign    Op = "assign"
	OpTaskSetStatus Op = "task_set_status"
	OpLease         Op = "lease"
	OpLeaseList     Op = "lease_list"
)

// Request is the unified workspace RPC body.
type Request struct {
	Op        Op     `json:"op"`
	Channel   string `json:"channel,omitempty"`
	Body      string `json:"body,omitempty"`
	Name      string `json:"name,omitempty"`
	Kind      string `json:"kind,omitempty"` // human | agent
	MemberID  string `json:"member_id,omitempty"`
	SessionID string `json:"session_id,omitempty"`
	ReplyTo   string `json:"reply_to,omitempty"`
	Topic     string `json:"topic,omitempty"`
	Limit     int    `json:"limit,omitempty"`
	// SinceID is a history/inbox cursor: return messages after this id.
	SinceID string `json:"since_id,omitempty"`
	// AfterTS is an RFC3339 timestamp cursor for history (exclusive).
	AfterTS string `json:"after_ts,omitempty"`
	// Since is the wait cursor (message id). Alias of since_id for workspace_wait.
	Since string `json:"since,omitempty"`
	// Timeout is wait timeout in seconds (default/max 60).
	Timeout int `json:"timeout,omitempty"`
	// FilePath is a local path for upload (source file).
	FilePath string `json:"file_path,omitempty"`
	// FileID is a stored file id (or message id) for download.
	FileID string `json:"file_id,omitempty"`
	// Status is availability for OpSetStatus (idle|working|waiting|blocked|away|custom).
	Status string `json:"status,omitempty"`
	// StatusNote is optional free text for OpSetStatus.
	// Nil = leave note unchanged; non-nil (incl. empty) = set/clear.
	StatusNote *string `json:"status_note,omitempty"`
	// Role is for OpClaimRole (pm|engine|content|"").
	Role string `json:"role,omitempty"`
	// TaskID is E7 / C1 / etc. for task ops (alias of MCP "task").
	TaskID string `json:"task_id,omitempty"`
	// Title for task_create.
	Title string `json:"title,omitempty"`
	// Files are paths a task may touch.
	Files []string `json:"files,omitempty"`
	// TaskStatus for task_set_status (todo|claimed|done|blocked).
	TaskStatus string `json:"task_status,omitempty"`
	// Path is a workspace-relative file path for lease.
	Path string `json:"path,omitempty"`
	// TTL is a duration string (e.g. 10m) for lease.
	TTL string `json:"ttl,omitempty"`
	// Steal allows taking an existing path lease.
	Steal bool `json:"steal,omitempty"`
}

// Result is a JSON-friendly workspace response.
type Result struct {
	OK       bool           `json:"ok"`
	Path     string         `json:"path,omitempty"`
	Error    string         `json:"error,omitempty"`
	Status   map[string]any `json:"status,omitempty"`
	Member   *Member        `json:"member,omitempty"`
	Members  []Member       `json:"members,omitempty"`
	Channel  *Channel       `json:"channel,omitempty"`
	Channels []Channel      `json:"channels,omitempty"`
	Message  *Message       `json:"message,omitempty"`
	Messages []Message      `json:"messages,omitempty"`
	File     *FileRef       `json:"file,omitempty"`
	// LocalPath is the absolute path for download/upload destination.
	LocalPath string  `json:"local_path,omitempty"`
	Count     int     `json:"count,omitempty"`
	// SessionID / MemberID are set on join so agents do not have to dig into member.
	SessionID string  `json:"session_id,omitempty"`
	MemberID  string  `json:"member_id,omitempty"`
	Task      *Task   `json:"task,omitempty"`
	Tasks     []Task  `json:"tasks,omitempty"`
	Lease     *Lease  `json:"lease,omitempty"`
	Leases    []Lease `json:"leases,omitempty"`
}

// Apply runs req against Default (or s if non-nil).
func Apply(s *Store, req Request) Result {
	if s == nil {
		s = Default
	}
	path := s.Root()
	op := Op(strings.ToLower(string(req.Op)))
	if op == "" {
		op = OpStatus
	}
	switch op {
	case OpStatus:
		st, err := s.Status()
		if err != nil {
			return Result{OK: false, Path: path, Error: err.Error()}
		}
		return Result{OK: true, Path: path, Status: st}

	case OpJoin:
		kind := MemberKind(strings.ToLower(req.Kind))
		if kind == "" {
			kind = KindAgent
		}
		m, err := s.Join(req.Name, kind, req.SessionID)
		if err != nil {
			return Result{OK: false, Path: path, Error: err.Error()}
		}
		return Result{OK: true, Path: path, Member: &m, SessionID: m.SessionID, MemberID: m.ID}

	case OpLeave:
		if err := s.Leave(req.MemberID, req.Name); err != nil {
			return Result{OK: false, Path: path, Error: err.Error()}
		}
		return Result{OK: true, Path: path}

	case OpSetStatus:
		m, err := s.SetStatus(req.MemberID, req.Name, Availability(req.Status), req.StatusNote)
		if err != nil {
			return Result{OK: false, Path: path, Error: err.Error()}
		}
		return Result{OK: true, Path: path, Member: &m}

	case OpClaimRole:
		m, err := s.ClaimRole(req.MemberID, req.Role)
		if err != nil {
			return Result{OK: false, Path: path, Error: err.Error()}
		}
		return Result{OK: true, Path: path, Member: &m}

	case OpMembers:
		list, err := s.Members()
		if err != nil {
			return Result{OK: false, Path: path, Error: err.Error()}
		}
		return Result{OK: true, Path: path, Members: list, Count: len(list)}

	case OpChannels:
		list, err := s.ListChannels()
		if err != nil {
			return Result{OK: false, Path: path, Error: err.Error()}
		}
		return Result{OK: true, Path: path, Channels: list, Count: len(list)}

	case OpChannelCreate:
		ch, err := s.CreateChannel(req.Channel, req.Topic)
		if err != nil {
			// Allow name field as alias for channel create.
			if req.Channel == "" && req.Name != "" {
				ch, err = s.CreateChannel(req.Name, req.Topic)
			}
		}
		if err != nil {
			return Result{OK: false, Path: path, Error: err.Error()}
		}
		return Result{OK: true, Path: path, Channel: &ch}

	case OpChannelDelete:
		name := req.Channel
		if name == "" {
			name = req.Name
		}
		slug, err := s.DeleteChannel(name)
		if err != nil {
			return Result{OK: false, Path: path, Error: err.Error()}
		}
		return Result{OK: true, Path: path, Status: map[string]any{
			"deleted": slug,
			"note":    "channel directory removed (messages + files)",
		}}

	case OpPost:
		kind := MemberKind(strings.ToLower(req.Kind))
		if kind == "" {
			kind = KindAgent
		}
		msg, err := s.Post(req.Channel, req.Body, req.MemberID, req.Name, kind, req.ReplyTo)
		if err != nil {
			return Result{OK: false, Path: path, Error: err.Error()}
		}
		return Result{OK: true, Path: path, Message: &msg}

	case OpHistory:
		var after time.Time
		if strings.TrimSpace(req.AfterTS) != "" {
			t, err := parseAfterTS(req.AfterTS)
			if err != nil {
				return Result{OK: false, Path: path, Error: err.Error()}
			}
			after = t
		}
		sinceID := strings.TrimSpace(req.SinceID)
		if sinceID == "" {
			sinceID = strings.TrimSpace(req.Since)
		}
		list, err := s.HistorySince(req.Channel, req.Limit, sinceID, after)
		if err != nil {
			return Result{OK: false, Path: path, Error: err.Error()}
		}
		if list == nil {
			list = []Message{}
		}
		return Result{OK: true, Path: path, Messages: list, Count: len(list)}

	case OpWait:
		sinceID := strings.TrimSpace(req.Since)
		if sinceID == "" {
			sinceID = strings.TrimSpace(req.SinceID)
		}
		if req.MemberID != "" {
			_ = s.Touch(req.MemberID)
		}
		timeout := time.Duration(req.Timeout) * time.Second
		list, err := s.Wait(req.Channel, sinceID, timeout)
		if err != nil {
			return Result{OK: false, Path: path, Error: err.Error()}
		}
		if list == nil {
			list = []Message{}
		}
		return Result{OK: true, Path: path, Messages: list, Count: len(list)}

	case OpInbox:
		if req.MemberID != "" {
			_ = s.Touch(req.MemberID)
		}
		sinceID := strings.TrimSpace(req.SinceID)
		if sinceID == "" {
			sinceID = strings.TrimSpace(req.Since)
		}
		list, err := s.Inbox(req.MemberID, sinceID, req.Limit)
		if err != nil {
			return Result{OK: false, Path: path, Error: err.Error()}
		}
		if list == nil {
			list = []Message{}
		}
		return Result{OK: true, Path: path, Messages: list, Count: len(list)}

	case OpUpload:
		kind := MemberKind(strings.ToLower(req.Kind))
		if kind == "" {
			kind = KindAgent
		}
		msg, err := s.Upload(req.Channel, req.FilePath, req.MemberID, req.Name, kind, req.Body)
		if err != nil {
			return Result{OK: false, Path: path, Error: err.Error()}
		}
		out := Result{OK: true, Path: path, Message: &msg}
		if msg.File != nil {
			out.File = msg.File
			out.LocalPath = filepathJoinRoot(s, msg.File.RelPath)
		}
		return out

	case OpDownload:
		abs, ref, err := s.ResolveFile(req.Channel, req.FileID)
		if err != nil {
			return Result{OK: false, Path: path, Error: err.Error()}
		}
		return Result{OK: true, Path: path, File: &ref, LocalPath: abs}

	case OpTaskCreate:
		t, err := s.CreateTask(req.Title, req.TaskID, req.Files, req.Channel)
		if err != nil {
			return Result{OK: false, Path: path, Error: err.Error()}
		}
		return Result{OK: true, Path: path, Task: &t}

	case OpTaskList:
		list, err := s.ListTasks()
		if err != nil {
			return Result{OK: false, Path: path, Error: err.Error()}
		}
		return Result{OK: true, Path: path, Tasks: list, Count: len(list)}

	case OpTaskClaim:
		t, err := s.ClaimTask(req.TaskID, req.MemberID, req.Channel)
		if err != nil {
			return Result{OK: false, Path: path, Error: err.Error()}
		}
		return Result{OK: true, Path: path, Task: &t}

	case OpTaskAssign:
		t, err := s.AssignTask(req.TaskID, req.MemberID, req.Channel)
		if err != nil {
			return Result{OK: false, Path: path, Error: err.Error()}
		}
		return Result{OK: true, Path: path, Task: &t}

	case OpTaskSetStatus:
		t, err := s.SetTaskStatus(req.TaskID, TaskStatus(req.TaskStatus), req.Channel)
		if err != nil {
			return Result{OK: false, Path: path, Error: err.Error()}
		}
		return Result{OK: true, Path: path, Task: &t}

	case OpLease:
		leasePath := req.Path
		if leasePath == "" {
			leasePath = req.FilePath
		}
		l, err := s.AcquireLease(leasePath, req.MemberID, req.TTL, req.Steal, req.Channel)
		if err != nil {
			return Result{OK: false, Path: path, Error: err.Error()}
		}
		return Result{OK: true, Path: path, Lease: &l}

	case OpLeaseList:
		list, err := s.ListLeases()
		if err != nil {
			return Result{OK: false, Path: path, Error: err.Error()}
		}
		return Result{OK: true, Path: path, Leases: list, Count: len(list)}

	default:
		return Result{
			OK:    false,
			Path:  path,
			Error: fmt.Sprintf("unknown workspace op %q", req.Op),
		}
	}
}

func filepathJoinRoot(s *Store, rel string) string {
	return filepath.Join(s.Root(), filepath.FromSlash(rel))
}

func parseAfterTS(s string) (time.Time, error) {
	s = strings.TrimSpace(s)
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339} {
		if t, err := time.Parse(layout, s); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("invalid after_ts %q (use RFC3339)", s)
}
