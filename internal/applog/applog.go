// Package applog configures Charm's logger (github.com/charmbracelet/log)
// for suzuri. GUI hosts often have no useful console, so we always append to
// a file under %LOCALAPPDATA%\suzuri\ and mirror to stderr when present.
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
)

var (
	mu   sync.Mutex
	file *os.File
	// Path is the active log file, if any.
	Path string
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

func dataDir() (string, error) {
	base := os.Getenv("LOCALAPPDATA")
	if base == "" {
		base = os.TempDir()
	}
	dir := filepath.Join(base, "suzuri")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	return dir, nil
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
