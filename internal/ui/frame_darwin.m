#import <AppKit/AppKit.h>

void suzuri_round_main(void) {
	NSWindow *w = NSApp.mainWindow ?: NSApp.keyWindow;
	if (w == nil) return;
	w.hasShadow = YES;
	w.opaque = NO;
	w.backgroundColor = [NSColor colorWithCalibratedWhite:0.06 alpha:1];
	NSView *v = w.contentView;
	v.wantsLayer = YES;
	v.layer.cornerRadius = 12;
	v.layer.masksToBounds = YES;
}

void suzuri_screen_cursor(int *x, int *y) {
	NSPoint p = [NSEvent mouseLocation];
	NSArray<NSScreen *> *screens = [NSScreen screens];
	NSScreen *primary = screens.count > 0 ? screens[0] : [NSScreen mainScreen];
	CGFloat h = primary ? primary.frame.size.height : 0;
	if (x) *x = (int)p.x;
	if (y) *y = (int)(h - p.y);
}
