package notes

import (
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

// BankFindNote returns a note by id. Empty id selects ActiveID.
func BankFindNote(bank NotesBank, id string) (NoteDoc, bool) {
	bank = normalizeNotesBank(bank)
	if strings.TrimSpace(id) == "" {
		id = bank.ActiveID
	}
	for _, n := range bank.Notes {
		if n.ID == id {
			return n, true
		}
	}
	return NoteDoc{}, false
}

// BankCreateNote appends a note, makes it active, and returns the new doc.
func BankCreateNote(bank NotesBank, title, body string) (NotesBank, NoteDoc, error) {
	bank = normalizeNotesBank(bank)
	if len(bank.Notes) >= notesMaxBank {
		return bank, NoteDoc{}, fmt.Errorf("notes bank full (max %d)", notesMaxBank)
	}
	n := newNoteDoc(title, body)
	bank.Notes = append(bank.Notes, n)
	bank.ActiveID = n.ID
	return bank, n, nil
}

// BankUpdateNote patches title and/or body. Empty id selects ActiveID.
// Nil title/body means leave unchanged; non-nil empty string clears title.
func BankUpdateNote(bank NotesBank, id string, title, body *string) (NotesBank, NoteDoc, error) {
	bank = normalizeNotesBank(bank)
	if strings.TrimSpace(id) == "" {
		id = bank.ActiveID
	}
	for i := range bank.Notes {
		if bank.Notes[i].ID != id {
			continue
		}
		n := bank.Notes[i]
		if title != nil {
			n.Title = strings.TrimSpace(*title)
		}
		if body != nil {
			n.Body = *body
			if utf8.RuneCountInString(n.Body) > notesMaxRunes {
				rs := []rune(n.Body)
				n.Body = string(rs[:notesMaxRunes])
			}
		}
		n.Updated = time.Now()
		bank.Notes[i] = n
		return bank, n, nil
	}
	return bank, NoteDoc{}, fmt.Errorf("note not found: %s", id)
}

// BankDeleteNote removes a note by id. Empty id selects ActiveID.
// The last note is cleared instead of removing the bank entirely.
func BankDeleteNote(bank NotesBank, id string) (NotesBank, NoteDoc, error) {
	bank = normalizeNotesBank(bank)
	if strings.TrimSpace(id) == "" {
		id = bank.ActiveID
	}
	idx := -1
	var removed NoteDoc
	for i, n := range bank.Notes {
		if n.ID == id {
			idx = i
			removed = n
			break
		}
	}
	if idx < 0 {
		return bank, NoteDoc{}, fmt.Errorf("note not found: %s", id)
	}
	if len(bank.Notes) == 1 {
		n := bank.Notes[0]
		n.Title = ""
		n.Body = ""
		n.Updated = time.Now()
		bank.Notes[0] = n
		bank.ActiveID = n.ID
		return bank, n, nil
	}
	bank.Notes = append(bank.Notes[:idx], bank.Notes[idx+1:]...)
	if bank.ActiveID == id {
		if idx >= len(bank.Notes) {
			idx = len(bank.Notes) - 1
		}
		bank.ActiveID = bank.Notes[idx].ID
	}
	return bank, removed, nil
}

// BankSetActive switches ActiveID. Empty id is a no-op success if bank non-empty.
func BankSetActive(bank NotesBank, id string) (NotesBank, error) {
	bank = normalizeNotesBank(bank)
	if strings.TrimSpace(id) == "" {
		return bank, nil
	}
	for _, n := range bank.Notes {
		if n.ID == id {
			bank.ActiveID = id
			return bank, nil
		}
	}
	return bank, fmt.Errorf("note not found: %s", id)
}


