package ui

func blendByte(a, b byte, t float64) byte {
	if t <= 0 {
		return a
	}
	if t >= 1 {
		return b
	}
	return byte(float64(a)*(1-t) + float64(b)*t)
}

// blendRGB mixes two RGB triples by a∈[0,1].
func blendRGB(br, bg, bb, fr, fg, fb byte, a float64) (byte, byte, byte) {
	return blendByte(br, fr, a), blendByte(bg, fg, a), blendByte(bb, fb, a)
}

// rippleWaveColor maps crest parameter u∈[0,1] along:
// primary → white → primary → black.
func rippleWaveColor(u float64, pr, pg, pb, wr, wg, wb byte) (byte, byte, byte) {
	if u < 0 {
		u = 0
	}
	if u > 1 {
		u = 1
	}
	switch {
	case u < 1.0/3:
		t := u * 3
		return blendRGB(pr, pg, pb, wr, wg, wb, t)
	case u < 2.0/3:
		t := (u - 1.0/3) * 3
		return blendRGB(wr, wg, wb, pr, pg, pb, t)
	default:
		t := (u - 2.0/3) * 3
		return blendRGB(pr, pg, pb, 0, 0, 0, t)
	}
}
