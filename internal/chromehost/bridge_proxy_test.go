package chromehost

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/StephenSHorton/suzuri/internal/bridge"
)

func TestSnapshotFromChromeStatusRich(t *testing.T) {
	raw := `{
  "pid": 4242,
  "version": "1.0.0",
  "cols": 80,
  "rows": 24,
  "active_tab": 0,
  "tabs": [
    {
      "id": 0,
      "title": "shell 1",
      "alive": true,
      "shell": "zsh",
      "input": "ls",
      "alt_screen": false,
      "echo": {"armed": true, "cmd": "ls -la", "phase": 0},
      "live_lines": ["hello", "world"],
      "viewport": ["hello", "world"],
      "history_tail": [{"text": "old", "kind": "normal"}, {"text": "❯ ls -la", "kind": "cmd"}],
      "blocks": [{"command": "ls -la"}],
      "pty_tail": ""
    }
  ],
  "notes": ["ui=chrome"]
}`
	st, err := ParseChromeStatus([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	snap := SnapshotFromChromeStatus(st, 1)
	if snap.PID != 4242 {
		t.Fatalf("pid %d", snap.PID)
	}
	if snap.Cols != 80 || snap.Rows != 24 {
		t.Fatalf("grid %dx%d", snap.Cols, snap.Rows)
	}
	if len(snap.Tabs) != 1 {
		t.Fatalf("tabs %d", len(snap.Tabs))
	}
	tab := snap.Tabs[0]
	if tab.Title != "shell 1" || !tab.Alive {
		t.Fatalf("tab %+v", tab)
	}
	if len(tab.LiveLines) != 2 || tab.LiveLines[0] != "hello" {
		t.Fatalf("live_lines %+v", tab.LiveLines)
	}
	if len(tab.Viewport) != 2 {
		t.Fatalf("viewport %+v", tab.Viewport)
	}
	if len(tab.History) != 2 || tab.History[1].Kind != "cmd" {
		t.Fatalf("history %+v", tab.History)
	}
	if tab.Input != "ls" {
		t.Fatalf("input %q", tab.Input)
	}
	if !tab.Echo.Armed || tab.Echo.Cmd != "ls -la" {
		t.Fatalf("echo %+v", tab.Echo)
	}
	if len(tab.Blocks) != 1 || tab.Blocks[0].Command != "ls -la" {
		t.Fatalf("blocks %+v", tab.Blocks)
	}
	// notes should include version tag
	found := false
	for _, n := range snap.Notes {
		if n == "chrome_version=1.0.0" {
			found = true
		}
	}
	if !found {
		t.Fatalf("notes missing version: %+v", snap.Notes)
	}
}

func TestSnapshotFromChromeStatusLegacy(t *testing.T) {
	st := ChromeStatus{
		PID:         9,
		TabsCount:   2,
		ActiveTab:   1,
		ActiveTitle: "work",
		Cols:        100,
		Rows:        30,
	}
	snap := SnapshotFromChromeStatus(st, 0)
	if len(snap.Tabs) != 2 {
		t.Fatalf("tabs %d", len(snap.Tabs))
	}
	if snap.Tabs[1].Title != "work" {
		t.Fatalf("active title %q", snap.Tabs[1].Title)
	}
	if snap.PID != 9 {
		t.Fatalf("pid %d", snap.PID)
	}
}

func TestWriteSubmitRoundTrip(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("LOCALAPPDATA", dir)
	if err := WriteSubmit("echo hi"); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "suzuri", SubmitFile)
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != "echo hi\n" {
		t.Fatalf("body %q", b)
	}
}

func TestReadChromeStatusFile(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("LOCALAPPDATA", dir)
	path := filepath.Join(dir, "suzuri", StatusFile)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	body := `{"pid":7,"tabs":[{"id":0,"title":"t","alive":true,"input":"","live_lines":["a"],"viewport":["a"]}],"active_tab":0,"cols":10,"rows":5}`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	st, err := ReadChromeStatus()
	if err != nil {
		t.Fatal(err)
	}
	snap := SnapshotFromChromeStatus(st, 0)
	if snap.PID != 7 || len(snap.Tabs) != 1 || snap.Tabs[0].LiveLines[0] != "a" {
		t.Fatalf("snap %+v", snap)
	}
	// bridge.TabSnap type is used in ChromeStatus — compile-time check
	var _ []bridge.TabSnap = st.Tabs
}
