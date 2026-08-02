//go:build windows || darwin

package ui

import (
	"strings"

	"github.com/hinshun/vt10x"
)

// screenImageHit is an image path found on the live VT grid at a row.
type screenImageHit struct {
	ref string
	row int // 0-based VT row (path / open-image line)
}

// collectScreenImagePaths scans the live VT grid for image path strings.
func collectScreenImagePaths(term vt10x.Terminal) []string {
	hits := collectScreenImageHits(term)
	var out []string
	for _, h := range hits {
		out = append(out, h.ref)
	}
	return uniqueStrings(out)
}

// collectScreenImageHits returns paths with the screen row they appear on
// (so alt-screen paint can place images inline in the conversation).
func collectScreenImageHits(term vt10x.Terminal) []screenImageHit {
	if term == nil {
		return nil
	}
	cols, rows := term.Size()
	if cols < 1 || rows < 1 {
		return nil
	}
	rowText := make([]string, rows)
	var full strings.Builder
	for y := 0; y < rows; y++ {
		var b strings.Builder
		for x := 0; x < cols; x++ {
			ch := displayRune(term.Cell(x, y).Char)
			if ch == 0 {
				ch = ' '
			}
			b.WriteRune(ch)
		}
		rowText[y] = b.String()
		full.WriteString(rowText[y])
		full.WriteByte('\n')
	}

	var hits []screenImageHit
	seen := map[string]int{} // ref lower -> first row

	// Per-row and two-row windows (long paths wrap).
	for y := 0; y < rows; y++ {
		chunk := rowText[y]
		if y+1 < rows {
			chunk = chunk + rowText[y+1]
		}
		for _, ref := range findImagePathsInText(chunk) {
			key := strings.ToLower(ref)
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = y
			hits = append(hits, screenImageHit{ref: ref, row: y})
		}
	}

	// "[Open Image]" with no path on that line: use nearby path rows.
	for y := 0; y < rows; y++ {
		if !reOpenImage.MatchString(rowText[y]) {
			continue
		}
		// Prefer path on same line or adjacent.
		for _, dy := range []int{0, -1, 1, -2, 2} {
			yy := y + dy
			if yy < 0 || yy >= rows {
				continue
			}
			for _, ref := range findImagePathsInText(rowText[yy]) {
				key := strings.ToLower(ref)
				if prev, ok := seen[key]; ok {
					// Keep the higher (smaller row) of path vs open-image for placement.
					if y < prev {
						seen[key] = y
						for i := range hits {
							if strings.EqualFold(hits[i].ref, ref) {
								hits[i].row = y
							}
						}
					}
					continue
				}
				seen[key] = y
				hits = append(hits, screenImageHit{ref: ref, row: y})
			}
		}
	}

	_ = full
	return hits
}
