//go:build windows || darwin

package ui

import (
	"encoding/base64"
	"fmt"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/atotto/clipboard"
	"github.com/hinshun/vt10x"

	"github.com/StephenSHorton/suzuri/internal/chrome"
)

// termModes is the host side of terminal features hinshun/vt10x does not
// implement. Bytes still go to vt10x after this filter. Tracked here:
//
//   - synchronized output (CSI ? 2026 h / l), with a 1s safety flush
//   - bracketed paste mode (CSI ? 2004)
//   - DECRQM replies so programs can discover those modes
//   - OSC 52 clipboard
//   - OSC 8 hyperlinks (label text kept, target remembered on cells)
//   - DECSCUSR cursor shape (CSI Ps SP q)
//   - XTVERSION (CSI > 0 q)
//   - focus-event arming (CSI ? 1004 h) — the event itself is reportFocus
//   - OSC 9 / OSC 777 notifications
//   - ConEmu progress (OSC 9 ; 4)
type termModes struct {
	bracketPaste bool
	syncOn       bool
	syncAt       time.Time
	syncBuf      []byte
	heldSpans    []osc8Span
	pending      []byte

	cursorShape int // DECSCUSR 0..6 (0 = host default)

	progress termProgress

	capturing  bool
	captureURL string
	capture    []byte

	links    [][]string
	linkCols int
	osc99    map[string]*osc99Buf
}

type termProgress struct {
	kind int // 0 off, 1 set, 2 error, 3 indeterminate, 4 paused
	pct  int
}

type osc8Span struct {
	url  string
	text string
}

type feedResult struct {
	ready     []byte
	spans     []osc8Span
	replies   []byte
	clipSet   *string
	clipAsk   bool
	notify    bool
	ntitle    string
	nbody     string
	notes     []deskNote
	closedIDs []string
	aliveID   string
	aliveAsk  bool
	armFocus  bool
}

const (
	syncFlushAfter = time.Second
	maxOSCHold     = 2 << 20
)

var (
	clipWrite = clipboard.WriteAll
	clipRead  = clipboard.ReadAll
)

func (m *termModes) leaveApp() {
	if m == nil {
		return
	}
	m.bracketPaste = false
	m.syncOn = false
	m.syncBuf = nil
	m.heldSpans = nil
	m.cursorShape = 0
	m.progress = termProgress{}
	m.capturing = false
	m.capture = nil
	m.captureURL = ""
	m.osc99 = nil
	m.clearLinks()
}

func (m *termModes) clearLinks() {
	if m == nil {
		return
	}
	m.links = nil
	m.linkCols = 0
}

func (m *termModes) feed(now time.Time, data []byte, vtMode vt10x.ModeFlag) feedResult {
	var res feedResult
	if m == nil {
		res.ready = data
		return res
	}
	in := data
	if len(m.pending) > 0 {
		in = append(append([]byte{}, m.pending...), data...)
		m.pending = nil
	}
	var out []byte
	flush := func() {
		m.syncOn = false
		if len(m.syncBuf) > 0 {
			out = append(out, m.syncBuf...)
			m.syncBuf = nil
		}
		if len(m.heldSpans) > 0 {
			res.spans = append(res.spans, m.heldSpans...)
			m.heldSpans = nil
		}
	}
	if m.syncOn && !m.syncAt.IsZero() && now.Sub(m.syncAt) > syncFlushAfter {
		flush()
	}
	appendBytes := func(b []byte) {
		if m.syncOn {
			m.syncBuf = append(m.syncBuf, b...)
			if m.capturing {
				m.capture = append(m.capture, b...)
			}
			return
		}
		out = append(out, b...)
		if m.capturing {
			m.capture = append(m.capture, b...)
		}
	}
	i := 0
	for i < len(in) {
		if in[i] != 0x1b {
			j := i + 1
			for j < len(in) && in[j] != 0x1b {
				j++
			}
			appendBytes(in[i:j])
			i = j
			continue
		}
		if i+1 >= len(in) {
			m.holdTail(in[i:])
			break
		}
		switch in[i+1] {
		case ']':
			payload, seqLen, done := oscSeq(in[i:])
			if !done {
				if len(in[i:]) > maxOSCHold {
					appendBytes(in[i : i+1])
					i++
					continue
				}
				m.holdTail(in[i:])
				i = len(in)
				break
			}
			if !m.takeOSC(payload, vtMode, &res) {
				appendBytes(in[i : i+seqLen])
			}
			i += seqLen
		case '[':
			body, final, seqLen, done, bad := csiSeq(in[i:])
			if !done {
				if bad || len(in[i:]) > maxOSCHold {
					appendBytes(in[i : i+1])
					i++
					continue
				}
				m.holdTail(in[i:])
				i = len(in)
				break
			}
			if !m.takeCSI(body, final, vtMode, &res, now, flush) {
				appendBytes(in[i : i+seqLen])
			}
			i += seqLen
		default:
			appendBytes(in[i : i+2])
			i += 2
		}
	}
	res.ready = out
	return res
}

func (m *termModes) holdTail(b []byte) {
	m.pending = append([]byte{}, b...)
}

func (m *termModes) emitSpan(sp osc8Span, res *feedResult) {
	if sp.text == "" || sp.url == "" {
		return
	}
	if m.syncOn {
		m.heldSpans = append(m.heldSpans, sp)
		return
	}
	res.spans = append(res.spans, sp)
}

// takeCSI reports whether the sequence was consumed (not forwarded).
func (m *termModes) takeCSI(body string, final byte, vtMode vt10x.ModeFlag, res *feedResult, now time.Time, flush func()) bool {
	switch final {
	case 'h', 'l':
		modes, ok := decModes(body)
		if !ok {
			return false
		}
		set := final == 'h'
		for _, mode := range modes {
			switch mode {
			case 2004:
				m.bracketPaste = set
			case 2026:
				if set && !m.syncOn {
					m.syncOn = true
					m.syncAt = now
				}
				if !set && m.syncOn && flush != nil {
					flush()
				}
			case 1004:
				if set {
					res.armFocus = true
				}
			default:
				return false
			}
		}
		// 1004 must reach vt10x so ModeFocus is set. 2004 does not.
		// 2026 is host-only; keep it out of the display stream.
		for _, mode := range modes {
			if mode == 1004 {
				return false
			}
		}
		return true
	case 'p':
		priv, modes, ok := decrqm(body)
		if !ok {
			return false
		}
		for _, mode := range modes {
			pm := m.decrpm(priv, mode, vtMode)
			if priv {
				res.replies = append(res.replies, []byte(fmt.Sprintf("\x1b[?%d;%d$y", mode, pm))...)
			} else {
				res.replies = append(res.replies, []byte(fmt.Sprintf("\x1b[%d;%d$y", mode, pm))...)
			}
		}
		return true
	case 'q':
		if strings.HasPrefix(body, ">") {
			res.replies = append(res.replies, xtversion()...)
			return true
		}
		if n, ok := decscusr(body); ok {
			if n < 0 {
				n = 0
			}
			if n > 6 {
				n = 0
			}
			m.cursorShape = n
			return true
		}
	}
	return false
}

func (m *termModes) decrpm(priv bool, mode int, vtMode vt10x.ModeFlag) int {
	if !priv {
		return 0
	}
	bit := func(f vt10x.ModeFlag) int {
		if vtMode&f != 0 {
			return 1
		}
		return 2
	}
	switch mode {
	case 1:
		return bit(vt10x.ModeAppCursor)
	case 5:
		return bit(vt10x.ModeReverse)
	case 7:
		return bit(vt10x.ModeWrap)
	case 25:
		if vtMode&vt10x.ModeHide != 0 {
			return 2
		}
		return 1
	case 9:
		return bit(vt10x.ModeMouseX10)
	case 1000:
		return bit(vt10x.ModeMouseButton)
	case 1002:
		return bit(vt10x.ModeMouseMotion)
	case 1003:
		return bit(vt10x.ModeMouseMany)
	case 1004:
		return bit(vt10x.ModeFocus)
	case 1006:
		return bit(vt10x.ModeMouseSgr)
	case 47, 1047, 1049:
		return bit(vt10x.ModeAltScreen)
	case 2004:
		if m.bracketPaste {
			return 1
		}
		return 2
	case 2026:
		if m.syncOn {
			return 1
		}
		return 2
	default:
		return 0
	}
}

// takeOSC reports whether the sequence was consumed. Titles, cwd reports,
// and inline-image OSCs must pass through to the rest of the pipeline.
func (m *termModes) takeOSC(payload []byte, _ vt10x.ModeFlag, res *feedResult) bool {
	switch {
	case bytesHasPrefix(payload, "8;") || string(payload) == "8":
		m.takeOSC8(payload, res)
		return true
	case bytesHasPrefix(payload, "52;") || string(payload) == "52":
		m.takeOSC52(payload, res)
		return true
	case bytesHasPrefix(payload, "99;") || string(payload) == "99":
		m.takeOSC99(payload, res)
		return true
	case bytesHasPrefix(payload, "9;"):
		m.takeOSC9(payload, res)
		return true
	case bytesHasPrefix(payload, "777;"):
		m.takeOSC777(payload, res)
		return true
	default:
		return false
	}
}

func (m *termModes) takeOSC8(payload []byte, res *feedResult) {
	// 8 ; params ; uri   — empty uri closes the hyperlink.
	rest := payload
	if bytesHasPrefix(rest, "8;") {
		rest = rest[2:]
	} else {
		rest = nil
	}
	params, uri, _ := strings.Cut(string(rest), ";")
	_ = params
	uri = strings.TrimSpace(uri)
	if uri == "" {
		if m.capturing {
			text := visibleCapture(m.capture)
			m.emitSpan(osc8Span{url: safeLink(m.captureURL), text: text}, res)
		}
		m.capturing = false
		m.capture = nil
		m.captureURL = ""
		return
	}
	if m.capturing {
		text := visibleCapture(m.capture)
		m.emitSpan(osc8Span{url: safeLink(m.captureURL), text: text}, res)
		m.capture = nil
	}
	m.capturing = true
	m.captureURL = uri
}

func (m *termModes) takeOSC52(payload []byte, res *feedResult) {
	// 52 ; Pc ; Pd
	rest := string(payload)
	if strings.HasPrefix(rest, "52;") {
		rest = rest[3:]
	} else {
		rest = ""
	}
	pc, pd, _ := strings.Cut(rest, ";")
	_ = pc
	pd = strings.TrimSpace(pd)
	if pd == "?" {
		res.clipAsk = true
		return
	}
	if pd == "" {
		empty := ""
		res.clipSet = &empty
		return
	}
	raw, err := base64.StdEncoding.DecodeString(stripB64Space(pd))
	if err != nil {
		raw, err = base64.RawStdEncoding.DecodeString(stripB64Space(pd))
		if err != nil {
			return
		}
	}
	if len(raw) > 1<<20 {
		raw = raw[:1<<20]
	}
	s := string(raw)
	res.clipSet = &s
}

func (m *termModes) takeOSC9(payload []byte, res *feedResult) {
	rest := string(payload)
	if strings.HasPrefix(rest, "9;") {
		rest = rest[2:]
	}
	// ConEmu: 9 ; 4 ; state ; percent
	if strings.HasPrefix(rest, "4;") {
		parts := strings.Split(rest, ";")
		if len(parts) >= 2 {
			st, _ := strconv.Atoi(parts[1])
			pct := 0
			if len(parts) >= 3 {
				pct, _ = strconv.Atoi(parts[2])
			}
			if pct < 0 {
				pct = 0
			}
			if pct > 100 {
				pct = 100
			}
			if st < 0 || st > 4 {
				st = 0
			}
			m.progress = termProgress{kind: st, pct: pct}
			return
		}
	}
	msg := strings.TrimSpace(rest)
	if msg == "" {
		return
	}
	res.notes = append(res.notes, deskNote{
		Title: "Suzuri", Body: clipRunes(msg, 200), Sound: "info", Focus: true,
	})
}

func (m *termModes) takeOSC777(payload []byte, res *feedResult) {
	rest := string(payload)
	if strings.HasPrefix(rest, "777;") {
		rest = rest[4:]
	}
	parts := strings.SplitN(rest, ";", 3)
	if len(parts) == 0 || parts[0] != "notify" {
		return
	}
	title, body := "Suzuri", ""
	switch len(parts) {
	case 2:
		body = parts[1]
	case 3:
		title, body = parts[1], parts[2]
	}
	body = strings.TrimSpace(body)
	title = strings.TrimSpace(title)
	if body == "" {
		body = title
		title = "Suzuri"
	}
	if body == "" {
		return
	}
	res.notes = append(res.notes, deskNote{
		Title: clipRunes(title, 80), Body: clipRunes(body, 200), Sound: "info", Focus: true,
	})
}

func (m *termModes) observe(before, after []string, spans []osc8Span) {
	if m == nil {
		return
	}
	k := 0
	if screenWasCleared(before, after) {
		m.clearLinks()
	} else {
		k = scrollShift(before, after)
		if k > 0 {
			m.shiftRows(k)
		}
		m.dropStale(before, after, k)
	}
	m.stamp(after, spans)
}

// dropStale clears a hyperlink when the cell's character changed, so a
// full-screen redraw does not keep a target on a glyph that moved on.
func (m *termModes) dropStale(before, after []string, k int) {
	if len(m.links) == 0 {
		return
	}
	for y := 0; y < len(after) && y < len(m.links); y++ {
		var prev string
		if src := y + k; src >= 0 && src < len(before) {
			prev = before[src]
		}
		pr, ar := []rune(prev), []rune(after[y])
		row := m.links[y]
		for x := range row {
			var pc, ac rune = ' ', ' '
			if x < len(pr) && pr[x] != 0 {
				pc = pr[x]
			}
			if x < len(ar) && ar[x] != 0 {
				ac = ar[x]
			}
			if pc != ac {
				row[x] = ""
			}
		}
	}
}

func (m *termModes) shiftRows(k int) {
	if k <= 0 || len(m.links) == 0 {
		return
	}
	if k >= len(m.links) {
		m.clearLinks()
		return
	}
	copy(m.links, m.links[k:])
	for i := len(m.links) - k; i < len(m.links); i++ {
		if m.linkCols > 0 {
			m.links[i] = make([]string, m.linkCols)
		} else {
			m.links[i] = nil
		}
	}
}

func (m *termModes) stamp(screen []string, spans []osc8Span) {
	if len(spans) == 0 || len(screen) == 0 {
		return
	}
	cols := 0
	if len(screen) > 0 {
		cols = utf8.RuneCountInString(screen[0])
	}
	if cols < 1 {
		return
	}
	m.ensure(len(screen), cols)
	flat := make([]rune, 0, len(screen)*cols)
	for _, row := range screen {
		rs := []rune(row)
		if len(rs) < cols {
			pad := make([]rune, cols-len(rs))
			for i := range pad {
				pad[i] = ' '
			}
			rs = append(rs, pad...)
		}
		if len(rs) > cols {
			rs = rs[:cols]
		}
		flat = append(flat, rs...)
	}
	from := 0
	for _, sp := range spans {
		needle := []rune(sp.text)
		if len(needle) == 0 || sp.url == "" {
			continue
		}
		idx := indexRunes(flat, from, needle)
		if idx < 0 {
			idx = indexRunes(flat, 0, needle)
		}
		if idx < 0 {
			continue
		}
		for n := 0; n < len(needle); n++ {
			at := idx + n
			y := at / cols
			x := at % cols
			if y >= 0 && y < len(m.links) && x >= 0 && x < len(m.links[y]) {
				m.links[y][x] = sp.url
			}
		}
		from = idx + len(needle)
	}
}

func (m *termModes) ensure(rows, cols int) {
	if rows < 1 || cols < 1 {
		return
	}
	if len(m.links) == rows && m.linkCols == cols {
		return
	}
	next := make([][]string, rows)
	for y := 0; y < rows; y++ {
		next[y] = make([]string, cols)
		if y < len(m.links) {
			copy(next[y], m.links[y])
		}
	}
	m.links = next
	m.linkCols = cols
}

// overlayOSCLinks copies remembered OSC 8 targets onto a painted viewport.
func (t *tab) overlayOSCLinks(grid [][]cellPix) {
	if t == nil || t.term == nil || len(grid) == 0 || len(t.modes.links) == 0 {
		return
	}
	screen := snapshotScreenText(t.term)
	used := make([]bool, len(grid))
	for y := len(screen) - 1; y >= 0; y-- {
		if y >= len(t.modes.links) || !rowHasLink(t.modes.links[y]) {
			continue
		}
		for gy := len(grid) - 1; gy >= 0; gy-- {
			if used[gy] {
				continue
			}
			if rowText(grid[gy]) != screen[y] {
				continue
			}
			used[gy] = true
			row := grid[gy]
			src := t.modes.links[y]
			n := len(row)
			if len(src) < n {
				n = len(src)
			}
			for x := 0; x < n; x++ {
				if src[x] == "" {
					continue
				}
				row[x].Link = src[x]
				row[x].Underline = true
			}
			break
		}
	}
}

func (t *tab) viewCells(rows int) [][]cellPix {
	if t == nil || t.sb == nil {
		return nil
	}
	grid := t.sb.viewCells(t.term, rows)
	t.overlayOSCLinks(grid)
	return grid
}

func (t *tab) pullPTY(data []byte, focused, visible bool) ([]byte, []osc8Span) {
	if t == nil || t.term == nil {
		return data, nil
	}
	res := t.modes.feed(time.Now(), data, t.term.Mode())
	if len(res.replies) > 0 {
		t.sendKey(res.replies)
	}
	if res.clipSet != nil {
		_ = clipWrite(*res.clipSet)
	}
	if res.clipAsk {
		if s, err := clipRead(); err == nil {
			t.sendKey(encodeOSC52(s))
		}
	}
	for _, id := range res.closedIDs {
		closeDeskNote(id)
	}
	if res.aliveAsk {
		t.sendKey(osc99AliveReply(res.aliveID, aliveDeskIDs()))
	}
	for _, n := range res.notes {
		n.tab = t
		postDeskNote(n, focused, visible)
	}
	if res.notify {
		postDesktopNotification(res.ntitle, res.nbody)
	}
	if res.armFocus {
		// 1004 is applied when the bytes reach vt10x, which is after this
		// reply. Send the current focus now; later changes use reportFocus.
		if focused {
			t.sendKey([]byte("\x1b[I"))
		} else {
			t.sendKey([]byte("\x1b[O"))
		}
	}
	return res.ready, res.spans
}

func (t *tab) reportFocus(focused bool) {
	if t == nil || t.term == nil {
		return
	}
	if t.term.Mode()&vt10x.ModeFocus == 0 {
		return
	}
	if focused {
		t.sendKey([]byte("\x1b[I"))
		return
	}
	t.sendKey([]byte("\x1b[O"))
}

func (m *termModes) shellCursor(userStyle int) (style int, steady bool) {
	if m == nil {
		return userStyle, false
	}
	switch m.cursorShape {
	case 1:
		return 0, false
	case 2:
		return 0, true
	case 3:
		return 1, false
	case 4:
		return 1, true
	case 5:
		return 2, false
	case 6:
		return 2, true
	default:
		return userStyle, false
	}
}

func encodeOSC52(s string) []byte {
	b := base64.StdEncoding.EncodeToString([]byte(s))
	return []byte("\x1b]52;c;" + b + "\x07")
}

func xtversion() []byte {
	v := chrome.AppVersion()
	if v == "" {
		v = "dev"
	}
	return []byte("\x1bP>|Suzuri " + v + "\x1b\\")
}

func postDesktopNotification(title, body string) {
	title = strings.TrimSpace(title)
	body = strings.TrimSpace(body)
	if title == "" {
		title = "Suzuri"
	}
	if body == "" {
		return
	}
	if runtime.GOOS != "darwin" {
		return
	}
	script := fmt.Sprintf("display notification %s with title %s", appleString(body), appleString(title))
	go func() {
		_ = exec.Command("osascript", "-e", script).Run()
	}()
}

func appleString(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	return `"` + s + `"`
}

func decModes(body string) ([]int, bool) {
	if !strings.HasPrefix(body, "?") {
		return nil, false
	}
	rest := body[1:]
	if rest == "" || strings.ContainsAny(rest, "$<> ") {
		return nil, false
	}
	var modes []int
	for _, p := range strings.Split(rest, ";") {
		if p == "" {
			return nil, false
		}
		n, err := strconv.Atoi(p)
		if err != nil {
			return nil, false
		}
		modes = append(modes, n)
	}
	return modes, len(modes) > 0
}

func decrqm(body string) (priv bool, modes []int, ok bool) {
	if !strings.HasSuffix(body, "$") {
		return false, nil, false
	}
	body = strings.TrimSuffix(body, "$")
	if strings.HasPrefix(body, "?") {
		priv = true
		body = body[1:]
	}
	if body == "" {
		return priv, nil, false
	}
	for _, p := range strings.Split(body, ";") {
		if p == "" {
			return false, nil, false
		}
		n, err := strconv.Atoi(p)
		if err != nil {
			return false, nil, false
		}
		modes = append(modes, n)
	}
	return priv, modes, len(modes) > 0
}

func decscusr(body string) (int, bool) {
	if body == " " {
		return 1, true
	}
	if !strings.HasSuffix(body, " ") {
		return 0, false
	}
	num := strings.TrimSuffix(body, " ")
	if num == "" {
		return 1, true
	}
	n, err := strconv.Atoi(num)
	if err != nil {
		return 0, false
	}
	return n, true
}

func oscSeq(p []byte) (payload []byte, seqLen int, done bool) {
	if len(p) < 2 || p[0] != 0x1b || p[1] != ']' {
		return nil, 0, false
	}
	i := 2
	for i < len(p) {
		if p[i] == 0x07 {
			return p[2:i], i + 1, true
		}
		if p[i] == 0x1b && i+1 < len(p) && p[i+1] == '\\' {
			return p[2:i], i + 2, true
		}
		if p[i] == 0x1b && i+1 == len(p) {
			return nil, 0, false
		}
		i++
	}
	return nil, 0, false
}

func csiSeq(p []byte) (body string, final byte, seqLen int, done, bad bool) {
	if len(p) < 2 || p[0] != 0x1b || p[1] != '[' {
		return "", 0, 0, false, true
	}
	i := 2
	for i < len(p) {
		c := p[i]
		if c >= 0x40 && c <= 0x7e {
			return string(p[2:i]), c, i + 1, true, false
		}
		if c < 0x20 || c > 0x3f {
			return "", 0, 0, false, true
		}
		i++
	}
	return "", 0, 0, false, false
}

func visibleCapture(b []byte) string {
	var out []rune
	i := 0
	for i < len(b) {
		if b[i] == 0x1b {
			if i+1 < len(b) && b[i+1] == ']' {
				_, n, done := oscSeq(b[i:])
				if !done {
					break
				}
				i += n
				continue
			}
			if i+1 < len(b) && b[i+1] == '[' {
				_, _, n, done, bad := csiSeq(b[i:])
				if done {
					i += n
					continue
				}
				if !bad {
					break
				}
			}
			if i+1 < len(b) {
				i += 2
				continue
			}
			break
		}
		if b[i] < 0x20 && b[i] != '\t' {
			i++
			continue
		}
		r, size := utf8.DecodeRune(b[i:])
		if r == utf8.RuneError && size == 1 {
			i++
			continue
		}
		out = append(out, r)
		i += size
	}
	return strings.TrimSpace(string(out))
}

func safeLink(url string) string {
	url = strings.TrimSpace(url)
	if url == "" || strings.ContainsAny(url, "\r\n\x00") {
		return ""
	}
	lower := strings.ToLower(url)
	switch {
	case strings.HasPrefix(lower, "https://"),
		strings.HasPrefix(lower, "http://"),
		strings.HasPrefix(lower, "file://"),
		strings.HasPrefix(lower, "mailto:"):
		return url
	default:
		return ""
	}
}

func indexRunes(flat []rune, from int, needle []rune) int {
	if from < 0 {
		from = 0
	}
	if len(needle) == 0 || from > len(flat)-len(needle) {
		return -1
	}
	for i := from; i+len(needle) <= len(flat); i++ {
		match := true
		for j := range needle {
			if flat[i+j] != needle[j] {
				match = false
				break
			}
		}
		if match {
			return i
		}
	}
	return -1
}

func scrollShift(before, after []string) int {
	if len(before) == 0 || len(before) != len(after) {
		return 0
	}
	rows := len(before)
	bestK, bestN := 0, 0
	for k := 1; k < rows; k++ {
		n := 0
		for y := 0; y < rows-k; y++ {
			if strings.TrimSpace(before[y+k]) == "" {
				continue
			}
			if before[y+k] == after[y] {
				n++
			}
		}
		if n > bestN {
			bestN = n
			bestK = k
		}
	}
	if bestN < 2 {
		return 0
	}
	return bestK
}

func rowHasLink(row []string) bool {
	for _, s := range row {
		if s != "" {
			return true
		}
	}
	return false
}

func rowText(row []cellPix) string {
	rs := make([]rune, len(row))
	for i, c := range row {
		ch := c.Ch
		if ch == 0 {
			ch = ' '
		}
		rs[i] = ch
	}
	return string(rs)
}

func bytesHasPrefix(b []byte, pfx string) bool {
	return len(b) >= len(pfx) && string(b[:len(pfx)]) == pfx
}

func stripB64Space(s string) string {
	return strings.Map(func(r rune) rune {
		if r == ' ' || r == '\n' || r == '\r' || r == '\t' {
			return -1
		}
		return r
	}, s)
}

func clipRunes(s string, n int) string {
	rs := []rune(s)
	if len(rs) <= n {
		return s
	}
	return string(rs[:n])
}
