#import <AppKit/AppKit.h>
#include <string.h>

static int gTitleH;
static int gBlock[64 * 4];
static int gBlockN;
static id gTitleMonitor;

static BOOL titleBlocked(NSWindow *w, NSPoint winPt) {
	CGFloat H = w.contentView.bounds.size.height;
	CGFloat yTop = H - winPt.y;
	CGFloat x = winPt.x;
	if (yTop < 0 || yTop >= gTitleH) return YES;
	for (int i = 0; i < gBlockN; i++) {
		int bx = gBlock[i * 4 + 0];
		int by = gBlock[i * 4 + 1];
		int bw = gBlock[i * 4 + 2];
		int bh = gBlock[i * 4 + 3];
		if (x >= bx && x < bx + bw && yTop >= by && yTop < by + bh) return YES;
	}
	return NO;
}

static void clipView(NSView *v, CGFloat radius) {
	if (v == nil) return;
	v.wantsLayer = YES;
	v.layer.cornerRadius = radius;
	v.layer.masksToBounds = YES;
	for (NSView *sub in v.subviews) {
		clipView(sub, radius);
	}
}

void suzuri_round_main(void) {
	NSWindow *w = NSApp.mainWindow ?: NSApp.keyWindow;
	if (w == nil) return;
	w.hasShadow = YES;
	w.opaque = NO;
	w.backgroundColor = NSColor.clearColor;
	clipView(w.contentView, 16);
	if (gTitleMonitor != nil) return;
	gTitleMonitor = [NSEvent addLocalMonitorForEventsMatchingMask:NSEventMaskLeftMouseDown
		handler:^NSEvent *(NSEvent *e) {
			NSWindow *win = NSApp.mainWindow ?: NSApp.keyWindow;
			if (e.window != win || gTitleH < 1) return e;
			NSPoint p = [e locationInWindow];
			if (titleBlocked(win, p)) return e;
			if (e.clickCount >= 2) {
				[win zoom:nil];
				return nil;
			}
			[win performWindowDragWithEvent:e];
			return nil;
		}];
}

void suzuri_set_title_hits(int titleH, const int *rects, int n) {
	gTitleH = titleH;
	if (n > 64) n = 64;
	if (n < 0) n = 0;
	gBlockN = n;
	if (rects != NULL && n > 0) {
		memcpy(gBlock, rects, (size_t)n * 4 * sizeof(int));
	}
}

void suzuri_screen_cursor(int *x, int *y) {
	NSPoint p = [NSEvent mouseLocation];
	NSArray<NSScreen *> *screens = [NSScreen screens];
	NSScreen *primary = screens.count > 0 ? screens[0] : [NSScreen mainScreen];
	CGFloat h = primary ? primary.frame.size.height : 0;
	if (x) *x = (int)p.x;
	if (y) *y = (int)(h - p.y);
}
