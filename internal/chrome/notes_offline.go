package chrome

import (
	"fmt"
	"strings"
	"time"
)

// NotesBridgeResult is a JSON-friendly notes payload for MCP offline fallback
// (when the GUI is not running). Kept free of the bridge package to avoid cycles.
type NotesBridgeResult struct {
	OK    bool             `json:"ok"`
	Path  string           `json:"path,omitempty"`
	Note  *NotesBridgeItem `json:"note,omitempty"`
	Notes []NotesBridgeItem `json:"notes,omitempty"`
	Count int              `json:"count,omitempty"`
	Error string           `json:"error,omitempty"`
}

// NotesBridgeItem is one note for agents.
type NotesBridgeItem struct {
	ID      string    `json:"id"`
	Title   string    `json:"title"`
	Body    string    `json:"body"`
	Updated time.Time `json:"updated"`
	Active  bool      `json:"active,omitempty"`
}

// ApplyNotesDiskOp loads notes.json, applies op, and saves on mutations.
// op: list | get | create | update | delete
func ApplyNotesDiskOp(op, id string, title, body *string, setActive bool) NotesBridgeResult {
	path := NotesPath()
	bank, err := LoadNotesBank()
	if err != nil {
		return NotesBridgeResult{OK: false, Path: path, Error: err.Error()}
	}
	if op == "" {
		op = "list"
	}
	switch strings.ToLower(op) {
	case "list":
		return notesBridgeList(bank, path)
	case "get":
		n, ok := BankFindNote(bank, id)
		if !ok {
			return NotesBridgeResult{OK: false, Path: path, Error: "note not found"}
		}
		item := notesBridgeItem(n, bank.ActiveID)
		return NotesBridgeResult{OK: true, Path: path, Note: &item}
	case "create":
		t, b := "", ""
		if title != nil {
			t = *title
		}
		if body != nil {
			b = *body
		}
		next, n, err := BankCreateNote(bank, t, b)
		if err != nil {
			return NotesBridgeResult{OK: false, Path: path, Error: err.Error()}
		}
		if err := SaveNotesBank(next); err != nil {
			return NotesBridgeResult{OK: false, Path: path, Error: err.Error()}
		}
		item := notesBridgeItem(n, next.ActiveID)
		return NotesBridgeResult{OK: true, Path: path, Note: &item}
	case "update":
		next, n, err := BankUpdateNote(bank, id, title, body)
		if err != nil {
			return NotesBridgeResult{OK: false, Path: path, Error: err.Error()}
		}
		if setActive {
			if nb, err := BankSetActive(next, n.ID); err == nil {
				next = nb
			}
		}
		if err := SaveNotesBank(next); err != nil {
			return NotesBridgeResult{OK: false, Path: path, Error: err.Error()}
		}
		item := notesBridgeItem(n, next.ActiveID)
		return NotesBridgeResult{OK: true, Path: path, Note: &item}
	case "delete":
		next, n, err := BankDeleteNote(bank, id)
		if err != nil {
			return NotesBridgeResult{OK: false, Path: path, Error: err.Error()}
		}
		if err := SaveNotesBank(next); err != nil {
			return NotesBridgeResult{OK: false, Path: path, Error: err.Error()}
		}
		item := notesBridgeItem(n, next.ActiveID)
		return NotesBridgeResult{OK: true, Path: path, Note: &item}
	default:
		return NotesBridgeResult{
			OK:    false,
			Path:  path,
			Error: fmt.Sprintf("unknown notes op %q", op),
		}
	}
}

func notesBridgeList(bank NotesBank, path string) NotesBridgeResult {
	items := make([]NotesBridgeItem, 0, len(bank.Notes))
	for _, n := range bank.Notes {
		items = append(items, notesBridgeItem(n, bank.ActiveID))
	}
	return NotesBridgeResult{OK: true, Path: path, Notes: items, Count: len(items)}
}

func notesBridgeItem(n NoteDoc, activeID string) NotesBridgeItem {
	title := NoteDisplayTitle(n)
	if t := strings.TrimSpace(n.Title); t != "" {
		title = t
	}
	return NotesBridgeItem{
		ID:      n.ID,
		Title:   title,
		Body:    n.Body,
		Updated: n.Updated,
		Active:  n.ID == activeID,
	}
}
