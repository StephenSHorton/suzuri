//go:build darwin

package ui

import "github.com/hajimehoshi/ebiten/v2"

func (u *macUI) pageState() (*[]*page, *int, *int) {
	return &u.pages, &u.active, &u.nextTabID
}

func (u *macUI) afterPageMove() {
	u.selecting = false
	if t := u.activeTab(); t != nil {
		t.sel.clear()
		ebiten.SetWindowTitle(hostWindowTitle(t.displayTitle()))
	}
	u.syncChrome()
	u.markChromeDirty()
	u.applyClientSize(u.width, u.height)
	u.postLayoutSettle()
	u.markShellDirty()
	u.syncTermFocus()
	u.publishBridgeSnapshot()
}
