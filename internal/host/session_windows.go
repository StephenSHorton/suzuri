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
	// resizeMu serializes ResizePseudoConsole — concurrent Resize while I/O is
	// in flight has hard-crashed the host process (no Go panic trail).
	resizeMu sync.Mutex
}

// DefaultShell returns a sensible Windows shell command line.
//
// -NoProfile keeps the prompt ASCII-simple (profile themes often inject
// Nerd-Font glyphs). QuietPrompt strips the in-band PS/cmd prompt so the
// Warp-style bottom bar is the only command line the user types into.
func DefaultShell() string {
	if ps, err := exec.LookPath("pwsh.exe"); err == nil {
		return QuietPrompt(ps + " -NoLogo -NoProfile")
	}
	if ps, err := exec.LookPath("powershell.exe"); err == nil {
		return QuietPrompt(ps + " -NoLogo -NoProfile")
	}
	if s := os.Getenv("COMSPEC"); s != "" {
		return QuietPrompt(s)
	}
	return QuietPrompt(`C:\Windows\System32\cmd.exe`)
}

// QuietPrompt rewrites a shell command line so the in-band prompt is empty.
// The host owns input via the bottom bar; a visible PS/cmd prompt only
// duplicates chrome. Custom -Command/-c user shells are left alone.
func QuietPrompt(commandLine string) string {
	cl := strings.TrimSpace(commandLine)
	if cl == "" {
		return cl
	}
	lower := strings.ToLower(cl)
	// Already customized startup — don't double-wrap.
	if strings.Contains(lower, " -command ") || strings.Contains(lower, " -command\"") ||
		strings.Contains(lower, " -c ") || strings.Contains(lower, " /c ") ||
		strings.Contains(lower, "function prompt") {
		return cl
	}
	base := filepathBase(cl)
	switch {
	case strings.EqualFold(base, "pwsh.exe") || strings.EqualFold(base, "pwsh") ||
		strings.EqualFold(base, "powershell.exe") || strings.EqualFold(base, "powershell"):
		// Quiet visual prompt (single space) + OSC cwd report for the Warp bar
		// and command blocks. ESC]7878;cwd=<path>BEL — parsed by the host.
		// Clear-Host wipes the leftover banner row after -NoLogo.
		// Emit cwd once at startup and on every subsequent prompt.
		ps := `function global:prompt { try { $p=(Get-Location).ProviderPath; if(-not $p){$p=(Get-Location).Path}; $e=[char]27; $b=[char]7; [Console]::Out.Write(($e+']7878;cwd='+$p+$b)) } catch {}; ' ' }; Clear-Host; try { $p=(Get-Location).ProviderPath; if(-not $p){$p=(Get-Location).Path}; $e=[char]27; $b=[char]7; [Console]::Out.Write(($e+']7878;cwd='+$p+$b)) } catch {}`
		return cl + ` -NoExit -Command "` + ps + `"`
	case strings.EqualFold(base, "cmd.exe") || strings.EqualFold(base, "cmd"):
		// $S = space in cmd PROMPT; $E = ESC. Emit OSC cwd + blank visual.
		// $P = current path. BEL is ASCII 7 — use prompt $E]7878;cwd=$P$G style
		// is wrong ($G is '>'). Use $E]7878;cwd=$P$E\$S via ST terminator.
		if strings.Contains(lower, "/k") || strings.Contains(lower, "/c") {
			return cl
		}
		// ESC]7878;cwd=$P ESC\ then a space. cmd $E = ESC, $_ not available for BEL.
		return cl + ` /k prompt $E]7878;cwd=$P$E\$S`
	default:
		return cl
	}
}

// filepathBase returns the executable name from a command line (first token).
func filepathBase(commandLine string) string {
	s := strings.TrimSpace(commandLine)
	if s == "" {
		return ""
	}
	// Quoted path.
	if s[0] == '"' {
		if i := strings.IndexByte(s[1:], '"'); i >= 0 {
			s = s[1 : 1+i]
		}
	} else if i := strings.IndexAny(s, " \t"); i >= 0 {
		s = s[:i]
	}
	s = strings.ReplaceAll(s, `/`, `\`)
	if i := strings.LastIndexByte(s, '\\'); i >= 0 {
		s = s[i+1:]
	}
	return s
}

// StartSession launches commandLine (e.g. powershell) on a ConPTY of size cols×rows.
// workDir empty uses the user home directory (not the exe install path).
// extraEnv is KEY=VAL entries merged into the process environment (e.g. SUZURI_TAB_ID).
func StartSession(commandLine string, cols, rows int, workDir string, extraEnv ...string) (*Session, error) {
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
	// Always pass a full env block so Grok/Kitty graphics branding applies even
	// when callers pass no extraEnv (newTab currently starts bare shells).
	env := sessionEnv(os.Environ(), extraEnv...)
	opts := []conpty.ConPtyOption{
		conpty.ConPtyDimensions(cols, rows),
		conpty.ConPtyWorkDir(wd),
		conpty.ConPtyEnv(env),
	}
	cpty, err := conpty.Start(commandLine, opts...)
	if err != nil {
		return nil, fmt.Errorf("conpty start: %w", err)
	}
	return &Session{cpty: cpty}, nil
}

// sessionEnv builds the ConPTY process environment: base + extra, then
// set-if-empty Kitty/Ghostty-class branding so Grok emits graphics APCs.
func sessionEnv(base []string, extra ...string) []string {
	env := mergeEnv(base, extra)
	return applyGraphicsBrandEnv(env)
}

// applyGraphicsBrandEnv sets TERM / COLORTERM / TERM_PROGRAM / KITTY_WINDOW_ID
// only when absent (matches unix quietShellEnv). User or extraEnv values win.
func applyGraphicsBrandEnv(env []string) []string {
	env = setEnvIfEmpty(env, "TERM", "xterm-256color")
	env = setEnvIfEmpty(env, "COLORTERM", "truecolor")
	// Advertise Kitty/Ghostty-class graphics so Grok emits pixel previews
	// (Kitty APC) instead of metadata-only image chips.
	env = setEnvIfEmpty(env, "TERM_PROGRAM", "ghostty")
	env = setEnvIfEmpty(env, "TERM_PROGRAM_VERSION", "1.0.0")
	env = setEnvIfEmpty(env, "KITTY_WINDOW_ID", "1")
	return env
}

// setEnvIfEmpty appends KEY=val when KEY is missing or empty (case-insensitive).
func setEnvIfEmpty(env []string, key, val string) []string {
	want := strings.ToUpper(key)
	for i, e := range env {
		k, v, ok := strings.Cut(e, "=")
		if !ok {
			continue
		}
		if strings.EqualFold(k, key) || strings.ToUpper(k) == want {
			if strings.TrimSpace(v) != "" {
				return env
			}
			env[i] = key + "=" + val
			return env
		}
	}
	return append(env, key+"="+val)
}

// getenvEnv returns the value for KEY in an env block (case-insensitive).
func getenvEnv(env []string, key string) string {
	for _, e := range env {
		k, v, ok := strings.Cut(e, "=")
		if ok && strings.EqualFold(k, key) {
			return v
		}
	}
	return ""
}

// mergeEnv overlays KEY=VAL pairs onto a base environment block.
func mergeEnv(base, extra []string) []string {
	m := map[string]string{}
	order := make([]string, 0, len(base)+len(extra))
	put := func(e string) {
		k, v, ok := strings.Cut(e, "=")
		if !ok || k == "" {
			return
		}
		// Windows env keys are case-insensitive; normalize lookup by upper.
		key := strings.ToUpper(k)
		if _, exists := m[key]; !exists {
			order = append(order, key)
		}
		m[key] = k + "=" + v // preserve original key casing from last write
		// Prefer the extra entry's key spelling.
		if strings.Contains(e, "=") {
			m[key] = e
		}
	}
	for _, e := range base {
		put(e)
	}
	for _, e := range extra {
		put(e)
	}
	out := make([]string, 0, len(order))
	for _, key := range order {
		out = append(out, m[key])
	}
	return out
}

// mustWd is the default shell cwd. Prefer the user home (~) so Start Menu /
// installed launches don't open in %LOCALAPPDATA%\Programs\suzuri.
func mustWd() string {
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		if st, err := os.Stat(home); err == nil && st.IsDir() {
			return home
		}
	}
	if wd, err := os.Getwd(); err == nil {
		return wd
	}
	return ""
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
// Serialized: never call ResizePseudoConsole from multiple goroutines at once.
func (s *Session) Resize(cols, rows int) error {
	if s == nil || s.cpty == nil {
		return nil
	}
	if cols < 1 || rows < 1 {
		return nil
	}
	// COORD is int16 in the ConPTY API.
	if cols > 32767 {
		cols = 32767
	}
	if rows > 32767 {
		rows = 32767
	}
	s.resizeMu.Lock()
	defer s.resizeMu.Unlock()
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
