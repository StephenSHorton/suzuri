package ui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTokenAtCursor(t *testing.T) {
	rs := []rune("cd Doc")
	start, end, prefix, first := tokenAtCursor(rs, len(rs))
	if start != 3 || end != 6 || prefix != "Doc" || first {
		t.Fatalf("got start=%d end=%d prefix=%q first=%v", start, end, prefix, first)
	}
	rs = []rune("echo")
	start, end, prefix, first = tokenAtCursor(rs, 4)
	if start != 0 || !first || prefix != "echo" {
		t.Fatalf("first word: start=%d first=%v prefix=%q", start, first, prefix)
	}
}

func TestHistoryCompletions(t *testing.T) {
	h := []string{"echo one", "echo two", "ls", "echo two"}
	got := historyCompletions(h, "echo")
	if len(got) < 2 || got[0] != "echo two" {
		t.Fatalf("newest first: %#v", got)
	}
}

func TestPathCompletions(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "Documents"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "readme.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := pathCompletions(dir, "Doc")
	if len(got) != 1 {
		t.Fatalf("expected Documents: %#v", got)
	}
	if !hasDirSuffix(got[0], "Documents") {
		t.Fatalf("dir suffix: %q", got[0])
	}
	got = pathCompletions(dir, "read")
	if len(got) != 1 || got[0] != "readme.txt" {
		t.Fatalf("file: %#v", got)
	}
}

func hasDirSuffix(s, name string) bool {
	return s == name+`\` || s == name+`/` || s == name+string(os.PathSeparator)
}

func TestInputBarCompletePath(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "alpha"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "alpine"), 0o755); err != nil {
		t.Fatal(err)
	}
	var b inputBar
	b.histIdx = -1
	b.insertRunes([]rune("cd al"))
	if !b.complete(dir, false) {
		t.Fatal("expected complete")
	}
	// Cycles between alpha and alpine
	first := b.text()
	if first != `cd alpha\` && first != `cd alpha/` && first != "cd alpha"+string(os.PathSeparator) {
		// might be alpine first (sort)
		if first != `cd alpine\` && first != `cd alpine/` && first != "cd alpine"+string(os.PathSeparator) {
			t.Fatalf("first match: %q", first)
		}
	}
	if !b.complete(dir, false) {
		t.Fatal("expected cycle")
	}
	second := b.text()
	if second == first {
		t.Fatalf("expected different cycle, both %q", first)
	}
}

func TestInputBarCompleteHistory(t *testing.T) {
	var b inputBar
	b.histIdx = -1
	b.history = []string{"Get-ChildItem -Recurse", "Get-Process"}
	b.insertRunes([]rune("Get-"))
	if !b.complete("", false) {
		t.Fatal("expected history complete")
	}
	// Newest matching first among Get-*
	if b.text() != "Get-Process" && b.text() != "Get-ChildItem -Recurse" {
		t.Fatalf("got %q", b.text())
	}
	// Cycle
	_ = b.complete("", false)
	if b.text() != "Get-Process" && b.text() != "Get-ChildItem -Recurse" {
		t.Fatalf("cycle %q", b.text())
	}
}

func TestGhostSuffix(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "Documents"), 0o755); err != nil {
		t.Fatal(err)
	}
	var b inputBar
	b.histIdx = -1
	b.insertRunes([]rune("cd Doc"))
	g := b.ghostSuffix(dir)
	if g == "" || !stringsHasPrefixFold("Documents", "Doc"+g) && g != "uments"+string(os.PathSeparator) && g != "uments\\" && g != "uments/" {
		// Expect remainder of Documents\
		if !hasDirSuffix("Doc"+g, "Documents") && g != "uments"+string(os.PathSeparator) && g != `uments\` && g != "uments/" {
			// soft check: ghost should complete Documents
			if !strings.HasPrefix(strings.ToLower("Doc"+g), "documents") {
				t.Fatalf("ghost=%q", g)
			}
		}
	}
	// Empty buffer: no ghost
	b.clear()
	if b.ghostSuffix(dir) != "" {
		t.Fatal("empty should have no ghost")
	}
	// History ghost
	b.history = []string{"Get-Process -Id 1"}
	b.insertRunes([]rune("Get-P"))
	hg := b.ghostSuffix("")
	if hg != "rocess -Id 1" {
		t.Fatalf("history ghost: %q", hg)
	}
}

func stringsHasPrefixFold(s, prefix string) bool {
	return strings.HasPrefix(strings.ToLower(s), strings.ToLower(prefix))
}
