package chrome

import (
	"crypto/rand"
	hexenc "encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/StephenSHorton/suzuri/internal/config"
)

// NotesPath is %LOCALAPPDATA%/suzuri/notes.json (same dir as config).
func NotesPath() string {
	return filepath.Join(config.Dir(), "notes.json")
}

// NoteDoc is one note in the bank (persisted).
type NoteDoc struct {
	ID      string    `json:"id"`
	Title   string    `json:"title,omitempty"` // optional override; empty → first line of body
	Body    string    `json:"body"`
	Updated time.Time `json:"updated"`
}

// NotesBank is the on-disk multi-note store.
type NotesBank struct {
	ActiveID string    `json:"active_id"`
	Notes    []NoteDoc `json:"notes"`
}

type notesFileDTO struct {
	ActiveID string    `json:"active_id"`
	Notes    []NoteDoc `json:"notes"`
}

// LoadNotesBank reads notes.json, or a single empty Scratch note if missing.
func LoadNotesBank() (NotesBank, error) {
	path := NotesPath()
	b, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return defaultNotesBank(), nil
		}
		return defaultNotesBank(), err
	}
	var dto notesFileDTO
	if err := json.Unmarshal(b, &dto); err != nil {
		return defaultNotesBank(), fmt.Errorf("parse notes: %w", err)
	}
	bank := NotesBank{ActiveID: dto.ActiveID, Notes: dto.Notes}
	return normalizeNotesBank(bank), nil
}

// SaveNotesBank writes notes.json atomically.
func SaveNotesBank(bank NotesBank) error {
	bank = normalizeNotesBank(bank)
	dir := filepath.Dir(NotesPath())
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(notesFileDTO{
		ActiveID: bank.ActiveID,
		Notes:    bank.Notes,
	}, "", "  ")
	if err != nil {
		return err
	}
	raw = append(raw, '\n')
	path := NotesPath()
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

func defaultNotesBank() NotesBank {
	// Empty title so first open focuses the name field (not the body).
	n := newNoteDoc("", "")
	return NotesBank{ActiveID: n.ID, Notes: []NoteDoc{n}}
}

func normalizeNotesBank(b NotesBank) NotesBank {
	if len(b.Notes) == 0 {
		return defaultNotesBank()
	}
	// Drop empty IDs / repair.
	out := make([]NoteDoc, 0, len(b.Notes))
	for _, n := range b.Notes {
		if strings.TrimSpace(n.ID) == "" {
			n.ID = newNoteID()
		}
		if n.Updated.IsZero() {
			n.Updated = time.Now()
		}
		// Cap runaway bodies.
		if utf8.RuneCountInString(n.Body) > notesMaxRunes {
			rs := []rune(n.Body)
			n.Body = string(rs[:notesMaxRunes])
		}
		out = append(out, n)
	}
	b.Notes = out
	if b.ActiveID == "" {
		b.ActiveID = b.Notes[0].ID
	} else {
		found := false
		for _, n := range b.Notes {
			if n.ID == b.ActiveID {
				found = true
				break
			}
		}
		if !found {
			b.ActiveID = b.Notes[0].ID
		}
	}
	return b
}

func newNoteID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("n%d", time.Now().UnixNano())
	}
	return hexenc.EncodeToString(b[:])
}

func newNoteDoc(title, body string) NoteDoc {
	return NoteDoc{
		ID:      newNoteID(),
		Title:   strings.TrimSpace(title),
		Body:    body,
		Updated: time.Now(),
	}
}

// NoteDisplayTitle is the label for the bank strip / dialog.
func NoteDisplayTitle(n NoteDoc) string {
	if t := strings.TrimSpace(n.Title); t != "" {
		return truncateNoteTitle(t, 28)
	}
	body := strings.TrimSpace(n.Body)
	if body == "" {
		return "Untitled"
	}
	line := body
	if i := strings.IndexByte(body, '\n'); i >= 0 {
		line = body[:i]
	}
	line = strings.TrimSpace(line)
	if line == "" {
		return "Untitled"
	}
	return truncateNoteTitle(line, 28)
}

func truncateNoteTitle(s string, max int) string {
	rs := []rune(s)
	if len(rs) <= max {
		return s
	}
	if max < 2 {
		return string(rs[:max])
	}
	return string(rs[:max-1]) + "…"
}
