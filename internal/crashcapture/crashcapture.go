// Package crashcapture notices when the previous suzuri run died without a
// clean exit and says where its traces went.
//
// Each GUI launch writes a run marker (runs/<pid>.json under the data dir).
// A clean exit removes it. A marker left behind by a process that is no
// longer running means that process crashed or was killed. The next launch
// reads the marker, looks for the WER LocalDumps file for that pid
// (suzuri.exe.<pid>.dmp), takes the exception code (NTSTATUS, which is also
// the process exit code for an unhandled exception) out of the dump, appends
// one line to suzuri-crashes.log and returns a Crash for the startup notice.
//
// Native access violations (ConPTY, GDI) never reach Go's panic output; see
// docs/crash-dumps.md for the WER setup and how to open a dump.
package crashcapture

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Marker is what a running process leaves in runs/<pid>.json.
type Marker struct {
	PID     int       `json:"pid"`
	Started time.Time `json:"started"`
	Version string    `json:"version"`
}

// Crash describes a previous run that ended without a clean exit.
type Crash struct {
	Marker
	// DumpPath is the WER minidump for that pid, if one was written.
	DumpPath string
	// Code is the exception code read from the dump (an NTSTATUS such as
	// 0xC0000005). It is also the process exit code. Valid when HasCode.
	Code    uint32
	HasCode bool
	// DumpErr says why the dump could not be read, when it exists but failed.
	DumpErr error
	// Found is when this launch noticed the crash.
	Found time.Time
}

// ExitCode is the code as the shell would print it ("0xC0000005"), or
// "unknown" without a dump.
func (c Crash) ExitCode() string {
	if !c.HasCode {
		return "unknown"
	}
	return fmt.Sprintf("0x%08X", c.Code)
}

// Status is the NTSTATUS name for Code, or "" when unknown.
func (c Crash) Status() string {
	if !c.HasCode {
		return ""
	}
	return StatusName(c.Code)
}

var (
	mu      sync.Mutex
	current string // this process's marker path
)

// RunsDir is where run markers live under the data dir.
func RunsDir(dataDir string) string { return filepath.Join(dataDir, "runs") }

// CrashLogPath is the one-line-per-crash record under the data dir.
func CrashLogPath(dataDir string) string { return filepath.Join(dataDir, "suzuri-crashes.log") }

// DumpDirs lists where WER LocalDumps may put suzuri.exe dumps: the folder
// enable-wer-dumps.ps1 configures first, then WER's default.
func DumpDirs(dataDir string) []string {
	dirs := []string{filepath.Join(dataDir, "CrashDumps")}
	if base := os.Getenv("LOCALAPPDATA"); base != "" {
		dirs = append(dirs, filepath.Join(base, "CrashDumps"))
	}
	return dirs
}

// Begin writes this process's run marker. Call once, after the
// single-instance check, so a second launch that only activates the first
// window leaves nothing behind.
func Begin(dataDir, version string) error {
	dir := RunsDir(dataDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	m := Marker{PID: os.Getpid(), Started: time.Now().UTC(), Version: version}
	b, err := json.Marshal(m)
	if err != nil {
		return err
	}
	p := filepath.Join(dir, strconv.Itoa(m.PID)+".json")
	f, err := os.OpenFile(p, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	_, werr := f.Write(b)
	serr := f.Sync()
	cerr := f.Close()
	if err := errors.Join(werr, serr, cerr); err != nil {
		return err
	}
	mu.Lock()
	current = p
	mu.Unlock()
	return nil
}

// MarkCleanExit removes this process's run marker. Call on every exit that
// is not a crash: the end of main, and the updater's relaunch-and-exit.
// Safe to call more than once and without Begin.
func MarkCleanExit() {
	mu.Lock()
	p := current
	current = ""
	mu.Unlock()
	if p != "" {
		_ = os.Remove(p)
	}
}

// Collect finds markers left by processes that are no longer running,
// records each in suzuri-crashes.log, removes the marker and returns them,
// newest first. Markers of live processes (another instance) are kept.
func Collect(dataDir string) []Crash {
	return collect(dataDir, DumpDirs(dataDir), processAlive, time.Now())
}

func collect(dataDir string, dumpDirs []string, alive func(Marker) bool, now time.Time) []Crash {
	dir := RunsDir(dataDir)
	ents, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	self := os.Getpid()
	var out []Crash
	for _, e := range ents {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".json") {
			continue
		}
		p := filepath.Join(dir, name)
		b, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		var m Marker
		if json.Unmarshal(b, &m) != nil || m.PID <= 0 {
			_ = os.Remove(p) // unreadable marker: nothing to report
			continue
		}
		if m.PID == self || alive(m) {
			continue
		}
		c := Crash{Marker: m, Found: now}
		c.DumpPath = findDump(dumpDirs, m)
		if c.DumpPath != "" {
			code, err := ReadExceptionCode(c.DumpPath)
			if err != nil {
				c.DumpErr = err
			} else {
				c.Code, c.HasCode = code, true
			}
		}
		appendCrashLog(CrashLogPath(dataDir), c)
		_ = os.Remove(p)
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Started.After(out[j].Started) })
	return out
}

// findDump returns the WER dump for m's pid: suzuri.exe.<pid>.dmp, written
// after the run started. WER appends ".1", ".2"... when names collide, so
// the newest match wins.
func findDump(dirs []string, m Marker) string {
	var best string
	var bestT time.Time
	prefix := "suzuri.exe." + strconv.Itoa(m.PID) + "."
	for _, d := range dirs {
		ents, err := os.ReadDir(d)
		if err != nil {
			continue
		}
		for _, e := range ents {
			n := strings.ToLower(e.Name())
			if e.IsDir() || !strings.HasPrefix(n, prefix) || !strings.Contains(n, ".dmp") {
				continue
			}
			info, err := e.Info()
			if err != nil {
				continue
			}
			t := info.ModTime()
			if !m.Started.IsZero() && t.Before(m.Started.Add(-time.Minute)) {
				continue // an older process with a reused pid
			}
			if best == "" || t.After(bestT) {
				best, bestT = filepath.Join(d, e.Name()), t
			}
		}
	}
	return best
}

func appendCrashLog(path string, c Crash) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = f.WriteString(LogLine(c) + "\n")
}

// LogLine is the suzuri-crashes.log record for c.
func LogLine(c Crash) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s pid=%d started=%s version=%s exit=%s",
		c.Found.UTC().Format(time.RFC3339), c.PID, c.Started.UTC().Format(time.RFC3339),
		orDash(c.Version), c.ExitCode())
	if s := c.Status(); s != "" {
		fmt.Fprintf(&b, " ntstatus=%s", s)
	}
	if c.DumpPath != "" {
		fmt.Fprintf(&b, " dump=%q", c.DumpPath)
	} else {
		b.WriteString(" dump=none")
	}
	if c.DumpErr != nil {
		fmt.Fprintf(&b, " dump_err=%q", c.DumpErr.Error())
	}
	return b.String()
}

// Notice is the startup card for the newest crash. The card is narrow, so
// the title stays short and the body leads with the folder; a click opens
// dir. The full log and dump paths are in suzuri.log and suzuri-crashes.log.
func Notice(c Crash, logPath string) (title, body, dir string) {
	title = "suzuri closed unexpectedly"
	dir = filepath.Dir(logPath)
	what := "log saved to "
	if c.DumpPath != "" {
		what = "log + dump saved to "
		if dd := filepath.Dir(c.DumpPath); dd != dir {
			dir = dd // dump outside the data dir (WER default folder)
		}
	}
	parts := []string{what + dir}
	if c.HasCode {
		e := "exit " + c.ExitCode()
		if s := c.Status(); s != "" {
			e += " " + s
		}
		parts = append(parts, e)
	} else if c.DumpPath == "" {
		parts = append(parts, "no dump: see docs/crash-dumps.md")
	}
	parts = append(parts, "click to open")
	return title, strings.Join(parts, " · "), dir
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
