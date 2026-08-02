//go:build windows

package ui

import "github.com/lxn/win"

func rgbCOLORREF(r, g, b byte) win.COLORREF {
	return win.RGB(r, g, b)
}
