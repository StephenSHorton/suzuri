//go:build unix

package host

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"

	"github.com/creack/pty"
)

// Session is a live shell attached to a POSIX PTY.
type Session struct {
	cmd  *exec.Cmd
	ptmx *os.File
	once sync.Once
	// resizeMu serializes pty.Setsize against concurrent Resize calls.
	resizeMu sync.Mutex
}

// DefaultShell returns $SHELL or a sensible interactive shell path.
func DefaultShell() string {
	if s := strings.TrimSpace(os.Getenv("SHELL")); s != "" {
		return s
	}
	for _, name := range []string{"zsh", "bash", "sh"} {
		if p, err := exec.LookPath(name); err == nil {
			return p
		}
	}
	return "/bin/sh"
}

// QuietPrompt is a Windows ConPTY helper; on Unix the host launches shells
// with quiet-prompt env (see StartSession). Custom -c/-Command lines pass through.
func QuietPrompt(commandLine string) string {
	return strings.TrimSpace(commandLine)
}

// StartSession launches commandLine on a PTY of size cols×rows.
// workDir empty uses the process working directory.
func StartSession(commandLine string, cols, rows int, workDir string) (*Session, error) {
	if commandLine == "" {
		commandLine = DefaultShell()
	} else {
		commandLine = QuietPrompt(commandLine)
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

	name, args := splitCommandLine(commandLine)
	if name == "" {
		name = DefaultShell()
		args = nil
	}
	cmd := exec.Command(name, args...)
	cmd.Dir = wd
	cmd.Env = quietShellEnv(name, os.Environ())

	if cols > 32767 {
		cols = 32767
	}
	if rows > 32767 {
		rows = 32767
	}
	ptmx, err := pty.StartWithSize(cmd, &pty.Winsize{
		Rows: uint16(rows),
		Cols: uint16(cols),
	})
	if err != nil {
		return nil, fmt.Errorf("pty start: %w", err)
	}
	return &Session{cmd: cmd, ptmx: ptmx}, nil
}

func mustWd() string {
	wd, err := os.Getwd()
	if err != nil {
		return ""
	}
	return wd
}

// quietShellEnv keeps the Warp-style bottom bar as the primary command surface
// by blanking common prompt variables for interactive shells.
func quietShellEnv(shellPath string, base []string) []string {
	env := append([]string(nil), base...)
	// Always advertise a modern terminal for color/apps.
	env = setEnv(env, "TERM", "xterm-256color")
	if getenv(env, "COLORTERM") == "" {
		env = setEnv(env, "COLORTERM", "truecolor")
	}
	baseName := strings.ToLower(filepathBase(shellPath))
	switch {
	case strings.Contains(baseName, "zsh"):
		// zsh reads PROMPT from the environment when not set in rc for some setups;
		// also force a minimal prompt via ZDOTDIR is heavier — env is enough for -f-less starts.
		env = setEnv(env, "PROMPT", " ")
		env = setEnv(env, "RPROMPT", "")
		env = setEnv(env, "PS1", " ")
	case strings.Contains(baseName, "bash"), strings.Contains(baseName, "sh"):
		env = setEnv(env, "PS1", " ")
		env = setEnv(env, "PROMPT_COMMAND", "")
	}
	return env
}

func setEnv(env []string, key, val string) []string {
	prefix := key + "="
	for i, e := range env {
		if strings.HasPrefix(e, prefix) {
			env[i] = prefix + val
			return env
		}
	}
	return append(env, prefix+val)
}

func getenv(env []string, key string) string {
	prefix := key + "="
	for _, e := range env {
		if strings.HasPrefix(e, prefix) {
			return e[len(prefix):]
		}
	}
	return ""
}

// splitCommandLine splits a simple shell command into name + args.
// Quoted tokens with spaces are supported (double quotes only).
func splitCommandLine(cl string) (string, []string) {
	cl = strings.TrimSpace(cl)
	if cl == "" {
		return "", nil
	}
	var (
		parts []string
		cur   strings.Builder
		inQ   bool
	)
	flush := func() {
		if cur.Len() == 0 {
			return
		}
		parts = append(parts, cur.String())
		cur.Reset()
	}
	for i := 0; i < len(cl); i++ {
		c := cl[i]
		switch {
		case c == '"':
			inQ = !inQ
		case (c == ' ' || c == '\t') && !inQ:
			flush()
		default:
			cur.WriteByte(c)
		}
	}
	flush()
	if len(parts) == 0 {
		return "", nil
	}
	return parts[0], parts[1:]
}

func filepathBase(commandLine string) string {
	s := strings.TrimSpace(commandLine)
	if s == "" {
		return ""
	}
	if s[0] == '"' {
		if i := strings.IndexByte(s[1:], '"'); i >= 0 {
			s = s[1 : 1+i]
		}
	} else if i := strings.IndexAny(s, " \t"); i >= 0 {
		s = s[:i]
	}
	if i := strings.LastIndexByte(s, '/'); i >= 0 {
		s = s[i+1:]
	}
	return s
}

// Read implements io.Reader (PTY → host).
func (s *Session) Read(p []byte) (int, error) {
	if s == nil || s.ptmx == nil {
		return 0, io.EOF
	}
	return s.ptmx.Read(p)
}

// Write implements io.Writer (host → PTY).
func (s *Session) Write(p []byte) (int, error) {
	if s == nil || s.ptmx == nil {
		return 0, io.ErrClosedPipe
	}
	return s.ptmx.Write(p)
}

// Resize updates the PTY dimensions.
func (s *Session) Resize(cols, rows int) error {
	if s == nil || s.ptmx == nil {
		return nil
	}
	if cols < 1 || rows < 1 {
		return nil
	}
	if cols > 32767 {
		cols = 32767
	}
	if rows > 32767 {
		rows = 32767
	}
	s.resizeMu.Lock()
	defer s.resizeMu.Unlock()
	return pty.Setsize(s.ptmx, &pty.Winsize{
		Rows: uint16(rows),
		Cols: uint16(cols),
	})
}

// Pid of the attached shell process.
func (s *Session) Pid() int {
	if s == nil || s.cmd == nil || s.cmd.Process == nil {
		return 0
	}
	return s.cmd.Process.Pid
}

// Wait blocks until the process exits.
func (s *Session) Wait(ctx context.Context) (uint32, error) {
	if s == nil || s.cmd == nil {
		return 0, fmt.Errorf("no session")
	}
	done := make(chan error, 1)
	go func() { done <- s.cmd.Wait() }()
	select {
	case <-ctx.Done():
		return 0, ctx.Err()
	case err := <-done:
		if err == nil {
			return 0, nil
		}
		if ee, ok := err.(*exec.ExitError); ok {
			if status, ok := ee.Sys().(syscall.WaitStatus); ok {
				return uint32(status.ExitStatus()), nil
			}
			return uint32(ee.ExitCode()), nil
		}
		return 1, err
	}
}

// Close tears down the PTY and process.
func (s *Session) Close() error {
	var err error
	s.once.Do(func() {
		if s.ptmx != nil {
			_ = s.ptmx.Close()
		}
		if s.cmd != nil && s.cmd.Process != nil {
			// Hangup the process group first; fall back to kill.
			_ = s.cmd.Process.Signal(syscall.SIGHUP)
			_ = s.cmd.Process.Kill()
		}
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
			// PTY close after process exit often surfaces as EIO.
			if strings.Contains(err.Error(), "input/output error") ||
				strings.Contains(err.Error(), "file already closed") {
				return nil
			}
			return err
		}
	}
}
