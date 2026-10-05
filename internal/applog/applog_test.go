package applog

import (
	"os"
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

func TestFormatKVLine(t *testing.T) {
	line := formatKVLine("crash", "os.Exit", "code", 1, "reason", "ui.Run failed")
	if !strings.Contains(line, "crash os.Exit") || !strings.Contains(line, "code=1") {
		t.Fatalf("%q", line)
	}
	if !strings.HasSuffix(line, "\n") {
		t.Fatal("must end with newline")
	}
}
