package chrome

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/StephenSHorton/suzuri/internal/config"
)

func TestViewHasTabs(t *testing.T) {
	m := New(80)
	m.Tabs = []Tab{{ID: 0, Title: "alpha", Alive: true}, {ID: 1, Title: "beta", Alive: true}}
	m.Active = 1
	v := m.View()
	if !strings.Contains(v, "beta") {
		t.Fatalf("view missing tab title: %q", v)
	}
	if !strings.Contains(v, "alpha") {
		t.Fatalf("view missing inactive tab: %q", v)
	}
	// Single-row strip — both titles present is enough.
}

func TestTabBoundsMatchLayout(t *testing.T) {
	m := New(100)
	m.Tabs = []Tab{{ID: 0, Title: "one"}, {ID: 1, Title: "two"}, {ID: 2, Title: "three"}}
	m.Active = 0
	bounds := m.TabBounds()
	if len(bounds) != 3 {
		t.Fatalf("bounds len=%d", len(bounds))
	}
	for i, b := range bounds {
		if b[1] <= b[0] {
			t.Fatalf("tab %d empty bound %v", i, b)
		}
		if i > 0 && b[0] < bounds[i-1][1] {
			t.Fatalf("tab %d overlaps prev: %v vs %v", i, b, bounds[i-1])
		}
	}
}

func TestTabStripIsOneRow(t *testing.T) {
	m := New(80)
	m.Tabs = []Tab{{ID: 0, Title: "shell"}}
	if m.RowCount() != 1 {
		t.Fatalf("rows=%d want 1 (calm strip)", m.RowCount())
	}
	if TabStripRows() != 1 {
		t.Fatal("TabStripRows")
	}
}

func TestPaletteSettingsFirst(t *testing.T) {
	m := New(80)
	r := m.UpdateChrome(OpenPaletteMsg{})
	m = r.Model
	if !m.PaletteOpen {
		t.Fatal("palette should open")
	}
	// First registry command is Settings.
	r = m.UpdateChrome(tea.KeyMsg{Type: tea.KeyEnter})
	// Opening settings from palette may still return SettingsPreview (legacy)
	// or None; require the dialog open either way.
	if !r.Model.SettingsOpen {
		t.Fatal("settings should be open")
	}
}

func TestSettingsNudgePreview(t *testing.T) {
	m := New(80)
	cfg := config.Default()
	r := m.UpdateChrome(OpenSettingsMsg{Config: cfg})
	m = r.Model
	if !m.SettingsOpen {
		t.Fatal("settings open")
	}
	// Open no longer fires preview (config already live).
	if r.Action != ActionNone {
		t.Fatalf("open action=%v want None", r.Action)
	}
	// Move to font size and increase.
	r = m.UpdateChrome(tea.KeyMsg{Type: tea.KeyDown})
	m = r.Model
	r = m.UpdateChrome(tea.KeyMsg{Type: tea.KeyRight})
	if r.Action != ActionSettingsPreview {
		t.Fatalf("action=%v", r.Action)
	}
	if r.Settings.FontSizePx != cfg.FontSizePx+1 {
		t.Fatalf("size=%d want %d", r.Settings.FontSizePx, cfg.FontSizePx+1)
	}
}

func TestNewTabFromPalette(t *testing.T) {
	m := New(80)
	r := m.UpdateChrome(OpenPaletteMsg{})
	m = r.Model
	// Filter to the entry instead of hard-coding palette order (zoom cmds insert above).
	for _, ch := range "new tab" {
		r = m.UpdateChrome(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{ch}})
		m = r.Model
	}
	// Prefer exact "New tab" (not "New tab: Profile") — first match after filter.
	r = m.UpdateChrome(tea.KeyMsg{Type: tea.KeyEnter})
	if r.Action != ActionNewTab && r.Action != ActionNewTabProfile {
		t.Fatalf("action=%v want NewTab", r.Action)
	}
}

func TestNewWindowFromPalette(t *testing.T) {
	m := New(80)
	r := m.UpdateChrome(OpenPaletteMsg{})
	m = r.Model
	for _, ch := range "new window" {
		r = m.UpdateChrome(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{ch}})
		m = r.Model
	}
	r = m.UpdateChrome(tea.KeyMsg{Type: tea.KeyEnter})
	if r.Action != ActionNewWindow {
		t.Fatalf("action=%v want NewWindow", r.Action)
	}
}

func TestStatusToastRow(t *testing.T) {
	m := New(80)
	if m.RowCount() != 1 {
		t.Fatalf("idle rows=%d want 1", m.RowCount())
	}
	if m.showStatus() {
		t.Fatal("empty status should not show")
	}
	r := m.UpdateChrome(StatusMsg("opened link"))
	m = r.Model
	if !m.showStatus() || m.Status != "opened link" {
		t.Fatalf("status=%q show=%v", m.Status, m.showStatus())
	}
	if m.RowCount() != 2 {
		t.Fatalf("toast rows=%d want 2", m.RowCount())
	}
	strip := m.StripView()
	if !strings.Contains(ansi.Strip(strip), "opened link") {
		t.Fatalf("strip missing toast: %q", ansi.Strip(strip))
	}
	r = m.UpdateChrome(StatusMsg(""))
	m = r.Model
	if m.showStatus() || m.RowCount() != 1 {
		t.Fatalf("cleared status still showing rows=%d", m.RowCount())
	}
}

func TestZoomCommandsInPalette(t *testing.T) {
	m := New(80)
	r := m.UpdateChrome(OpenPaletteMsg{})
	m = r.Model
	for _, ch := range "reset zoom" {
		r = m.UpdateChrome(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{ch}})
		m = r.Model
	}
	r = m.UpdateChrome(tea.KeyMsg{Type: tea.KeyEnter})
	if r.Action != ActionZoomReset {
		t.Fatalf("action=%v want ZoomReset", r.Action)
	}
}

func TestPaletteFilterNarrowsItems(t *testing.T) {
	m := New(80)
	r := m.UpdateChrome(OpenPaletteMsg{})
	m = r.Model
	if !m.PaletteOpen {
		t.Fatal("palette open")
	}
	before := len(m.palView)
	if before < 2 {
		t.Fatalf("expected several commands, got %d", before)
	}
	// Type "split" — should leave only pane split commands (sync substring filter).
	for _, ch := range "split" {
		r = m.UpdateChrome(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{ch}})
		m = r.Model
	}
	after := len(m.palView)
	if after == 0 {
		t.Fatal("filter matched nothing")
	}
	if after >= before {
		t.Fatalf("filter did not narrow: before=%d after=%d val=%q", before, after, m.palFilter)
	}
	if m.palFilter != "split" {
		t.Fatalf("filter=%q want split", m.palFilter)
	}
	// Enter should run the selected (highlighted) match.
	r = m.UpdateChrome(tea.KeyMsg{Type: tea.KeyEnter})
	if r.Action != ActionSplitRight && r.Action != ActionSplitDown {
		t.Fatalf("action=%v want a split action", r.Action)
	}
}

func TestHelpAndSplash(t *testing.T) {
	m := New(80)
	r := m.UpdateChrome(OpenHelpMsg{})
	m = r.Model
	if !m.HelpOpen {
		t.Fatal("help")
	}
	// Wide window → two-column shortcuts (much shorter than single-stack).
	view := m.OverlayView()
	h := lipgloss.Height(view)
	if h < 8 || h > 30 {
		t.Fatalf("help height=%d (expected two-col card ~14–26 rows)", h)
	}
	// Two-col must be shorter than one-col at the same width budget.
	one := helpBodyOneCol(80)
	if h >= lipgloss.Height(one) {
		t.Fatalf("two-col height %d should be < one-col %d", h, lipgloss.Height(one))
	}
	if !strings.Contains(view, "Shortcuts") {
		t.Fatal("help title missing")
	}
	if !strings.Contains(view, "Tabs") || !strings.Contains(view, "Terminal") {
		t.Fatal("help sections missing")
	}
	// Narrow window still opens (single column).
	m2 := New(40)
	r2 := m2.UpdateChrome(OpenHelpMsg{})
	if !r2.Model.HelpOpen {
		t.Fatal("narrow help")
	}
	if lipgloss.Height(r2.Model.OverlayView()) < 10 {
		t.Fatal("narrow help too short")
	}
	r = m.UpdateChrome(tea.KeyMsg{Type: tea.KeyEsc})
	m = r.Model
	if m.HelpOpen {
		t.Fatal("help should close")
	}
	r = m.UpdateChrome(OpenSplashMsg{})
	m = r.Model
	if !m.SplashOpen {
		t.Fatal("splash")
	}
	r = m.UpdateChrome(tea.KeyMsg{Type: tea.KeyEnter})
	if r.Action != ActionSplashDone || r.Model.SplashOpen {
		t.Fatalf("splash done action=%v open=%v", r.Action, r.Model.SplashOpen)
	}
}

func TestConfirmQuit(t *testing.T) {
	m := New(80)
	r := m.UpdateChrome(OpenConfirmQuitMsg{})
	m = r.Model
	if !m.ConfirmOpen || !m.OverlayOpen() {
		t.Fatal("confirm should open")
	}
	r = m.UpdateChrome(tea.KeyMsg{Type: tea.KeyEnter})
	if r.Action != ActionQuit {
		t.Fatalf("action=%v want Quit", r.Action)
	}
}

func TestDismissOverlay(t *testing.T) {
	m := New(80)
	r := m.UpdateChrome(OpenPaletteMsg{})
	m = r.Model
	r = m.UpdateChrome(DismissOverlayMsg{})
	if r.Model.PaletteOpen {
		t.Fatal("palette should close")
	}
}

func TestNotesToggleKeepsBuffer(t *testing.T) {
	m := openNotesBody(New(80))
	if !m.NotesOpen {
		t.Fatal("notes open")
	}
	if m.notesFocus != notesFocusEditor {
		t.Fatalf("open focus=%v want editor", m.notesFocus)
	}
	var r Result
	for _, ch := range "hello" {
		r = m.UpdateChrome(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{ch}})
		m = r.Model
	}
	// Esc from editor → list; Esc from list → close
	r = m.UpdateChrome(tea.KeyMsg{Type: tea.KeyEsc})
	m = r.Model
	if !m.NotesOpen || m.notesFocus != notesFocusList {
		t.Fatalf("esc→list open=%v focus=%v", m.NotesOpen, m.notesFocus)
	}
	r = m.UpdateChrome(tea.KeyMsg{Type: tea.KeyEsc})
	m = r.Model
	if m.NotesOpen {
		t.Fatal("notes should close")
	}
	if string(m.notesRunes) != "hello" {
		t.Fatalf("buffer=%q want hello", string(m.notesRunes))
	}
	r = m.UpdateChrome(ToggleNotesMsg{})
	m = r.Model
	if !m.NotesOpen || string(m.notesRunes) != "hello" {
		t.Fatalf("reopen open=%v buf=%q", m.NotesOpen, string(m.notesRunes))
	}
	if m.notesFocus != notesFocusEditor {
		t.Fatalf("reopen focus=%v want editor", m.notesFocus)
	}
	// Toggle again hides even from editor (Ctrl+Shift+M).
	r = m.UpdateChrome(ToggleNotesMsg{})
	m = r.Model
	if m.NotesOpen {
		t.Fatal("toggle should hide notes")
	}
}

func TestNotesSelectAllBackspaceTab(t *testing.T) {
	m := openNotesBody(New(80))
	var r Result
	for _, ch := range "ab" {
		r = m.UpdateChrome(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{ch}})
		m = r.Model
	}
	// Ctrl+A selects all
	r = m.UpdateChrome(tea.KeyMsg{Type: tea.KeyCtrlA})
	m = r.Model
	if m.NotesSelectedText() != "ab" {
		t.Fatalf("select all=%q", m.NotesSelectedText())
	}
	// Backspace deletes selection
	r = m.UpdateChrome(tea.KeyMsg{Type: tea.KeyBackspace})
	m = r.Model
	if string(m.notesRunes) != "" {
		t.Fatalf("after BS sel buf=%q", string(m.notesRunes))
	}
	// Tab inserts a real tab character
	r = m.UpdateChrome(tea.KeyMsg{Type: tea.KeyTab})
	m = r.Model
	if string(m.notesRunes) != "\t" {
		t.Fatalf("tab buf=%q", string(m.notesRunes))
	}
	// Type, shift-select left, cut deletes selection
	r = m.UpdateChrome(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
	m = r.Model
	r = m.UpdateChrome(tea.KeyMsg{Type: tea.KeyShiftLeft})
	m = r.Model
	if m.NotesSelectedText() != "x" {
		t.Fatalf("shift-left sel=%q", m.NotesSelectedText())
	}
	r = m.UpdateChrome(tea.KeyMsg{Type: tea.KeyCtrlX})
	m = r.Model
	if string(m.notesRunes) != "\t" {
		t.Fatalf("after cut buf=%q", string(m.notesRunes))
	}
	// Typing replaces selection
	r = m.UpdateChrome(tea.KeyMsg{Type: tea.KeyCtrlA})
	m = r.Model
	r = m.UpdateChrome(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'z'}})
	m = r.Model
	if string(m.notesRunes) != "z" {
		t.Fatalf("type over sel buf=%q", string(m.notesRunes))
	}
}

func TestNotesBankListNewRenameDelete(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("LOCALAPPDATA", dir)

	m := openNotesBody(New(80))
	var r Result
	// Fresh blank note opens on title; openNotesBody enters body.
	if m.notesFocus != notesFocusEditor {
		t.Fatalf("want editor after openNotesBody, got %v", m.notesFocus)
	}
	for _, ch := range "one" {
		r = m.UpdateChrome(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{ch}})
		m = r.Model
	}
	// Esc to list, n for new (title mode)
	r = m.UpdateChrome(tea.KeyMsg{Type: tea.KeyEsc})
	m = r.Model
	if m.notesFocus != notesFocusList {
		t.Fatal("esc → list")
	}
	r = m.UpdateChrome(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})
	m = r.Model
	if len(m.notesBank) != 2 {
		t.Fatalf("bank len=%d", len(m.notesBank))
	}
	if m.notesFocus != notesFocusTitle {
		t.Fatal("new → title field (not body)")
	}
	if m.notesTitle != "" {
		t.Fatalf("new note title should start empty, got %q", m.notesTitle)
	}
	for _, ch := range "Two" {
		r = m.UpdateChrome(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{ch}})
		m = r.Model
	}
	r = m.UpdateChrome(tea.KeyMsg{Type: tea.KeyEnter})
	m = r.Model
	if m.notesBank[m.notesActive].Title != "Two" {
		t.Fatalf("title=%q", m.notesBank[m.notesActive].Title)
	}
	// Esc to list, up to first note, d delete second remains
	r = m.UpdateChrome(tea.KeyMsg{Type: tea.KeyEsc})
	m = r.Model
	r = m.UpdateChrome(tea.KeyMsg{Type: tea.KeyUp})
	m = r.Model
	if string(m.notesRunes) != "one" {
		t.Fatalf("switched body=%q", string(m.notesRunes))
	}
	r = m.UpdateChrome(tea.KeyMsg{Type: tea.KeyDown})
	m = r.Model
	r = m.UpdateChrome(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})
	m = r.Model
	// Delete asks for confirmation first.
	if !m.ConfirmOpen {
		t.Fatal("d should open delete confirm")
	}
	if len(m.notesBank) != 2 {
		t.Fatalf("before confirm len=%d", len(m.notesBank))
	}
	// Esc cancels
	r = m.UpdateChrome(tea.KeyMsg{Type: tea.KeyEsc})
	m = r.Model
	if m.ConfirmOpen || len(m.notesBank) != 2 {
		t.Fatalf("cancel: confirm=%v len=%d", m.ConfirmOpen, len(m.notesBank))
	}
	// d + Enter confirms
	r = m.UpdateChrome(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})
	m = r.Model
	r = m.UpdateChrome(tea.KeyMsg{Type: tea.KeyEnter})
	m = r.Model
	if m.ConfirmOpen {
		t.Fatal("confirm should close after enter")
	}
	if len(m.notesBank) != 1 {
		t.Fatalf("after delete len=%d", len(m.notesBank))
	}
	// Open editor, ↑ from top of body → title field
	r = m.UpdateChrome(tea.KeyMsg{Type: tea.KeyEnter})
	m = r.Model
	r = m.UpdateChrome(tea.KeyMsg{Type: tea.KeyUp})
	m = r.Model
	if m.notesFocus != notesFocusTitle {
		t.Fatal("up from top → title")
	}
	r = m.UpdateChrome(tea.KeyMsg{Type: tea.KeyDown})
	m = r.Model
	if m.notesFocus != notesFocusEditor {
		t.Fatal("down from title → body")
	}
	bank := m.NotesSnapshot()
	if err := SaveNotesBank(bank); err != nil {
		t.Fatal(err)
	}
	got, err := LoadNotesBank()
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Notes) != 1 {
		t.Fatalf("loaded len=%d", len(got.Notes))
	}
}

func TestNotesEnterSingleBlankLine(t *testing.T) {
	m := openNotesBody(New(80))
	var r Result
	for _, ch := range "hi" {
		r = m.UpdateChrome(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{ch}})
		m = r.Model
	}
	r = m.UpdateChrome(tea.KeyMsg{Type: tea.KeyEnter})
	m = r.Model
	if string(m.notesRunes) != "hi\n" {
		t.Fatalf("buf=%q", string(m.notesRunes))
	}
	// One hard line + one caret blank — not two blanks after Enter.
	lines := notesSoftLines(m.notesRunes, 40)
	if len(lines) != 2 {
		t.Fatalf("soft lines=%d want 2: %+v", len(lines), lines)
	}
	if !lines[0].hard || lines[0].end-lines[0].start != 2 {
		t.Fatalf("line0=%+v", lines[0])
	}
	if lines[1].start != 3 || lines[1].end != 3 {
		t.Fatalf("line1 (blank)=%+v", lines[1])
	}
	row, _ := notesCursorRowCol(m.notesRunes, lines, m.notesCursor)
	if row != 1 {
		t.Fatalf("caret row=%d want 1 (new line)", row)
	}
	// Typing lands on the new line, not the previous.
	r = m.UpdateChrome(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
	m = r.Model
	if string(m.notesRunes) != "hi\nx" {
		t.Fatalf("after type buf=%q", string(m.notesRunes))
	}
	lines = notesSoftLines(m.notesRunes, 40)
	row, _ = notesCursorRowCol(m.notesRunes, lines, m.notesCursor)
	if row != 1 {
		t.Fatalf("after type caret row=%d", row)
	}
}

func TestConfirmUpdateModal(t *testing.T) {
	m := New(80)
	r := m.UpdateChrome(OpenConfirmUpdateMsg{Version: "1.2.3"})
	m = r.Model
	if !m.ConfirmOpen {
		t.Fatal("confirm should open")
	}
	r = m.UpdateChrome(tea.KeyMsg{Type: tea.KeyEnter})
	if r.Action != ActionInstallUpdate {
		t.Fatalf("action=%v want InstallUpdate", r.Action)
	}
}

func TestRenameDialogApply(t *testing.T) {
	m := New(80)
	r := m.UpdateChrome(OpenRenameMsg{Target: RenameTargetPane, Seed: "shell"})
	m = r.Model
	if !m.RenameOpen {
		t.Fatal("rename should open")
	}
	// Clear seed and type "work"
	for i := 0; i < 5; i++ {
		r = m.UpdateChrome(tea.KeyMsg{Type: tea.KeyBackspace})
		m = r.Model
	}
	for _, ch := range "work" {
		r = m.UpdateChrome(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{ch}})
		m = r.Model
	}
	r = m.UpdateChrome(tea.KeyMsg{Type: tea.KeyEnter})
	if r.Action != ActionApplyRename {
		t.Fatalf("action=%v want ApplyRename", r.Action)
	}
	if r.Name != "work" {
		t.Fatalf("name=%q", r.Name)
	}
	if r.RenameTarget != RenameTargetPane {
		t.Fatalf("target=%v", r.RenameTarget)
	}
	if r.Model.RenameOpen {
		t.Fatal("rename should close")
	}
}

func TestPlusBounds(t *testing.T) {
	m := New(80)
	m.Tabs = []Tab{{ID: 0, Title: "a"}}
	b := m.PlusBounds()
	if b[1] <= b[0] {
		t.Fatalf("plus bounds %v", b)
	}
}

func TestCaffeineBoundsRight(t *testing.T) {
	m := New(80)
	m.Tabs = []Tab{{ID: 0, Title: "shell"}}
	b := m.CaffeineBounds()
	if b[1] <= b[0] {
		t.Fatalf("caffeine bounds %v", b)
	}
	// Cup should sit on the right half of the strip.
	if b[0] < 40 {
		t.Fatalf("expected right-side cup, got %v", b)
	}
	// Plus is left of caffeine with a spacer.
	plus := m.PlusBounds()
	if plus[1] > b[0] {
		t.Fatalf("plus %v overlaps caffeine %v", plus, b)
	}
	// Active chip still has a hit target.
	m.CaffeineOn = true
	m.CaffeineHint = "15m"
	b2 := m.CaffeineBounds()
	if b2[1] <= b2[0] {
		t.Fatalf("active caffeine bounds %v", b2)
	}
}

func TestRenderToTerm(t *testing.T) {
	m := New(60)
	m.Tabs = []Tab{{ID: 0, Title: "shell"}}
	m.Active = 0
	term := RenderToTerm(m, 60)
	cols, rows := term.Size()
	if cols < 20 || rows < 1 {
		t.Fatalf("size %d×%d", cols, rows)
	}
	found := false
	for y := 0; y < rows && y < 2; y++ {
		for x := 0; x < cols; x++ {
			if term.Cell(x, y).Char != 0 && term.Cell(x, y).Char != ' ' {
				found = true
				break
			}
		}
	}
	if !found {
		t.Fatal("chrome empty after render")
	}
}

// Wide brand 硯 must still leave room for tab titles on the strip.
func TestWideRuneAlignment(t *testing.T) {
	m := New(50)
	m.Tabs = []Tab{{ID: 0, Title: "PowerShell", Alive: true}, {ID: 1, Title: "sh", Alive: true}}
	m.Active = 0
	term := RenderToTerm(m, 50)
	// Find 'P' of PowerShell somewhere on row 0 after brand.
	found := false
	for x := 0; x < 50; x++ {
		if term.Cell(x, 0).Char == 'P' {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("PowerShell title missing from strip after wide brand")
	}
}

func TestTabStateGlyphs(t *testing.T) {
	SetTabStateGlyphs(TabGlyphsBraille)
	t.Cleanup(func() { SetTabStateGlyphs(TabGlyphsASCII) })
	// Reset spin to a known frame.
	for spinFrame.Load()%uint64(len(brailleSpinFrames)) != 0 {
		AdvanceTabSpinner()
	}
	cases := []struct {
		name string
		tab  Tab
		want string
	}{
		{"dead", Tab{Alive: false}, "⠁ "},
		{"idle shell", Tab{Alive: true}, ""},
		{"busy shell", Tab{Alive: true, Busy: true}, "⠋ "}, // first spin frame
		{"alt idle", Tab{Alive: true, AltScreen: true}, "⠿ "},
		{"alt busy", Tab{Alive: true, AltScreen: true, Busy: true}, "⠋ "},
	}
	for _, tc := range cases {
		if g := tabStateGlyph(tc.tab); g != tc.want {
			t.Fatalf("%s: got %q want %q", tc.name, g, tc.want)
		}
	}
	// Animation advances.
	AdvanceTabSpinner()
	if g := tabStateGlyph(Tab{Alive: true, Busy: true}); g != "⠙ " {
		t.Fatalf("after advance: %q", g)
	}
}

func TestSingleTabUsesWideTitleBudget(t *testing.T) {
	SetTabStateGlyphs(TabGlyphsBraille)
	t.Cleanup(func() { SetTabStateGlyphs(TabGlyphsASCII) })
	// Old code hard-clipped at 18 runes; a single tab on a wide strip should keep more.
	long := "Grok Build — suzuri polish session with a long title"
	m := New(100)
	m.Tabs = []Tab{{ID: 0, Title: long, Alive: true, AltScreen: true, Busy: true}}
	m.Active = 0
	v := m.View()
	// Busy-alt uses spin frames (braille dots), not a static ⣿.
	if !strings.Contains(v, "⠋") && !strings.ContainsAny(v, "⠙⠹⠸⠼⠴⠦⠧⠇⠏⣷⣿") {
		t.Fatalf("missing busy spin glyph in %q", v)
	}
	if !strings.Contains(v, "Grok Build") {
		t.Fatalf("title over-truncated in single-tab strip: %q", v)
	}
	// Budget for 1 tab on width 100 should be well above the old 18.
	if b := titleBudget(100, 1); b < 40 {
		t.Fatalf("titleBudget(100,1)=%d want >= 40", b)
	}
	if b := titleBudget(80, 4); b > titleBudget(80, 1) {
		t.Fatalf("more tabs should not get a larger per-tab budget")
	}
}
