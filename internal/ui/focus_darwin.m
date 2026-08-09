// Focus reclaim + clipboard PNG dump via AppKit (no osascript / Apple Events).
#import <AppKit/AppKit.h>
#import <Foundation/Foundation.h>

void suzuri_reclaim_focus(void) {
	@autoreleasepool {
		NSApplication *app = [NSApplication sharedApplication];
		if (app == nil) {
			return;
		}
		// activateIgnoringOtherApps so we regain key window after paste or
		// when a dismiss path left us unfocused (user had to alt-tab).
		[app activateIgnoringOtherApps:YES];
		NSWindow *key = [app keyWindow];
		if (key == nil) {
			key = [app mainWindow];
		}
		if (key != nil) {
			[key makeKeyAndOrderFront:nil];
		}
	}
}

// Write pasteboard image as PNG to path. Returns 1 on success, 0 if no image,
// -1 on I/O error. Prefer NSPasteboard over osascript (Hardened Runtime +
// invalid re-sign can break Apple Events; pasteboard access stays in-process).
int suzuri_clipboard_png_write(const char *path) {
	if (path == NULL || path[0] == '\0') {
		return -1;
	}
	@autoreleasepool {
		NSPasteboard *pb = [NSPasteboard generalPasteboard];
		if (pb == nil) {
			return 0;
		}
		// Prefer PNG bytes; fall back to any NSImage-compatible type (TIFF,
		// public.png, screenshots, browser "Copy Image").
		NSData *png = [pb dataForType:NSPasteboardTypePNG];
		if (png == nil || [png length] < 32) {
			NSImage *img = [[NSImage alloc] initWithPasteboard:pb];
			if (img == nil) {
				return 0;
			}
			NSData *tiff = [img TIFFRepresentation];
			if (tiff == nil) {
				return 0;
			}
			NSBitmapImageRep *rep = [NSBitmapImageRep imageRepWithData:tiff];
			if (rep == nil) {
				return 0;
			}
			png = [rep representationUsingType:NSBitmapImageFileTypePNG properties:@{}];
			if (png == nil || [png length] < 32) {
				return 0;
			}
		}
		NSString *nsPath = [NSString stringWithUTF8String:path];
		if (nsPath == nil) {
			return -1;
		}
		BOOL ok = [png writeToFile:nsPath atomically:YES];
		return ok ? 1 : -1;
	}
}
