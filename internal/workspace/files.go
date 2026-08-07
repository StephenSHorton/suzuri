package workspace

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Upload copies srcPath into the channel files dir and posts a file message.
// caption is optional body text (defaults to the file name).
func (s *Store) Upload(channel, srcPath, memberID, name string, kind MemberKind, caption string) (Message, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.ensureLocked(); err != nil {
		return Message{}, err
	}
	srcPath = strings.TrimSpace(srcPath)
	if srcPath == "" {
		return Message{}, fmt.Errorf("path required")
	}
	if strings.HasPrefix(srcPath, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			srcPath = filepath.Join(home, srcPath[2:])
		}
	}
	srcPath = filepath.Clean(srcPath)
	info, err := os.Stat(srcPath)
	if err != nil {
		return Message{}, fmt.Errorf("stat: %w", err)
	}
	if info.IsDir() {
		return Message{}, fmt.Errorf("path is a directory (files only for now)")
	}
	if info.Size() > maxUploadBytes {
		return Message{}, fmt.Errorf("file too large (max %d bytes)", maxUploadBytes)
	}
	channel = NormalizeChannel(channel)
	if channel == "" {
		channel = DefaultChannel
	}
	if err := s.ensureChannelLocked(channel, ""); err != nil {
		return Message{}, err
	}
	if kind == "" {
		kind = KindAgent
	}
	from, err := s.resolveMemberLocked(memberID, name, kind)
	if err != nil {
		return Message{}, err
	}
	from.LastSeen = time.Now().UTC()
	_ = s.touchMemberLocked(from)

	fileID := newID("f")
	base := filepath.Base(srcPath)
	safe := sanitizeFileName(base)
	rel := filepath.ToSlash(filepath.Join("channels", channel, filesDirName, fileID+"_"+safe))
	dst := filepath.Join(s.rootLocked(), filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return Message{}, err
	}
	sum, n, err := copyFileSHA256(srcPath, dst)
	if err != nil {
		_ = os.Remove(dst)
		return Message{}, err
	}
	ref := &FileRef{
		ID:      fileID,
		Name:    base,
		Bytes:   n,
		SHA256:  sum,
		RelPath: rel,
	}
	body := strings.TrimSpace(caption)
	if body == "" {
		body = base
	}
	return s.postLocked(channel, from, "file", body, "", ref)
}

// ResolveFile returns the absolute path to a stored file by file id or message id.
func (s *Store) ResolveFile(channel, fileID string) (absPath string, ref FileRef, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.ensureLocked(); err != nil {
		return "", FileRef{}, err
	}
	channel = NormalizeChannel(channel)
	if channel == "" {
		channel = DefaultChannel
	}
	fileID = strings.TrimSpace(fileID)
	if fileID == "" {
		return "", FileRef{}, fmt.Errorf("file_id required")
	}
	all, err := s.readAllMessagesLocked(channel)
	if err != nil {
		return "", FileRef{}, err
	}
	for i := len(all) - 1; i >= 0; i-- {
		m := all[i]
		if m.File == nil {
			continue
		}
		if m.File.ID == fileID || m.ID == fileID {
			abs := filepath.Join(s.rootLocked(), filepath.FromSlash(m.File.RelPath))
			if _, err := os.Stat(abs); err != nil {
				return "", FileRef{}, fmt.Errorf("file missing on disk: %w", err)
			}
			return abs, *m.File, nil
		}
	}
	return "", FileRef{}, fmt.Errorf("file not found in channel %q", channel)
}

func (s *Store) readAllMessagesLocked(channel string) ([]Message, error) {
	channel = NormalizeChannel(channel)
	if channel == "" {
		channel = DefaultChannel
	}
	path := filepath.Join(s.rootLocked(), "channels", channel, messagesFileName)
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()
	var all []Message
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 256*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var m Message
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			continue
		}
		all = append(all, m)
	}
	return all, sc.Err()
}

func sanitizeFileName(name string) string {
	name = filepath.Base(name)
	var b strings.Builder
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '.', r == '-', r == '_':
			b.WriteRune(r)
		case r == ' ':
			b.WriteByte('_')
		default:
			b.WriteByte('_')
		}
	}
	out := b.String()
	if out == "" || out == "." || out == ".." {
		out = "file"
	}
	if len(out) > 80 {
		out = out[:80]
	}
	return out
}

func copyFileSHA256(src, dst string) (sum string, n int64, err error) {
	in, err := os.Open(src)
	if err != nil {
		return "", 0, err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return "", 0, err
	}
	defer func() {
		cerr := out.Close()
		if err == nil {
			err = cerr
		}
	}()
	h := sha256.New()
	w := io.MultiWriter(out, h)
	n, err = io.Copy(w, in)
	if err != nil {
		return "", n, err
	}
	return hex.EncodeToString(h.Sum(nil)), n, nil
}
