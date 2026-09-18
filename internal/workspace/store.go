// Package workspace is the local shared working area: channels, members,
// and messages that humans (suzuri UI) and AIs (suzuri MCP) both use.
package workspace

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/StephenSHorton/suzuri/internal/config"
)

const (
	DefaultChannel    = "general"
	maxBodyRunes      = 16 * 1024
	maxChannels       = 64
	maxMembers        = 128
	maxHistoryDefault = 50
	maxHistoryCap     = 200
	maxUploadBytes    = 64 << 20 // 64 MiB local attach cap
	staleWorkingAfter = 2 * time.Minute
	messagesFileName  = "messages.jsonl"
	metaFileName      = "meta.json"
	membersFileName   = "members.json"
	workspaceFile     = "workspace.json"
	filesDirName      = "files"
)

// Dir returns the workspace root under the suzuri config directory.
func Dir() string {
	return filepath.Join(config.Dir(), "workspace")
}

// MemberKind is who is posting.
type MemberKind string

const (
	KindHuman MemberKind = "human"
	KindAgent MemberKind = "agent"
)

// Availability is a member's presence in the shared room.
type Availability string

const (
	AvailIdle    Availability = "idle"    // present, not doing work
	AvailWorking Availability = "working" // busy on a task
	AvailWaiting Availability = "waiting" // blocked on human/agent reply
	AvailBlocked Availability = "blocked" // cannot proceed (error / missing info)
	AvailAway    Availability = "away"    // not watching the channel
)

// NormalizeAvailability maps free text to a known code (empty → idle).
func NormalizeAvailability(s string) Availability {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "idle", "online", "available", "ready":
		return AvailIdle
	case "working", "busy", "active", "in_progress", "in-progress":
		return AvailWorking
	case "waiting", "waiting_for", "waiting-for", "pending":
		return AvailWaiting
	case "blocked", "stuck", "error":
		return AvailBlocked
	case "away", "offline", "dnd", "brb":
		return AvailAway
	default:
		// Unknown codes still accepted as opaque short labels (truncated later).
		a := Availability(strings.ToLower(strings.TrimSpace(s)))
		if len(a) > 24 {
			a = a[:24]
		}
		return a
	}
}

// Member is a workspace participant.
type Member struct {
	ID        string     `json:"id"`
	Name      string     `json:"name"`
	Kind      MemberKind `json:"kind"`
	SessionID string     `json:"session_id,omitempty"`
	// Role is a claimed function (pm|engine|content). Distinct from Name.
	Role Role `json:"role,omitempty"`
	// Status is a short code (idle|working|waiting|blocked|away|custom).
	Status Availability `json:"status,omitempty"`
	// StatusNote is optional free text (e.g. "waiting on review from bob").
	StatusNote string    `json:"status_note,omitempty"`
	JoinedAt   time.Time `json:"joined_at"`
	LastSeen   time.Time `json:"last_seen"`
	// Polling / Stale / PresenceNote are computed on read (not a stored format).
	// working + last_seen > 2m → polling=false, stale=true, presence_note=not_polling.
	Polling      *bool  `json:"polling,omitempty"`
	Stale        bool   `json:"stale,omitempty"`
	PresenceNote string `json:"presence_note,omitempty"`
}

// Channel is a named room inside the workspace.
type Channel struct {
	ID        string    `json:"id"`   // slug without #
	Name      string    `json:"name"` // display, usually same as id
	CreatedAt time.Time `json:"created_at"`
	Topic     string    `json:"topic,omitempty"`
}

// FileRef is a file attached to a channel message (stored under the channel).
type FileRef struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Bytes   int64  `json:"bytes"`
	SHA256  string `json:"sha256,omitempty"`
	RelPath string `json:"rel_path"` // relative to workspace root
}

// Message is one post in a channel.
type Message struct {
	ID       string     `json:"id"`
	Channel  string     `json:"channel"`
	TS       time.Time  `json:"ts"`
	FromID   string     `json:"from_id"`
	FromName string     `json:"from_name"`
	FromKind MemberKind `json:"from_kind"`
	Kind     string     `json:"kind"` // text | system | file
	Body     string     `json:"body"`
	ReplyTo  string     `json:"reply_to,omitempty"`
	File     *FileRef   `json:"file,omitempty"`
	// Mentions are member ids resolved from @display-name tokens at post time.
	Mentions []string `json:"mentions,omitempty"`
	// Optional assignment / alias mention fields (read-only). Inbox reads these
	// if present alongside Mentions member ids.
	MentionIDs       []string `json:"mention_ids,omitempty"`
	MentionMemberIDs []string `json:"mention_member_ids,omitempty"`
	Assign           string   `json:"assign,omitempty"`
	AssignTo         string   `json:"assign_to,omitempty"`
	To               string   `json:"to,omitempty"`
	ToID             string   `json:"to_id,omitempty"`
}

// Meta is workspace-level metadata.
type Meta struct {
	ID        string    `json:"id"`
	Title     string    `json:"title"`
	CreatedAt time.Time `json:"created_at"`
}

// Store is a process-wide handle for the local workspace (file-backed).
type Store struct {
	mu  sync.Mutex
	dir string // empty → resolve Dir() on each use (respects config path / env)
}

// Default is the process-local store under Dir().
var Default = &Store{}

// New returns a store rooted at dir (created on first write).
// Pass empty dir to resolve config.Dir()/workspace dynamically.
func New(dir string) *Store {
	return &Store{dir: dir}
}

// Root returns the store directory.
func (s *Store) Root() string {
	if s.dir != "" {
		return s.dir
	}
	return Dir()
}

func (s *Store) rootLocked() string {
	return s.Root()
}

// Ensure initializes workspace.json, members, and #general if missing.
func (s *Store) Ensure() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.ensureLocked()
}

func (s *Store) ensureLocked() error {
	root := s.rootLocked()
	if err := os.MkdirAll(root, 0o755); err != nil {
		return err
	}
	metaPath := filepath.Join(root, workspaceFile)
	if _, err := os.Stat(metaPath); errors.Is(err, os.ErrNotExist) {
		meta := Meta{
			ID:        newID("ws"),
			Title:     "Suzuri workspace",
			CreatedAt: time.Now().UTC(),
		}
		if err := writeJSON(metaPath, meta); err != nil {
			return err
		}
	}
	membersPath := filepath.Join(root, membersFileName)
	if _, err := os.Stat(membersPath); errors.Is(err, os.ErrNotExist) {
		if err := writeJSON(membersPath, []Member{}); err != nil {
			return err
		}
	}
	return s.ensureChannelLocked(DefaultChannel, "")
}

func (s *Store) ensureChannelLocked(slug, topic string) error {
	slug = NormalizeChannel(slug)
	if slug == "" {
		return fmt.Errorf("invalid channel name")
	}
	root := s.rootLocked()
	chDir := filepath.Join(root, "channels", slug)
	if err := os.MkdirAll(chDir, 0o755); err != nil {
		return err
	}
	metaPath := filepath.Join(chDir, metaFileName)
	if _, err := os.Stat(metaPath); errors.Is(err, os.ErrNotExist) {
		ch := Channel{
			ID:        slug,
			Name:      slug,
			CreatedAt: time.Now().UTC(),
			Topic:     topic,
		}
		if err := writeJSON(metaPath, ch); err != nil {
			return err
		}
	}
	msgPath := filepath.Join(chDir, messagesFileName)
	if _, err := os.Stat(msgPath); errors.Is(err, os.ErrNotExist) {
		f, err := os.OpenFile(msgPath, os.O_CREATE|os.O_WRONLY, 0o644)
		if err != nil {
			return err
		}
		_ = f.Close()
	}
	return nil
}

// Status returns workspace path and basic counts.
func (s *Store) Status() (map[string]any, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.ensureLocked(); err != nil {
		return nil, err
	}
	meta, _ := s.readMetaLocked()
	chs, _ := s.listChannelsLocked()
	members, _ := s.readMembersLocked()
	return map[string]any{
		"ok":       true,
		"path":     s.rootLocked(),
		"id":       meta.ID,
		"title":    meta.Title,
		"channels": len(chs),
		"members":  len(members),
	}, nil
}

// ListChannels returns all channels.
func (s *Store) ListChannels() ([]Channel, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.ensureLocked(); err != nil {
		return nil, err
	}
	return s.listChannelsLocked()
}

func (s *Store) listChannelsLocked() ([]Channel, error) {
	root := filepath.Join(s.rootLocked(), "channels")
	entries, err := os.ReadDir(root)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var out []Channel
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		var ch Channel
		if err := readJSON(filepath.Join(root, e.Name(), metaFileName), &ch); err != nil {
			ch = Channel{ID: e.Name(), Name: e.Name()}
		}
		out = append(out, ch)
	}
	// Prefer general first.
	for i, ch := range out {
		if ch.ID == DefaultChannel {
			out[0], out[i] = out[i], out[0]
			break
		}
	}
	return out, nil
}

// CreateChannel creates a channel (idempotent if exists).
func (s *Store) CreateChannel(name, topic string) (Channel, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.ensureLocked(); err != nil {
		return Channel{}, err
	}
	chs, err := s.listChannelsLocked()
	if err != nil {
		return Channel{}, err
	}
	slug := NormalizeChannel(name)
	if slug == "" {
		return Channel{}, fmt.Errorf("invalid channel name")
	}
	for _, ch := range chs {
		if ch.ID == slug {
			return ch, nil
		}
	}
	if len(chs) >= maxChannels {
		return Channel{}, fmt.Errorf("channel limit reached (%d)", maxChannels)
	}
	if err := s.ensureChannelLocked(slug, topic); err != nil {
		return Channel{}, err
	}
	var ch Channel
	_ = readJSON(filepath.Join(s.rootLocked(), "channels", slug, metaFileName), &ch)
	return ch, nil
}

// DeleteChannel removes a channel directory (meta, messages.jsonl, files/).
// #general cannot be deleted. Returns the slug that was removed.
func (s *Store) DeleteChannel(name string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.ensureLocked(); err != nil {
		return "", err
	}
	slug := NormalizeChannel(name)
	if slug == "" {
		return "", fmt.Errorf("invalid channel name")
	}
	if slug == DefaultChannel {
		return "", fmt.Errorf("cannot delete #%s", DefaultChannel)
	}
	dir := filepath.Join(s.rootLocked(), "channels", slug)
	if _, err := os.Stat(dir); err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("channel %q not found", slug)
		}
		return "", err
	}
	if err := os.RemoveAll(dir); err != nil {
		return "", err
	}
	return slug, nil
}

// Join registers or updates a member.
// A member is reused only when sessionID is non-empty and matches an existing
// session_id. An empty sessionID always creates a new member. If the display
// name is taken, a numeric suffix is added (engine, engine-2).
// Join does not post a system line to #general.
func (s *Store) Join(name string, kind MemberKind, sessionID string) (Member, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.ensureLocked(); err != nil {
		return Member{}, err
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return Member{}, fmt.Errorf("name required")
	}
	if kind != KindHuman && kind != KindAgent {
		kind = KindAgent
	}
	sessionID = strings.TrimSpace(sessionID)
	members, err := s.readMembersLocked()
	if err != nil {
		return Member{}, err
	}
	now := time.Now().UTC()
	// Reuse only on a non-empty session_id match. Never merge on display name.
	if sessionID != "" {
		for i, m := range members {
			if m.SessionID != "" && m.SessionID == sessionID {
				members[i].Name = uniqueDisplayName(name, members, m.ID)
				members[i].Kind = kind
				members[i].LastSeen = now
				if members[i].Status == "" {
					members[i].Status = AvailIdle
				}
				if err := s.writeMembersLocked(members); err != nil {
					return Member{}, err
				}
				return members[i], nil
			}
		}
	}
	if len(members) >= maxMembers {
		return Member{}, fmt.Errorf("member limit reached (%d)", maxMembers)
	}
	m := Member{
		ID:        newID("m"),
		Name:      uniqueDisplayName(name, members, ""),
		Kind:      kind,
		SessionID: sessionID,
		Status:    AvailIdle,
		JoinedAt:  now,
		LastSeen:  now,
	}
	members = append(members, m)
	if err := s.writeMembersLocked(members); err != nil {
		return Member{}, err
	}
	return m, nil
}

// SetStatus updates a member's availability. Identify by memberID or name.
// note is optional; pass nil to leave note unchanged, empty string to clear.
func (s *Store) SetStatus(memberID, name string, status Availability, note *string) (Member, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.ensureLocked(); err != nil {
		return Member{}, err
	}
	status = NormalizeAvailability(string(status))
	members, err := s.readMembersLocked()
	if err != nil {
		return Member{}, err
	}
	now := time.Now().UTC()
	for i, m := range members {
		if (memberID != "" && m.ID == memberID) || (name != "" && m.Name == name && memberID == "") {
			members[i].Status = status
			members[i].LastSeen = now
			if note != nil {
				n := strings.TrimSpace(*note)
				if utf8.RuneCountInString(n) > 120 {
					rs := []rune(n)
					n = string(rs[:120])
				}
				members[i].StatusNote = n
			}
			if err := s.writeMembersLocked(members); err != nil {
				return Member{}, err
			}
			return members[i], nil
		}
	}
	return Member{}, fmt.Errorf("member not found")
}

// Leave removes a member by id or name.
func (s *Store) Leave(memberID, name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.ensureLocked(); err != nil {
		return err
	}
	members, err := s.readMembersLocked()
	if err != nil {
		return err
	}
	var left *Member
	out := members[:0]
	for _, m := range members {
		if (memberID != "" && m.ID == memberID) || (name != "" && m.Name == name && memberID == "") {
			cp := m
			left = &cp
			continue
		}
		out = append(out, m)
	}
	if left == nil {
		return fmt.Errorf("member not found")
	}
	if err := s.writeMembersLocked(out); err != nil {
		return err
	}
	return nil
}

// Members returns the member list with computed stale-presence fields.
func (s *Store) Members() ([]Member, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.ensureLocked(); err != nil {
		return nil, err
	}
	list, err := s.readMembersLocked()
	if err != nil {
		return nil, err
	}
	return annotatePresence(list, time.Now().UTC()), nil
}

// Post appends a text message. memberID optional if name+kind identify the poster.
func (s *Store) Post(channel, body, memberID, name string, kind MemberKind, replyTo string) (Message, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.ensureLocked(); err != nil {
		return Message{}, err
	}
	body = strings.TrimSpace(body)
	if body == "" {
		return Message{}, fmt.Errorf("body required")
	}
	if utf8.RuneCountInString(body) > maxBodyRunes {
		return Message{}, fmt.Errorf("body too long (max %d runes)", maxBodyRunes)
	}
	channel = NormalizeChannel(channel)
	if channel == "" {
		channel = DefaultChannel
	}
	if err := s.ensureChannelLocked(channel, ""); err != nil {
		return Message{}, err
	}
	from, err := s.resolveMemberLocked(memberID, name, kind)
	if err != nil {
		return Message{}, err
	}
	from.LastSeen = time.Now().UTC()
	_ = s.touchMemberLocked(from)
	return s.postLocked(channel, from, "text", body, replyTo, nil)
}

func (s *Store) postLocked(channel string, from Member, kind, body, replyTo string, file *FileRef) (Message, error) {
	var mentions []string
	if kind != "system" && body != "" {
		if mems, err := s.readMembersLocked(); err == nil {
			mentions = ResolveMentions(body, mems)
		}
	}
	msg := Message{
		ID:       newID("msg"),
		Channel:  channel,
		TS:       time.Now().UTC(),
		FromID:   from.ID,
		FromName: from.Name,
		FromKind: from.Kind,
		Kind:     kind,
		Body:     body,
		ReplyTo:  replyTo,
		File:     file,
		Mentions: mentions,
	}
	path := filepath.Join(s.rootLocked(), "channels", channel, messagesFileName)
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return Message{}, err
	}
	defer f.Close()
	raw, err := json.Marshal(msg)
	if err != nil {
		return Message{}, err
	}
	if _, err := f.Write(append(raw, '\n')); err != nil {
		return Message{}, err
	}
	return msg, nil
}

// History returns the last n messages in a channel (oldest first).
func (s *Store) History(channel string, limit int) ([]Message, error) {
	return s.HistorySince(channel, limit, "", time.Time{})
}

// HistorySince returns messages after sinceID and/or afterTS (exclusive).
// Without a cursor this is the last `limit` messages (existing behavior).
// With a cursor, only newer messages are returned (oldest first, still capped).
func (s *Store) HistorySince(channel string, limit int, sinceID string, afterTS time.Time) ([]Message, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.ensureLocked(); err != nil {
		return nil, err
	}
	channel = NormalizeChannel(channel)
	if channel == "" {
		channel = DefaultChannel
	}
	if err := s.ensureChannelLocked(channel, ""); err != nil {
		return nil, err
	}
	all, err := s.readAllMessagesLocked(channel)
	if err != nil {
		return nil, err
	}
	incremental := sinceID != "" || !afterTS.IsZero()
	all = filterAfterCursor(all, sinceID, afterTS)
	return clampHistory(all, limit, incremental), nil
}

// readAllMessages is the unlocked-entry wrapper around readAllMessagesLocked.
func (s *Store) readAllMessages(channel string) ([]Message, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.ensureLocked(); err != nil {
		return nil, err
	}
	channel = NormalizeChannel(channel)
	if channel == "" {
		channel = DefaultChannel
	}
	if err := s.ensureChannelLocked(channel, ""); err != nil {
		return nil, err
	}
	return s.readAllMessagesLocked(channel)
}

func filterAfterCursor(all []Message, sinceID string, afterTS time.Time) []Message {
	if sinceID == "" && afterTS.IsZero() {
		return all
	}
	out := make([]Message, 0, len(all))
	seenID := sinceID == ""
	for _, m := range all {
		if !seenID {
			if m.ID == sinceID {
				seenID = true
			}
			continue
		}
		if !afterTS.IsZero() && !m.TS.After(afterTS) {
			continue
		}
		out = append(out, m)
	}
	return out
}

func clampHistory(all []Message, limit int, incremental bool) []Message {
	if limit <= 0 {
		limit = maxHistoryDefault
	}
	if limit > maxHistoryCap {
		limit = maxHistoryCap
	}
	if len(all) <= limit {
		return all
	}
	if incremental {
		return all[:limit]
	}
	return all[len(all)-limit:]
}

// Touch updates last_seen for a member (wait/inbox heartbeat).
func (s *Store) Touch(memberID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.ensureLocked(); err != nil {
		return err
	}
	memberID = strings.TrimSpace(memberID)
	if memberID == "" {
		return fmt.Errorf("member_id required")
	}
	members, err := s.readMembersLocked()
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	for i, m := range members {
		if m.ID == memberID || strings.EqualFold(m.Name, memberID) {
			members[i].LastSeen = now
			return s.writeMembersLocked(members)
		}
	}
	return fmt.Errorf("member not found")
}

func annotatePresence(members []Member, now time.Time) []Member {
	out := make([]Member, len(members))
	for i, m := range members {
		stale := m.Status == AvailWorking && (now.Sub(m.LastSeen) > staleWorkingAfter || m.LastSeen.IsZero())
		polling := !stale
		m.Polling = &polling
		if stale {
			m.Stale = true
			m.PresenceNote = "not_polling"
		} else {
			m.Stale = false
			m.PresenceNote = ""
		}
		out[i] = m
	}
	return out
}

func stripPresenceView(members []Member) []Member {
	out := make([]Member, len(members))
	for i, m := range members {
		m.Polling = nil
		m.Stale = false
		m.PresenceNote = ""
		out[i] = m
	}
	return out
}

func (s *Store) resolveMemberLocked(id, name string, kind MemberKind) (Member, error) {
	members, err := s.readMembersLocked()
	if err != nil {
		return Member{}, err
	}
	if id != "" {
		for _, m := range members {
			if m.ID == id {
				return m, nil
			}
		}
		return Member{}, fmt.Errorf("member id not found; call workspace_join first")
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return Member{}, fmt.Errorf("name or member_id required")
	}
	if kind == "" {
		kind = KindAgent
	}
	// Never merge on display name. Auto-join always creates a new member
	// (suffix if the name is taken). Empty session_id never fuses.
	if len(members) >= maxMembers {
		return Member{}, fmt.Errorf("member limit reached (%d)", maxMembers)
	}
	now := time.Now().UTC()
	m := Member{
		ID:       newID("m"),
		Name:     uniqueDisplayName(name, members, ""),
		Kind:     kind,
		Status:   AvailIdle,
		JoinedAt: now,
		LastSeen: now,
	}
	members = append(members, m)
	if err := s.writeMembersLocked(members); err != nil {
		return Member{}, err
	}
	return m, nil
}

func (s *Store) touchMemberLocked(m Member) error {
	members, err := s.readMembersLocked()
	if err != nil {
		return err
	}
	for i := range members {
		if members[i].ID == m.ID {
			members[i].LastSeen = m.LastSeen
			members[i].Name = m.Name
			return s.writeMembersLocked(members)
		}
	}
	return nil
}

func (s *Store) readMetaLocked() (Meta, error) {
	var meta Meta
	err := readJSON(filepath.Join(s.rootLocked(), workspaceFile), &meta)
	return meta, err
}

func (s *Store) readMembersLocked() ([]Member, error) {
	var members []Member
	path := filepath.Join(s.rootLocked(), membersFileName)
	if err := readJSON(path, &members); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return []Member{}, nil
		}
		return nil, err
	}
	if members == nil {
		members = []Member{}
	}
	return members, nil
}

func (s *Store) writeMembersLocked(members []Member) error {
	return writeJSON(filepath.Join(s.rootLocked(), membersFileName), stripPresenceView(members))
}

// NormalizeChannel turns "#Fix Auth" into "fix-auth".
func NormalizeChannel(s string) string {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "#")
	s = strings.ToLower(s)
	var b strings.Builder
	prevDash := false
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			prevDash = false
		case r == ' ' || r == '_' || r == '-' || r == '.':
			if !prevDash && b.Len() > 0 {
				b.WriteByte('-')
				prevDash = true
			}
		}
	}
	out := strings.Trim(b.String(), "-")
	if len(out) > 48 {
		out = out[:48]
		out = strings.Trim(out, "-")
	}
	return out
}

func newID(prefix string) string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return prefix + "_" + hex.EncodeToString(b[:])
}

func writeJSON(path string, v any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	raw = append(raw, '\n')
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o644); err != nil {
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

func readJSON(path string, v any) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, v)
}
