package chrome

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/StephenSHorton/suzuri/internal/config"
)

func TestViewHasTabs(t *testing.T) {
	m := New(80)
	m.Tabs = []Tab{{ID: 0, Title: "alpha"}, {ID: 1, Title: "beta"}}
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
	if r.Action != ActionSettingsPreview {
		t.Fatalf("action=%v want SettingsPreview (open settings)", r.Action)
	}
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
	// 0 Settings, 1 Help, 2 New tab
	r = m.UpdateChrome(tea.KeyMsg{Type: tea.KeyDown})
	m = r.Model
	r = m.UpdateChrome(tea.KeyMsg{Type: tea.KeyDown})
	m = r.Model
	r = m.UpdateChrome(tea.KeyMsg{Type: tea.KeyEnter})
	if r.Action != ActionNewTab {
		t.Fatalf("action=%v want NewTab", r.Action)
	}
}

func TestHelpAndSplash(t *testing.T) {
	m := New(80)
	r := m.UpdateChrome(OpenHelpMsg{})
	m = r.Model
	if !m.HelpOpen {
		t.Fatal("help")
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

func TestPlusBounds(t *testing.T) {
	m := New(80)
	m.Tabs = []Tab{{ID: 0, Title: "a"}}
	b := m.PlusBounds()
	if b[1] <= b[0] {
		t.Fatalf("plus bounds %v", b)
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
	m.Tabs = []Tab{{ID: 0, Title: "PowerShell"}, {ID: 1, Title: "sh"}}
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
