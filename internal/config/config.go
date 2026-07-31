package config

// CursorStyle is how the caret is drawn once the real cell grid exists.
type CursorStyle int

const (
	// CursorBlock is a solid block covering the cell (default — matches
	// the personal Warp-fork preference).
	CursorBlock CursorStyle = iota
	CursorUnderline
	CursorBar
)

// Config holds product defaults. Persistence comes later.
type Config struct {
	Cursor   CursorStyle
	// FontFace is the preferred monospaced face (Cascadia Mono when installed).
	FontFace string
	// FontSizePx is logical cell height in pixels (negative LOGFONT height).
	FontSizePx int
}

// Default returns shipping defaults.
func Default() Config {
	return Config{
		Cursor:     CursorBlock,
		FontFace:   "Cascadia Mono",
		FontSizePx: 16,
	}
}
