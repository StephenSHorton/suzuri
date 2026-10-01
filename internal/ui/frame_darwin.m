#import <AppKit/AppKit.h>
#import <objc/message.h>
#import <objc/runtime.h>
#import <QuartzCore/QuartzCore.h>
#include <dlfcn.h>
#include <stdio.h>
#include <stdatomic.h>
#include <string.h>

static void suzuri_toggle_zoom(NSWindow *w);
static void restoreBorderless(NSWindow *w);
static BOOL framesClose(NSRect a, NSRect b);
void suzuri_toggle_fullscreen(NSWindow *w);

static int gTitleH;
static int gHoverLight = -1;
static int gBlock[64 * 4];
static int gBlockN;
static id gTitleMonitor;

// gInFullscreen covers the whole transition, so layout does not resize the
// window mid-animation. gFSBusy ignores a second toggle until the animation ends.
static atomic_int gInFullscreen;
static atomic_int gFSBusy;
static atomic_int gFSScheduled;
static atomic_int gRequestClose;
static int gFSSawWill;
static int gFSNotes;

@interface GLFWWindow : NSWindow
@end

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
	// A clipped layer makes the fullscreen transition fail and fall back.
	layer.masksToBounds = radius > 0;
}

static int gRoundQueued;

// A borderless window (style mask without NSWindowStyleMaskTitled) ignores
// toggleFullScreen:. macOS only starts the fullscreen Space when the window is
// titled and resizable. FullSizeContentView keeps our painted title strip
// under a transparent title bar instead of inset below a native one.
// Electron and Chromium do the same dance in windowWillEnterFullScreen, then
// put the borderless mask back after the Space exits.
static void hideStandardButtons(NSWindow *w) {
	NSWindowButton types[3] = {NSWindowCloseButton, NSWindowMiniaturizeButton, NSWindowZoomButton};
	for (int i = 0; i < 3; i++) {
		NSButton *b = [w standardWindowButton:types[i]];
		b.hidden = YES;
	}
}

static void applyChromeLook(NSWindow *w) {
	w.titlebarAppearsTransparent = YES;
	w.titleVisibility = NSWindowTitleHidden;
	if ([w respondsToSelector:@selector(setTitlebarSeparatorStyle:)]) {
		w.titlebarSeparatorStyle = NSTitlebarSeparatorStyleNone;
	}
	w.opaque = NO;
	w.backgroundColor = NSColor.clearColor;
	w.hasShadow = YES;
	[w makeFirstResponder:w.contentView];
}

static void prepareEnterFullscreen(NSWindow *w) {
	NSWindowCollectionBehavior behavior = w.collectionBehavior;
	behavior |= NSWindowCollectionBehaviorFullScreenPrimary | NSWindowCollectionBehaviorManaged;
	behavior &= ~NSWindowCollectionBehaviorFullScreenNone;
	w.collectionBehavior = behavior;

	// A clear, non-opaque window is rejected by the fullscreen Space animation.
	w.opaque = YES;
	w.backgroundColor = NSColor.blackColor;
	applyChromeLook(w);
	w.opaque = YES;
	w.backgroundColor = NSColor.blackColor;
	NSRect before = w.frame;
	NSUInteger mask = w.styleMask;
	mask |= NSWindowStyleMaskTitled | NSWindowStyleMaskResizable |
		NSWindowStyleMaskFullSizeContentView | NSWindowStyleMaskMiniaturizable;
	w.styleMask = mask;
	if (!framesClose(w.frame, before)) {
		[w setFrame:before display:YES animate:NO];
	}
	applyChromeLook(w);
	w.opaque = YES;
	w.backgroundColor = NSColor.blackColor;
	hideStandardButtons(w);
	roundContent(w.contentView, 0);
}

static void restoreBorderless(NSWindow *w) {
	if (w == nil) return;
	NSRect before = w.frame;
	// Keep Resizable. GLFW added it for the custom frame, and dropping it
	// clears fullscreen eligibility on the next click.
	w.styleMask = NSWindowStyleMaskMiniaturizable | NSWindowStyleMaskResizable;
	if (!framesClose(w.frame, before)) {
		[w setFrame:before display:YES animate:NO];
	}
	applyChromeLook(w);
	roundContent(w.contentView, 16);
	atomic_store(&gInFullscreen, 0);
	atomic_store(&gFSBusy, 0);
}

static void suzuri_fullscreen_failed(id self, SEL cmd, NSWindow *window) {
	(void)self;
	(void)cmd;
	restoreBorderless(window);
}

static void noteFullscreen(NSWindow *w) {
	NSNotificationCenter *nc = [NSNotificationCenter defaultCenter];
	[nc addObserverForName:NSWindowWillEnterFullScreenNotification object:w queue:nil usingBlock:^(NSNotification *note) {
		gFSSawWill = 1;
		atomic_store(&gInFullscreen, 1);
		NSWindow *win = note.object;
		hideStandardButtons(win);
		roundContent(win.contentView, 0);
	}];
	[nc addObserverForName:NSWindowDidEnterFullScreenNotification object:w queue:nil usingBlock:^(NSNotification *note) {
		atomic_store(&gFSBusy, 0);
		hideStandardButtons(note.object);
		roundContent(((NSWindow *)note.object).contentView, 0);
	}];
	[nc addObserverForName:NSWindowWillExitFullScreenNotification object:w queue:nil usingBlock:^(NSNotification *note) {
		(void)note;
		gFSSawWill = 1;
	}];
	[nc addObserverForName:NSWindowDidExitFullScreenNotification object:w queue:nil usingBlock:^(NSNotification *note) {
		restoreBorderless(note.object);
	}];
	// Failure is a delegate callback, not a notification. Ebiten's delegate
	// does not implement it, so add the callback once on that class.
	Class delegateClass = NSClassFromString(@"EbitengineWindowDelegate");
	if (delegateClass != Nil) {
		class_addMethod(delegateClass, @selector(windowDidFailToEnterFullScreen:), (IMP)suzuri_fullscreen_failed, "v@:@");
	}
}

@implementation GLFWWindow (SuzuriFullScreen)

- (void)toggleFullScreen:(id)sender {
	if (atomic_load(&gFSBusy)) return;
	atomic_store(&gFSBusy, 1);
	BOOL entering = (self.styleMask & NSWindowStyleMaskFullScreen) == 0;
	if (entering) prepareEnterFullscreen(self);
	gFSSawWill = 0;
	Method m = class_getInstanceMethod([NSWindow class], @selector(toggleFullScreen:));
	void (*imp)(id, SEL, id) = (void (*)(id, SEL, id))method_getImplementation(m);
	imp(self, @selector(toggleFullScreen:), sender);
	if (gFSSawWill) return;
	// toggleFullScreen: is synchronous about will-enter. No callback means the
	// call was ignored; put the borderless mask back.
	atomic_store(&gFSBusy, 0);
	if (entering && (self.styleMask & NSWindowStyleMaskTitled) != 0 &&
		(self.styleMask & NSWindowStyleMaskFullScreen) == 0) {
		restoreBorderless(self);
	}
}

@end

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

// Desktop blur for alpha-0 shell holes. NSVisualEffectView materials on
// macOS 26 paint a solid slab and hide the desktop. This private WindowServer
// call blurs whatever is behind the window into transparent framebuffer
// pixels. Developer ID builds can call it; the App Store would not.
typedef int32_t CGSConnectionID;
typedef CGSConnectionID (*CGSConnectionFn)(void);
typedef int32_t (*CGSBlurFn)(CGSConnectionID, int32_t wid, int32_t radius);

static CGSConnectionFn gDefaultConn;
static CGSBlurFn gSetBlur;
static int gBlurLookup;
static int gBlurMissLogged;
static atomic_int gGlassWant;
static atomic_int gBlurRadius;
static int gGlassApplied = -1;
static int gBlurApplied = -1;
static int gBlurW, gBlurH;

static int loadBlur(void) {
	if (gBlurLookup != 0) return gBlurLookup;
	void *h = dlopen("/System/Library/Frameworks/ApplicationServices.framework/ApplicationServices", RTLD_LAZY);
	if (h == NULL) {
		gBlurLookup = -1;
		return -1;
	}
	gDefaultConn = (CGSConnectionFn)dlsym(h, "CGSDefaultConnectionForThread");
	gSetBlur = (CGSBlurFn)dlsym(h, "CGSSetWindowBackgroundBlurRadius");
	if (gDefaultConn == NULL || gSetBlur == NULL) {
		gBlurLookup = -1;
		return -1;
	}
	gBlurLookup = 1;
	return 1;
}

static void applyGlassNow(NSWindow *w) {
	if (w == nil || w.windowNumber <= 0) return;
	int on = atomic_load(&gGlassWant) && !atomic_load(&gInFullscreen);
	int radius = on ? atomic_load(&gBlurRadius) : 0;
	NSRect frame = w.frame;
	int ww = (int)frame.size.width;
	int hh = (int)frame.size.height;
	// The chrome pass sets a clear background and a corner radius every
	// frame, which clears this frost. Always send the radius again.
	if (loadBlur() != 1) {
		if (!gBlurMissLogged) {
			fprintf(stderr, "suzuri: desktop blur unavailable\n");
			gBlurMissLogged = 1;
		}
		gGlassApplied = on;
		gBlurApplied = radius;
		gBlurW = ww;
		gBlurH = hh;
		return;
	}
	int err = gSetBlur(gDefaultConn(), (int32_t)w.windowNumber, radius);
	if (gBlurApplied != radius || gGlassApplied != on) {
		fprintf(stderr, "suzuri: glass blur on=%d radius=%d err=%d window=%ld\n",
			on, radius, err, (long)w.windowNumber);
	}
	gGlassApplied = on;
	gBlurApplied = radius;
	gBlurW = ww;
	gBlurH = hh;
}

// Reapply after every main-queue drain. Ebiten and the chrome pass both
// clear the frost later in the same turn, so a one-shot apply does not last.
static void ensureBlurObserver(void) {
	static int once;
	if (once) return;
	once = 1;
	CFRunLoopObserverRef obs = CFRunLoopObserverCreateWithHandler(
		kCFAllocatorDefault, kCFRunLoopBeforeWaiting, true, 0,
		^(CFRunLoopObserverRef observer, CFRunLoopActivity activity) {
			(void)observer;
			(void)activity;
			applyGlassNow(hostWindow());
		});
	if (obs == NULL) return;
	CFRunLoopAddObserver(CFRunLoopGetMain(), obs, kCFRunLoopCommonModes);
}

void suzuri_set_glass(int on, int radius) {
	if (radius < 0) radius = 0;
	if (radius > 80) radius = 80;
	atomic_store(&gBlurRadius, radius);
	atomic_store(&gGlassWant, on ? 1 : 0);
}

void suzuri_apply_glass(void) {
	dispatch_async(dispatch_get_main_queue(), ^{
		NSWindow *w = hostWindow();
		if (w == nil) return;
		applyGlassNow(w);
	});
}

void suzuri_round_main(void) {
	if (!__sync_bool_compare_and_swap(&gRoundQueued, 0, 1)) return;
	dispatch_async(dispatch_get_main_queue(), ^{
		gRoundQueued = 0;
		NSWindow *w = hostWindow();
		if (w == nil) return;
		// Setting a clear background and a corner radius clears the frost.
		// Do that only when the frame size changes, then let the run-loop
		// observer put the blur back and keep it there.
		int ww = (int)w.frame.size.width;
		int hh = (int)w.frame.size.height;
		static int gChromeW, gChromeH;
		if (gChromeW != ww || gChromeH != hh) {
			w.hasShadow = YES;
			w.opaque = NO;
			w.backgroundColor = NSColor.clearColor;
			roundContent(w.contentView, 16);
			gChromeW = ww;
			gChromeH = hh;
		}
		ensureBlurObserver();
		applyGlassNow(w);
		if (!gFSNotes) {
			gFSNotes = 1;
			noteFullscreen(w);
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
					// 4pt slop matches hitMacLight in the Go host.
					if (x >= bx - 4 && x < bx + bw + 4 && yTop >= by - 4 && yTop < by + bh + 4) {
						gHoverLight = i;
						break;
					}
				}
			}
			BOOL fullscreen = atomic_load(&gInFullscreen) || (win.styleMask & NSWindowStyleMaskFullScreen);
			if (e.window == win && e.type == NSEventTypeLeftMouseDown && gHoverLight >= 0) {
				switch (gHoverLight) {
				case 0:
					atomic_store(&gRequestClose, 1);
					return nil;
				case 1:
					[win miniaturize:nil];
					return nil;
				default:
					if (!fullscreen && (e.modifierFlags & NSEventModifierFlagOption)) {
						suzuri_toggle_zoom(win);
						return nil;
					}
					suzuri_toggle_fullscreen(win);
					return nil;
				}
			}
			if (e.window == win) {
				int edge = edgeAt(win, p);
				if (!fullscreen && (e.type == NSEventTypeMouseMoved || e.type == NSEventTypeMouseExited)) {
					if (edge) [edgeCursor(edge) set];
					return e;
				}
				if (!fullscreen && e.type == NSEventTypeLeftMouseDown && edge) {
					trackResize(win, edge);
					return nil;
				}
			}
			if (e.type != NSEventTypeLeftMouseDown) return e;
			if (fullscreen) {
				// The temporary title bar sits above the content view and would
				// treat an empty-strip click as a native zoom or drag.
				if (e.window == win && gTitleH > 0 && !titleBlocked(win, p)) return nil;
				return e;
			}
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
	if (w == nil || atomic_load(&gInFullscreen)) return;
	if (w.styleMask & NSWindowStyleMaskFullScreen) return;
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

// One scheduled toggle. A second click in the same turn, or a Go fallback
// for a click the monitor also saw, does not start a second animation.
void suzuri_toggle_fullscreen(NSWindow *w) {
	if (w == nil) return;
	if (atomic_exchange(&gFSScheduled, 1)) return;
	dispatch_async(dispatch_get_main_queue(), ^{
		if (atomic_load(&gFSBusy)) {
			atomic_store(&gFSScheduled, 0);
			return;
		}
		// Hold layout still as soon as the transition is requested. Will-enter
		// confirms it; a rejected toggle clears it again.
		if ((w.styleMask & NSWindowStyleMaskFullScreen) == 0) {
			atomic_store(&gInFullscreen, 1);
		}
		atomic_store(&gFSScheduled, 0);
		[w toggleFullScreen:nil];
		// A non-GLFW window never hits the override, so the optimistic flag
		// would otherwise stick and freeze resizing.
		if (atomic_load(&gInFullscreen) && !gFSSawWill && !atomic_load(&gFSBusy) &&
			(w.styleMask & NSWindowStyleMaskFullScreen) == 0) {
			atomic_store(&gInFullscreen, 0);
		}
	});
}

void suzuri_toggle_fullscreen_main(void) {
	suzuri_toggle_fullscreen(hostWindow());
}

int suzuri_hover_light(void) { return gHoverLight; }

int suzuri_in_fullscreen(void) { return atomic_load(&gInFullscreen); }

int suzuri_take_close(void) { return atomic_exchange(&gRequestClose, 0); }

void suzuri_screen_cursor(int *x, int *y) {
	NSPoint p = [NSEvent mouseLocation];
	NSArray<NSScreen *> *screens = [NSScreen screens];
	NSScreen *primary = screens.count > 0 ? screens[0] : [NSScreen mainScreen];
	CGFloat h = primary ? primary.frame.size.height : 0;
	if (x) *x = (int)p.x;
	if (y) *y = (int)(h - p.y);
}
