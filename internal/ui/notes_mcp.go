//go:build windows || darwin

package ui

import (
	"fmt"
	"strings"

	"github.com/StephenSHorton/suzuri/internal/bridge"
	"github.com/StephenSHorton/suzuri/internal/chrome"
)

// applyNotesRequest mutates a notes bank for the MCP bridge (pure; no I/O).
func applyNotesRequest(bank chrome.NotesBank, req bridge.NotesRequest) (chrome.NotesBank, bridge.NotesResult) {
	path := chrome.NotesPath()
	op := req.Op
	if op == "" {
		op = bridge.NotesOpList
	}

	switch op {
	case bridge.NotesOpList:
		return bank, notesListResult(bank, path)

	case bridge.NotesOpGet:
		n, ok := chrome.BankFindNote(bank, req.ID)
		if !ok {
			return bank, bridge.NotesResult{OK: false, Path: path, Error: "note not found"}
		}
		item := noteToItem(n, bank.ActiveID)
		return bank, bridge.NotesResult{OK: true, Path: path, Note: &item}

	case bridge.NotesOpCreate:
		title, body := "", ""
		if req.Title != nil {
			title = *req.Title
		}
		if req.Body != nil {
			body = *req.Body
		}
		next, n, err := chrome.BankCreateNote(bank, title, body)
		if err != nil {
			return bank, bridge.NotesResult{OK: false, Path: path, Error: err.Error()}
		}
		item := noteToItem(n, next.ActiveID)
		return next, bridge.NotesResult{OK: true, Path: path, Note: &item}

	case bridge.NotesOpUpdate:
		// set_active-only: switch active without rewriting the note.
		if req.Title == nil && req.Body == nil {
			if !req.SetActive {
				return bank, bridge.NotesResult{OK: false, Path: path, Error: "update requires title, body, and/or set_active"}
			}
			id := req.ID
			if id == "" {
				id = bank.ActiveID
			}
			next, err := chrome.BankSetActive(bank, id)
			if err != nil {
				return bank, bridge.NotesResult{OK: false, Path: path, Error: err.Error()}
			}
			n, ok := chrome.BankFindNote(next, id)
			if !ok {
				return bank, bridge.NotesResult{OK: false, Path: path, Error: "note not found"}
			}
			item := noteToItem(n, next.ActiveID)
			return next, bridge.NotesResult{OK: true, Path: path, Note: &item}
		}
		next, n, err := chrome.BankUpdateNote(bank, req.ID, req.Title, req.Body)
		if err != nil {
			return bank, bridge.NotesResult{OK: false, Path: path, Error: err.Error()}
		}
		if req.SetActive {
			if nb, err := chrome.BankSetActive(next, n.ID); err == nil {
				next = nb
			}
		}
		item := noteToItem(n, next.ActiveID)
		return next, bridge.NotesResult{OK: true, Path: path, Note: &item}

	case bridge.NotesOpDelete:
		next, n, err := chrome.BankDeleteNote(bank, req.ID)
		if err != nil {
			return bank, bridge.NotesResult{OK: false, Path: path, Error: err.Error()}
		}
		item := noteToItem(n, next.ActiveID)
		return next, bridge.NotesResult{OK: true, Path: path, Note: &item}

	default:
		return bank, bridge.NotesResult{
			OK:    false,
			Path:  path,
			Error: fmt.Sprintf("unknown notes op %q (list|get|create|update|delete)", op),
		}
	}
}

func notesListResult(bank chrome.NotesBank, path string) bridge.NotesResult {
	items := make([]bridge.NoteItem, 0, len(bank.Notes))
	for _, n := range bank.Notes {
		items = append(items, noteToItem(n, bank.ActiveID))
	}
	return bridge.NotesResult{
		OK:    true,
		Path:  path,
		Notes: items,
		Count: len(items),
	}
}

func noteToItem(n chrome.NoteDoc, activeID string) bridge.NoteItem {
	title := chrome.NoteDisplayTitle(n)
	// Prefer raw stored title when set so agents can round-trip edits.
	if t := strings.TrimSpace(n.Title); t != "" {
		title = t
	}
	return bridge.NoteItem{
		ID:      n.ID,
		Title:   title,
		Body:    n.Body,
		Updated: n.Updated,
		Active:  n.ID == activeID,
	}
}

// runNotesOnChrome applies a notes op to the chrome model, persists mutations,
// and reloads the bank so the open notes UI stays in sync.
func runNotesOnChrome(m *chrome.Model, req bridge.NotesRequest) bridge.NotesResult {
	if m == nil {
		return bridge.NotesResult{OK: false, Error: "no chrome model"}
	}
	wasOpen := m.NotesOpen
	bank := m.NotesSnapshot()
	next, res := applyNotesRequest(bank, req)
	if !res.OK {
		return res
	}
	op := req.Op
	if op == "" {
		op = bridge.NotesOpList
	}
	switch op {
	case bridge.NotesOpCreate, bridge.NotesOpUpdate, bridge.NotesOpDelete:
		if err := chrome.SaveNotesBank(next); err != nil {
			return bridge.NotesResult{OK: false, Path: chrome.NotesPath(), Error: err.Error()}
		}
		*m = m.UpdateChrome(chrome.LoadNotesMsg{Bank: next}).Model
		m.ClearNotesDirty()
		if wasOpen {
			*m = m.UpdateChrome(chrome.OpenNotesMsg{}).Model
		}
	default:
		// list/get — still flush snapshot side-effects if dirty (NotesSnapshot already did)
		if m.NotesDirty() {
			if err := chrome.SaveNotesBank(bank); err == nil {
				m.ClearNotesDirty()
			}
		}
	}
	return res
}
