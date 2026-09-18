//go:build windows || darwin

package ui

import (
	"bytes"
	"path/filepath"
	"strings"
)

// OSC 7880 — grok-fork asks the host to split the emitting pane.
//
//	ESC]7880;fork=1;resume=…;cwd=…;bin=…;prompt=…;title=…;brand=…BEL
//	ESC]7880;new=1;session=…;cwd=…;bin=…;prompt=…;title=…;brand=…BEL

type forkPaneRequest struct {
	resume     string
	cwd        string
	bin        string
	prompt     string
	title      string
	brand      string
	newSession bool
}

func stripAndTakeFork(data []byte) (clean []byte, reqs []forkPaneRequest) {
	if len(data) == 0 {
		return data, nil
	}
	out := make([]byte, 0, len(data))
	i := 0
	for i < len(data) {
		if data[i] == 0x1b && i+1 < len(data) && data[i+1] == ']' {
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
				out = append(out, data[i:]...)
				break
			}
			payload := data[i+2 : j]
			termLen := 1
			if data[j] == 0x1b {
				termLen = 2
			}
			if req, ok := parseForkOSCPayload(payload); ok {
				reqs = append(reqs, req)
				i = j + termLen
				continue
			}
			out = append(out, data[i:j+termLen]...)
			i = j + termLen
			continue
		}
		out = append(out, data[i])
		i++
	}
	return out, reqs
}

func parseForkOSCPayload(payload []byte) (forkPaneRequest, bool) {
	if !bytes.HasPrefix(payload, []byte("7880;")) {
		return forkPaneRequest{}, false
	}
	rest := string(payload[len("7880;"):])
	var req forkPaneRequest
	forked, newSess := false, false
	for _, part := range strings.Split(rest, ";") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		key, val, ok := strings.Cut(part, "=")
		if ok {
			key = strings.ToLower(strings.TrimSpace(key))
			val = pctDecode(strings.TrimSpace(val))
			switch key {
			case "fork":
				if truthyOSC(val) {
					forked = true
				}
			case "new", "open", "pane":
				if truthyOSC(val) {
					newSess = true
				}
			case "resume", "session", "id":
				req.resume = val
			case "cwd":
				req.cwd = val
			case "bin", "exe":
				req.bin = val
			case "prompt", "directive":
				req.prompt = val
			case "title":
				req.title = val
			case "brand":
				req.brand = val
			}
			continue
		}
		switch strings.ToLower(part) {
		case "fork", "fork-pane":
			forked = true
		case "new", "open", "pane":
			newSess = true
		}
	}
	if !(forked || newSess) || strings.TrimSpace(req.resume) == "" || strings.TrimSpace(req.bin) == "" {
		return forkPaneRequest{}, false
	}
	req.newSession = newSess
	return req, true
}

func truthyOSC(val string) bool {
	switch strings.ToLower(strings.TrimSpace(val)) {
	case "1", "true", "yes", "on", "pane", "new", "open":
		return true
	default:
		return false
	}
}

func pctDecode(s string) string {
	b := []byte(s)
	out := make([]byte, 0, len(b))
	for i := 0; i < len(b); {
		if b[i] == '%' && i+2 < len(b) {
			h, ok1 := fromHex(b[i+1])
			l, ok2 := fromHex(b[i+2])
			if ok1 && ok2 {
				out = append(out, (h<<4)|l)
				i += 3
				continue
			}
		}
		out = append(out, b[i])
		i++
	}
	return string(out)
}

func fromHex(c byte) (byte, bool) {
	switch {
	case c >= '0' && c <= '9':
		return c - '0', true
	case c >= 'a' && c <= 'f':
		return c - 'a' + 10, true
	case c >= 'A' && c <= 'F':
		return c - 'A' + 10, true
	default:
		return 0, false
	}
}

func allowedForkBin(path string) bool {
	path = strings.TrimSpace(path)
	if path == "" {
		return false
	}
	if !filepath.IsAbs(path) {
		return false
	}
	if strings.Contains(path, "..") {
		return false
	}
	base := strings.ToLower(strings.TrimSuffix(filepath.Base(path), filepath.Ext(path)))
	switch base {
	case "grok", "grok-fork", "xai-grok-pager":
		return true
	default:
		return false
	}
}

func forkLaunchSpec(req forkPaneRequest) (bin string, args []string, env []string, err string) {
	if !allowedForkBin(req.bin) {
		return "", nil, nil, "bin not allowlisted"
	}
	if strings.TrimSpace(req.resume) == "" {
		return "", nil, nil, "missing session id"
	}
	if req.newSession {
		args = []string{"--session-id", req.resume}
	} else {
		args = []string{"--resume", req.resume}
	}
	if p := strings.TrimSpace(req.prompt); p != "" {
		args = append(args, "--", p)
	}
	// The grok-fork launcher refuses GROK_SKIP_* / GROK_DISABLE_AUTOUPDATER.
	env = []string{}
	if strings.EqualFold(strings.TrimSpace(req.brand), "fork") {
		env = append(env,
			"GROK_PROCESS_BRAND=fork",
			"GROK_FORK=1",
			"GROK_MCP_CHANNELS=1",
		)
	}
	if t := strings.TrimSpace(req.title); t != "" {
		env = append(env, "GROK_SESSION_TITLE="+t)
	}
	return req.bin, args, env, ""
}

func forkTitle(req forkPaneRequest) string {
	if t := strings.TrimSpace(req.title); t != "" {
		return t
	}
	if req.newSession {
		return "session"
	}
	return "fork"
}

func joinCommandLine(bin string, args []string) string {
	parts := append([]string{bin}, args...)
	for i, p := range parts {
		if strings.ContainsAny(p, " \t\"") {
			parts[i] = `"` + strings.ReplaceAll(p, `"`, `\"`) + `"`
		}
	}
	return strings.Join(parts, " ")
}

func chooseForkSplitDir(cols, rows int) splitDir {
	if cols < 1 {
		cols = 1
	}
	if rows < 1 {
		rows = 1
	}
	// Prefer the split that keeps the larger min-dimension (matches rust).
	minV := cols / 2
	if rows < minV {
		minV = rows
	}
	minH := rows / 2
	if cols < minH {
		minH = cols
	}
	if minH > minV {
		return splitHoriz
	}
	return splitVert
}
