//go:build unix

package host

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
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
	// zdot is a temp ZDOTDIR for quiet zsh prompts (cleaned on Close).
	zdot string
}

// DefaultShell returns $SHELL or a sensible interactive shell path.
func DefaultShell() string {
	if s := strings.TrimSpace(os.Getenv("SHELL")); s != "" {
		return QuietPrompt(s)
	}
	for _, name := range []string{"zsh", "bash", "sh"} {
		if p, err := exec.LookPath(name); err == nil {
			return QuietPrompt(p)
		}
	}
	return QuietPrompt("/bin/sh")
}

// QuietPrompt rewrites a bare shell path so the in-band prompt is blank.
// The host owns input via the Warp-style bottom bar; a visible PS1/PROMPT only
// duplicates chrome (same idea as Windows ConPTY QuietPrompt).
//
// Custom -c / user command lines are left alone. Profile shells that already
// carry flags are left alone too.
func QuietPrompt(commandLine string) string {
	cl := strings.TrimSpace(commandLine)
	if cl == "" {
		return cl
	}
	lower := strings.ToLower(cl)
	// Already customized — don't double-wrap.
	if strings.Contains(lower, " -c ") || strings.Contains(lower, " -c\"") ||
		strings.Contains(lower, " --command") || strings.Contains(lower, " -command ") ||
		strings.Contains(lower, "prompt=") || strings.Contains(lower, "zdotdir") ||
		strings.Contains(lower, "--rcfile") || strings.Contains(lower, "--norc") ||
		strings.Contains(lower, " -f ") || strings.HasSuffix(lower, " -f") {
		return cl
	}
	base := filepathBase(cl)
	// Only rewrite bare shell paths (optional leading args like login -l later).
	fields := strings.Fields(cl)
	if len(fields) != 1 {
		// e.g. "/bin/zsh -l" — still quiet via env/ZDOTDIR in StartSession.
		return cl
	}
	switch {
	case base == "zsh" || strings.HasSuffix(base, "/zsh"):
		// Interactive; quiet prompt is applied via ZDOTDIR in StartSession.
		return cl
	case base == "bash" || strings.HasSuffix(base, "/bash"):
		return cl
	case base == "sh" || strings.HasSuffix(base, "/sh"):
		return cl
	default:
		return cl
	}
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
		name, args = splitCommandLine(name)
	}

	env, zdot, err := quietShellEnv(name, args, os.Environ())
	if err != nil {
		return nil, err
	}
	// bash: apply quiet --rcfile when we staged one (see quietShellEnv).
	if rc := getenv(env, "SUZURI_BASHRC"); rc != "" {
		bn := strings.ToLower(filepathBase(name))
		if bn == "bash" || strings.HasSuffix(bn, "/bash") {
			has := false
			for _, a := range args {
				if a == "--rcfile" || a == "--norc" || a == "--noprofile" {
					has = true
					break
				}
			}
			if !has {
				args = append([]string{"--rcfile", rc}, args...)
			}
		}
	}

	cmd := exec.Command(name, args...)
	cmd.Dir = wd
	cmd.Env = env

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
		if zdot != "" {
			_ = os.RemoveAll(zdot)
		}
		return nil, fmt.Errorf("pty start: %w", err)
	}
	return &Session{cmd: cmd, ptmx: ptmx, zdot: zdot}, nil
}

func mustWd() string {
	wd, err := os.Getwd()
	if err != nil {
		return ""
	}
	return wd
}

// quietShellEnv keeps the Warp bar as the only command surface by blanking
// the shell's in-band prompt (Windows QuietPrompt equivalent).
//
// zsh: temp ZDOTDIR with a bootstrap .zshrc that sources the user's config
// then forces PROMPT/RPROMPT blank (env PROMPT alone is overwritten by .zshrc).
// bash: inject --rcfile that sources bashrc then sets PS1.
func quietShellEnv(shellPath string, args []string, base []string) (env []string, zdot string, err error) {
	env = append([]string(nil), base...)
	env = setEnv(env, "TERM", "xterm-256color")
	if getenv(env, "COLORTERM") == "" {
		env = setEnv(env, "COLORTERM", "truecolor")
	}

	baseName := strings.ToLower(filepathBase(shellPath))
	// Skip quieting when the user already passed a custom -c / rcfile.
	joined := strings.ToLower(strings.Join(args, " "))
	custom := strings.Contains(joined, "-c") || strings.Contains(joined, "rcfile") ||
		strings.Contains(joined, "norc") || strings.Contains(joined, "noprofile")

	switch {
	case !custom && (baseName == "zsh" || strings.HasSuffix(baseName, "/zsh")):
		dir, err := os.MkdirTemp("", "suzuri-zdot-*")
		if err != nil {
			return env, "", err
		}
		// Bootstrap: load user rc when present, then force a blank prompt.
		// A single space keeps zsh happy (empty PROMPT can fall back to defaults).
		// Themes/plugins often re-set PROMPT in precmd — append a hook that wins last.
		content := `# suzuri quiet-prompt bootstrap — Warp bar owns the command line
[[ -f "$HOME/.zshenv" ]] && source "$HOME/.zshenv"
[[ -f "$HOME/.zprofile" ]] && source "$HOME/.zprofile"
[[ -f "$HOME/.zshrc" ]] && source "$HOME/.zshrc"
_suzuri_quiet_prompt() {
  PROMPT=' '
  RPROMPT=''
  PS1=' '
  unset RPS1
  # Hide the reverse-video "%" zsh draws when the prior line had no newline.
  PROMPT_EOL_MARK=''
}
_suzuri_quiet_prompt
# Run after theme precmd hooks so starship/p10k/oh-my-zsh cannot repaint a prompt.
precmd_functions+=(_suzuri_quiet_prompt)
`
		if err := os.WriteFile(filepath.Join(dir, ".zshrc"), []byte(content), 0o644); err != nil {
			_ = os.RemoveAll(dir)
			return env, "", err
		}
		// Empty .zshenv so nested zsh doesn't re-enter user zshenv before ours.
		_ = os.WriteFile(filepath.Join(dir, ".zshenv"), []byte("# suzuri\n"), 0o644)
		env = setEnv(env, "ZDOTDIR", dir)
		env = setEnv(env, "PROMPT", " ")
		env = setEnv(env, "RPROMPT", "")
		return env, dir, nil

	case !custom && (baseName == "bash" || strings.HasSuffix(baseName, "/bash")):
		dir, err := os.MkdirTemp("", "suzuri-brc-*")
		if err != nil {
			return env, "", err
		}
		content := `# suzuri quiet-prompt bootstrap
[[ -f "$HOME/.bashrc" ]] && source "$HOME/.bashrc"
PS1=' '
PROMPT_COMMAND=''
`
		rc := filepath.Join(dir, "bashrc")
		if err := os.WriteFile(rc, []byte(content), 0o644); err != nil {
			_ = os.RemoveAll(dir)
			return env, "", err
		}
		// bash reads --rcfile only if we pass it; inject via BASH_ENV for non-interactive
		// and rely on caller args. For interactive bash without --rcfile, set ENV vars
		// and use --rcfile by rewriting is hard here — put path in env for StartSession.
		env = setEnv(env, "SUZURI_BASHRC", rc)
		env = setEnv(env, "PS1", " ")
		env = setEnv(env, "PROMPT_COMMAND", "")
		// Interactive bash: if no args, pty Start uses just "bash". Append --rcfile
		// is done by rewriting args in StartSession — handled below via marker.
		return env, dir, nil

	default:
		env = setEnv(env, "PS1", " ")
		env = setEnv(env, "PROMPT", " ")
		return env, "", nil
	}
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
// For bare bash, injects --rcfile from SUZURI_BASHRC when present (set later).
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
			_ = s.cmd.Process.Signal(syscall.SIGHUP)
			_ = s.cmd.Process.Kill()
		}
		if s.zdot != "" {
			_ = os.RemoveAll(s.zdot)
			s.zdot = ""
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
			if strings.Contains(err.Error(), "input/output error") ||
				strings.Contains(err.Error(), "file already closed") {
				return nil
			}
			return err
		}
	}
}
