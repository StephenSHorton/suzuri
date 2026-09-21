#import <AppKit/AppKit.h>
#import <objc/message.h>
#import <QuartzCore/QuartzCore.h>
#include <string.h>

static void suzuri_toggle_zoom(NSWindow *w);

static int gTitleH;
static int gHoverLight = -1;
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

static NSWindow *hostWindow(void) {
	for (NSWindow *w in NSApp.windows) {
		if ([w isKindOfClass:[NSPanel class]]) continue;
		if (w.contentView == nil) continue;
		return w;
	}
	return nil;
}

// Clip the content view only. A mask on the Metal layer faults the renderer.
static void roundContent(NSView *v, CGFloat radius) {
	if (v == nil || v.bounds.size.width < 2 || v.bounds.size.height < 2) return;
	v.wantsLayer = YES;
	CALayer *layer = v.layer;
	if (layer == nil) return;
	// cornerRadius clips. A shape mask on CAMetalLayer faults the renderer.
	layer.cornerRadius = radius;
	layer.masksToBounds = YES;
}

static int gRoundQueued;

static const CGFloat kResizeEdge = 6;

static int edgeAt(NSWindow *w, NSPoint p) {
	NSRect b = w.contentView.bounds;
	int e = 0;
	if (p.x <= kResizeEdge) e |= 1;
	if (p.x >= b.size.width - kResizeEdge) e |= 2;
	if (p.y <= kResizeEdge) e |= 4;
	if (p.y >= b.size.height - kResizeEdge) e |= 8;
	return e;
}

static NSCursor *edgeCursor(int e) {
	BOOL nwse = ((e & 1) && (e & 4)) || ((e & 2) && (e & 8));
	BOOL nesw = ((e & 1) && (e & 8)) || ((e & 2) && (e & 4));
	if (nwse || nesw) {
		SEL sel = NSSelectorFromString(nwse ? @"_windowResizeNorthWestSouthEastCursor" : @"_windowResizeNorthEastSouthWestCursor");
		if ([[NSCursor class] respondsToSelector:sel]) {
			return ((NSCursor *(*)(id, SEL))objc_msgSend)([NSCursor class], sel);
		}
	}
	if ((e & 1) || (e & 2)) return [NSCursor resizeLeftRightCursor];
	if ((e & 4) || (e & 8)) return [NSCursor resizeUpDownCursor];
	return [NSCursor arrowCursor];
}

static void trackResize(NSWindow *w, int edge) {
	NSRect start = w.frame;
	NSPoint origin = [NSEvent mouseLocation];
	const CGFloat minW = 640, minH = 400;
	for (;;) {
		NSEvent *ev = [w nextEventMatchingMask:(NSEventMaskLeftMouseDragged | NSEventMaskLeftMouseUp)
			untilDate:[NSDate distantFuture]
			inMode:NSEventTrackingRunLoopMode
			dequeue:YES];
		if (ev == nil || ev.type == NSEventTypeLeftMouseUp) break;
		NSPoint now = [NSEvent mouseLocation];
		CGFloat dx = now.x - origin.x;
		CGFloat dy = now.y - origin.y;
		NSRect f = start;
		if (edge & 1) {
			CGFloat nw = start.size.width - dx;
			if (nw < minW) {
				dx = start.size.width - minW;
				nw = minW;
			}
			f.origin.x = start.origin.x + dx;
			f.size.width = nw;
		}
		if (edge & 2) {
			f.size.width = start.size.width + dx;
			if (f.size.width < minW) f.size.width = minW;
		}
		if (edge & 4) {
			CGFloat nh = start.size.height - dy;
			if (nh < minH) {
				dy = start.size.height - minH;
				nh = minH;
			}
			f.origin.y = start.origin.y + dy;
			f.size.height = nh;
		}
		if (edge & 8) {
			f.size.height = start.size.height + dy;
			if (f.size.height < minH) f.size.height = minH;
		}
		[w setFrame:f display:YES];
		[edgeCursor(edge) set];
	}
	[[NSCursor arrowCursor] set];
}

void suzuri_round_main(void) {
	if (!__sync_bool_compare_and_swap(&gRoundQueued, 0, 1)) return;
	dispatch_async(dispatch_get_main_queue(), ^{
		gRoundQueued = 0;
		NSWindow *w = hostWindow();
		if (w == nil) return;
		w.hasShadow = YES;
		w.opaque = NO;
		w.backgroundColor = NSColor.clearColor;
		roundContent(w.contentView, 16);
		if (gTitleMonitor != nil) return;
	gTitleMonitor = [NSEvent addLocalMonitorForEventsMatchingMask:NSEventMaskLeftMouseDown | NSEventMaskMouseMoved | NSEventMaskMouseExited
		handler:^NSEvent *(NSEvent *e) {
			NSWindow *win = hostWindow();
			if (win == nil) return e;
			NSPoint p = [e locationInWindow];
			gHoverLight = -1;
			if (e.window == win) {
				CGFloat H = win.contentView.bounds.size.height;
				CGFloat yTop = H - p.y;
				CGFloat x = p.x;
				for (int i = 0; i < 3 && i < gBlockN; i++) {
					int bx = gBlock[i * 4], by = gBlock[i * 4 + 1];
					int bw = gBlock[i * 4 + 2], bh = gBlock[i * 4 + 3];
					if (x >= bx && x < bx + bw && yTop >= by && yTop < by + bh) {
						gHoverLight = i;
						break;
					}
				}
			}
			if (e.window == win) {
				int edge = edgeAt(win, p);
				if (e.type == NSEventTypeMouseMoved || e.type == NSEventTypeMouseExited) {
					if (edge) [edgeCursor(edge) set];
					return e;
				}
				if (e.type == NSEventTypeLeftMouseDown && edge) {
					trackResize(win, edge);
					return nil;
				}
			}
			if (e.type != NSEventTypeLeftMouseDown) return e;
			if (e.window != win || gTitleH < 1) return e;
			if (titleBlocked(win, p)) return e;
			if (e.clickCount >= 2) {
				suzuri_toggle_zoom(win);
				return nil;
			}
			[win performWindowDragWithEvent:e];
			return nil;
		}];
	});
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

static NSRect gUnzoomed;

static BOOL nearf(CGFloat a, CGFloat b) {
	CGFloat d = a - b;
	if (d < 0) d = -d;
	return d < 2;
}

static BOOL framesClose(NSRect a, NSRect b) {
	return nearf(a.origin.x, b.origin.x) && nearf(a.origin.y, b.origin.y) &&
		nearf(a.size.width, b.size.width) && nearf(a.size.height, b.size.height);
}

// Toggle between the window's last size and the screen area below the menu bar.
// NSWindow zoom: on a borderless window treats every call as "grow" and the
// second one uses the full screen, which slides under the menu bar.
void suzuri_toggle_zoom(NSWindow *w) {
	if (w == nil) return;
	NSRect vis = w.screen.visibleFrame;
	if (framesClose(w.frame, vis) && gUnzoomed.size.width > 40 && gUnzoomed.size.height > 40) {
		[w setFrame:gUnzoomed display:YES animate:YES];
		return;
	}
	gUnzoomed = w.frame;
	[w setFrame:vis display:YES animate:YES];
}

void suzuri_toggle_zoom_main(void) {
	suzuri_toggle_zoom(NSApp.mainWindow ?: NSApp.keyWindow);
}

int suzuri_hover_light(void) { return gHoverLight; }

void suzuri_screen_cursor(int *x, int *y) {
	NSPoint p = [NSEvent mouseLocation];
	NSArray<NSScreen *> *screens = [NSScreen screens];
	NSScreen *primary = screens.count > 0 ? screens[0] : [NSScreen mainScreen];
	CGFloat h = primary ? primary.frame.size.height : 0;
	if (x) *x = (int)p.x;
	if (y) *y = (int)(h - p.y);
}
