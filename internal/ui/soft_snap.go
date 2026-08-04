package ui

// Soft magnetic size snap while the user is interactively resizing.
// Targets are fractions of the monitor work area (full, ½, ⅓, ¼, ⅔, ¾).
// threshold is the magnet strength in pixels — keep it small so the snap
// "lets go easily" once the cursor pulls away.

const softSnapThresholdPx = 18

// softSnapDim returns size snapped to the nearest target fraction of full
// when within threshold, otherwise size unchanged.
func softSnapDim(size, full, threshold int) int {
	if full < 1 || threshold < 1 {
		return size
	}
	// Minimum useful window dimension — don't snap below this.
	const minDim = 240
	targets := []int{
		full,
		full / 2,
		full / 3,
		full / 4,
		(2 * full) / 3,
		(3 * full) / 4,
	}
	best := size
	bestDist := threshold + 1
	for _, t := range targets {
		if t < minDim {
			continue
		}
		d := size - t
		if d < 0 {
			d = -d
		}
		if d < bestDist {
			bestDist = d
			best = t
		}
	}
	if bestDist <= threshold {
		return best
	}
	return size
}

// softSnapSize applies softSnapDim independently to width and height.
func softSnapSize(w, h, monW, monH, threshold int) (nw, nh int) {
	nw = softSnapDim(w, monW, threshold)
	nh = softSnapDim(h, monH, threshold)
	return nw, nh
}

// softSnapRect adjusts a proposed outer-frame rect during interactive resize.
// edge is a WMSZ_* code (1=left … 8=bottomright); 0 = any / both dimensions.
// work is the monitor work area in the same coordinate space as r.
// Returns true when the rect was changed.
func softSnapRect(left, top, right, bottom *int, edge int, workL, workT, workR, workB, threshold int) bool {
	if left == nil || top == nil || right == nil || bottom == nil {
		return false
	}
	workW := workR - workL
	workH := workB - workT
	if workW < 1 || workH < 1 {
		return false
	}
	w := *right - *left
	h := *bottom - *top
	nw := softSnapDim(w, workW, threshold)
	nh := softSnapDim(h, workH, threshold)
	if nw == w && nh == h {
		return false
	}
	// WMSZ_*: left=1, right=2, top=3, topleft=4, topright=5, bottom=6, bottomleft=7, bottomright=8
	switch edge {
	case 1, 4, 7: // left edge moves
		*left = *right - nw
	case 2, 5, 8: // right edge moves
		*right = *left + nw
	default:
		// Unknown / both: prefer adjusting the right edge.
		if nw != w {
			*right = *left + nw
		}
	}
	switch edge {
	case 3, 4, 5: // top edge moves
		*top = *bottom - nh
	case 6, 7, 8: // bottom edge moves
		*bottom = *top + nh
	default:
		if nh != h {
			*bottom = *top + nh
		}
	}
	// Re-apply if only one dimension snapped under a corner edge.
	if nw != w {
		switch edge {
		case 1, 4, 7:
			*left = *right - nw
		default:
			*right = *left + nw
		}
	}
	if nh != h {
		switch edge {
		case 3, 4, 5:
			*top = *bottom - nh
		default:
			*bottom = *top + nh
		}
	}
	return true
}
