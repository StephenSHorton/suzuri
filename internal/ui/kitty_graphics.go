//go:build windows || darwin

package ui

import (
	"bytes"
	"encoding/base64"
	"image"
	"strconv"
	"strings"
	"sync"

	"github.com/charmbracelet/log"
)

// Kitty graphics protocol (APC): ESC _ G <key=value,...> ; <payload> ESC \
// Grok emits this for prompt image previews when the host brands as
// Kitty/Ghostty (TERM_PROGRAM / KITTY_WINDOW_ID).

type kittyPlace struct {
	id       uint32
	col, row int // 0-based cell top-left
	cols     int
	rows     int
	z        int
	srcX, srcY, srcW, srcH int // pixel crop; 0 w/h = full
}

type kittyTxMeta struct {
	action string // t or T
	format int
	cols   int
	rows   int
	z      int
	place  bool
}

type kittyGfxState struct {
	mu         sync.Mutex
	images     map[uint32]image.Image
	placements []kittyPlace
	// multi-chunk transmit in progress
	openID   uint32
	openBuf  []byte
	openMeta kittyTxMeta
	open     bool
}

func newKittyGfx() *kittyGfxState {
	return &kittyGfxState{
		images: make(map[uint32]image.Image),
	}
}

func (k *kittyGfxState) clear() {
	if k == nil {
		return
	}
	k.mu.Lock()
	defer k.mu.Unlock()
	k.images = make(map[uint32]image.Image)
	k.placements = nil
	k.open = false
	k.openBuf = nil
	k.openID = 0
}

func (k *kittyGfxState) snapshotPlacements() []kittyPlace {
	if k == nil {
		return nil
	}
	k.mu.Lock()
	defer k.mu.Unlock()
	if len(k.placements) == 0 {
		return nil
	}
	out := make([]kittyPlace, len(k.placements))
	copy(out, k.placements)
	return out
}

func (k *kittyGfxState) image(id uint32) image.Image {
	if k == nil {
		return nil
	}
	k.mu.Lock()
	defer k.mu.Unlock()
	return k.images[id]
}

// feedKittyAPCs strips Kitty graphics APCs, applies them after writing
// preceding VT bytes (so a=p sees the correct cursor), returns residual VT.
func feedKittyAPCs(k *kittyGfxState, data []byte, writeVT func([]byte), cursor func() (col, row int)) []byte {
	if len(data) == 0 {
		return data
	}
	if k == nil {
		k = newKittyGfx()
	}
	var out []byte
	i := 0
	for i < len(data) {
		if data[i] == 0x1b && i+2 < len(data) && data[i+1] == '_' && data[i+2] == 'G' {
			if len(out) > 0 {
				if writeVT != nil {
					writeVT(out)
				}
				out = out[:0]
			}
			j := i + 3
			for j+1 < len(data) {
				if data[j] == 0x1b && data[j+1] == '\\' {
					k.handleAPC(data[i+3:j], cursor)
					i = j + 2
					goto cont
				}
				j++
			}
			// Incomplete — keep for next read.
			out = append(out, data[i:]...)
			break
		}
		out = append(out, data[i])
		i++
	cont:
	}
	return out
}

func (k *kittyGfxState) handleAPC(body []byte, cursor func() (col, row int)) {
	semi := bytes.IndexByte(body, ';')
	var header string
	var payload []byte
	if semi < 0 {
		header = string(body)
	} else {
		header = string(body[:semi])
		payload = body[semi+1:]
	}
	kv := parseKittyKV(header)
	action := kv["a"]
	id := uint32(atoiDef(kv["i"], 0))
	more := kv["m"] == "1"

	// Continuation chunks: no a=, only m= and payload (same open stream).
	if action == "" {
		k.mu.Lock()
		if !k.open {
			k.mu.Unlock()
			return
		}
		k.openBuf = append(k.openBuf, payload...)
		done := !more
		buf := append([]byte(nil), k.openBuf...)
		meta := k.openMeta
		oid := k.openID
		if done {
			k.open = false
			k.openBuf = nil
		}
		k.mu.Unlock()
		if done {
			k.finishTransmit(oid, buf, meta, cursor)
		}
		return
	}

	switch action {
	case "t", "T":
		meta := kittyTxMeta{
			action: action,
			format: atoiDef(kv["f"], 100),
			cols:   atoiDef(kv["c"], 0),
			rows:   atoiDef(kv["r"], 0),
			z:      atoiDef(kv["z"], 1),
			place:  action == "T",
		}
		k.mu.Lock()
		k.open = true
		k.openID = id
		k.openMeta = meta
		k.openBuf = append(k.openBuf[:0], payload...)
		buf := append([]byte(nil), k.openBuf...)
		done := !more
		if done {
			k.open = false
			k.openBuf = nil
		}
		k.mu.Unlock()
		if done {
			k.finishTransmit(id, buf, meta, cursor)
		}
	case "p":
		col, row := 0, 0
		if cursor != nil {
			col, row = cursor()
		}
		pl := kittyPlace{
			id: id, col: col, row: row,
			cols: atoiDef(kv["c"], 1),
			rows: atoiDef(kv["r"], 1),
			z:    atoiDef(kv["z"], 1),
			srcX: atoiDef(kv["x"], 0),
			srcY: atoiDef(kv["y"], 0),
			srcW: atoiDef(kv["w"], 0),
			srcH: atoiDef(kv["h"], 0),
		}
		if pl.cols < 1 {
			pl.cols = 1
		}
		if pl.rows < 1 {
			pl.rows = 1
		}
		k.mu.Lock()
		filtered := k.placements[:0]
		for _, p := range k.placements {
			if p.id != id {
				filtered = append(filtered, p)
			}
		}
		k.placements = append(filtered, pl)
		k.mu.Unlock()
	case "d":
		k.mu.Lock()
		if kv["d"] == "A" || (kv["d"] == "" && id == 0) {
			k.images = make(map[uint32]image.Image)
			k.placements = nil
		} else {
			delete(k.images, id)
			filtered := k.placements[:0]
			for _, p := range k.placements {
				if p.id != id {
					filtered = append(filtered, p)
				}
			}
			k.placements = filtered
		}
		k.mu.Unlock()
	}
}

func (k *kittyGfxState) finishTransmit(id uint32, b64 []byte, meta kittyTxMeta, cursor func() (col, row int)) {
	// Strip whitespace that some hosts inject.
	b64 = bytes.Map(func(r rune) rune {
		if r == '\n' || r == '\r' || r == ' ' {
			return -1
		}
		return r
	}, b64)
	raw, err := base64.StdEncoding.DecodeString(string(b64))
	if err != nil {
		raw, err = base64.RawStdEncoding.DecodeString(string(b64))
	}
	if err != nil || len(raw) == 0 {
		log.Debug("kitty graphics b64 failed", "id", id, "err", err, "n", len(b64))
		return
	}
	ti, err := loadImageBytes("kitty.png", raw)
	if err != nil || ti == nil || ti.img == nil {
		log.Debug("kitty graphics png failed", "id", id, "err", err)
		return
	}
	k.mu.Lock()
	k.images[id] = ti.img
	k.mu.Unlock()
	log.Info("kitty graphics ready", "id", id, "bytes", len(raw),
		"w", ti.pxW, "h", ti.pxH)

	if meta.place {
		col, row := 0, 0
		if cursor != nil {
			col, row = cursor()
		}
		pl := kittyPlace{
			id: id, col: col, row: row,
			cols: meta.cols, rows: meta.rows, z: meta.z,
		}
		if pl.cols < 1 {
			pl.cols = 10
		}
		if pl.rows < 1 {
			pl.rows = 5
		}
		k.mu.Lock()
		filtered := k.placements[:0]
		for _, p := range k.placements {
			if p.id != id {
				filtered = append(filtered, p)
			}
		}
		k.placements = append(filtered, pl)
		k.mu.Unlock()
	}
}

func parseKittyKV(header string) map[string]string {
	out := make(map[string]string)
	for _, part := range strings.Split(header, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		key, val, ok := strings.Cut(part, "=")
		if !ok {
			continue
		}
		out[strings.TrimSpace(key)] = strings.TrimSpace(val)
	}
	return out
}

func atoiDef(s string, def int) int {
	if s == "" {
		return def
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return def
	}
	return n
}
