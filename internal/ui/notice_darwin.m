#import <AppKit/AppKit.h>
#import <Foundation/Foundation.h>
#include <os/lock.h>
#include <stdatomic.h>

// Mouse handlers must not call into Go. The notify command pumps this run
// loop from a Go call, and a re-entrant Go callback deadlocks the click.
static atomic_int gHover;
static atomic_int gClick;
static atomic_int gClickX;
static atomic_int gClickY;

static void suzuri_note_click(NSEvent *event, NSView *view, int kind) {
	NSPoint p = [view convertPoint:event.locationInWindow fromView:nil];
	const CGFloat scale = 2.0;
	atomic_store(&gClickX, (int)(p.x * scale));
	atomic_store(&gClickY, (int)((view.bounds.size.height - p.y) * scale));
	atomic_store(&gClick, kind);
}

static NSPanel *gPanel;
static NSMutableArray *gSounds;

@interface SuzuriNoticeView : NSView
@property (nonatomic, retain) NSImage *noticeImage;
@end

// This file is built without ARC. Every alloc/copy/new needs a release.
@implementation SuzuriNoticeView
- (void)dealloc {
	[_noticeImage release];
	[super dealloc];
}
- (BOOL)acceptsFirstMouse:(NSEvent *)event { return YES; }
- (BOOL)isOpaque { return NO; }
- (BOOL)isFlipped { return NO; }
- (void)drawRect:(NSRect)dirty {
	[self.noticeImage drawInRect:self.bounds fromRect:NSZeroRect operation:NSCompositingOperationSourceOver fraction:1];
}
- (void)resetCursorRects {
	[self addCursorRect:self.bounds cursor:[NSCursor arrowCursor]];
}
- (void)cursorUpdate:(NSEvent *)event {
	[[NSCursor arrowCursor] set];
}
- (void)mouseEntered:(NSEvent *)event {
	[[NSCursor arrowCursor] set];
	atomic_store(&gHover, 1);
}
- (void)mouseExited:(NSEvent *)event {
	[[NSCursor arrowCursor] set];
	atomic_store(&gHover, 0);
}
- (void)mouseDown:(NSEvent *)event { suzuri_note_click(event, self, 1); }
- (void)rightMouseDown:(NSEvent *)event { suzuri_note_click(event, self, 3); }
- (void)updateTrackingAreas {
	[super updateTrackingAreas];
	NSArray *old = [self.trackingAreas copy];
	for (NSTrackingArea *a in old) {
		[self removeTrackingArea:a];
	}
	[old release];
	NSTrackingArea *area = [[NSTrackingArea alloc]
		initWithRect:self.bounds
		options:NSTrackingMouseEnteredAndExited | NSTrackingActiveAlways | NSTrackingInVisibleRect | NSTrackingCursorUpdate
		owner:self userInfo:nil];
	[self addTrackingArea:area];
	[area release];
}
@end

static SuzuriNoticeView *ensurePanel(void) {
	if (gPanel != nil) {
		return (SuzuriNoticeView *)gPanel.contentView;
	}
	NSRect frame = NSMakeRect(0, 0, 320, 80);
	gPanel = [[NSPanel alloc] initWithContentRect:frame
		styleMask:(NSWindowStyleMaskBorderless | NSWindowStyleMaskNonactivatingPanel)
		backing:NSBackingStoreBuffered
		defer:NO];
	gPanel.level = NSStatusWindowLevel;
	gPanel.opaque = NO;
	gPanel.backgroundColor = NSColor.clearColor;
	gPanel.hasShadow = NO;
	gPanel.hidesOnDeactivate = NO;
	gPanel.floatingPanel = YES;
	gPanel.becomesKeyOnlyIfNeeded = YES;
	gPanel.collectionBehavior =
		NSWindowCollectionBehaviorCanJoinAllSpaces |
		NSWindowCollectionBehaviorStationary |
		NSWindowCollectionBehaviorFullScreenAuxiliary |
		NSWindowCollectionBehaviorIgnoresCycle;
	SuzuriNoticeView *view = [[SuzuriNoticeView alloc] initWithFrame:frame];
	gPanel.contentView = view;
	[view release];
	return view;
}

void suzuri_notice_take_click(int *kind, int *x, int *y) {
	int k = atomic_exchange(&gClick, 0);
	if (kind) *kind = k;
	if (x) *x = atomic_load(&gClickX);
	if (y) *y = atomic_load(&gClickY);
}

int suzuri_notice_hover(void) {
	return atomic_load(&gHover);
}

// The newest frame waits here for the main queue. A later present or hide
// replaces it, so a burst of ticks still lands on the newest state, and Go
// can skip ticks whose card did not change. nil pixels mean hide.
static os_unfair_lock gPendingLock = OS_UNFAIR_LOCK_INIT;
static NSData *gPendingPix;
static int gPendingStride;
static int gPendingW;
static int gPendingH;
static int gPendingAnchor;
static int gPresentQueued;
static atomic_int gPlaceQueued;
// Main queue only.
static int gShownAnchor;

// anchor matches config.NoticePositionIndex: 0 bottom-left, then bottom-center,
// bottom-right, center-left, center, center-right, top-left, top-center, top-right.
// Origin is the panel's bottom-left in a y-up visibleFrame. Edge anchors inset
// by 16pt; a centered axis ignores the inset. Keep this in step with
// noticePanelOrigin in notices.go.
static NSRect suzuri_notice_frame(NSRect vis, NSSize pt, int anchor) {
	const CGFloat inset = 16;
	int col = 0;
	int row = 0;
	switch (anchor) {
	case 1: case 4: case 7: col = 1; break;
	case 2: case 5: case 8: col = 2; break;
	default: break;
	}
	switch (anchor) {
	case 3: case 4: case 5: row = 1; break;
	case 6: case 7: case 8: row = 2; break;
	default: break;
	}
	CGFloat x = vis.origin.x + inset;
	CGFloat y = vis.origin.y + inset;
	if (col == 1) {
		x = vis.origin.x + (vis.size.width - pt.width) / 2.0;
	} else if (col == 2) {
		x = vis.origin.x + vis.size.width - pt.width - inset;
	}
	if (row == 1) {
		y = vis.origin.y + (vis.size.height - pt.height) / 2.0;
	} else if (row == 2) {
		y = vis.origin.y + vis.size.height - pt.height - inset;
	}
	return NSMakeRect(x, y, pt.width, pt.height);
}

// placePanel keeps the panel on the host's screen at its anchor. Main queue only.
static void placePanel(SuzuriNoticeView *view, NSSize pt, int anchor) {
	NSWindow *host = NSApp.mainWindow ?: NSApp.keyWindow;
	NSScreen *screen = host.screen ?: NSScreen.mainScreen;
	NSRect vis = screen.visibleFrame;
	NSRect frame = suzuri_notice_frame(vis, pt, anchor);
	if (!NSEqualRects(gPanel.frame, frame)) {
		[gPanel setFrame:frame display:YES];
		[view resetCursorRects];
	}
}

static void suzuri_notice_apply(void) {
	os_unfair_lock_lock(&gPendingLock);
	NSData *copy = gPendingPix;
	int stride = gPendingStride;
	int width = gPendingW;
	int height = gPendingH;
	int anchor = gPendingAnchor;
	gPendingPix = nil;
	gPresentQueued = 0;
	os_unfair_lock_unlock(&gPendingLock);
	if (copy == nil) {
		if (gPanel != nil) {
			[gPanel orderOut:nil];
		}
		return;
	}
	@autoreleasepool {
		SuzuriNoticeView *view = ensurePanel();
		const CGFloat scale = 2.0;
		NSBitmapImageRep *rep = [[NSBitmapImageRep alloc]
			initWithBitmapDataPlanes:NULL
			pixelsWide:width
			pixelsHigh:height
			bitsPerSample:8
			samplesPerPixel:4
			hasAlpha:YES
			isPlanar:NO
			colorSpaceName:NSCalibratedRGBColorSpace
			bitmapFormat:NSBitmapFormatAlphaNonpremultiplied
			bytesPerRow:width * 4
			bitsPerPixel:32];
		const unsigned char *src = copy.bytes;
		unsigned char *dst = rep.bitmapData;
		for (int y = 0; y < height; y++) {
			memcpy(dst + y * width * 4, src + y * stride, (size_t)width * 4);
		}
		NSSize pt = NSMakeSize(width / scale, height / scale);
		rep.size = pt;
		NSImage *img = [[NSImage alloc] initWithSize:pt];
		[img addRepresentation:rep];
		[rep release];
		// noticeImage retains. Drop ours so the old frame frees on the next set.
		view.noticeImage = img;
		[img release];
		[view setNeedsDisplay:YES];

		gShownAnchor = anchor;
		placePanel(view, pt, anchor);
		if (!gPanel.isVisible) {
			[gPanel orderFrontRegardless];
		}
	}
	[copy release];
}

// queueNotice takes ownership of pix (nil hides).
static void queueNotice(NSData *pix, int stride, int width, int height, int anchor) {
	os_unfair_lock_lock(&gPendingLock);
	[gPendingPix release];
	gPendingPix = pix;
	gPendingStride = stride;
	gPendingW = width;
	gPendingH = height;
	gPendingAnchor = anchor;
	int dispatch = !gPresentQueued;
	gPresentQueued = 1;
	os_unfair_lock_unlock(&gPendingLock);
	// Ebiten updates off the main thread. AppKit windows must be created there.
	if (dispatch) {
		dispatch_async(dispatch_get_main_queue(), ^{ suzuri_notice_apply(); });
	}
}

void suzuri_notice_hide(void) {
	queueNotice(nil, 0, 0, 0, 0);
}

void suzuri_notice_present(const void *pix, int stride, int width, int height, int anchor) {
	if (pix == NULL || width < 1 || height < 1) {
		suzuri_notice_hide();
		return;
	}
	size_t nbytes = (size_t)stride * (size_t)height;
	queueNotice([[NSData alloc] initWithBytes:pix length:nbytes], stride, width, height, anchor);
}

// suzuri_notice_place follows the host to another screen while the shown
// card is unchanged. It moves the panel without redrawing it.
void suzuri_notice_place(void) {
	if (atomic_exchange(&gPlaceQueued, 1)) {
		return;
	}
	dispatch_async(dispatch_get_main_queue(), ^{
		atomic_store(&gPlaceQueued, 0);
		if (gPanel == nil || !gPanel.isVisible) {
			return;
		}
		SuzuriNoticeView *view = (SuzuriNoticeView *)gPanel.contentView;
		if (view.noticeImage == nil) {
			return;
		}
		placePanel(view, view.noticeImage.size, gShownAnchor);
	});
}

void suzuri_play_wav(const void *bytes, int len) {
	if (bytes == NULL || len < 44) return;
	// MRC: the block must own the bytes after this function returns.
	NSData *data = [[NSData alloc] initWithBytes:bytes length:(NSUInteger)len];
	dispatch_async(dispatch_get_main_queue(), ^{
		NSSound *sound = [[NSSound alloc] initWithData:data];
		[data release];
		if (sound == nil) return;
		if (gSounds == nil) gSounds = [[NSMutableArray alloc] init];
		// The ring owns the sound. It frees once four newer sounds push it out.
		[gSounds addObject:sound];
		[sound release];
		if (gSounds.count > 4) {
			[gSounds removeObjectAtIndex:0];
		}
		[sound play];
	});
}

void suzuri_play_wav_sync(const void *bytes, int len) {
	if (bytes == NULL || len < 44) return;
	@autoreleasepool {
		NSData *data = [[NSData alloc] initWithBytes:bytes length:(NSUInteger)len];
		NSSound *sound = [[NSSound alloc] initWithData:data];
		[data release];
		if (sound == nil) return;
		[sound play];
		while ([sound isPlaying]) {
			[[NSRunLoop currentRunLoop] runUntilDate:[NSDate dateWithTimeIntervalSinceNow:0.02]];
		}
		[sound release];
	}
}

void suzuri_app_init(void) {
	[NSApplication sharedApplication];
	[NSApp setActivationPolicy:NSApplicationActivationPolicyAccessory];
}

int suzuri_is_main_thread(void) {
	return [NSThread isMainThread] ? 1 : 0;
}

void suzuri_pump(double seconds) {
	if (seconds <= 0) return;
	if (![NSThread isMainThread]) return;
	[NSApplication sharedApplication];
	NSDate *until = [NSDate dateWithTimeIntervalSinceNow:seconds];
	while ([until timeIntervalSinceNow] > 0) {
		@autoreleasepool {
			NSEvent *event = [NSApp nextEventMatchingMask:NSEventMaskAny
				untilDate:[NSDate dateWithTimeIntervalSinceNow:0.01]
				inMode:NSDefaultRunLoopMode
				dequeue:YES];
			if (event == nil) continue;
			if (gPanel != nil && event.window == gPanel &&
				(event.type == NSEventTypeLeftMouseDown || event.type == NSEventTypeRightMouseDown)) {
				int kind = event.type == NSEventTypeRightMouseDown ? 3 : 1;
				suzuri_note_click(event, gPanel.contentView, kind);
			}
			[NSApp sendEvent:event];
		}
	}
}

void suzuri_focus_host(void) {
	dispatch_async(dispatch_get_main_queue(), ^{
		for (NSWindow *w in NSApp.windows) {
			if (w == gPanel) continue;
			if (w.miniaturized) [w deminiaturize:nil];
			[w makeKeyAndOrderFront:nil];
			break;
		}
	});
}
