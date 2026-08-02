package ui

import (
	"math"
	"strings"

	"github.com/hinshun/vt10x"

	"github.com/StephenSHorton/suzuri/internal/chrome"
)

const defaultScrollbackMax = 5000

// History line kinds for Warp-style command blocks.
const (
	histNormal byte = iota
	histBlockRule  // horizontal rule above a command
	histBlockCmd   // path + "❯ command" (primary); path is optional prefix
	histImage      // host-rendered image (occupies span document rows)
)

type histLine struct {
	text string
	kind byte
	// Image block (kind == histImage): decoded bitmap + vertical span in cells.
	img  *tabImage
	span int // document rows reserved (≥1); only used for histImage
}

func (hl histLine) docRows() int {
	if hl.kind == histImage {
		if hl.span < 1 {
			return 1
		}
		return hl.span
	}
	return 1
}

// scrollback keeps rows that have scrolled off the live VT screen.
// History stores plain text (color not preserved for v0.3); live rows keep full cells.
// Command blocks are host-injected history lines with kind metadata for color.
//
// pin is a stick-bottom floor set on clear: history before pin stays in the
// buffer for scroll-up, but does not reappear when following the bottom.
//
// offset is the integer scroll target (wheel/keys). visual is a smoothed
// float that eases toward offset for animated scrolling.
type scrollback struct {
	lines  []histLine
	max    int
	prev   []string
	offset int
	visual float64 // smoothed lines-from-bottom; drives paint
	pin    int     // first history index shown at stick-bottom (0 = no pin)
	// skipNoteCapture: after commitLive / host clear, ignore noteScreen scroll
	// capture for a few frames. Spam-submit otherwise double-ingests the same
	// lines (commitLive + false multi-line scrollAmount) and the history thrashs.
	skipNoteCapture int
}

func newScrollback() *scrollback {
	return &scrollback{max: defaultScrollbackMax}
}

func snapshotScreenText(term vt10x.Terminal) []string {
	cols, rows := term.Size()
	out := make([]string, rows)
	buf := make([]rune, cols)
	for y := 0; y < rows; y++ {
		for x := 0; x < cols; x++ {
			buf[x] = displayRune(term.Cell(x, y).Char)
		}
		out[y] = string(buf)
	}
	return out
}

func snapshotScreenCells(term vt10x.Terminal) [][]cellPix {
	cols, rows := term.Size()
	out := make([][]cellPix, rows)
	for y := 0; y < rows; y++ {
		row := make([]cellPix, cols)
		for x := 0; x < cols; x++ {
			row[x] = glyphToCell(term.Cell(x, y))
		}
		out[y] = row
	}
	return out
}

// liveExtent returns how many leading live rows belong in the document.
// Trailing blank PTY rows are omitted so host-injected command blocks (history
// just above the live screen) stay visible at the bottom of the viewport
// instead of sitting above a full screen of empty cells.
//
// On the alternate screen (full-screen TUI apps) the entire grid is used —
// no clipping.
func liveExtent(term vt10x.Terminal) int {
	cols, rows := term.Size()
	if rows < 1 {
		return 0
	}
	if term.Mode()&vt10x.ModeAltScreen != 0 {
		return rows
	}
	last := -1
	if cur := term.Cursor(); cur.Y >= 0 && cur.Y < rows {
		last = cur.Y
	}
	for y := 0; y < rows; y++ {
		for x := 0; x < cols; x++ {
			ch := displayRune(term.Cell(x, y).Char)
			if ch != ' ' && ch != 0 {
				if y > last {
					last = y
				}
				break
			}
		}
	}
	if last < 0 {
		// Empty screen: keep a single row so the viewport isn't history-only
		// with no live anchor (quiet space prompt counts as blank).
		return 1
	}
	return last + 1
}

func snapshotLiveCells(term vt10x.Terminal) [][]cellPix {
	live := snapshotScreenCells(term)
	n := liveExtent(term)
	if n > len(live) {
		n = len(live)
	}
	return live[:n]
}

func snapshotLiveText(term vt10x.Terminal) []string {
	live := snapshotScreenText(term)
	n := liveExtent(term)
	if n > len(live) {
		n = len(live)
	}
	return live[:n]
}

func (s *scrollback) noteScreen(term vt10x.Terminal) {
	cur := snapshotScreenText(term)
	if s.skipNoteCapture > 0 {
		s.skipNoteCapture--
		s.prev = cur
		return
	}
	if len(s.prev) == len(cur) && len(cur) > 1 {
		// Full clear (clear / Clear-Host): previous live content becomes history
		// and pin stick-bottom so pre-clear history stays above (scroll up).
		if screenWasCleared(s.prev, cur) {
			for _, line := range s.prev {
				if strings.TrimSpace(line) != "" {
					s.push(line)
				}
			}
			// Floor for stick-bottom: nothing before this index until user scrolls up.
			s.pin = len(s.lines)
			s.stickBottom()
			// Host/PTY wipe — next frames are repaint noise, not scrolls.
			s.skipNoteCapture = 4
		} else if n := scrollAmount(s.prev, cur); n > 0 {
			// Multi-line scroll: find largest n where prev[n:] == cur[:len-n]
			for i := 0; i < n; i++ {
				// Dedup only for noteScreen captures (spam / double-ingest).
				s.pushDedup(s.prev[i], histNormal)
			}
		}
	}
	s.prev = cur
	// Clamp with a generous ceiling; precise max uses viewport in scrollBy.
	if max := s.docLen(); s.offset > max {
		s.offset = max
	}
}

// screenWasCleared is true when a non-empty screen becomes effectively blank
// (CSI 2J / clear). Distinguishes from a normal scroll.
func screenWasCleared(prev, cur []string) bool {
	if len(prev) != len(cur) || len(cur) == 0 {
		return false
	}
	prevHad := false
	for _, line := range prev {
		if strings.TrimSpace(line) != "" {
			prevHad = true
			break
		}
	}
	if !prevHad {
		return false
	}
	for _, line := range cur {
		if strings.TrimSpace(line) != "" {
			return false
		}
	}
	return true
}

// maxScrollDetect caps how many rows noteScreen will promote to history in one
// PTY frame. Spam-submit / partial VT updates used to false-match large n
// (O(rows) coincidence) and dump half the screen into history every tick.
const maxScrollDetect = 3

// scrollAmount returns how many rows the screen shifted up (0 if none).
func scrollAmount(prev, cur []string) int {
	if len(prev) != len(cur) || len(cur) < 2 {
		return 0
	}
	maxN := len(cur) - 1
	if maxN > maxScrollDetect {
		maxN = maxScrollDetect
	}
	for n := maxN; n >= 1; n-- {
		ok := true
		for i := 0; i < len(cur)-n; i++ {
			if prev[i+n] != cur[i] {
				ok = false
				break
			}
		}
		if !ok {
			continue
		}
		// Require a real change at the seam (not an identical screen).
		if prev[0] == cur[0] && prev[len(prev)-1] == cur[len(cur)-1] {
			continue
		}
		// Prefer matches where the scrolled-off rows had content (blank spam
		// from clear/repaint should not inflate history).
		had := false
		for i := 0; i < n; i++ {
			if strings.TrimSpace(prev[i]) != "" {
				had = true
				break
			}
		}
		if !had {
			continue
		}
		return n
	}
	return 0
}

func screenShiftedUp(prev, cur []string) bool {
	return scrollAmount(prev, cur) == 1
}

func (s *scrollback) push(line string) {
	s.pushKindImg(line, histNormal, nil, 0)
}

func (s *scrollback) pushKind(line string, kind byte) {
	s.pushKindImg(line, kind, nil, 0)
}

// commitLive folds the current primary-screen VT content into history and
// clears the local VT model so the next command's output is not stacked on top
// of the previous run in the live region.
//
// Without this, pushBlock only inserts the command header while shell output
// stays on the live surface — so blocks pile up in history and all outputs
// appear as one live stack at the bottom of the viewport.
//
// Only the live extent is committed (not trailing blank PTY rows). Using the
// full grid on small split panes duplicated empty-ish rows and made history
// thrash as blocks accumulated.
//
// The PTY/shell is not cleared (only the host's vt10x view). For line-oriented
// shells this is fine: new output is stream text that repaints cleanly.
// Alternate-screen apps are left alone.
func (s *scrollback) commitLive(term vt10x.Terminal) {
	if s == nil || term == nil {
		return
	}
	if term.Mode()&vt10x.ModeAltScreen != 0 {
		return
	}
	// Prefer live-extent lines so we don't re-ingest the whole PTY grid.
	lines := snapshotLiveText(term)
	for _, line := range lines {
		t := strings.TrimRight(line, " \t")
		if strings.TrimSpace(t) == "" {
			continue
		}
		s.push(t)
	}
	// Reset host-side VT so viewCells doesn't duplicate committed lines as live.
	_, _ = term.Write([]byte("\x1b[H\x1b[2J"))
	s.prev = snapshotScreenText(term)
	s.stickBottom()
	// Incoming PTY will repaint / echo; don't also noteScreen-capture that as
	// scrolled history (spam-submit reproduction).
	s.skipNoteCapture = 6
}

// pushBlock injects a Warp-style command block into history (above live screen).
// cols is used for the rule width; cmd may be multi-line.
// cwd when non-empty is shown on the same line as the command (primary color),
// e.g.  ~\projects  ❯ echo hi
// Callers should commitLive(term) first so the previous command's output is
// already in history under its block.
func (s *scrollback) pushBlock(cmd string, cols int, cwd string) {
	if stringsTrimSpace(cmd) == "" {
		return
	}
	if cols < 12 {
		cols = 12
	}
	// Full terminal width (no artificial cap) so the rule matches the pane.
	ruleW := cols
	if ruleW < 8 {
		ruleW = 8
	}
	// Spacer + rule + command line(s).
	s.pushKind("", histNormal)
	s.pushKind(strings.Repeat("─", ruleW), histBlockRule)
	// First line: optional path + one space + prompt + command (all primary).
	// e.g. ~\projects ❯ echo hi
	prompt := inputBarPrompt
	indent := strings.Repeat(" ", len([]rune(prompt)))
	path := displayPath(cwd)
	parts := strings.Split(cmd, "\n")
	for i, p := range parts {
		if i == 0 {
			line := prompt + p
			if path != "" {
				cmdRunes := len([]rune(prompt + p))
				pathBudget := cols - cmdRunes - 1 // room for the space before ❯
				if pathBudget < 6 {
					pathBudget = 6
				}
				if pathBudget > cols/2 {
					pathBudget = cols / 2
				}
				line = truncateRunes(path, pathBudget) + " " + prompt + p
			}
			s.pushKind(truncateRunes(line, cols), histBlockCmd)
		} else {
			s.pushKind(indent+p, histBlockCmd)
		}
	}
}

// docLen is history length in document rows (image blocks span multiple rows).
func (s *scrollback) docLen() int {
	if s == nil {
		return 0
	}
	n := 0
	for _, hl := range s.lines {
		n += hl.docRows()
	}
	return n
}

// pinDoc is the document-row index of pin (sum of docRows for lines[:pin]).
func (s *scrollback) pinDoc() int {
	if s == nil || s.pin <= 0 {
		return 0
	}
	n := 0
	lim := s.pin
	if lim > len(s.lines) {
		lim = len(s.lines)
	}
	for i := 0; i < lim; i++ {
		n += s.lines[i].docRows()
	}
	return n
}

// maxOffset is how far from the bottom the user can scroll (document rows).
// liveRows should match the live extent used by viewCells (0 if unknown).
func (s *scrollback) maxOffset(viewportRows, liveRows int) int {
	if s == nil {
		return 0
	}
	if viewportRows < 1 {
		viewportRows = 1
	}
	if liveRows < 0 {
		liveRows = 0
	}
	histDoc := s.docLen()
	docLen := histDoc + liveRows
	pin := s.pinDoc()
	post := docLen - pin
	if post < 0 {
		post = 0
	}
	// After clear with short post-pin content: can walk through pre-pin history.
	if pin > 0 && post <= viewportRows {
		return pin
	}
	// Normal: stop once the oldest document row is at the top of the viewport.
	m := docLen - viewportRows
	if m < 0 {
		m = 0
	}
	return m
}

// pushImage appends a multi-row image block under the current history (after
// the latest command output). span is height in cells.
func (s *scrollback) pushImage(im *tabImage, span int) {
	if s == nil || im == nil {
		return
	}
	if span < 3 {
		span = 3
	}
	if span > 40 {
		span = 40
	}
	label := im.path
	if label == "" {
		label = im.key
	}
	s.pushKindImg(label, histImage, im, span)
}

func (s *scrollback) pushKindImg(line string, kind byte, im *tabImage, span int) {
	s.lines = append(s.lines, histLine{text: strings.TrimRight(line, " "), kind: kind, img: im, span: span})
	if len(s.lines) > s.max {
		drop := len(s.lines) - s.max
		droppedRows := 0
		for i := 0; i < drop; i++ {
			droppedRows += s.lines[i].docRows()
		}
		s.lines = append([]histLine(nil), s.lines[drop:]...)
		if s.offset > 0 {
			s.offset -= droppedRows
			if s.offset < 0 {
				s.offset = 0
			}
		}
		if s.pin > 0 {
			s.pin -= drop // pin is still a line index
			if s.pin < 0 {
				s.pin = 0
			}
		}
	}
}

func (s *scrollback) scrollBy(delta, viewportRows int) {
	// liveRows unknown here — use 0 (slightly conservative max). Callers that
	// know live extent can clamp again; viewWindow still floors start at 0.
	maxOff := s.maxOffset(viewportRows, 0)
	s.offset += delta
	if s.offset < 0 {
		s.offset = 0
	}
	if s.offset > maxOff {
		s.offset = maxOff
	}
	// Snap visual most of the way so wheel works even if tickSmooth is delayed;
	// remaining ease is applied by tickSmooth on the blink/frame loop.
	target := float64(s.offset)
	if absFloat(target-s.visual) > 8 {
		// Big jump (page up/down): snap.
		s.visual = target
	} else {
		// Small wheel steps: keep smooth feel but stay responsive.
		s.visual += (target - s.visual) * 0.55
		if absFloat(target-s.visual) < 0.15 {
			s.visual = target
		}
	}
}

func (s *scrollback) atBottom() bool { return s.offset == 0 && s.visual < 0.05 }

func (s *scrollback) stickBottom() {
	s.offset = 0
	s.visual = 0
}

// pinHere floors stick-bottom at the current history length. Pre-pin lines
// remain reachable by scrolling up (used after clear).
func (s *scrollback) pinHere() {
	if s == nil {
		return
	}
	s.pin = len(s.lines)
	s.stickBottom()
	s.skipNoteCapture = 4
}

// pushDedup appends a normal history line unless it duplicates the last row.
// Used only for noteScreen scroll capture (not pushBlock / intentional pushes).
func (s *scrollback) pushDedup(line string, kind byte) {
	line = strings.TrimRight(line, " ")
	if strings.TrimSpace(line) == "" {
		return
	}
	if n := len(s.lines); n > 0 {
		last := s.lines[n-1]
		if last.kind == kind && last.text == line {
			return
		}
	}
	s.pushKindImg(line, kind, nil, 0)
}

// tickSmooth eases visual toward offset. Call once per frame (dt in seconds).
func (s *scrollback) tickSmooth(dt float64) {
	if s == nil {
		return
	}
	if dt <= 0 {
		dt = 1.0 / 60
	}
	if dt > 0.05 {
		dt = 0.05
	}
	target := float64(s.offset)
	// Exponential ease — snappy but readable (~smooth scroll).
	const k = 16.0
	s.visual += (target - s.visual) * (1 - math.Exp(-k*dt))
	if absFloat(target-s.visual) < 0.02 {
		s.visual = target
	}
}

func absFloat(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}

// viewWindow returns absolute document start index and top pad for the viewport.
//
// Uses smoothed visual offset. After clear (pin set, short post-pin content),
// history is revealed from the TOP of the shell (pad bottom), one line per
// offset notch — not bottom-aligned growth.
func (s *scrollback) viewWindow(viewportRows, liveRows int) (start, pad int) {
	if viewportRows < 1 {
		viewportRows = 1
	}
	if liveRows < 0 {
		liveRows = 0
	}
	histDoc := s.docLen()
	docLen := histDoc + liveRows
	pin := s.pinDoc()
	// Use floor of visual so partial smooth steps don't skip lines oddly.
	off := int(s.visual + 1e-6)
	if off < 0 {
		off = 0
	}

	// Post-pin content length (history after pin + live).
	post := docLen - pin
	if post < 0 {
		post = 0
	}

	// After clear: reveal pre-pin from the top of the viewport.
	// offset 0 → start=pin (empty/short post at top, blanks below)
	// offset k → start=pin-k (k history lines enter from the top)
	if pin > 0 && post <= viewportRows {
		start = pin - off
		if start < 0 {
			start = 0
		}
		// Top-align: no top pad; blanks fall out naturally past doc end.
		return start, 0
	}

	// Normal document scroll.
	start = docLen - viewportRows - off
	if start < 0 {
		start = 0
	}
	// Stick-bottom only: don't peek above pin when fully at bottom.
	if off == 0 && s.visual < 0.05 && pin > 0 && start < pin {
		start = pin
	}
	return start, 0
}

// scrollFrac is the fractional part of visual for sub-line pixel smooth scroll.
func (s *scrollback) scrollFrac() float64 {
	if s == nil {
		return 0
	}
	f := s.visual - float64(int(s.visual+1e-6))
	if f < 0 {
		return 0
	}
	return f
}

// Scrollbar reports thumb placement for a track of the given pixel height.
// atBottom → thumb near bottom; scrolled to oldest → thumb near top.
// visible is false when there is nothing to scroll.
func (s *scrollback) Scrollbar(viewportRows, liveRows, trackH int) (thumbY, thumbH int, visible bool) {
	if s == nil || trackH < 8 || viewportRows < 1 {
		return 0, 0, false
	}
	histDoc := s.docLen()
	docLen := histDoc + liveRows
	maxOff := s.maxOffset(viewportRows, liveRows)
	// Show bar if we have history beyond one viewport, or a pin with history above.
	if maxOff < 1 && (s.pin == 0 || histDoc == 0) {
		return 0, 0, false
	}
	if docLen <= 1 && maxOff < 1 {
		return 0, 0, false
	}

	// Thumb size ∝ viewport / document (min 18px).
	den := docLen
	if den < viewportRows+1 {
		den = viewportRows + 1
	}
	ratio := float64(viewportRows) / float64(den)
	if ratio > 1 {
		ratio = 1
	}
	thumbH = int(float64(trackH) * ratio)
	if thumbH < 18 {
		thumbH = 18
	}
	if thumbH > trackH {
		thumbH = trackH
	}
	travel := trackH - thumbH
	if travel < 0 {
		travel = 0
	}
	// visual 0 = bottom (newest) → thumb at bottom; visual max = top.
	var t float64
	if maxOff > 0 {
		t = s.visual / float64(maxOff)
	}
	if t < 0 {
		t = 0
	}
	if t > 1 {
		t = 1
	}
	thumbY = int(float64(travel) * (1 - t))
	return thumbY, thumbH, true
}

// isClearCommand reports shell wipes that should pin scrollback (empty pane,
// history only via scroll-up). Matches common clear spellings on Win/mac/Linux.
func isClearCommand(line string) bool {
	s := strings.TrimSpace(line)
	if s == "" {
		return false
	}
	// First token only (ignore args like `clear -x` if any).
	fields := strings.Fields(s)
	if len(fields) == 0 {
		return false
	}
	cmd := strings.ToLower(fields[0])
	// Strip path: /usr/bin/clear → clear
	if i := strings.LastIndexAny(cmd, `/\`); i >= 0 {
		cmd = cmd[i+1:]
	}
	// PowerShell: Clear-Host / clear-host
	switch cmd {
	case "clear", "cls", "clear-host":
		return true
	default:
		return false
	}
}

// historyTail returns the last n history lines for diagnostics.
func (s *scrollback) historyTail(n int) []histLine {
	if n < 1 || len(s.lines) == 0 {
		return nil
	}
	if n > len(s.lines) {
		n = len(s.lines)
	}
	out := make([]histLine, n)
	copy(out, s.lines[len(s.lines)-n:])
	return out
}

// recentBlocks returns recent host-injected command block texts (newest last).
func (s *scrollback) recentBlocks(max int) []string {
	if max < 1 {
		return nil
	}
	var cmds []string
	var cur []string
	flush := func() {
		if len(cur) == 0 {
			return
		}
		cmds = append(cmds, strings.Join(cur, "\n"))
		cur = nil
	}
	for _, hl := range s.lines {
		switch hl.kind {
		case histBlockCmd:
			// Strip leading prompt on first line of a block.
			t := hl.text
			if strings.HasPrefix(t, inputBarPrompt) {
				t = strings.TrimPrefix(t, inputBarPrompt)
			} else {
				t = strings.TrimLeft(t, " ")
			}
			cur = append(cur, t)
		case histBlockRule:
			flush()
		default:
			if len(cur) > 0 {
				flush()
			}
		}
	}
	flush()
	if len(cmds) > max {
		cmds = cmds[len(cmds)-max:]
	}
	return cmds
}

// viewCells builds the viewport with color for live rows and styled history.
// Live rows are clipped to liveExtent so trailing blank PTY cells don't push
// recent history (command blocks) off the bottom of the screen.
//
// Alternate-screen apps own the whole viewport — history is not mixed in.
//
// Stick-bottom (offset==0) respects pin after clear: pre-pin history stays
// above and only appears when the user scrolls up.
func (s *scrollback) viewCells(term vt10x.Terminal, viewportRows int) [][]cellPix {
	cols, _ := term.Size()
	if cols < 1 {
		cols = 1
	}
	if viewportRows < 1 {
		viewportRows = 1
	}

	blankRow := func() []cellPix {
		row := make([]cellPix, cols)
		for x := range row {
			row[x] = cellPix{Ch: ' ', FR: 220, FG: 220, FB: 220}
		}
		return row
	}

	if term.Mode()&vt10x.ModeAltScreen != 0 {
		live := snapshotScreenCells(term)
		out := make([][]cellPix, viewportRows)
		for i := 0; i < viewportRows; i++ {
			row := blankRow()
			if i < len(live) {
				copy(row, live[i])
			}
			out[i] = row
		}
		return out
	}

	live := snapshotLiveCells(term)
	histDoc := s.docLen()
	start, pad := s.viewWindow(viewportRows, len(live))

	out := make([][]cellPix, viewportRows)
	for i := 0; i < viewportRows; i++ {
		if i < pad {
			out[i] = blankRow()
			continue
		}
		out[i] = s.rowAtDoc(start+(i-pad), histDoc, live, cols)
	}
	return out
}

// rowAtDoc maps a flattened document row index (history spans + live) to cells.
func (s *scrollback) rowAtDoc(idx, histDoc int, live [][]cellPix, cols int) []cellPix {
	row := make([]cellPix, cols)
	for x := range row {
		row[x] = cellPix{Ch: ' ', FR: 220, FG: 220, FB: 220}
	}
	if idx < 0 {
		return row
	}
	if idx < histDoc {
		hl, sub := s.histAtDoc(idx)
		if hl.kind == histImage {
			// Leave cells empty — host paints the bitmap over this span.
			// Tiny caption on first sub-row only (muted).
			if sub == 0 && hl.text != "" {
				rs := []rune("img " + hl.text)
				fr, fg, fb := chrome.SoftR, chrome.SoftG, chrome.SoftB
				for x := 0; x < cols && x < len(rs); x++ {
					row[x] = cellPix{Ch: rs[x], FR: fr, FG: fg, FB: fb}
				}
			}
			return row
		}
		rs := []rune(hl.text)
		fr, fg, fb := histColor(hl.kind)
		for x := 0; x < cols && x < len(rs); x++ {
			row[x] = cellPix{Ch: rs[x], FR: fr, FG: fg, FB: fb}
		}
		return row
	}
	li := idx - histDoc
	if li >= 0 && li < len(live) {
		copy(row, live[li])
	}
	return row
}

// histAtDoc returns the history line and sub-row within its span for doc index idx.
func (s *scrollback) histAtDoc(idx int) (histLine, int) {
	if idx < 0 || s == nil {
		return histLine{}, 0
	}
	at := 0
	for _, hl := range s.lines {
		n := hl.docRows()
		if idx < at+n {
			return hl, idx - at
		}
		at += n
	}
	return histLine{}, 0
}

// visibleImages returns images that intersect the current viewport, with the
// viewport row of the top of each image block (for pixel placement).
func (s *scrollback) visibleImages(term vt10x.Terminal, viewportRows int) []visImage {
	if s == nil || term == nil || term.Mode()&vt10x.ModeAltScreen != 0 {
		return nil
	}
	liveN := liveExtent(term)
	// Prefer live cell count used by viewCells
	live := snapshotLiveCells(term)
	liveN = len(live)
	histDoc := s.docLen()
	start, pad := s.viewWindow(viewportRows, liveN)
	var out []visImage
	at := 0
	for _, hl := range s.lines {
		n := hl.docRows()
		if hl.kind == histImage && hl.img != nil {
			// First doc row of this image in absolute coords = at
			// Viewport row = at - start + pad
			vr := at - start + pad
			// Visible if any of the span overlaps [0, viewportRows)
			if vr+n > 0 && vr < viewportRows {
				out = append(out, visImage{img: hl.img, viewRow: vr, span: n})
			}
		}
		at += n
	}
	_ = histDoc
	return out
}

type visImage struct {
	img     *tabImage
	viewRow int // top row in viewport (may be negative if partially scrolled off)
	span    int
}

// histColor returns FG for a history line kind (theme-aware).
func histColor(kind byte) (r, g, b byte) {
	switch kind {
	case histBlockRule:
		// Soft primary for the rule.
		return chrome.PrimR/2 + 40, chrome.PrimG/2 + 40, chrome.PrimB/2 + 40
	case histBlockCmd:
		return chrome.PrimR, chrome.PrimG, chrome.PrimB
	case histImage:
		return chrome.SoftR, chrome.SoftG, chrome.SoftB
	default:
		return chrome.SoftR, chrome.SoftG, chrome.SoftB
	}
}

// view keeps rune-only API for tests / simple callers.
func (s *scrollback) view(term vt10x.Terminal, viewportRows int) [][]rune {
	cells := s.viewCells(term, viewportRows)
	out := make([][]rune, len(cells))
	for y := range cells {
		row := make([]rune, len(cells[y]))
		for x := range cells[y] {
			row[x] = cells[y][x].Ch
		}
		out[y] = row
	}
	return out
}

// absLine maps a viewport row to a document line index. liveRows should be the
// effective live height (liveExtent), not the full PTY row count.
func (s *scrollback) absLine(viewY, viewportRows, liveRows int) int {
	if liveRows < 1 {
		liveRows = 1
	}
	start, pad := s.viewWindow(viewportRows, liveRows)
	if viewY < pad {
		return start
	}
	return start + (viewY - pad)
}

func (s *scrollback) lineText(abs int, term vt10x.Terminal) string {
	if abs < 0 {
		return ""
	}
	if abs < len(s.lines) {
		return s.lines[abs].text
	}
	live := snapshotLiveText(term)
	i := abs - len(s.lines)
	if i >= 0 && i < len(live) {
		return strings.TrimRight(live[i], " ")
	}
	return ""
}
