#import <AppKit/AppKit.h>
#import <Foundation/Foundation.h>

extern void suzuriNoticeMouse(int x, int y, int kind);

static NSPanel *gPanel;
static NSMutableArray *gSounds;

@interface SuzuriNoticeView : NSImageView
@end

@implementation SuzuriNoticeView
- (BOOL)acceptsFirstMouse:(NSEvent *)event { return YES; }
- (void)mouseDown:(NSEvent *)event { [self emit:event kind:1]; }
- (void)rightMouseDown:(NSEvent *)event { [self emit:event kind:3]; }
- (void)mouseMoved:(NSEvent *)event { [self emit:event kind:2]; }
- (void)mouseExited:(NSEvent *)event { suzuriNoticeMouse(0, 0, 0); }
- (void)emit:(NSEvent *)event kind:(int)kind {
	NSPoint p = [self convertPoint:event.locationInWindow fromView:nil];
	CGFloat scale = self.window.backingScaleFactor;
	if (scale < 1) scale = 1;
	int x = (int)(p.x * scale);
	int y = (int)((self.bounds.size.height - p.y) * scale);
	suzuriNoticeMouse(x, y, kind);
}
- (void)updateTrackingAreas {
	[super updateTrackingAreas];
	for (NSTrackingArea *a in self.trackingAreas) {
		[self removeTrackingArea:a];
	}
	NSTrackingArea *area = [[NSTrackingArea alloc]
		initWithRect:self.bounds
		options:NSTrackingMouseMoved | NSTrackingMouseEnteredAndExited | NSTrackingActiveAlways | NSTrackingInVisibleRect
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
	view.imageScaling = NSImageScaleNone;
	view.imageAlignment = NSImageAlignBottomLeft;
	gPanel.contentView = view;
	return view;
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
			view.image = img;

			NSWindow *host = NSApp.mainWindow ?: NSApp.keyWindow;
			NSScreen *screen = host.screen ?: NSScreen.mainScreen;
			NSRect vis = screen.visibleFrame;
			NSRect frame = NSMakeRect(vis.origin.x + 16, vis.origin.y + 16, pt.width, pt.height);
			[gPanel setFrame:frame display:YES];
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
