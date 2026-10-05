package applog

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteCrashNoteAndRaw(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("LOCALAPPDATA", dir)
	if _, err := Init(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(Close)
	if CrashPath == "" || TrailPath == "" {
		t.Fatal("crash/trail paths empty")
	}
	WriteCrashNote("test-reason", "k", "v")
	WriteRaw([]byte("native-exception code=0xC00000FD\n"))
	Sync()

	trail, err := os.ReadFile(TrailPath)
	if err != nil {
		t.Fatal(err)
	}
	s := string(trail)
	if !strings.Contains(s, "crash test-reason") || !strings.Contains(s, "k=v") {
		t.Fatalf("trail missing crash note: %s", s)
	}
	if !strings.Contains(s, "native-exception code=0xC00000FD") {
		t.Fatalf("trail missing raw: %s", s)
	}
	crash, err := os.ReadFile(CrashPath)
	if err != nil {
		t.Fatal(err)
	}
	cs := string(crash)
	if !strings.Contains(cs, "crash-output open") {
		t.Fatal("startup marker missing")
	}
	if !strings.Contains(cs, "test-reason") || !strings.Contains(cs, "native-exception code=0xC00000FD") {
		t.Fatalf("crash file missing notes: %s", cs)
	}
}

func TestLogRotatesWhenHuge(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("LOCALAPPDATA", dir)
	logDir := filepath.Join(dir, "suzuri")
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(logDir, "suzuri.log")
	if err := os.WriteFile(path, bytes.Repeat([]byte("x"), maxLogBytes+32), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Init(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(Close)
	if _, err := os.Stat(path + ".1"); err != nil {
		t.Fatalf("rotated log missing: %v", err)
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Size() >= maxLogBytes {
		t.Fatalf("new log still huge: %d", fi.Size())
	}
}

func TestPreviousUncleanAndHeartbeat(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("LOCALAPPDATA", dir)
	logDir := filepath.Join(dir, "suzuri")
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		t.Fatal(err)
	}
	alive := filepath.Join(logDir, "suzuri-alive")
	if err := os.WriteFile(alive, []byte("heartbeat leftover gdi=12\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Init(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(Close)
	if got := PreviousUnclean(); !strings.Contains(got, "heartbeat leftover") {
		t.Fatalf("unclean %q", got)
	}
	Heartbeat("gdi", 4, "paint_ms", 12)
	Sync()
	b, err := os.ReadFile(TrailPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "heartbeat") || !strings.Contains(string(b), "gdi=4") {
		t.Fatalf("trail %s", b)
	}
	MarkClean()
	if _, err := os.Stat(alive); !os.IsNotExist(err) {
		t.Fatalf("alive file should be gone after clean: %v", err)
	}
}

func TestCrashOutputConfiguredForThrowAndFatal(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("LOCALAPPDATA", dir)
	if _, err := Init(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(Close)
	if CrashPath == "" {
		t.Fatal("SetCrashOutput must install a crash file")
	}
	b, err := os.ReadFile(CrashPath)
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	if !strings.Contains(s, "crash-output open") {
		t.Fatal("startup marker")
	}
	if !strings.Contains(s, "throw/fatal") || !strings.Contains(s, "unrecovered panic") {
		t.Fatalf("must document that SetCrashOutput is throw/fatal, not only panic: %s", s)
	}
}

func TestLooksSevere(t *testing.T) {
	if !looksSevere([]byte("2026-01-01 ERR suzuri foo")) {
		t.Fatal("ERR")
	}
	if !looksSevere([]byte("2026-01-01 WRN suzuri bar")) {
		t.Fatal("WRN")
	}
	if looksSevere([]byte("2026-01-01 INF suzuri ok")) {
		t.Fatal("info must not fsync")
	}
}

func TestFormatKVLine(t *testing.T) {
	line := formatKVLine("crash", "os.Exit", "code", 1, "reason", "ui.Run failed")
	if !strings.Contains(line, "crash os.Exit") || !strings.Contains(line, "code=1") {
		t.Fatalf("%q", line)
	}
	if !strings.HasSuffix(line, "\n") {
		t.Fatal("must end with newline")
	}
}
