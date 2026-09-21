#import <AppKit/AppKit.h>
#import <Foundation/Foundation.h>
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

@implementation SuzuriNoticeView
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
	for (NSTrackingArea *a in [self.trackingAreas copy]) {
		[self removeTrackingArea:a];
	}
	NSTrackingArea *area = [[NSTrackingArea alloc]
		initWithRect:self.bounds
		options:NSTrackingMouseEnteredAndExited | NSTrackingActiveAlways | NSTrackingInVisibleRect | NSTrackingCursorUpdate
		owner:self userInfo:nil];
	[self addTrackingArea:area];
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

void suzuri_notice_hide(void) {
	dispatch_async(dispatch_get_main_queue(), ^{
		if (gPanel != nil) {
			[gPanel orderOut:nil];
		}
	});
}

static int gPresentQueued;

void suzuri_notice_present(const void *pix, int stride, int width, int height) {
	if (pix == NULL || width < 1 || height < 1) {
		dispatch_async(dispatch_get_main_queue(), ^{ suzuri_notice_hide(); });
		return;
	}
	// Ebiten updates off the main thread. AppKit windows must be created there.
	if (!__sync_bool_compare_and_swap(&gPresentQueued, 0, 1)) {
		return;
	}
	size_t nbytes = (size_t)stride * (size_t)height;
	NSData *copy = [[NSData alloc] initWithBytes:pix length:nbytes];
	dispatch_async(dispatch_get_main_queue(), ^{
		gPresentQueued = 0;
		[copy autorelease];
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
			view.noticeImage = img;
			[view setNeedsDisplay:YES];

			NSWindow *host = NSApp.mainWindow ?: NSApp.keyWindow;
			NSScreen *screen = host.screen ?: NSScreen.mainScreen;
			NSRect vis = screen.visibleFrame;
			NSRect frame = NSMakeRect(vis.origin.x + 16, vis.origin.y + 16, pt.width, pt.height);
			if (!NSEqualRects(gPanel.frame, frame)) {
				[gPanel setFrame:frame display:YES];
				[view resetCursorRects];
			}
			if (!gPanel.isVisible) {
				[gPanel orderFrontRegardless];
			}
		}
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
		[gSounds addObject:sound];
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

void suzuri_pump(double seconds) {
	if (seconds <= 0) return;
	@autoreleasepool {
		[[NSRunLoop currentRunLoop] runUntilDate:[NSDate dateWithTimeIntervalSinceNow:seconds]];
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
