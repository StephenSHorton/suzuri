package chrome

import "github.com/charmbracelet/lipgloss"

// Frame says who draws the window buttons.
// FrameNative leaves them to the OS title bar.
type Frame int

const (
	FrameNative Frame = iota
	FrameMac
	FrameWindows
)

// FrameButton is close, minimize, or zoom/maximize, in visual order.
// Mac: close, minimize, zoom on the left.
// Windows: minimize, maximize, close on the right.
type FrameButton int

const (
	FrameClose FrameButton = iota
	FrameMinimize
	FrameZoom
)

func styleFrameClose() lipgloss.Style {
	return lipgloss.NewStyle().Foreground(lipgloss.Color("#e85d5d")).Background(colBar).Padding(0, 0)
}

func styleFrameMin() lipgloss.Style {
	return lipgloss.NewStyle().Foreground(lipgloss.Color("#e6b450")).Background(colBar).Padding(0, 0)
}

func styleFrameZoom() lipgloss.Style {
	return lipgloss.NewStyle().Foreground(lipgloss.Color("#6ecf8a")).Background(colBar).Padding(0, 0)
}

func styleFrameWin() lipgloss.Style {
	return lipgloss.NewStyle().Foreground(colSoft).Background(colBar).Padding(0, 1)
}
