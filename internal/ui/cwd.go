//go:build windows || darwin

package ui

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
)

// Host OSC for cwd reports from the quiet shell prompt:
//
//	ESC ] 7878 ; cwd = <path> BEL
//
// Also accepts OSC 7 file-URI (common shell integration).
const cwdOSCPrefix = "\x1b]7878;cwd="

// stripAndTakeCwd removes host cwd OSC sequences from PTY bytes and returns
// the last path found (if any). Other bytes pass through unchanged.
func stripAndTakeCwd(data []byte) (clean []byte, cwd string, ok bool) {
	if len(data) == 0 {
		return data, "", false
	}
	out := make([]byte, 0, len(data))
	i := 0
	for i < len(data) {
		// ESC ]
		if data[i] == 0x1b && i+1 < len(data) && data[i+1] == ']' {
			// Find terminator BEL or ST (ESC \).
			j := i + 2
			for j < len(data) {
				if data[j] == 0x07 {
					break
				}
				if data[j] == 0x1b && j+1 < len(data) && data[j+1] == '\\' {
					break
				}
				j++
			}
			if j >= len(data) {
				// Incomplete OSC — keep rest raw (may complete next chunk).
				out = append(out, data[i:]...)
				break
			}
			payload := data[i+2 : j]
			termLen := 1
			if data[j] == 0x1b {
				termLen = 2
			}
			if path, parsed := parseCwdOSCPayload(payload); parsed {
				cwd = path
				ok = true
				i = j + termLen
				continue
			}
			// Unknown OSC: keep as-is for vt10x (title etc.).
			out = append(out, data[i:j+termLen]...)
			i = j + termLen
			continue
		}
		out = append(out, data[i])
		i++
	}
	return out, cwd, ok
}

func parseCwdOSCPayload(payload []byte) (string, bool) {
	// 7878;cwd=<path>
	if bytes.HasPrefix(payload, []byte("7878;cwd=")) {
		p := string(payload[len("7878;cwd="):])
		p = strings.TrimSpace(p)
		if p != "" {
			return p, true
		}
		return "", false
	}
	// 7;file://... or 7;file:////server/share
	if bytes.HasPrefix(payload, []byte("7;")) {
		uri := string(payload[2:])
		if path, ok := fileURIPath(uri); ok {
			return path, true
		}
	}
	return "", false
}

func fileURIPath(uri string) (string, bool) {
	uri = strings.TrimSpace(uri)
	if !strings.HasPrefix(uri, "file:") {
		return "", false
	}
	// file:///C:/Users/...  or file://localhost/C:/...  or file:/C:/...
	rest := strings.TrimPrefix(uri, "file:")
	rest = strings.TrimPrefix(rest, "//")
	if i := strings.Index(rest, "/"); i >= 0 {
		// Drop host (empty, localhost, …)
		host := rest[:i]
		path := rest[i:]
		if host != "" && !strings.EqualFold(host, "localhost") && host != "127.0.0.1" {
			// UNC: file://server/share → \\server\share
			return `\\` + host + strings.ReplaceAll(path, "/", `\`), true
		}
		rest = path
	}
	// /C:/Users → C:\Users on Windows-style
	rest = strings.TrimPrefix(rest, "/")
	if len(rest) >= 2 && rest[1] == ':' {
		rest = strings.ReplaceAll(rest, "/", `\`)
		return rest, true
	}
	if rest == "" {
		return "", false
	}
	// POSIX absolute
	if !strings.HasPrefix(rest, "/") {
		rest = "/" + rest
	}
	return rest, true
}

// displayPath shortens home to ~ for chrome.
func displayPath(cwd string) string {
	cwd = strings.TrimSpace(cwd)
	if cwd == "" {
		return ""
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return cwd
	}
	// Normalize for compare
	cleanCwd := filepath.Clean(cwd)
	cleanHome := filepath.Clean(home)
	if strings.EqualFold(cleanCwd, cleanHome) {
		return "~"
	}
	sep := string(os.PathSeparator)
	prefix := cleanHome + sep
	// Case-insensitive prefix on Windows.
	if len(cleanCwd) > len(prefix) {
		if strings.EqualFold(cleanCwd[:len(prefix)], prefix) {
			return "~" + sep + cleanCwd[len(prefix):]
		}
	}
	// Also try forward slashes
	if strings.HasPrefix(strings.ToLower(cleanCwd), strings.ToLower(strings.ReplaceAll(prefix, `\`, `/`))) {
		// fall through to raw
	}
	return cwd
}

// truncateRunes clips s to max runes with an ellipsis.
func truncateRunes(s string, max int) string {
	if max < 1 {
		return ""
	}
	rs := []rune(s)
	if len(rs) <= max {
		return s
	}
	if max == 1 {
		return "…"
	}
	return string(rs[:max-1]) + "…"
}

// expandBarSubmit rewrites a few bare commands so the Warp bar matches
// common shell expectations (especially bare `cd` → home).
// display is what we show in the command block; payload is sent to the PTY.
func expandBarSubmit(line, shellCmd string) (display, payload string) {
	display = line
	payload = line
	t := strings.TrimSpace(line)
	if t == "" {
		return display, payload
	}
	// Only exact bare forms (no args).
	low := strings.ToLower(t)
	switch low {
	case "cd", "chdir", "set-location", "sl":
		// bash/zsh: bare cd goes home. PowerShell's bare Set-Location errors.
		// cmd's bare cd only prints the path — send an explicit home jump.
		base := strings.ToLower(shellBaseName(shellCmd))
		switch {
		case base == "cmd.exe" || base == "cmd":
			display = "cd ~"
			payload = `cd /d "%USERPROFILE%"`
		case strings.Contains(base, "powershell") || base == "pwsh.exe" || base == "pwsh":
			display = "cd ~"
			payload = "Set-Location ~"
		default:
			display = "cd"
			payload = "cd"
		}
	}
	return display, payload
}

func shellBaseName(shellCmd string) string {
	s := strings.TrimSpace(shellCmd)
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
	s = strings.ReplaceAll(s, `/`, `\`)
	if i := strings.LastIndexByte(s, '\\'); i >= 0 {
		s = s[i+1:]
	}
	return s
}

// cwdAfterCommand best-effort updates cwd after a bar submit (OSC corrects later).
func cwdAfterCommand(cwd, cmd string) (string, bool) {
	t := strings.TrimSpace(cmd)
	if t == "" {
		return cwd, false
	}
	// Strip PowerShell call operator etc. lightly.
	fields := strings.Fields(t)
	if len(fields) == 0 {
		return cwd, false
	}
	verb := strings.ToLower(fields[0])
	// Set-Location ~ / cd ~
	isCD := verb == "cd" || verb == "chdir" || verb == "set-location" || verb == "sl"
	if !isCD {
		return cwd, false
	}
	if len(fields) == 1 {
		if home, err := os.UserHomeDir(); err == nil && home != "" {
			return home, true
		}
		return cwd, false
	}
	arg := fields[1]
	// Drop PowerShell -Path / -LiteralPath
	if strings.EqualFold(arg, "-path") || strings.EqualFold(arg, "-literalpath") {
		if len(fields) < 3 {
			return cwd, false
		}
		arg = fields[2]
	}
	arg = strings.Trim(arg, `"'`)
	if arg == "" {
		return cwd, false
	}
	if arg == "~" || strings.HasPrefix(arg, "~/") || strings.HasPrefix(arg, `~\`) {
		home, err := os.UserHomeDir()
		if err != nil || home == "" {
			return cwd, false
		}
		if arg == "~" {
			return home, true
		}
		return filepath.Join(home, arg[2:]), true
	}
	if filepath.IsAbs(arg) || (len(arg) >= 2 && arg[1] == ':') {
		return filepath.Clean(arg), true
	}
	// cmd: cd /d C:\foo
	if strings.EqualFold(arg, "/d") && len(fields) >= 3 {
		p := strings.Trim(fields[2], `"'`)
		if p != "" {
			return filepath.Clean(p), true
		}
	}
	if cwd == "" {
		return cwd, false
	}
	return filepath.Clean(filepath.Join(cwd, arg)), true
}
