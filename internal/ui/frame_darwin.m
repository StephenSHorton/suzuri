#import <AppKit/AppKit.h>
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

// A shape mask clips the Metal surface. cornerRadius alone does not.
static void maskView(NSView *v, CGFloat radius) {
	if (v == nil || v.bounds.size.width < 2 || v.bounds.size.height < 2) return;
	v.wantsLayer = YES;
	CALayer *layer = v.layer;
	if (layer == nil) return;
	CGRect r = CGRectMake(0, 0, v.bounds.size.width, v.bounds.size.height);
	CAShapeLayer *mask = [layer.mask isKindOfClass:[CAShapeLayer class]] ? (CAShapeLayer *)layer.mask : nil;
	if (mask == nil) {
		mask = [CAShapeLayer layer];
		layer.mask = mask;
	}
	CGPathRef path = CGPathCreateWithRoundedRect(r, radius, radius, NULL);
	mask.frame = r;
	mask.path = path;
	CGPathRelease(path);
	layer.cornerRadius = radius;
	layer.masksToBounds = YES;
}

static int gRoundQueued;

void suzuri_round_main(void) {
	if (!__sync_bool_compare_and_swap(&gRoundQueued, 0, 1)) return;
	dispatch_async(dispatch_get_main_queue(), ^{
		gRoundQueued = 0;
		NSWindow *w = hostWindow();
		if (w == nil) return;
		w.hasShadow = YES;
		w.opaque = NO;
		w.backgroundColor = NSColor.clearColor;
		maskView(w.contentView, 16);
		for (NSView *sub in w.contentView.subviews) {
			maskView(sub, 16);
		}
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
