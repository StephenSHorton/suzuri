// Package applog configures Charm's logger (github.com/charmbracelet/log)
// for suzuri. GUI hosts often have no useful console, so we always append to
// a file under the OS config dir (…/suzuri/suzuri.log) and mirror to stderr
// when present.
package applog

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/log"

	"github.com/StephenSHorton/suzuri/internal/config"
)

var (
	mu   sync.Mutex
	file *os.File
	// Path is the active log file, if any.
	Path string

	// trail is a tiny durable breadcrumb file (synced every write) so native
	// hard deaths that skip Go's logger still leave a last-op trail.
	trail *os.File
	// TrailPath is the breadcrumb file, if open.
	TrailPath string
	// CrashPath is where runtime fatal output (panic/throw) is mirrored.
	CrashPath string
	crash     *os.File
)

// Init opens the log file, sets the package default Charm logger, and returns
// the log path. Safe to call once at process start.
func Init() (string, error) {
	mu.Lock()
	defer mu.Unlock()

	dir, err := dataDir()
	if err != nil {
		setup(os.Stderr, log.InfoLevel)
		return "", err
	}
	path := filepath.Join(dir, "suzuri.log")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		setup(os.Stderr, log.InfoLevel)
		return "", err
	}
	file = f
	Path = path

	// Durable trail + runtime crash output (best-effort; never fail Init).
	openTrailLocked(dir)
	openCrashOutputLocked(dir)

	level := log.InfoLevel
	if v := strings.TrimSpace(os.Getenv("SUZURI_LOG_LEVEL")); v != "" {
		if lv, perr := log.ParseLevel(v); perr == nil {
			level = lv
		}
	} else {
		// Early product: debug to the file by default so crashes are diagnosable.
		level = log.DebugLevel
	}

	// File always; stderr when launched from a console.
	w := io.Writer(f)
	if isTerminal(os.Stderr) {
		w = io.MultiWriter(f, os.Stderr)
	}
	setup(w, level)
	return path, nil
}

func openTrailLocked(dir string) {
	tp := filepath.Join(dir, "suzuri-trail.log")
	tf, err := os.OpenFile(tp, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	trail = tf
	TrailPath = tp
}

func openCrashOutputLocked(dir string) {
	cp := filepath.Join(dir, "suzuri-crash.txt")
	cf, err := os.OpenFile(cp, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	// runtime/debug.SetCrashOutput duplicates the fd; we keep cf open for Sync.
	if err := debug.SetCrashOutput(cf, debug.CrashOptions{}); err != nil {
		_ = cf.Close()
		return
	}
	crash = cf
	CrashPath = cp
	// runtime/debug.SetCrashOutput duplicates the fd; we keep ours so
	// WriteCrashNote / WriteRaw can append a reason when the process dies
	// without a Go panic (native AV, stack overflow, os.Exit).
	_, _ = fmt.Fprintf(cf, "\n--- crash-output open pid=%d t=%s ---\n",
		os.Getpid(), time.Now().Format(time.RFC3339))
	_ = cf.Sync()
}

func dataDir() (string, error) {
	dir := config.Dir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	return dir, nil
}

// FilePath is the default suzuri.log location (even if Init has not run).
func FilePath() string {
	mu.Lock()
	p := Path
	mu.Unlock()
	if p != "" {
		return p
	}
	return filepath.Join(config.Dir(), "suzuri.log")
}

// Tail returns the last n lines of the log file (and flushes if this process owns it).
// n is clamped to [1, 5000]. Works from the MCP process without Init.
func Tail(n int) (path string, lines []string, err error) {
	if n < 1 {
		n = 100
	}
	if n > 5000 {
		n = 5000
	}
	Sync()
	path = FilePath()
	b, err := os.ReadFile(path)
	if err != nil {
		return path, nil, err
	}
	// Split on \n; keep content without trailing empty from final newline.
	text := string(b)
	if text == "" {
		return path, nil, nil
	}
	raw := strings.Split(text, "\n")
	// Drop a single trailing empty segment from a final \n.
	if len(raw) > 0 && raw[len(raw)-1] == "" {
		raw = raw[:len(raw)-1]
	}
	if len(raw) > n {
		raw = raw[len(raw)-n:]
	}
	// Strip trailing \r (Windows).
	lines = make([]string, len(raw))
	for i, ln := range raw {
		lines[i] = strings.TrimRight(ln, "\r")
	}
	return path, lines, nil
}

func setup(w io.Writer, level log.Level) {
	logger := log.NewWithOptions(w, log.Options{
		ReportTimestamp: true,
		ReportCaller:    true,
		TimeFormat:      time.RFC3339,
		Level:           level,
		Prefix:          "suzuri",
	})
	log.SetDefault(logger)
}

// Sync flushes the log file so a native crash shortly after still leaves
// the last lines on disk.
func Sync() {
	mu.Lock()
	defer mu.Unlock()
	if file != nil {
		_ = file.Sync()
	}
	if trail != nil {
		_ = trail.Sync()
	}
	if crash != nil {
		_ = crash.Sync()
	}
}

// Trail writes a single durable breadcrumb line and fsyncs. Use immediately
// before/after native-risk ops (ConPTY ResizePseudoConsole, full layout settle).
// Format: RFC3339 pid=N where msg key=val...
// Never panics; safe from any goroutine.
func Trail(where string, kvs ...any) {
	mu.Lock()
	defer mu.Unlock()
	if trail == nil {
		return
	}
	var b strings.Builder
	b.WriteString(time.Now().Format(time.RFC3339))
	b.WriteString(" pid=")
	b.WriteString(fmt.Sprint(os.Getpid()))
	b.WriteByte(' ')
	b.WriteString(where)
	for i := 0; i+1 < len(kvs); i += 2 {
		b.WriteByte(' ')
		b.WriteString(fmt.Sprint(kvs[i]))
		b.WriteByte('=')
		b.WriteString(fmt.Sprint(kvs[i+1]))
	}
	b.WriteByte('\n')
	_, _ = trail.WriteString(b.String())
	_ = trail.Sync()
}

func formatKVLine(kind, where string, kvs ...any) string {
	var b strings.Builder
	b.WriteString(time.Now().Format(time.RFC3339))
	b.WriteString(" pid=")
	b.WriteString(fmt.Sprint(os.Getpid()))
	b.WriteByte(' ')
	b.WriteString(kind)
	if where != "" {
		b.WriteByte(' ')
		b.WriteString(where)
	}
	for i := 0; i+1 < len(kvs); i += 2 {
		b.WriteByte(' ')
		b.WriteString(fmt.Sprint(kvs[i]))
		b.WriteByte('=')
		b.WriteString(fmt.Sprint(kvs[i+1]))
	}
	b.WriteByte('\n')
	return b.String()
}

// WriteRaw appends bytes to the trail and crash files and fsyncs. Safe from a
// vectored exception handler: no fmt, no logger. Empty p is a no-op.
func WriteRaw(p []byte) {
	if len(p) == 0 {
		return
	}
	mu.Lock()
	defer mu.Unlock()
	writeRawLocked(p)
}

func writeRawLocked(p []byte) {
	if trail != nil {
		_, _ = trail.Write(p)
		if p[len(p)-1] != '\n' {
			_, _ = trail.Write([]byte{'\n'})
		}
		_ = trail.Sync()
	}
	if crash != nil {
		_, _ = crash.Write(p)
		if p[len(p)-1] != '\n' {
			_, _ = crash.Write([]byte{'\n'})
		}
		_ = crash.Sync()
	}
}

// WriteCrashNote records a process-death reason on both the trail and the
// crash file. Use from os.Exit wrappers, last-tab quit, and native hooks.
func WriteCrashNote(reason string, kvs ...any) {
	line := formatKVLine("crash", reason, kvs...)
	mu.Lock()
	if trail != nil {
		_, _ = trail.WriteString(line)
		_ = trail.Sync()
	}
	if crash != nil {
		_, _ = crash.WriteString(line)
		_ = crash.Sync()
	}
	if file != nil {
		_, _ = file.WriteString(line)
		_ = file.Sync()
	}
	mu.Unlock()
	log.Error("crash note", "reason", reason)
}

// Exit writes a crash-trail reason and terminates. os.Exit skips defers, so
// this is the only host path that should call it after applog.Init.
func Exit(code int, reason string) {
	WriteCrashNote("os.Exit", "code", code, "reason", reason)
	log.Error("process exit", "code", code, "reason", reason)
	Sync()
	Close()
	os.Exit(code)
}

// Close flushes and closes the log file.
func Close() {
	mu.Lock()
	defer mu.Unlock()
	if file != nil {
		_ = file.Sync()
		_ = file.Close()
		file = nil
	}
	if trail != nil {
		_ = trail.Sync()
		_ = trail.Close()
		trail = nil
	}
	if crash != nil {
		_ = crash.Sync()
		_ = crash.Close()
		crash = nil
	}
}

// Recover logs a panic with stack and re-panics if repanic is true.
// Use: defer applog.Recover("wndproc", false)
func Recover(where string, repanic bool) {
	r := recover()
	if r == nil {
		return
	}
	log.Error("panic",
		"where", where,
		"err", fmt.Sprint(r),
		"stack", string(debug.Stack()),
	)
	mu.Lock()
	if file != nil {
		_ = file.Sync()
	}
	mu.Unlock()
	if repanic {
		panic(r)
	}
}

func isTerminal(f *os.File) bool {
	if f == nil {
		return false
	}
	fi, err := f.Stat()
	if err != nil {
		return false
	}
	// Character device (console) — rough but avoids needing golang.org/x/term.
	return (fi.Mode() & os.ModeCharDevice) != 0
}
