package ui

import (
	"bytes"
	"strconv"
	"strings"

	"github.com/hinshun/vt10x"

	"github.com/StephenSHorton/suzuri/internal/chrome"
)

// sgrState rewrites CSI SGR so hinshun/vt10x can see Grok's theme:
//   - colon subparams (38:2:r:g:b) → semicolons
//   - SGR 2 (faint/dim) → actually dimmer FG (vt10x ignores 2)
type sgrState struct {
	faint      bool
	hasFG      bool
	fr, fg, fb int
}

func (s *sgrState) rewrite(in []byte) []byte {
	if s == nil || len(in) == 0 || !bytes.Contains(in, []byte{0x1b, '['}) {
		return in
	}
	out := make([]byte, 0, len(in)+32)
	i := 0
	for i < len(in) {
		if in[i] != 0x1b || i+1 >= len(in) || in[i+1] != '[' {
			out = append(out, in[i])
			i++
			continue
		}
		j := i + 2
		for j < len(in) {
			b := in[j]
			if b >= 0x40 && b <= 0x7e {
				break
			}
			j++
		}
		if j >= len(in) {
			out = append(out, in[i:]...)
			break
		}
		if in[j] != 'm' {
			out = append(out, in[i:j+1]...)
			i = j + 1
			continue
		}
		params := string(in[i+2 : j])
		out = append(out, s.rewriteSGR(params)...)
		i = j + 1
	}
	return out
}

func (s *sgrState) rewriteSGR(params string) []byte {
	params = strings.ReplaceAll(params, ":", ";")
	if params == "" {
		s.reset()
		return []byte("\x1b[m")
	}
	parts := strings.Split(params, ";")
	nums := make([]int, 0, len(parts)+4)
	ok := true
	for _, p := range parts {
		if p == "" {
			nums = append(nums, 0)
			continue
		}
		n, err := strconv.Atoi(p)
		if err != nil {
			ok = false
			break
		}
		nums = append(nums, n)
	}
	if !ok {
		return []byte("\x1b[" + params + "m")
	}

	out := make([]int, 0, len(nums)+6)
	i := 0
	for i < len(nums) {
		a := nums[i]
		switch a {
		case 0:
			s.reset()
			out = append(out, 0)
			i++
		case 2:
			s.faint = true
			i++
			if s.hasFG {
				r, g, b := dimRGB(s.fr, s.fg, s.fb)
				out = append(out, 38, 2, r, g, b)
			}
		case 22:
			s.faint = false
			i++
			if s.hasFG {
				out = append(out, 38, 2, s.fr, s.fg, s.fb)
			} else {
				out = append(out, 39)
			}
		case 39:
			s.hasFG = false
			s.fr, s.fg, s.fb = 0, 0, 0
			if s.faint {
				out = append(out, dimDefaultFG()...)
			} else {
				out = append(out, 39)
			}
			i++
		case 38:
			r, g, b, n, parsed := takeSGRColor(nums, i)
			if !parsed {
				out = append(out, a)
				i++
				break
			}
			s.hasFG = true
			s.fr, s.fg, s.fb = r, g, b
			if s.faint {
				r, g, b = dimRGB(r, g, b)
			}
			out = append(out, 38, 2, r, g, b)
			i = n
		case 48:
			// Combined CSI is 38;2;r;g;b;48;2;r;g;b — the "2" after 48 is
			// truecolor, NOT SGR faint. Eating it flattened Grok's bands.
			r, g, b, n, parsed := takeSGRColor(nums, i)
			if !parsed {
				out = append(out, a)
				i++
				break
			}
			out = append(out, 48, 2, r, g, b)
			i = n
		case 49:
			out = append(out, 49)
			i++
		default:
			if a >= 30 && a <= 37 {
				r, g, b := ansi16RGB(a - 30)
				s.hasFG = true
				s.fr, s.fg, s.fb = r, g, b
				if s.faint {
					r, g, b = dimRGB(r, g, b)
					out = append(out, 38, 2, r, g, b)
				} else {
					out = append(out, a)
				}
			} else if a >= 90 && a <= 97 {
				r, g, b := ansi16RGB(a - 90 + 8)
				s.hasFG = true
				s.fr, s.fg, s.fb = r, g, b
				if s.faint {
					r, g, b = dimRGB(r, g, b)
					out = append(out, 38, 2, r, g, b)
				} else {
					out = append(out, a)
				}
			} else {
				out = append(out, a)
			}
			i++
		}
	}
	if s.faint && !s.hasFG {
		// Dim default ink so secondary/placeholder isn't identical to body text.
		out = append(out, dimDefaultFG()...)
	}
	return encodeSGR(out)
}

func (s *sgrState) reset() {
	s.faint = false
	s.hasFG = false
	s.fr, s.fg, s.fb = 0, 0, 0
}

func takeSGRColor(nums []int, i int) (r, g, b int, next int, ok bool) {
	// 38 ; 5 ; n   or  38 ; 2 ; r ; g ; b
	if i+2 < len(nums) && nums[i+1] == 5 {
		idx := nums[i+2]
		r, g, b = colorToRGBInts(idx)
		return r, g, b, i + 3, true
	}
	if i+1 < len(nums) && nums[i+1] == 2 {
		// ITU 38:2::R:G:B → 38;2;;R;G;B (empty colorspace is 0).
		if i+5 < len(nums) && nums[i+2] == 0 {
			return nums[i+3], nums[i+4], nums[i+5], i + 6, true
		}
		if i+4 < len(nums) {
			return nums[i+2], nums[i+3], nums[i+4], i + 5, true
		}
	}
	return 0, 0, 0, i + 1, false
}

func colorToRGBInts(idx int) (r, g, b int) {
	if idx < 0 {
		idx = 0
	}
	rb, gb, bb := colorToRGB(vt10x.Color(uint32(idx)), false)
	return int(rb), int(gb), int(bb)
}

func ansi16RGB(idx int) (r, g, b int) {
	if idx < 0 {
		idx = 0
	}
	if idx > 15 {
		idx = 15
	}
	rb, gb, bb := colorToRGB(vt10x.Color(uint32(idx)), false)
	return int(rb), int(gb), int(bb)
}

func dimRGB(r, g, b int) (int, int, int) {
	return dim1(r), dim1(g), dim1(b)
}

func dim1(v int) int {
	v = v * 55 / 100
	if v < 12 {
		return 12
	}
	if v > 255 {
		return 255
	}
	return v
}

func dimDefaultFG() []int {
	// 55% of body ink — chrome.Soft on high_contrast is #e0e0e0, useless.
	r, g, b := dimRGB(int(chrome.TextR), int(chrome.TextG), int(chrome.TextB))
	if r+g+b < 80 {
		return []int{38, 2, 140, 140, 140}
	}
	return []int{38, 2, r, g, b}
}

func encodeSGR(nums []int) []byte {
	if len(nums) == 0 {
		return []byte("\x1b[m")
	}
	var b strings.Builder
	b.WriteString("\x1b[")
	for i, n := range nums {
		if i > 0 {
			b.WriteByte(';')
		}
		b.WriteString(strconv.Itoa(n))
	}
	b.WriteByte('m')
	return []byte(b.String())
}
