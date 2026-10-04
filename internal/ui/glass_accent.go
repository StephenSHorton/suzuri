package ui

// glassAccentColor packs DWM GradientColor as AABBGGRR. Windows reads the
// low byte as red and the next as green; AARRGGBB (or RGB/BGR swapped)
// turns a cool tint into orange.
func glassAccentColor(alpha, r, g, b byte) uint32 {
	return uint32(alpha)<<24 | uint32(b)<<16 | uint32(g)<<8 | uint32(r)
}
