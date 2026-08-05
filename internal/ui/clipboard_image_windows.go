//go:build windows

package ui

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"github.com/lxn/win"
	"golang.org/x/sys/windows"
)

// Standard clipboard formats (winuser.h).
const (
	cfBitmap = 2
	cfDIB    = 8
	cfHDROP  = 15
	cfDIBV5  = 17
)

var (
	modUser32Clip       = windows.NewLazySystemDLL("user32.dll")
	modKernel32Clip     = windows.NewLazySystemDLL("kernel32.dll")
	modShell32Clip      = windows.NewLazySystemDLL("shell32.dll")
	procRegisterClipFmt = modUser32Clip.NewProc("RegisterClipboardFormatW")
	procIsClipboardFmt  = modUser32Clip.NewProc("IsClipboardFormatAvailable")
	procGlobalSizeClip  = modKernel32Clip.NewProc("GlobalSize")
	procDragQueryFileW  = modShell32Clip.NewProc("DragQueryFileW")
)

// readClipboardImageFile writes a clipboard raster (if any) to a temp PNG and
// returns its path. Empty / non-image clipboards return "".
//
// Order: PNG registered format → CF_DIB / CF_DIBV5 → CF_HDROP single image path.
func readClipboardImageFile() (path string, err error) {
	dir := filepath.Join(os.TempDir(), "suzuri-paste")
	if mkErr := os.MkdirAll(dir, 0o700); mkErr != nil {
		return "", mkErr
	}
	name := fmt.Sprintf("clip-%d.png", time.Now().UnixNano())
	out := filepath.Join(dir, name)

	if !win.OpenClipboard(0) {
		return "", windows.GetLastError()
	}
	defer win.CloseClipboard()

	// 1) PNG clipboard format (Chrome, Edge, many apps).
	if pngFmt := registerClipboardFormat("PNG"); pngFmt != 0 && isClipboardFormatAvailable(pngFmt) {
		if data := getClipboardBytes(uint(pngFmt)); len(data) >= 32 && bytes.HasPrefix(data, []byte{0x89, 'P', 'N', 'G'}) {
			if len(data) > maxImageFileBytes {
				return "", fmt.Errorf("clipboard png too large: %d", len(data))
			}
			// Re-encode via load path so huge screenshots are downscaled before
			// Grok (and later Kitty preview paint) sees a multi-megapixel file.
			if ti, err := loadImageBytes("clip.png", data); err == nil && ti != nil && ti.img != nil {
				if werr := writePNGFile(out, ti.img); werr != nil {
					return "", werr
				}
				return out, nil
			}
			if werr := os.WriteFile(out, data, 0o600); werr != nil {
				return "", werr
			}
			return out, nil
		}
	}

	// 2) CF_DIB / CF_DIBV5 → encode PNG.
	for _, fmtID := range []uint{cfDIBV5, cfDIB} {
		if !isClipboardFormatAvailable(fmtID) {
			continue
		}
		data := getClipboardBytes(fmtID)
		if len(data) < 40 {
			continue
		}
		img, decErr := decodeDIB(data)
		if decErr != nil || img == nil {
			continue
		}
		if werr := writePNGFile(out, img); werr != nil {
			return "", werr
		}
		return out, nil
	}

	// 3) CF_HDROP: single dropped/copied file that looks like an image.
	if isClipboardFormatAvailable(cfHDROP) {
		if p := firstHDROPImagePath(); p != "" {
			// Prefer a temp PNG copy so the path stays valid if the source moves.
			if isPNGPath(p) {
				if b, rerr := os.ReadFile(p); rerr == nil && len(b) >= 32 && bytes.HasPrefix(b, []byte{0x89, 'P', 'N', 'G'}) {
					if werr := os.WriteFile(out, b, 0o600); werr != nil {
						return p, nil // still usable original path
					}
					return out, nil
				}
			}
			// Decode other formats via Go image stack (jpeg/gif/png registered).
			if f, oerr := os.Open(p); oerr == nil {
				img, _, derr := image.Decode(f)
				_ = f.Close()
				if derr == nil && img != nil {
					if werr := writePNGFile(out, img); werr == nil {
						return out, nil
					}
				}
			}
			// Fall back to original path (Grok may still open it).
			if looksLikeImagePath(p) {
				return p, nil
			}
		}
	}

	return "", nil
}

func writePNGFile(path string, img image.Image) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return png.Encode(f, img)
}

func registerClipboardFormat(name string) uint {
	p, err := syscall.UTF16PtrFromString(name)
	if err != nil {
		return 0
	}
	r, _, _ := procRegisterClipFmt.Call(uintptr(unsafe.Pointer(p)))
	return uint(r)
}

func isClipboardFormatAvailable(fmt uint) bool {
	r, _, _ := procIsClipboardFmt.Call(uintptr(fmt))
	return r != 0
}

func getClipboardBytes(fmt uint) []byte {
	h := win.GetClipboardData(uint32(fmt))
	if h == 0 {
		return nil
	}
	ptr := win.GlobalLock(win.HGLOBAL(uintptr(h)))
	if ptr == nil {
		return nil
	}
	defer win.GlobalUnlock(win.HGLOBAL(uintptr(h)))
	sz, _, _ := procGlobalSizeClip.Call(uintptr(h))
	if sz == 0 || sz > 64<<20 {
		return nil
	}
	src := unsafe.Slice((*byte)(ptr), int(sz))
	out := make([]byte, len(src))
	copy(out, src)
	return out
}

// decodeDIB parses a CF_DIB / CF_DIBV5 payload (BITMAPINFOHEADER + pixels).
func decodeDIB(data []byte) (image.Image, error) {
	if len(data) < 40 {
		return nil, fmt.Errorf("dib too short")
	}
	hdrSize := binary.LittleEndian.Uint32(data[0:4])
	if hdrSize < 40 || int(hdrSize) > len(data) {
		return nil, fmt.Errorf("bad biSize %d", hdrSize)
	}
	width := int(int32(binary.LittleEndian.Uint32(data[4:8])))
	height := int(int32(binary.LittleEndian.Uint32(data[8:12])))
	planes := binary.LittleEndian.Uint16(data[12:14])
	bitCount := binary.LittleEndian.Uint16(data[14:16])
	compression := binary.LittleEndian.Uint32(data[16:20])
	if planes != 1 || width == 0 || height == 0 {
		return nil, fmt.Errorf("unsupported dib geometry")
	}
	// Top-down DIB has negative height.
	topDown := height < 0
	if topDown {
		height = -height
	}
	if width < 1 || height < 1 || width > 16384 || height > 16384 {
		return nil, fmt.Errorf("dib size out of range")
	}
	// BI_RGB = 0, BI_BITFIELDS = 3
	if compression != 0 && compression != 3 {
		return nil, fmt.Errorf("unsupported compression %d", compression)
	}
	if bitCount != 24 && bitCount != 32 {
		return nil, fmt.Errorf("unsupported bitCount %d", bitCount)
	}
	pixOff := int(hdrSize)
	if compression == 3 {
		// Color masks after header for BITMAPINFOHEADER (not V5 which embeds them).
		if hdrSize == 40 {
			pixOff += 12 // 3 DWORD masks
		}
	}
	// Optional color table for ≤8bpp — we only handle 24/32 so skip.
	if pixOff >= len(data) {
		return nil, fmt.Errorf("no pixel data")
	}
	pixels := data[pixOff:]
	rowBytes := ((width*int(bitCount) + 31) / 32) * 4
	need := rowBytes * height
	if len(pixels) < need {
		// Some sources omit padding at the end; require at least full rows we can read.
		if len(pixels) < rowBytes {
			return nil, fmt.Errorf("pixel buffer short")
		}
		height = len(pixels) / rowBytes
		if height < 1 {
			return nil, fmt.Errorf("pixel buffer short")
		}
	}
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		srcY := y
		if !topDown {
			srcY = height - 1 - y
		}
		row := pixels[srcY*rowBytes : srcY*rowBytes+rowBytes]
		for x := 0; x < width; x++ {
			var r, g, b, a byte
			switch bitCount {
			case 24:
				i := x * 3
				if i+2 >= len(row) {
					break
				}
				b, g, r = row[i], row[i+1], row[i+2]
				a = 255
			case 32:
				i := x * 4
				if i+3 >= len(row) {
					break
				}
				b, g, r, a = row[i], row[i+1], row[i+2], row[i+3]
				if a == 0 {
					// Many DIB sources store opaque RGB with a=0; treat as opaque.
					a = 255
				}
			}
			off := img.PixOffset(x, y)
			img.Pix[off+0] = r
			img.Pix[off+1] = g
			img.Pix[off+2] = b
			img.Pix[off+3] = a
		}
	}
	return img, nil
}

func firstHDROPImagePath() string {
	h := win.GetClipboardData(cfHDROP)
	if h == 0 {
		return ""
	}
	// DragQueryFileW(hDrop, 0xFFFFFFFF, nil, 0) → count
	count, _, _ := procDragQueryFileW.Call(uintptr(h), 0xFFFFFFFF, 0, 0)
	if count != 1 {
		return "" // only single-file drops
	}
	n, _, _ := procDragQueryFileW.Call(uintptr(h), 0, 0, 0)
	if n == 0 || n > 4096 {
		return ""
	}
	buf := make([]uint16, n+1)
	r, _, _ := procDragQueryFileW.Call(uintptr(h), 0, uintptr(unsafe.Pointer(&buf[0])), uintptr(n+1))
	if r == 0 {
		return ""
	}
	return syscall.UTF16ToString(buf)
}

func isPNGPath(p string) bool {
	return strings.EqualFold(filepath.Ext(p), ".png")
}

func looksLikeImagePath(p string) bool {
	switch strings.ToLower(filepath.Ext(p)) {
	case ".png", ".jpg", ".jpeg", ".gif", ".bmp", ".webp":
		return true
	default:
		return false
	}
}

