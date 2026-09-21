//go:build windows || darwin

package ui

import (
	"encoding/base64"
	"image"
	"image/color"
	"image/draw"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/image/font"
	"golang.org/x/image/font/basicfont"
	"golang.org/x/image/math/fixed"

	"github.com/StephenSHorton/suzuri/internal/chrome"
)

// deskNote is one desktop notification requested by a terminal program.
// Protocols: OSC 9 (iTerm2), OSC 777 (urxvt / WezTerm / Ghostty), OSC 99 (kitty).
type deskNote struct {
	ID       string
	Title    string
	Body     string
	Sound    string // system, silent, error, warn, info, question
	Icon     string // error, warn, info, question, or empty
	Urgency  int    // 0 low, 1 normal, 2 critical
	Occasion string // always, unfocused, invisible
	Expire   time.Duration
	Never    bool
	Report   bool
	Focus    bool
	CloseEvt bool
	// OnActivate runs on a left click (install, for the update card).
	OnActivate func()
	// OnDismiss runs on a right click.
	OnDismiss func()
	tab       *tab
}

type liveNote struct {
	deskNote
	born  time.Time
	until time.Time
	hover bool
}

var (
	noticeMu     sync.Mutex
	noticeLive   []liveNote
	noticeHover  bool
	noticeHist   []playedNotice
	noticeUnread int
	bellDirty    bool
)

// onNoticeBell runs on the UI thread when the session history changes.
var onNoticeBell func()

type playedNotice struct {
	Title string
	Body  string
	At    time.Time
	TabID int
	ID    string
}

func postDeskNote(n deskNote, focused, visible bool) {
	switch n.Occasion {
	case "unfocused":
		if focused {
			return
		}
	case "invisible":
		if focused || visible {
			return
		}
	}
	if strings.TrimSpace(n.Title) == "" && strings.TrimSpace(n.Body) == "" {
		return
	}
	if n.Title == "" {
		n.Title = n.Body
		n.Body = ""
	}
	if n.Expire <= 0 && !n.Never {
		n.Expire = 3200 * time.Millisecond
		if n.Urgency >= 2 {
			n.Expire = 6400 * time.Millisecond
		}
	}
	now := time.Now()
	noticeMu.Lock()
	defer noticeMu.Unlock()
	if n.ID != "" {
		for i := range noticeLive {
			if noticeLive[i].ID == n.ID {
				keep := noticeLive[i].born
				noticeLive[i] = liveNote{deskNote: n, born: keep, until: expireAt(now, n)}
				noticeMu.Unlock()
				rememberNotice(n)
				noticeMu.Lock()
				return
			}
		}
	}
	var dropped *liveNote
	if len(noticeLive) >= 5 {
		dropIdx := 0
		for i, existing := range noticeLive {
			if !existing.Never {
				dropIdx = i
				break
			}
		}
		d := noticeLive[dropIdx]
		noticeLive = append(noticeLive[:dropIdx], noticeLive[dropIdx+1:]...)
		dropped = &d
	}
	ln := liveNote{deskNote: n, born: now, until: expireAt(now, n)}
	noticeLive = append(noticeLive, ln)
	noticeMu.Unlock()
	rememberNotice(n)
	if dropped != nil {
		sendNoticeClose(dropped)
	}
	playNoticeSound(n.Sound, n.Urgency)
	noticeMu.Lock()
}

func expireAt(now time.Time, n deskNote) time.Time {
	if n.Never {
		return time.Time{}
	}
	return now.Add(n.Expire)
}

func closeDeskNote(id string) {
	if id == "" {
		return
	}
	noticeMu.Lock()
	defer noticeMu.Unlock()
	for i := range noticeLive {
		if noticeLive[i].ID == id {
			noticeLive = append(noticeLive[:i], noticeLive[i+1:]...)
			return
		}
	}
}

func rememberNotice(n deskNote) {
	id := -1
	if n.tab != nil {
		id = n.tab.id
	}
	item := playedNotice{
		Title: strings.TrimSpace(n.Title),
		Body:  strings.TrimSpace(n.Body),
		At:    time.Now(),
		TabID: id,
		ID:    n.ID,
	}
	noticeMu.Lock()
	if item.ID != "" {
		for i := range noticeHist {
			if noticeHist[i].ID == item.ID {
				noticeHist = append(noticeHist[:i], noticeHist[i+1:]...)
				break
			}
		}
	}
	noticeHist = append([]playedNotice{item}, noticeHist...)
	if len(noticeHist) > 40 {
		noticeHist = noticeHist[:40]
	}
	noticeUnread++
	bellDirty = true
	noticeMu.Unlock()
}

func noticeHistory() []playedNotice {
	noticeMu.Lock()
	defer noticeMu.Unlock()
	out := make([]playedNotice, len(noticeHist))
	copy(out, noticeHist)
	noticeUnread = 0
	return out
}

func takeBellDirty() bool {
	noticeMu.Lock()
	defer noticeMu.Unlock()
	d := bellDirty
	bellDirty = false
	return d
}

func noticeBellState() (count int, unread bool) {
	noticeMu.Lock()
	defer noticeMu.Unlock()
	return len(noticeHist), noticeUnread > 0
}

func noticeCount() int {
	noticeMu.Lock()
	defer noticeMu.Unlock()
	return len(noticeLive)
}

func aliveDeskIDs() []string {
	noticeMu.Lock()
	defer noticeMu.Unlock()
	var ids []string
	for _, n := range noticeLive {
		if n.ID != "" {
			ids = append(ids, n.ID)
		}
	}
	return ids
}

func tickNotices(now time.Time) []liveNote {
	noticeMu.Lock()
	defer noticeMu.Unlock()
	if len(noticeLive) == 0 {
		return nil
	}
	var dead []liveNote
	keep := noticeLive[:0]
	for _, n := range noticeLive {
		if noticeHover {
			keep = append(keep, n)
			continue
		}
		if !n.until.IsZero() && now.After(n.until) {
			dead = append(dead, n)
			continue
		}
		keep = append(keep, n)
	}
	noticeLive = keep
	return dead
}

func sendNoticeClose(n *liveNote) {
	if n == nil || !n.CloseEvt || n.tab == nil || n.ID == "" {
		return
	}
	n.tab.sendKey([]byte("\x1b]99;i=" + n.ID + ":p=;\x1b\\"))
}

func activateNotice(idx int) {
	noticeMu.Lock()
	if idx < 0 || idx >= len(noticeLive) {
		noticeMu.Unlock()
		return
	}
	n := noticeLive[idx]
	noticeLive = append(noticeLive[:idx], noticeLive[idx+1:]...)
	noticeMu.Unlock()
	if n.OnActivate != nil {
		n.OnActivate()
	} else {
		if n.Focus && n.tab != nil && revealNoticePane != nil {
			revealNoticePane(n.tab.id)
		} else if n.Focus {
			focusNoticeHost()
		}
		if n.Report && n.tab != nil {
			id := n.ID
			if id == "" {
				id = "0"
			}
			n.tab.sendKey([]byte("\x1b]99;i=" + id + "\x1b\\"))
		}
	}
	sendNoticeClose(&n)
}

func dismissNoticeAt(idx int) {
	noticeMu.Lock()
	if idx < 0 || idx >= len(noticeLive) {
		noticeMu.Unlock()
		return
	}
	n := noticeLive[idx]
	noticeLive = append(noticeLive[:idx], noticeLive[idx+1:]...)
	noticeMu.Unlock()
	if n.OnDismiss != nil {
		n.OnDismiss()
	}
	sendNoticeClose(&n)
}

type noticeCard struct {
	x, y, w, h int
	index      int // index in bottom-up visual order, 0 = bottom (oldest)
}

func renderNotices(now time.Time) (pix []byte, stride, width, height int, cards []noticeCard) {
	noticeMu.Lock()
	items := append([]liveNote(nil), noticeLive...)
	noticeMu.Unlock()
	if len(items) == 0 {
		return nil, 0, 0, 0, nil
	}
	const (
		scale  = 2
		cardW  = 320
		cardH  = 72
		gap    = 8
		margin = 16
	)
	hpx := margin + len(items)*(cardH+gap)
	wpx := margin + cardW + 48
	img := image.NewRGBA(image.Rect(0, 0, wpx*scale, hpx*scale))
	cards = make([]noticeCard, len(items))
	// Oldest sits on the bottom, matching the s&box stack.
	y := hpx - margin - cardH
	for i, n := range items {
		slide := 0
		age := now.Sub(n.born)
		if age < 180*time.Millisecond {
			slide = int((1 - float64(age)/float64(180*time.Millisecond)) * 36)
		}
		x := margin - slide
		drawCard(img, scale, x, y, cardW, cardH, n)
		cards[i] = noticeCard{x: x * scale, y: y * scale, w: cardW * scale, h: cardH * scale, index: i}
		y -= cardH + gap
	}
	return img.Pix, img.Stride, img.Bounds().Dx(), img.Bounds().Dy(), cards
}

func drawCard(dst *image.RGBA, scale, x, y, w, h int, n liveNote) {
	accent := color.RGBA{R: chrome.PrimR, G: chrome.PrimG, B: chrome.PrimB, A: 255}
	switch {
	case n.Urgency >= 2 || n.Icon == "error":
		accent = color.RGBA{R: 220, G: 70, B: 70, A: 255}
	case n.Icon == "warn" || n.Icon == "warning":
		accent = color.RGBA{R: 210, G: 170, B: 60, A: 255}
	}
	bg := color.RGBA{R: chrome.PanelR, G: chrome.PanelG, B: chrome.PanelB, A: 242}
	text := color.RGBA{R: chrome.TextR, G: chrome.TextG, B: chrome.TextB, A: 255}
	soft := color.RGBA{R: chrome.SoftR, G: chrome.SoftG, B: chrome.SoftB, A: 255}
	fillNoticeRect(dst, (x+3)*scale, (y+4)*scale, w*scale, h*scale, color.RGBA{A: 70})
	fillNoticeRound(dst, x*scale, y*scale, w*scale, h*scale, 8*scale, bg)
	fillNoticeRect(dst, x*scale, y*scale, 4*scale, h*scale, accent)
	drawString(dst, n.Title, (x+16)*scale, (y+12)*scale, scale, text)
	if n.Body != "" {
		drawString(dst, n.Body, (x+16)*scale, (y+36)*scale, scale, soft)
	}
}

func fillNoticeRect(dst *image.RGBA, x, y, w, h int, c color.RGBA) {
	draw.Draw(dst, image.Rect(x, y, x+w, y+h), &image.Uniform{C: c}, image.Point{}, draw.Over)
}

func fillNoticeRound(dst *image.RGBA, x, y, w, h, r int, c color.RGBA) {
	fillNoticeRect(dst, x+r, y, w-2*r, h, c)
	fillNoticeRect(dst, x, y+r, w, h-2*r, c)
	fillNoticeRect(dst, x, y, r, r, c)
	fillNoticeRect(dst, x+w-r, y, r, r, c)
	fillNoticeRect(dst, x, y+h-r, r, r, c)
	fillNoticeRect(dst, x+w-r, y+h-r, r, r, c)
}

func drawString(dst *image.RGBA, s string, x, y, scale int, col color.RGBA) {
	s = clipRunes(strings.TrimSpace(s), 42)
	if s == "" {
		return
	}
	tmp := image.NewRGBA(image.Rect(0, 0, 7*len(s)+2, 14))
	d := font.Drawer{
		Dst:  tmp,
		Src:  image.NewUniform(col),
		Face: basicfont.Face7x13,
		Dot:  fixed.P(0, 12),
	}
	d.DrawString(s)
	for py := 0; py < tmp.Bounds().Dy(); py++ {
		for px := 0; px < tmp.Bounds().Dx(); px++ {
			p := tmp.RGBAAt(px, py)
			if p.A == 0 {
				continue
			}
			for sy := 0; sy < scale; sy++ {
				for sx := 0; sx < scale; sx++ {
					dst.SetRGBA(x+px*scale+sx, y+py*scale+sy, p)
				}
			}
		}
	}
}

func hitNotice(cards []noticeCard, x, y int) int {
	for i := len(cards) - 1; i >= 0; i-- {
		c := cards[i]
		if x >= c.x && x < c.x+c.w && y >= c.y && y < c.y+c.h {
			return c.index
		}
	}
	return -1
}

// revealNoticePane focuses the pane that emitted a notification.
// The running host installs it. Nil in the notify preview command.
var revealNoticePane func(tabID int)

var noticeCards []noticeCard

func driveNotices(now time.Time, focused, visible bool) {
	pollNoticeInput()
	if takeBellDirty() && onNoticeBell != nil {
		onNoticeBell()
	}
	for _, n := range tickNotices(now) {
		nn := n
		sendNoticeClose(&nn)
	}
	pix, stride, width, height, cards := renderNotices(now)
	noticeCards = cards
	presentNoticeImage(pix, stride, width, height)
	_ = focused
	_ = visible
}

func onNoticeMouse(x, y, kind int) {
	switch kind {
	case 0: // leave
		noticeMu.Lock()
		noticeHover = false
		noticeMu.Unlock()
	case 2: // move
		noticeMu.Lock()
		noticeHover = hitNotice(noticeCards, x, y) >= 0
		noticeMu.Unlock()
	case 1: // left — activate
		if i := hitNotice(noticeCards, x, y); i >= 0 {
			activateNotice(i)
		}
	case 3: // right — dismiss
		if i := hitNotice(noticeCards, x, y); i >= 0 {
			dismissNoticeAt(i)
		}
	}
}

func (m *termModes) takeOSC99(payload []byte, res *feedResult) {
	rest := string(payload)
	if strings.HasPrefix(rest, "99;") {
		rest = rest[3:]
	} else if rest == "99" {
		rest = ""
	}
	meta, body, _ := strings.Cut(rest, ";")
	kv := parseOSC99Meta(meta)
	id := sanitizeNoticeID(lastMeta(kv, "i"))
	done := true
	if d, ok := lastMetaOK(kv, "d"); ok {
		done = d != "0"
	}
	p := lastMeta(kv, "p")
	if p == "" {
		p = "title"
	}
	enc := lastMeta(kv, "e") == "1"
	text := decodeOSC99Payload(body, enc)
	switch p {
	case "?":
		res.replies = append(res.replies, osc99QueryReply(id)...)
		return
	case "alive":
		res.aliveID = id
		res.aliveAsk = true
		return
	case "close":
		if id != "" {
			res.closedIDs = append(res.closedIDs, id)
		}
		return
	case "title", "body":
	default:
		// Unknown payload type: ignore this chunk's text, still finish if done.
		text = ""
		p = ""
	}
	key := id
	buf := m.osc99[key]
	if buf == nil {
		buf = &osc99Buf{urgency: 1, sound: "system", occasion: "always", expireMS: -1, focus: true}
		if m.osc99 == nil {
			m.osc99 = map[string]*osc99Buf{}
		}
		m.osc99[key] = buf
	}
	if id != "" {
		buf.id = id
	}
	applyOSC99Meta(buf, kv)
	if p == "title" {
		buf.title += text
	} else if p == "body" {
		buf.body += text
	}
	if !done {
		return
	}
	note := buf.toNote()
	delete(m.osc99, key)
	if strings.TrimSpace(note.Title) == "" && strings.TrimSpace(note.Body) == "" {
		return
	}
	res.notes = append(res.notes, note)
}

type osc99Buf struct {
	id       string
	title    string
	body     string
	sound    string
	icon     string
	urgency  int
	occasion string
	expireMS int
	report   bool
	focus    bool
	closeEvt bool
}

func (b *osc99Buf) toNote() deskNote {
	n := deskNote{
		ID: b.id, Title: clipRunes(b.title, 180), Body: clipRunes(b.body, 400),
		Sound: b.sound, Icon: b.icon, Urgency: b.urgency, Occasion: b.occasion,
		Report: b.report, Focus: b.focus, CloseEvt: b.closeEvt,
	}
	switch {
	case b.expireMS == 0:
		n.Never = true
	case b.expireMS > 0:
		n.Expire = time.Duration(b.expireMS) * time.Millisecond
	}
	return n
}

func applyOSC99Meta(b *osc99Buf, kv map[string][]string) {
	if u, ok := lastMetaOK(kv, "u"); ok {
		if n, err := strconv.Atoi(u); err == nil && n >= 0 && n <= 2 {
			b.urgency = n
		}
	}
	if o, ok := lastMetaOK(kv, "o"); ok && (o == "always" || o == "unfocused" || o == "invisible") {
		b.occasion = o
	}
	if s, ok := lastMetaOK(kv, "s"); ok {
		if name := noticeSoundName(s); name != "" {
			b.sound = name
		}
	}
	if w, ok := lastMetaOK(kv, "w"); ok {
		if n, err := strconv.Atoi(w); err == nil && n >= -1 && n < 600000 {
			b.expireMS = n
		}
	}
	if c, ok := lastMetaOK(kv, "c"); ok {
		b.closeEvt = c == "1"
	}
	if a, ok := lastMetaOK(kv, "a"); ok {
		b.focus = false
		b.report = false
		for _, part := range strings.Split(a, ",") {
			part = strings.TrimSpace(part)
			off := strings.HasPrefix(part, "-")
			name := strings.TrimPrefix(part, "-")
			switch name {
			case "focus":
				if !off {
					b.focus = true
				}
			case "report":
				if !off {
					b.report = true
				}
			}
		}
	}
	for _, raw := range kv["n"] {
		if name := noticeIconName(raw); name != "" {
			b.icon = name
			break
		}
	}
}

func parseOSC99Meta(meta string) map[string][]string {
	out := map[string][]string{}
	if meta == "" {
		return out
	}
	for _, part := range strings.Split(meta, ":") {
		k, v, ok := strings.Cut(part, "=")
		if !ok || len(k) != 1 {
			continue
		}
		out[k] = append(out[k], v)
	}
	return out
}

func lastMeta(kv map[string][]string, k string) string {
	s, _ := lastMetaOK(kv, k)
	return s
}

func lastMetaOK(kv map[string][]string, k string) (string, bool) {
	vs := kv[k]
	if len(vs) == 0 {
		return "", false
	}
	return vs[len(vs)-1], true
}

func decodeOSC99Payload(s string, b64 bool) string {
	if !b64 {
		return stripControls(s)
	}
	raw, err := base64.StdEncoding.DecodeString(stripB64Space(s))
	if err != nil {
		raw, err = base64.RawStdEncoding.DecodeString(stripB64Space(s))
		if err != nil {
			return ""
		}
	}
	if len(raw) > 8192 {
		raw = raw[:8192]
	}
	return stripControls(string(raw))
}

func stripControls(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r < 0x20 || r == 0x7f || (r >= 0x80 && r <= 0x9f) {
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

func sanitizeNoticeID(id string) string {
	if id == "" || id == "0" {
		return ""
	}
	var b strings.Builder
	for _, r := range id {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '_' || r == '-' || r == '+' || r == '.':
			b.WriteRune(r)
		}
	}
	return b.String()
}

func noticeSoundName(raw string) string {
	if isSoundName(raw) {
		return raw
	}
	dec := decodeOSC99Payload(raw, true)
	if isSoundName(dec) {
		return dec
	}
	return ""
}

func isSoundName(s string) bool {
	switch s {
	case "system", "silent", "error", "warn", "warning", "info", "question":
		return true
	default:
		return false
	}
}

func noticeIconName(raw string) string {
	if isIconName(raw) {
		return raw
	}
	dec := decodeOSC99Payload(raw, true)
	if isIconName(dec) {
		return dec
	}
	return ""
}

func isIconName(s string) bool {
	switch s {
	case "error", "warn", "warning", "info", "question", "help":
		return true
	default:
		return false
	}
}

func osc99QueryReply(id string) []byte {
	if id == "" {
		id = "0"
	}
	body := "a=focus,report:c=1:o=always,unfocused,invisible:p=title,body,close,alive,?:s=system,silent,error,warn,warning,info,question:u=0,1,2:w=1"
	return []byte("\x1b]99;i=" + id + ":p=?;" + body + "\x1b\\")
}

func osc99AliveReply(id string, ids []string) []byte {
	if id == "" {
		id = "0"
	}
	return []byte("\x1b]99;i=" + id + ":p=alive;" + strings.Join(ids, ",") + "\x1b\\")
}
