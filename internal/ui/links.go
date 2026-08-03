package ui

import (
	"os/exec"
	"regexp"
	"runtime"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/charmbracelet/log"

	"github.com/StephenSHorton/suzuri/internal/chrome"
)

// linkSpan is a URL on one viewport row (x0 inclusive, x1 exclusive in cells).
type linkSpan struct {
	row    int
	x0, x1 int
	url    string
}

// urlPattern matches http(s) and www. URLs in terminal text.
var urlPattern = regexp.MustCompile(`(?i)\b(?:https?://|www\.)[^\s<>"'{}|\\^` + "`" + `\[\]()]+`)

// findLinksInGrid scans each row's visible cells for URLs (one cell = one rune).
func findLinksInGrid(grid [][]cellPix) []linkSpan {
	if len(grid) == 0 {
		return nil
	}
	var out []linkSpan
	for y, row := range grid {
		if len(row) == 0 {
			continue
		}
		rs := make([]rune, len(row))
		for x, c := range row {
			ch := c.Ch
			if ch == 0 {
				ch = ' '
			}
			rs[x] = ch
		}
		line := string(rs)
		for _, ij := range urlPattern.FindAllStringIndex(line, -1) {
			b0, b1 := ij[0], ij[1]
			if b0 < 0 || b1 > len(line) || b0 >= b1 {
				continue
			}
			raw := line[b0:b1]
			// Trim trailing punctuation commonly glued to URLs in prose.
			trimN := 0
			rr := []rune(raw)
			for len(rr) > 0 && isURLTrailPunct(rr[len(rr)-1]) {
				rr = rr[:len(rr)-1]
				trimN++
			}
			// Unbalanced closing paren: (see https://x.com) → drop ')'
			for len(rr) > 0 && rr[len(rr)-1] == ')' &&
				strings.Count(string(rr), "(") < strings.Count(string(rr), ")") {
				rr = rr[:len(rr)-1]
				trimN++
			}
			if len(rr) < 4 {
				continue
			}
			raw = string(rr)
			url := normalizeURL(raw)
			if url == "" {
				continue
			}
			x0 := utf8.RuneCountInString(line[:b0])
			x1 := x0 + len(rr)
			if x0 < 0 {
				x0 = 0
			}
			if x1 > len(row) {
				x1 = len(row)
			}
			if x1 <= x0 {
				continue
			}
			out = append(out, linkSpan{row: y, x0: x0, x1: x1, url: url})
		}
	}
	return out
}

func isURLTrailPunct(r rune) bool {
	switch r {
	case '.', ',', ';', ':', '!', '?', ']', '}', '\'', '"', '»', '›':
		return true
	default:
		return unicode.IsSpace(r)
	}
}

func normalizeURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	lower := strings.ToLower(raw)
	if strings.HasPrefix(lower, "www.") {
		raw = "https://" + raw
		lower = strings.ToLower(raw)
	}
	if !strings.HasPrefix(lower, "http://") && !strings.HasPrefix(lower, "https://") {
		return ""
	}
	rest := raw
	if i := strings.Index(rest, "://"); i >= 0 {
		rest = rest[i+3:]
	}
	if rest == "" || strings.HasPrefix(rest, "/") {
		return ""
	}
	return raw
}

// cleanURL is used by tests / open path (normalize + trail trim on free text).
func cleanURL(raw string) string {
	rr := []rune(strings.TrimSpace(raw))
	for len(rr) > 0 && isURLTrailPunct(rr[len(rr)-1]) {
		rr = rr[:len(rr)-1]
	}
	for len(rr) > 0 && rr[len(rr)-1] == ')' &&
		strings.Count(string(rr), "(") < strings.Count(string(rr), ")") {
		rr = rr[:len(rr)-1]
	}
	return normalizeURL(string(rr))
}

// linkAt returns the link under (col, row) if any.
func linkAt(spans []linkSpan, col, row int) (linkSpan, bool) {
	for _, s := range spans {
		if s.row == row && col >= s.x0 && col < s.x1 {
			return s, true
		}
	}
	return linkSpan{}, false
}

// applyLinkHoverTint recolors the hovered URL span with the theme primary.
func applyLinkHoverTint(grid [][]cellPix, span linkSpan) {
	if span.row < 0 || span.row >= len(grid) {
		return
	}
	row := grid[span.row]
	pr, pg, pb := chrome.PrimR, chrome.PrimG, chrome.PrimB
	for x := span.x0; x < span.x1 && x < len(row); x++ {
		if x < 0 {
			continue
		}
		row[x].FR, row[x].FG, row[x].FB = pr, pg, pb
		row[x].BR = blendByte(row[x].BR, pr, 0.12)
		row[x].BG = blendByte(row[x].BG, pg, 0.12)
		row[x].BB = blendByte(row[x].BB, pb, 0.12)
	}
	grid[span.row] = row
}

// openURLInBrowser launches the system default browser for url.
func openURLInBrowser(url string) {
	url = strings.TrimSpace(url)
	if url == "" {
		return
	}
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "windows":
		cmd = exec.Command("cmd", "/c", "start", "", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	if err := cmd.Start(); err != nil {
		log.Warn("open url failed", "url", url, "err", err)
		return
	}
	go func() { _ = cmd.Wait() }()
	log.Info("opened url", "url", url)
}
