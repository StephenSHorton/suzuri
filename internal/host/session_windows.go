//go:build windows

package host

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"

	"github.com/UserExistsError/conpty"
)

// Session is a live shell attached to a Windows ConPTY.
type Session struct {
	cpty *conpty.ConPty
	once sync.Once
}

// DefaultShell returns a sensible Windows shell command line.
//
// -NoProfile keeps the prompt ASCII-simple for v0 (profile themes often inject
// Nerd-Font / private-use glyphs that show as "unknown character" boxes).
func DefaultShell() string {
	if ps, err := exec.LookPath("pwsh.exe"); err == nil {
		return ps + " -NoLogo -NoProfile"
	}
	if ps, err := exec.LookPath("powershell.exe"); err == nil {
		return ps + " -NoLogo -NoProfile"
	}
	if s := os.Getenv("COMSPEC"); s != "" {
		return s
	}
	return `C:\Windows\System32\cmd.exe`
}

// StartSession launches commandLine (e.g. powershell) on a ConPTY of size cols×rows.
// workDir empty uses the process working directory.
func StartSession(commandLine string, cols, rows int, workDir string) (*Session, error) {
	if commandLine == "" {
		commandLine = DefaultShell()
	}
	if cols < 20 {
		cols = 80
	}
	if rows < 5 {
		rows = 24
	}
	wd := strings.TrimSpace(workDir)
	if wd == "" {
		wd = mustWd()
	} else if st, err := os.Stat(wd); err != nil || !st.IsDir() {
		wd = mustWd()
	}
	cpty, err := conpty.Start(
		commandLine,
		conpty.ConPtyDimensions(cols, rows),
		conpty.ConPtyWorkDir(wd),
	)
	if err != nil {
		return nil, fmt.Errorf("conpty start: %w", err)
	}
	return &Session{cpty: cpty}, nil
}

func mustWd() string {
	wd, err := os.Getwd()
	if err != nil {
		return ""
	}
	return wd
}

// Read implements io.Reader (PTY → host).
func (s *Session) Read(p []byte) (int, error) {
	return s.cpty.Read(p)
}

// Write implements io.Writer (host → PTY).
func (s *Session) Write(p []byte) (int, error) {
	return s.cpty.Write(p)
}

// Resize updates the ConPTY dimensions.
func (s *Session) Resize(cols, rows int) error {
	if cols < 1 || rows < 1 {
		return nil
	}
	return s.cpty.Resize(cols, rows)
}

// Pid of the attached console process.
func (s *Session) Pid() int {
	return s.cpty.Pid()
}

// Wait blocks until the process exits.
func (s *Session) Wait(ctx context.Context) (uint32, error) {
	return s.cpty.Wait(ctx)
}

// Close tears down the ConPTY and process.
func (s *Session) Close() error {
	var err error
	s.once.Do(func() {
		err = s.cpty.Close()
	})
	return err
}

// CopyOutput continuously reads the PTY into w until EOF/error.
func (s *Session) CopyOutput(w io.Writer) error {
	buf := make([]byte, 4096)
	for {
		n, err := s.Read(buf)
		if n > 0 {
			if _, werr := w.Write(buf[:n]); werr != nil {
				return werr
			}
		}
		if err != nil {
			if err == io.EOF {
				return nil
			}
			// ConPTY often surfaces read errors on process exit.
			if strings.Contains(err.Error(), "handle is invalid") {
				return nil
			}
			return err
		}
	}
}
