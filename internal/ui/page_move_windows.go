//go:build windows

package ui

func (u *winUI) pageState() (*[]*page, *int, *int) {
	return &u.pages, &u.active, &u.nextTabID
}

func (u *winUI) afterPageMove() {
	u.selecting = false
	if t := u.activeTab(); t != nil {
		t.sel.clear()
		setWindowTitle(u.hwnd, "suzuri — "+t.displayTitle())
	}
	u.syncChrome()
	u.markChromeDirty()
	u.postLayoutSettle()
	u.markShellDirty()
	u.syncTermFocus()
	if u.hwnd != 0 {
		u.requestPaint()
	}
	u.publishBridgeSnapshot()
}

func (u *winUI) activeLayout() []paneGeom {
	return u.computeActiveLayout()
}
