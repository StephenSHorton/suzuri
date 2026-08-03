//go:build windows || darwin

package ui

import (
	"bytes"
	"encoding/base64"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"

	"github.com/charmbracelet/log"
	"golang.org/x/image/draw"
)

const (
	maxTabImages     = 8
	maxImageFileBytes = 12 << 20 // 12 MiB decode cap
	maxImageEdgePx   = 2048      // downscale longer edge before paint cache
)

// tabImage is a decoded image shown in the shell viewport (not a VT cell).
type tabImage struct {
	key    string // path or synthetic name for OSC payloads
	path   string // filesystem path when known (for open-on-click)
	img    image.Image
	pxW    int
	pxH    int
	// viewRow is the alt-screen VT row to anchor under (conversation inline).
	// -1 means unset / use bottom stack.
	viewRow int
	// paint cache filled by platform painter
	ready bool
}

// imageStore holds recent images for one tab (newest last).
type imageStore struct {
	mu   sync.Mutex
	list []*tabImage
}

func (s *imageStore) add(im *tabImage) {
	s.addAt(im, -1)
}

// addAt stores im, optionally anchoring to a VT row for alt-screen inline paint.
// Dedupes by canonical path so mangled vs fixed refs don't produce two draws.
func (s *imageStore) addAt(im *tabImage, viewRow int) {
	if s == nil || im == nil || im.img == nil {
		return
	}
	canon := imageCanonKey(im)
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, old := range s.list {
		if imageCanonKey(old) == canon || old.key == im.key {
			if viewRow >= 0 {
				// Prefer a real conversation row over "unset".
				old.viewRow = viewRow
			}
			old.img = im.img
			old.pxW, old.pxH = im.pxW, im.pxH
			old.path = im.path
			old.key = im.key
			old.ready = false
			s.list = append(append(s.list[:i], s.list[i+1:]...), old)
			return
		}
	}
	if viewRow >= 0 {
		im.viewRow = viewRow
	} else {
		im.viewRow = -1
	}
	s.list = append(s.list, im)
	if len(s.list) > maxTabImages {
		s.list = s.list[len(s.list)-maxTabImages:]
	}
	log.Info("inline image ready", "key", im.key, "w", im.pxW, "h", im.pxH, "path", im.path, "row", im.viewRow)
}

func imageCanonKey(im *tabImage) string {
	if im == nil {
		return ""
	}
	p := im.path
	if p == "" {
		p = im.key
	}
	return strings.ToLower(filepath.Clean(p))
}

func (s *imageStore) snapshot() []*tabImage {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]*tabImage, len(s.list))
	copy(out, s.list)
	return out
}

func (s *imageStore) clear() {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.list = nil
	s.mu.Unlock()
}

// --- Extraction from PTY / text ------------------------------------------------

// reImagePath matches absolute or common relative image paths in text.
// Note: Grok may print mangled paths with a second drive letter after
// `.grok\sessions\` — still matched by the Windows absolute-path alternative.
// Unix absolute paths (/Users/…/file.png) are included for macOS parity.
var reImagePath = regexp.MustCompile(`(?i)(?:` +
	`[a-z]:[\\/][^\s"'<>|*?]+\.(?:png|jpe?g|gif|webp)|` + // Windows abs
	`/[^\s"'<>|*?]+\.(?:png|jpe?g|gif|webp)|` + // Unix abs
	`(?:images|\.grok)[\\/][^\s"'<>|*?]+\.(?:png|jpe?g|gif|webp)|` +
	`[^\s"'<>|*?]+[\\/]images[\\/][^\s"'<>|*?]+\.(?:png|jpe?g|gif|webp)` +
`)`)

// reSessionImage captures …\<uuid>\images\<file> (Grok session layout).
var reSessionImage = regexp.MustCompile(`(?i)([0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12})[\\/]+images[\\/]+([^\\/\s"'<>|*?]+)`)

// reOpenImage matches Grok-style "[Open Image]" labels.
var reOpenImage = regexp.MustCompile(`(?i)\[?\s*open\s+image\s*\]?`)

// stripAndTakeImages removes image OSC sequences and returns local paths / decoded blobs.
// Also scans remaining text for filesystem image paths (Grok Imagine, tools, etc.).
func stripAndTakeImages(data []byte) (clean []byte, paths []string, blobs []imageBlob) {
	if len(data) == 0 {
		return data, nil, nil
	}
	out := make([]byte, 0, len(data))
	i := 0
	for i < len(data) {
		// ESC ]
		if data[i] == 0x1b && i+1 < len(data) && data[i+1] == ']' {
			j := i + 2
			for j < len(data) {
				if data[j] == 0x07 { // BEL
					payload := data[i+2 : j]
					if p, blob, ok := parseImageOSC(payload); ok {
						if p != "" {
							paths = append(paths, p)
						}
						if blob.data != nil {
							blobs = append(blobs, blob)
						}
						i = j + 1
						goto next
					}
					// Not an image OSC â€” keep for VT (title, etc.)
					out = append(out, data[i:j+1]...)
					i = j + 1
					goto next
				}
				if data[j] == 0x1b && j+1 < len(data) && data[j+1] == '\\' { // ST
					payload := data[i+2 : j]
					if p, blob, ok := parseImageOSC(payload); ok {
						if p != "" {
							paths = append(paths, p)
						}
						if blob.data != nil {
							blobs = append(blobs, blob)
						}
						i = j + 2
						goto next
					}
					out = append(out, data[i:j+2]...)
					i = j + 2
					goto next
				}
				j++
			}
			// Incomplete OSC â€” keep rest
			out = append(out, data[i:]...)
			break
		}
		out = append(out, data[i])
		i++
	next:
	}
	// Path heuristics on cleaned text (and original, in case OSC kept noise).
	paths = append(paths, findImagePathsInText(string(out))...)
	paths = uniqueStrings(paths)
	return out, paths, blobs
}

type imageBlob struct {
	name string
	data []byte
}

// parseImageOSC handles:
//
//	1337;File=...:base64   (iTerm2 inline)
//	7879;image=<path>      (suzuri host)
func parseImageOSC(payload []byte) (path string, blob imageBlob, ok bool) {
	// 7879;image=C:\path\a.png
	if bytes.HasPrefix(payload, []byte("7879;image=")) {
		p := string(payload[len("7879;image="):])
		p = strings.TrimSpace(p)
		if p != "" {
			return p, imageBlob{}, true
		}
		return "", imageBlob{}, false
	}
	// 1337;File=args:base64
	if !bytes.HasPrefix(payload, []byte("1337;File=")) {
		return "", imageBlob{}, false
	}
	rest := payload[len("1337;File="):]
	// Split args from payload at last ':' before base64... protocol uses
	// File=[optional arguments]:base-64
	// Arguments are key=value; separated by ';'. Find first ':' that starts b64.
	colon := -1
	for i := 0; i < len(rest); i++ {
		if rest[i] == ':' {
			// Prefer the colon after args; if name= is base64 of filename it uses
			// name=xxx; â€” content is after the last colon that has only b64 after.
			colon = i
		}
	}
	if colon < 0 || colon+1 >= len(rest) {
		return "", imageBlob{}, false
	}
	args := string(rest[:colon])
	b64 := string(rest[colon+1:])
	// Only treat as inline image when inline=1 or no explicit inline=0.
	inline := true
	name := "inline"
	for _, part := range strings.Split(args, ";") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		k, v, _ := strings.Cut(part, "=")
		k = strings.ToLower(strings.TrimSpace(k))
		v = strings.TrimSpace(v)
		switch k {
		case "inline":
			inline = v == "1" || strings.EqualFold(v, "true")
		case "name":
			if dec, err := base64.StdEncoding.DecodeString(v); err == nil && len(dec) > 0 {
				name = string(dec)
			} else if v != "" {
				name = v
			}
		}
	}
	if !inline {
		return "", imageBlob{}, false
	}
	raw, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		// Try raw URL encoding / no padding
		raw, err = base64.RawStdEncoding.DecodeString(b64)
	}
	if err != nil || len(raw) == 0 {
		return "", imageBlob{}, false
	}
	if len(raw) > maxImageFileBytes {
		log.Warn("inline image too large", "bytes", len(raw))
		return "", imageBlob{}, false
	}
	return "", imageBlob{name: name, data: raw}, true
}

func findImagePathsInText(s string) []string {
	if s == "" {
		return nil
	}
	// Fast reject
	low := strings.ToLower(s)
	if !strings.Contains(low, ".png") && !strings.Contains(low, ".jpg") &&
		!strings.Contains(low, ".jpeg") && !strings.Contains(low, ".gif") &&
		!strings.Contains(low, ".webp") {
		return nil
	}
	var out []string
	for _, m := range reImagePath.FindAllString(s, 12) {
		m = strings.Trim(m, `"'()[]`)
		if m != "" {
			out = append(out, m)
		}
	}
	return out
}

func uniqueStrings(in []string) []string {
	seen := map[string]struct{}{}
	var out []string
	for _, s := range in {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		key := strings.ToLower(filepath.Clean(s))
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, s)
	}
	return out
}

// resolveImagePath turns a ref into an existing file path (cwd, then ~/.grok).
func resolveImagePath(cwd, ref string) string {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return ""
	}
	// file:// URI
	if strings.HasPrefix(strings.ToLower(ref), "file:") {
		if p, ok := fileURIPath(ref); ok {
			ref = p
		}
	}
	// Grok TUI often shows a mangled path with a second "C:\" after sessions\.
	if fixed := fixGrokSessionDisplayPath(ref); fixed != ref {
		ref = fixed
	}
	try := []string{ref, filepath.Clean(ref)}
	if cwd != "" && !filepath.IsAbs(ref) {
		try = append(try, filepath.Join(cwd, ref))
	}
	if home, err := os.UserHomeDir(); err == nil {
		try = append(try, filepath.Join(home, ".grok", ref))
	}
	for _, p := range try {
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p
		}
	}
	// Session UUID + images/file — look up under ~/.grok/sessions (encoded workspace dirs).
	if home, err := os.UserHomeDir(); err == nil {
		if m := reSessionImage.FindStringSubmatch(ref); len(m) == 3 {
			if hit := findSessionImage(filepath.Join(home, ".grok", "sessions"), m[1], m[2]); hit != "" {
				return hit
			}
		}
		base := filepath.Base(ref)
		if isImageFilename(base) {
			if hit := findUnder(filepath.Join(home, ".grok", "sessions"), base, 5); hit != "" {
				return hit
			}
		}
	}
	return ""
}

// fixGrokSessionDisplayPath maps Grok's on-screen path:
//
//	C:\Users\…\.grok\sessions\C:\Users\…\projects\suzuri\<uuid>\images\1.jpg
//
// to the real folder layout (workspace encoded as C%3A%5CUsers%5C…):
//
//	C:\Users\…\.grok\sessions\C%3A%5CUsers%5C…\projects%5Csuzuri\<uuid>\images\1.jpg
func fixGrokSessionDisplayPath(p string) string {
	p = strings.ReplaceAll(p, `/`, `\`)
	low := strings.ToLower(p)
	const marker = `.grok\sessions\`
	i := strings.Index(low, marker)
	if i < 0 {
		return p
	}
	prefix := p[:i+len(marker)]
	rest := p[i+len(marker):]
	m := reSessionImage.FindStringSubmatch(rest)
	if len(m) != 3 {
		return p
	}
	sid, file := m[1], m[2]
	j := strings.Index(strings.ToLower(rest), strings.ToLower(sid))
	if j < 0 {
		return p
	}
	ws := strings.TrimRight(rest[:j], `\`)
	if ws == "" {
		return p
	}
	// Encode like Grok: : → %3A, \ → %5C
	enc := strings.ReplaceAll(ws, `\`, `%5C`)
	enc = strings.ReplaceAll(enc, `:`, `%3A`)
	return prefix + enc + `\` + sid + `\images\` + file
}

// findSessionImage looks for sessions/<any>/<uuid>/images/<base>.
func findSessionImage(sessionsRoot, uuid, base string) string {
	// Prefer direct encoded children: sessions/<enc>/<uuid>/images/<base>
	entries, err := os.ReadDir(sessionsRoot)
	if err != nil {
		return ""
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		cand := filepath.Join(sessionsRoot, e.Name(), uuid, "images", base)
		if st, err := os.Stat(cand); err == nil && !st.IsDir() {
			return cand
		}
	}
	return ""
}

func isImageFilename(name string) bool {
	ext := strings.ToLower(filepath.Ext(name))
	switch ext {
	case ".png", ".jpg", ".jpeg", ".gif", ".webp":
		return true
	default:
		return false
	}
}

func findUnder(root, base string, maxDepth int) string {
	var hit string
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || hit != "" {
			return filepath.SkipAll
		}
		if d.IsDir() {
			rel, _ := filepath.Rel(root, path)
			if rel != "." && strings.Count(rel, string(os.PathSeparator)) >= maxDepth {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.EqualFold(d.Name(), base) {
			hit = path
			return filepath.SkipAll
		}
		return nil
	})
	return hit
}

// loadImageFile decodes and optionally downscales.
func loadImageFile(path string) (*tabImage, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	// Cap read
	stat, _ := f.Stat()
	if stat != nil && stat.Size() > maxImageFileBytes {
		return nil, os.ErrInvalid
	}
	img, _, err := image.Decode(f)
	if err != nil {
		return nil, err
	}
	return newTabImage(path, path, img), nil
}

func loadImageBytes(name string, data []byte) (*tabImage, error) {
	if len(data) == 0 || len(data) > maxImageFileBytes {
		return nil, os.ErrInvalid
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	key := name
	if key == "" {
		key = "blob"
	}
	return newTabImage(key, "", img), nil
}

func newTabImage(key, path string, img image.Image) *tabImage {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	if w < 1 || h < 1 {
		return nil
	}
	// Downscale long edge for memory / blit cost.
	if w > maxImageEdgePx || h > maxImageEdgePx {
		scale := float64(maxImageEdgePx) / float64(w)
		if float64(h)*scale > float64(maxImageEdgePx) {
			scale = float64(maxImageEdgePx) / float64(h)
		}
		nw := int(float64(w) * scale)
		nh := int(float64(h) * scale)
		if nw < 1 {
			nw = 1
		}
		if nh < 1 {
			nh = 1
		}
		dst := image.NewRGBA(image.Rect(0, 0, nw, nh))
		draw.CatmullRom.Scale(dst, dst.Bounds(), img, b, draw.Over, nil)
		img = dst
		w, h = nw, nh
	}
	return &tabImage{key: key, path: path, img: img, pxW: w, pxH: h}
}

// fitPreferNative returns src size when it fits in maxW×maxH; otherwise scales
// down uniformly (never upscales). Used for layout span and paint.
func fitPreferNative(srcW, srcH, maxW, maxH int) (int, int) {
	if srcW < 1 || srcH < 1 || maxW < 1 || maxH < 1 {
		return 0, 0
	}
	if srcW <= maxW && srcH <= maxH {
		return srcW, srcH
	}
	sw, sh := float64(srcW), float64(srcH)
	scale := float64(maxW) / sw
	if sh*scale > float64(maxH) {
		scale = float64(maxH) / sh
	}
	if scale > 1 {
		scale = 1
	}
	dw := int(sw * scale)
	dh := int(sh * scale)
	if dw < 1 {
		dw = 1
	}
	if dh < 1 {
		dh = 1
	}
	return dw, dh
}
