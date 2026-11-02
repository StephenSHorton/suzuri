package crashcapture

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// fakeDump builds a minimal minidump: header, a two-entry directory (a
// dummy stream, then the exception stream) and the exception record.
func fakeDump(t *testing.T, code uint32, withException bool) []byte {
	t.Helper()
	le := binary.LittleEndian
	var b bytes.Buffer
	w := func(v uint32) { _ = binary.Write(&b, le, v) }
	n := uint32(1)
	if withException {
		n = 2
	}
	w(mdmpSignature)
	w(0xa793) // version
	w(n)
	w(32) // directory rva
	w(0)
	w(0)
	w(0)
	w(0)
	// directory at 32
	w(3) // ThreadListStream, ignored
	w(4)
	w(56)
	if withException {
		w(exceptionStream)
		w(168)
		w(60)
	}
	for b.Len() < 56 {
		b.WriteByte(0)
	}
	w(0) // thread list count
	// exception stream at 60: ThreadId, __alignment, ExceptionCode, ...
	w(1234)
	w(0)
	w(code)
	for b.Len() < 60+168 {
		b.WriteByte(0)
	}
	return b.Bytes()
}

func TestExceptionCode(t *testing.T) {
	code, err := exceptionCode(bytes.NewReader(fakeDump(t, 0xC0000005, true)))
	if err != nil || code != 0xC0000005 {
		t.Fatalf("code=%#x err=%v", code, err)
	}
	if _, err := exceptionCode(bytes.NewReader(fakeDump(t, 0, false))); !errors.Is(err, ErrNoException) {
		t.Fatalf("no exception stream: err=%v", err)
	}
	if _, err := exceptionCode(bytes.NewReader([]byte("PK\x03\x04 not a dump at all"))); err == nil {
		t.Fatal("non-dump accepted")
	}
}

func writeMarker(t *testing.T, dir string, m Marker) {
	t.Helper()
	b, _ := json.Marshal(m)
	if err := os.MkdirAll(RunsDir(dir), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(RunsDir(dir), strconv.Itoa(m.PID)+".json"), b, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestCollectReportsDeadRunsWithDump(t *testing.T) {
	data := t.TempDir()
	dumps := filepath.Join(data, "CrashDumps")
	if err := os.MkdirAll(dumps, 0o755); err != nil {
		t.Fatal(err)
	}
	started := time.Now().Add(-time.Hour).UTC().Truncate(time.Second)
	writeMarker(t, data, Marker{PID: 21324, Started: started, Version: "0.9.104"})
	writeMarker(t, data, Marker{PID: 16220, Started: started.Add(-time.Hour), Version: "0.9.103"})
	writeMarker(t, data, Marker{PID: 777, Started: started, Version: "0.9.104"}) // still running
	dump := filepath.Join(dumps, "suzuri.exe.21324.dmp")
	if err := os.WriteFile(dump, fakeDump(t, 0xC0000005, true), 0o644); err != nil {
		t.Fatal(err)
	}
	// A dump for another pid must not be matched.
	_ = os.WriteFile(filepath.Join(dumps, "suzuri.exe.2132.dmp"), fakeDump(t, 0xC0000409, true), 0o644)

	alive := func(m Marker) bool { return m.PID == 777 }
	got := collect(data, []string{dumps}, alive, time.Now())
	if len(got) != 2 {
		t.Fatalf("got %d crashes, want 2: %+v", len(got), got)
	}
	c := got[0] // newest first
	if c.PID != 21324 || c.DumpPath != dump || !c.HasCode || c.Code != 0xC0000005 {
		t.Fatalf("newest crash = %+v", c)
	}
	if c.ExitCode() != "0xC0000005" || c.Status() != "STATUS_ACCESS_VIOLATION" {
		t.Fatalf("exit=%s status=%s", c.ExitCode(), c.Status())
	}
	if got[1].PID != 16220 || got[1].DumpPath != "" || got[1].HasCode || got[1].ExitCode() != "unknown" {
		t.Fatalf("dumpless crash = %+v", got[1])
	}
	// Markers of dead runs are gone, the live one stays; a second launch
	// reports nothing new.
	if _, err := os.Stat(filepath.Join(RunsDir(data), "21324.json")); !os.IsNotExist(err) {
		t.Fatal("dead marker kept")
	}
	if _, err := os.Stat(filepath.Join(RunsDir(data), "777.json")); err != nil {
		t.Fatal("live marker removed")
	}
	if again := collect(data, []string{dumps}, alive, time.Now()); len(again) != 0 {
		t.Fatalf("reported twice: %+v", again)
	}
	log, err := os.ReadFile(CrashLogPath(data))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(log)), "\n")
	if len(lines) != 2 {
		t.Fatalf("crash log:\n%s", log)
	}
	if !strings.Contains(string(log), "pid=21324") || !strings.Contains(string(log), "exit=0xC0000005 ntstatus=STATUS_ACCESS_VIOLATION") ||
		!strings.Contains(string(log), "pid=16220") || !strings.Contains(string(log), "exit=unknown") || !strings.Contains(string(log), "dump=none") {
		t.Fatalf("crash log:\n%s", log)
	}
}

func TestFindDumpSkipsOlderPidReuse(t *testing.T) {
	dumps := t.TempDir()
	p := filepath.Join(dumps, "suzuri.exe.42.dmp")
	_ = os.WriteFile(p, fakeDump(t, 0xC0000005, true), 0o644)
	old := time.Now().Add(-48 * time.Hour)
	_ = os.Chtimes(p, old, old)
	if got := findDump([]string{dumps}, Marker{PID: 42, Started: time.Now().Add(-time.Hour)}); got != "" {
		t.Fatalf("matched a dump older than the run: %s", got)
	}
	if got := findDump([]string{dumps}, Marker{PID: 42, Started: old.Add(-time.Hour)}); got != p {
		t.Fatalf("findDump = %q", got)
	}
}

func TestBeginAndCleanExit(t *testing.T) {
	data := t.TempDir()
	if err := Begin(data, "dev"); err != nil {
		t.Fatal(err)
	}
	mp := filepath.Join(RunsDir(data), strconv.Itoa(os.Getpid())+".json")
	b, err := os.ReadFile(mp)
	if err != nil {
		t.Fatal(err)
	}
	var m Marker
	if err := json.Unmarshal(b, &m); err != nil || m.PID != os.Getpid() || m.Version != "dev" {
		t.Fatalf("marker %s: %+v %v", b, m, err)
	}
	// Our own marker is never reported, even though we are "alive".
	if got := Collect(data); len(got) != 0 {
		t.Fatalf("own marker reported: %+v", got)
	}
	MarkCleanExit()
	MarkCleanExit()
	if _, err := os.Stat(mp); !os.IsNotExist(err) {
		t.Fatal("marker left after clean exit")
	}
}

func TestNotice(t *testing.T) {
	logPath := filepath.Join("data", "suzuri.log")
	c := Crash{Marker: Marker{PID: 9}, DumpPath: filepath.Join("data", "CrashDumps", "suzuri.exe.9.dmp"), Code: 0xC0000005, HasCode: true}
	title, body, dir := Notice(c, logPath)
	if title != "suzuri closed unexpectedly" || dir != filepath.Join("data", "CrashDumps") {
		t.Fatalf("title %q dir %q", title, dir)
	}
	for _, want := range []string{"log + dump saved to " + dir, "exit 0xC0000005 STATUS_ACCESS_VIOLATION", "click to open"} {
		if !strings.Contains(body, want) {
			t.Fatalf("body %q lacks %q", body, want)
		}
	}
	_, body, dir = Notice(Crash{Marker: Marker{PID: 9}}, logPath)
	if dir != "data" || !strings.Contains(body, "log saved to data") || !strings.Contains(body, "no dump") {
		t.Fatalf("dumpless notice: %q in %q", body, dir)
	}
}

func TestArmDevCrashOnlyInDevWithEnv(t *testing.T) {
	t.Setenv(DevCrashEnv, "av")
	if ArmDevCrash("0.9.104") {
		t.Fatal("release build armed the dev crash")
	}
	t.Setenv(DevCrashEnv, "")
	if ArmDevCrash("dev") {
		t.Fatal("armed without SUZURI_DEV_CRASH=av")
	}
}
