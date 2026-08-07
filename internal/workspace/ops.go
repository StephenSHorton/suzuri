package workspace

import (
	"fmt"
	"path/filepath"
	"strings"
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
	OpUpload        Op = "upload"
	OpDownload      Op = "download"
	OpSetStatus     Op = "set_status" // member availability (idle|working|waiting|blocked|away)
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
	// FilePath is a local path for upload (source file).
	FilePath string `json:"file_path,omitempty"`
	// FileID is a stored file id (or message id) for download.
	FileID string `json:"file_id,omitempty"`
	// Status is availability for OpSetStatus (idle|working|waiting|blocked|away|custom).
	Status string `json:"status,omitempty"`
	// StatusNote is optional free text for OpSetStatus.
	// Nil = leave note unchanged; non-nil (incl. empty) = set/clear.
	StatusNote *string `json:"status_note,omitempty"`
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
	LocalPath string `json:"local_path,omitempty"`
	Count     int   `json:"count,omitempty"`
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
		return Result{OK: true, Path: path, Member: &m}

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
		list, err := s.History(req.Channel, req.Limit)
		if err != nil {
			return Result{OK: false, Path: path, Error: err.Error()}
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
