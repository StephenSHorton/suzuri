package chrome

import (
	"testing"
)

func TestBankCreateUpdateDelete(t *testing.T) {
	bank := defaultNotesBank()
	if len(bank.Notes) != 1 {
		t.Fatalf("default bank len=%d", len(bank.Notes))
	}

	bank, n, err := BankCreateNote(bank, "Hello", "body one")
	if err != nil {
		t.Fatal(err)
	}
	if n.Title != "Hello" || n.Body != "body one" {
		t.Fatalf("created %+v", n)
	}
	if bank.ActiveID != n.ID {
		t.Fatal("create should set active")
	}
	if len(bank.Notes) != 2 {
		t.Fatalf("len=%d", len(bank.Notes))
	}

	title := "Hello2"
	body := "body two"
	bank, n2, err := BankUpdateNote(bank, n.ID, &title, &body)
	if err != nil {
		t.Fatal(err)
	}
	if n2.Title != "Hello2" || n2.Body != "body two" {
		t.Fatalf("updated %+v", n2)
	}

	// Partial update: body only
	body3 := "body three"
	bank, n3, err := BankUpdateNote(bank, n.ID, nil, &body3)
	if err != nil {
		t.Fatal(err)
	}
	if n3.Title != "Hello2" || n3.Body != "body three" {
		t.Fatalf("partial %+v", n3)
	}

	bank, _, err = BankDeleteNote(bank, n.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(bank.Notes) != 1 {
		t.Fatalf("after delete len=%d", len(bank.Notes))
	}

	// Last note: delete clears body instead of emptying bank.
	lastID := bank.Notes[0].ID
	bank, cleared, err := BankDeleteNote(bank, lastID)
	if err != nil {
		t.Fatal(err)
	}
	if len(bank.Notes) != 1 {
		t.Fatalf("last delete should keep one note, got %d", len(bank.Notes))
	}
	if cleared.Body != "" && bank.Notes[0].Body != "" {
		t.Fatalf("last note should be cleared, got body=%q", bank.Notes[0].Body)
	}
}

func TestBankFindActive(t *testing.T) {
	bank := defaultNotesBank()
	bank, created, err := BankCreateNote(bank, "A", "aaa")
	if err != nil {
		t.Fatal(err)
	}
	got, ok := BankFindNote(bank, "")
	if !ok || got.ID != created.ID {
		t.Fatalf("empty id should find active: ok=%v id=%s want=%s", ok, got.ID, created.ID)
	}
}

func TestApplyNotesDiskOpRoundTrip(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("LOCALAPPDATA", dir)
	// macOS/Linux use UserConfigDir; also set XDG and HOME if needed.
	// NotesPath uses config.Dir() which uses UserConfigDir on non-Windows.
	// Force via temp: Setenv HOME and XDG_CONFIG_HOME.
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("HOME", dir)

	title := "DiskNote"
	body := "hello from disk"
	res := ApplyNotesDiskOp("create", "", &title, &body, false)
	if !res.OK || res.Note == nil {
		t.Fatalf("create: %+v", res)
	}
	id := res.Note.ID

	list := ApplyNotesDiskOp("list", "", nil, nil, false)
	if !list.OK || list.Count < 1 {
		t.Fatalf("list: %+v", list)
	}

	got := ApplyNotesDiskOp("get", id, nil, nil, false)
	if !got.OK || got.Note == nil || got.Note.Body != body {
		t.Fatalf("get: %+v", got)
	}

	newBody := "updated"
	upd := ApplyNotesDiskOp("update", id, nil, &newBody, false)
	if !upd.OK || upd.Note == nil || upd.Note.Body != "updated" {
		t.Fatalf("update: %+v", upd)
	}
}
