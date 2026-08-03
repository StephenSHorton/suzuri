package ui

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode"
)

// completeSession holds an in-progress Tab cycle so Tab / Shift+Tab walk matches.
type completeSession struct {
	active     bool
	tokenStart int
	matches    []string
	// kindPath: path token replacement; false = history (from tokenStart to EOL).
	kindPath bool
	idx      int
}

// clearComplete drops any Tab-cycle state (called on normal edits).
func (b *inputBar) clearComplete() {
	b.comp = completeSession{}
	b.ghostKey = ""
	b.ghostCache = ""
}

// ghostSuffix is the soft placeholder after the caret (zsh-autosuggest style).
// Prefer newest history line that has the current buffer as a prefix; fall
// back to path/Tab completion remainder. Empty when none / not at EOL.
func (b *inputBar) ghostSuffix(cwd string) string {
	if b == nil || len(b.runes) == 0 || b.cursor != len(b.runes) || b.histIdx >= 0 {
		return ""
	}
	key := cwd + "\x00" + string(b.runes) + "\x00" + itoa(b.cursor)
	if key == b.ghostKey {
		return b.ghostCache
	}
	b.ghostKey = key
	b.ghostCache = b.computeGhostSuffix(cwd)
	return b.ghostCache
}

// acceptGhost appends the current ghost suggestion (Right-arrow at EOL).
// Returns true if the buffer changed.
func (b *inputBar) acceptGhost(cwd string) bool {
	if b == nil {
		return false
	}
	g := b.ghostSuffix(cwd)
	if g == "" {
		return false
	}
	b.clearComplete()
	b.leaveHistoryBrowse()
	gr := []rune(g)
	b.runes = append(b.runes, gr...)
	b.cursor = len(b.runes)
	b.ghostKey = ""
	b.ghostCache = ""
	return true
}

func (b *inputBar) computeGhostSuffix(cwd string) string {
	// While Tab-cycling, preview the next alternative (fish-style).
	if b.comp.active && b.stillInCompleteSession() && len(b.comp.matches) > 1 {
		idx := (b.comp.idx + 1) % len(b.comp.matches)
		m := b.comp.matches[idx]
		cur := b.currentCompleteToken()
		return ghostRemainder(cur, m)
	}

	linePrefix := string(b.runes[lineStartAt(b.runes, b.cursor):b.cursor])
	// zsh-autosuggestions: newest history entry with full line as prefix.
	if rem := historyGhostRemainder(b.history, linePrefix); rem != "" {
		return rem
	}

	// Fall back to path / token completion (empty token after "cd " etc.).
	start, _, prefix, firstWord := tokenAtCursor(b.runes, b.cursor)
	if prefix == "" && firstWord {
		return ""
	}
	matches, _ := collectCompletions(cwd, b.history, linePrefix, prefix, firstWord)
	if len(matches) == 0 {
		return ""
	}
	m := matches[0]
	cur := string(b.runes[start:b.cursor])
	// History match is whole line from tokenStart for non-path; path is token only.
	return ghostRemainder(cur, m)
}

// ghostRemainder is the part of match after cur (case-insensitive prefix).
func ghostRemainder(cur, match string) string {
	if cur == match || match == "" {
		return ""
	}
	cr, mr := []rune(cur), []rune(match)
	if len(mr) <= len(cr) {
		return ""
	}
	if !strings.EqualFold(string(mr[:len(cr)]), cur) {
		return ""
	}
	return string(mr[len(cr):])
}

// historyGhostRemainder returns the untyped suffix of the newest history line
// that has linePrefix as a prefix (zsh-autosuggestions strategy).
func historyGhostRemainder(history []string, linePrefix string) string {
	linePrefix = strings.TrimRight(linePrefix, "\n\r")
	if strings.TrimSpace(linePrefix) == "" {
		return ""
	}
	pl := strings.ToLower(linePrefix)
	for i := len(history) - 1; i >= 0; i-- {
		h := history[i]
		if h == linePrefix {
			continue
		}
		if !strings.HasPrefix(strings.ToLower(h), pl) {
			continue
		}
		return ghostRemainder(linePrefix, h)
	}
	return ""
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var neg bool
	if n < 0 {
		neg = true
		n = -n
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}

// complete applies path and/or history completion at the cursor.
// reverse true = Shift+Tab (previous match). Returns true if the buffer changed.
func (b *inputBar) complete(cwd string, reverse bool) bool {
	b.leaveHistoryBrowse()
	b.clampCursor()

	if b.comp.active && b.stillInCompleteSession() {
		n := len(b.comp.matches)
		if n == 0 {
			return false
		}
		if reverse {
			b.comp.idx = (b.comp.idx - 1 + n) % n
		} else {
			b.comp.idx = (b.comp.idx + 1) % n
		}
		return b.applyCompleteMatch()
	}

	start, _, prefix, firstWord := tokenAtCursor(b.runes, b.cursor)
	linePrefix := string(b.runes[lineStartAt(b.runes, b.cursor):b.cursor])

	matches, kindPath := collectCompletions(cwd, b.history, linePrefix, prefix, firstWord)
	if len(matches) == 0 {
		b.clearComplete()
		return false
	}

	idx := 0
	if reverse && len(matches) > 1 {
		idx = len(matches) - 1
	}
	b.comp = completeSession{
		active:     true,
		tokenStart: start,
		matches:    matches,
		kindPath:   kindPath,
		idx:        idx,
	}
	ok := b.applyCompleteMatch()
	// Unique match: drop session so the next Tab re-queries (e.g. into a dir).
	if len(matches) == 1 {
		b.clearComplete()
	}
	return ok
}

func (b *inputBar) stillInCompleteSession() bool {
	if !b.comp.active || len(b.comp.matches) == 0 {
		return false
	}
	if b.comp.tokenStart < 0 || b.comp.tokenStart > len(b.runes) {
		return false
	}
	if b.comp.idx < 0 || b.comp.idx >= len(b.comp.matches) {
		return false
	}
	return b.currentCompleteToken() == b.comp.matches[b.comp.idx]
}

func (b *inputBar) currentCompleteToken() string {
	start := b.comp.tokenStart
	if start < 0 || start > len(b.runes) {
		return ""
	}
	if b.comp.kindPath {
		end := start
		for end < len(b.runes) && !isTokenSep(b.runes[end]) {
			end++
		}
		return string(b.runes[start:end])
	}
	// History: rest of logical line from tokenStart.
	end := start
	for end < len(b.runes) && b.runes[end] != '\n' {
		end++
	}
	return string(b.runes[start:end])
}

func (b *inputBar) applyCompleteMatch() bool {
	if b.comp.idx < 0 || b.comp.idx >= len(b.comp.matches) {
		return false
	}
	start := b.comp.tokenStart
	if start < 0 || start > len(b.runes) {
		return false
	}
	match := b.comp.matches[b.comp.idx]
	var end int
	if b.comp.kindPath {
		end = start
		for end < len(b.runes) && !isTokenSep(b.runes[end]) {
			end++
		}
	} else {
		end = start
		for end < len(b.runes) && b.runes[end] != '\n' {
			end++
		}
	}
	mr := []rune(match)
	out := make([]rune, 0, len(b.runes)-(end-start)+len(mr))
	out = append(out, b.runes[:start]...)
	out = append(out, mr...)
	out = append(out, b.runes[end:]...)
	b.runes = out
	b.cursor = start + len(mr)
	return true
}

func isTokenSep(r rune) bool {
	switch r {
	case ' ', '\t', '\n', '\r', '|', '&', ';', '<', '>':
		return true
	default:
		return false
	}
}

func lineStartAt(runes []rune, cursor int) int {
	if cursor > len(runes) {
		cursor = len(runes)
	}
	i := cursor
	for i > 0 && runes[i-1] != '\n' {
		i--
	}
	return i
}

// tokenAtCursor returns [start,end) of the token containing the cursor,
// the prefix (start:cursor), and whether this is the first word on the line.
func tokenAtCursor(runes []rune, cursor int) (start, end int, prefix string, firstWord bool) {
	if cursor < 0 {
		cursor = 0
	}
	if cursor > len(runes) {
		cursor = len(runes)
	}
	ls := lineStartAt(runes, cursor)
	start = cursor
	for start > ls && !isTokenSep(runes[start-1]) {
		start--
	}
	end = cursor
	for end < len(runes) && runes[end] != '\n' && !isTokenSep(runes[end]) {
		end++
	}
	prefix = string(runes[start:cursor])
	firstWord = true
	for i := ls; i < start; i++ {
		if !unicode.IsSpace(runes[i]) {
			firstWord = false
			break
		}
	}
	return start, end, prefix, firstWord
}

func collectCompletions(cwd string, history []string, linePrefix, token string, firstWord bool) (matches []string, kindPath bool) {
	pathLike := tokenLooksPath(token)

	// Path when token looks path-like, or non-first-word (args / "cd ").
	wantPath := pathLike || !firstWord

	// Full-line history first whenever the typed line is non-empty (multi-word
	// too: "git sta" → "git status"). Path completions still win Tab into dirs.
	if strings.TrimSpace(linePrefix) != "" {
		if hm := historyCompletions(history, linePrefix); len(hm) > 0 {
			// For path-like tokens at first word (./foo), prefer path if any.
			if pathLike && firstWord {
				if pm := pathCompletions(cwd, token); len(pm) > 0 {
					return pm, true
				}
			}
			// Non-first-word path tokens: offer path when token is path-like.
			if !firstWord && pathLike {
				if pm := pathCompletions(cwd, token); len(pm) > 0 {
					return pm, true
				}
			}
			if firstWord || !pathLike {
				return hm, false
			}
		}
	}

	if wantPath || firstWord {
		if pm := pathCompletions(cwd, token); len(pm) > 0 {
			return pm, true
		}
	}
	if strings.TrimSpace(linePrefix) != "" {
		if hm := historyCompletions(history, linePrefix); len(hm) > 0 {
			return hm, false
		}
	}
	return nil, false
}

func tokenLooksPath(token string) bool {
	if token == "" {
		return false
	}
	if strings.HasPrefix(token, "~") || strings.HasPrefix(token, ".") {
		return true
	}
	if strings.ContainsAny(token, `/\`) {
		return true
	}
	if len(token) >= 2 {
		r0 := rune(token[0])
		if unicode.IsLetter(r0) && token[1] == ':' {
			return true
		}
	}
	return false
}

func historyCompletions(history []string, linePrefix string) []string {
	linePrefix = strings.TrimRight(linePrefix, "\n\r")
	if strings.TrimSpace(linePrefix) == "" {
		return nil
	}
	seen := map[string]struct{}{}
	var out []string
	pl := strings.ToLower(linePrefix)
	for i := len(history) - 1; i >= 0; i-- {
		h := history[i]
		if h == linePrefix {
			continue
		}
		if !strings.HasPrefix(strings.ToLower(h), pl) {
			continue
		}
		if _, ok := seen[strings.ToLower(h)]; ok {
			continue
		}
		seen[strings.ToLower(h)] = struct{}{}
		out = append(out, h)
		if len(out) >= 40 {
			break
		}
	}
	return out
}

// pathCompletions returns token replacements for filesystem matches under cwd.
func pathCompletions(cwd, token string) []string {
	dirPart, base, absDir, sep := splitPathToken(cwd, token)
	if absDir == "" {
		return nil
	}
	entries, err := os.ReadDir(absDir)
	if err != nil {
		return nil
	}
	baseLower := strings.ToLower(base)
	var names []string
	for _, e := range entries {
		name := e.Name()
		if strings.HasPrefix(name, ".") && !strings.HasPrefix(base, ".") {
			continue
		}
		if base != "" && !strings.HasPrefix(strings.ToLower(name), baseLower) {
			continue
		}
		cand := name
		if e.IsDir() {
			cand = name + sep
		}
		full := joinToken(dirPart, cand, sep)
		if needsQuote(full) {
			full = `"` + full + `"`
		}
		names = append(names, full)
	}
	sort.Slice(names, func(i, j int) bool {
		return strings.ToLower(names[i]) < strings.ToLower(names[j])
	})
	return names
}

func needsQuote(s string) bool {
	return strings.ContainsAny(s, " \t")
}

func joinToken(dirPart, name, sep string) string {
	if dirPart == "" {
		return name
	}
	if strings.HasSuffix(dirPart, `/`) || strings.HasSuffix(dirPart, `\`) {
		return dirPart + name
	}
	return dirPart + sep + name
}

// splitPathToken expands token relative to cwd.
// Returns dirPart (for rebuild), base name filter, absolute dir to ReadDir, and sep style.
func splitPathToken(cwd, token string) (dirPart, base, absDir, sep string) {
	sep = string(os.PathSeparator)
	if strings.Contains(token, `/`) && !strings.Contains(token, `\`) {
		sep = "/"
	} else if strings.Contains(token, `\`) {
		sep = `\`
	}

	home, _ := os.UserHomeDir()
	raw := token
	if len(raw) >= 1 && raw[0] == '"' {
		raw = raw[1:]
	}

	// Home-relative
	if raw == "~" || strings.HasPrefix(raw, "~/") || strings.HasPrefix(raw, `~\`) {
		if home == "" {
			return "", "", "", sep
		}
		if raw == "~" {
			// List home; completing will produce ~/name or ~\name
			return "~" + sep, "", home, sep
		}
		rest := raw[2:] // after ~/ or ~\
		if strings.HasSuffix(raw, `/`) || strings.HasSuffix(raw, `\`) {
			// ~/Documents/ → list Documents
			rel := strings.TrimRight(strings.ReplaceAll(rest, `\`, `/`), `/`)
			return raw, "", filepath.Join(home, filepath.FromSlash(rel)), sep
		}
		slash := lastSlash(rest)
		if slash < 0 {
			return "~" + sep, rest, home, sep
		}
		dirPart = "~" + sep + rest[:slash+1]
		// normalize: rest[:slash+1] includes trailing slash style from rest
		if sep == "/" {
			dirPart = "~/" + strings.ReplaceAll(rest[:slash], `\`, `/`) + "/"
		} else {
			dirPart = `~\` + strings.ReplaceAll(rest[:slash], `/`, `\`) + `\`
		}
		base = rest[slash+1:]
		absDir = filepath.Join(home, filepath.FromSlash(strings.ReplaceAll(rest[:slash], `\`, `/`)))
		return dirPart, base, absDir, sep
	}

	// Absolute
	if filepath.IsAbs(filepath.FromSlash(raw)) || isWindowsAbs(raw) {
		if strings.HasSuffix(token, `/`) || strings.HasSuffix(token, `\`) {
			abs := filepath.Clean(filepath.FromSlash(strings.ReplaceAll(raw, `\`, `/`)))
			// Windows: preserve drive path
			if isWindowsAbs(raw) {
				abs = filepath.Clean(raw)
			}
			return token, "", abs, sep
		}
		slash := lastSlash(token)
		if slash < 0 {
			// rare: "C:" alone
			if isWindowsAbs(raw) {
				return "", raw, raw + string(os.PathSeparator), sep
			}
			return "", raw, filepath.Dir(raw), sep
		}
		dirPart = token[:slash+1]
		base = token[slash+1:]
		absDir = filepath.Clean(filepath.FromSlash(strings.ReplaceAll(raw[:lastSlash(raw)], `\`, `/`)))
		if isWindowsAbs(raw) {
			absDir = filepath.Dir(filepath.Clean(raw))
		}
		return dirPart, base, absDir, sep
	}

	// Relative to cwd
	if cwd == "" {
		if wd, err := os.Getwd(); err == nil {
			cwd = wd
		}
	}
	if cwd == "" {
		return "", "", "", sep
	}
	if token == "" {
		return "", "", cwd, sep
	}
	if strings.HasSuffix(token, `/`) || strings.HasSuffix(token, `\`) {
		rel := strings.TrimRight(strings.ReplaceAll(token, `\`, `/`), `/`)
		return token, "", filepath.Join(cwd, filepath.FromSlash(rel)), sep
	}
	slash := lastSlash(token)
	if slash < 0 {
		return "", token, cwd, sep
	}
	dirPart = token[:slash+1]
	base = token[slash+1:]
	relDir := filepath.FromSlash(strings.ReplaceAll(token[:slash], `\`, `/`))
	return dirPart, base, filepath.Join(cwd, relDir), sep
}

func lastSlash(s string) int {
	return strings.LastIndexAny(s, `/\`)
}

func isWindowsAbs(s string) bool {
	if len(s) >= 2 && unicode.IsLetter(rune(s[0])) && s[1] == ':' {
		return true
	}
	return strings.HasPrefix(s, `\\`)
}
