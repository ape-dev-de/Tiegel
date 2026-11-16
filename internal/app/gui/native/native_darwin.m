//go:build darwin && cgo && !ios

// Cocoa side of package native. See native.go for the menu buffer format.

#import <AppKit/AppKit.h>
#include <stdlib.h>
#include <string.h>
#include "_cgo_export.h"

static const NSEventModifierFlags kMods = NSEventModifierFlagCommand | NSEventModifierFlagShift |
	NSEventModifierFlagOption | NSEventModifierFlagControl;

// TiegelMenu never performs key equivalents of its items unless they are
// marked functional (representedObject set): the shortcuts are displayed
// but the key events reach the Gio window, which handles them by focus.
@interface TiegelMenu : NSMenu
@end

@implementation TiegelMenu
- (BOOL)performKeyEquivalent:(NSEvent *)event {
	NSEventModifierFlags mods = event.modifierFlags & kMods;
	NSString *key = event.charactersIgnoringModifiers.lowercaseString;
	for (NSMenuItem *it in self.itemArray) {
		if (it.representedObject != nil && it.enabled && it.keyEquivalent.length > 0 &&
			it.keyEquivalentModifierMask == mods && [it.keyEquivalent isEqualToString:key]) {
			return [super performKeyEquivalent:event];
		}
	}
	return NO;
}
@end

@interface TiegelMenuTarget : NSObject
@end

@implementation TiegelMenuTarget
- (void)fire:(id)sender {
	if ([sender isKindOfClass:[NSMenuItem class]]) {
		tiegelMenuAction((long long)((NSMenuItem *)sender).tag);
	}
}
@end

static TiegelMenuTarget *menuTarget;
static NSMenu *menuBar;

typedef struct {
	const uint8_t *p, *end;
	BOOL bad;
} reader;

static uint8_t rd8(reader *r) {
	if (r->bad || r->p + 1 > r->end) { r->bad = YES; return 0; }
	return *r->p++;
}

static uint64_t rdN(reader *r, int n) {
	if (r->bad || r->p + n > r->end) { r->bad = YES; return 0; }
	uint64_t v = 0;
	for (int i = 0; i < n; i++) v |= (uint64_t)r->p[i] << (8 * i);
	r->p += n;
	return v;
}

static NSString *rdStr(reader *r) {
	uint32_t n = (uint32_t)rdN(r, 4);
	if (r->bad || (size_t)(r->end - r->p) < n) { r->bad = YES; return @""; }
	NSString *s = [[NSString alloc] initWithBytes:r->p length:n encoding:NSUTF8StringEncoding];
	r->p += n;
	return s ? s : @"";
}

// fill appends records to menu until 'E' (or the end of the buffer).
static void fill(reader *r, NSMenu *menu, int depth) {
	menu.autoenablesItems = NO;
	while (!r->bad && r->p < r->end) {
		uint8_t op = rd8(r);
		if (op == 'E') return;
		if (op == 'S') { [menu addItem:[NSMenuItem separatorItem]]; continue; }
		if (op != 'I') { r->bad = YES; return; }
		uint8_t kind = rd8(r), flags = rd8(r);
		NSInteger tag = (NSInteger)rdN(r, 8);
		NSEventModifierFlags mods = (NSEventModifierFlags)rdN(r, 4);
		NSString *title = rdStr(r), *key = rdStr(r);
		SEL action = @selector(fire:);
		id target = menuTarget;
		switch (kind) {
		case 1: action = @selector(hide:); target = NSApp; break;
		case 2: action = @selector(hideOtherApplications:); target = NSApp; break;
		case 3: action = @selector(unhideAllApplications:); target = NSApp; break;
		}
		NSMenuItem *it = [[NSMenuItem alloc] initWithTitle:title action:action keyEquivalent:key];
		it.target = target;
		it.tag = tag;
		it.keyEquivalentModifierMask = mods;
		it.enabled = (flags & 1) == 0;
		it.state = (flags & 2) ? NSControlStateValueOn : NSControlStateValueOff;
		if (flags & 8) it.representedObject = @YES;
		if ((flags & 4) && depth < 16) {
			NSMenu *sub = [[TiegelMenu alloc] initWithTitle:title];
			fill(r, sub, depth + 1);
			it.submenu = sub;
			it.action = nil;
		}
		[menu addItem:it];
	}
}

static NSMenu *build(const uint8_t *buf, size_t len) {
	reader r = {buf, buf + len, NO};
	NSMenu *bar = [[NSMenu alloc] initWithTitle:@""];
	NSMenu *windows = nil;
	while (!r.bad && r.p < r.end) {
		if (rd8(&r) != 'M') return nil;
		uint8_t flags = rd8(&r);
		NSString *title = rdStr(&r);
		if (flags & 1) title = [[NSProcessInfo processInfo] processName];
		TiegelMenu *menu = [[TiegelMenu alloc] initWithTitle:title];
		fill(&r, menu, 0);
		NSMenuItem *top = [[NSMenuItem alloc] initWithTitle:title action:nil keyEquivalent:@""];
		top.submenu = menu;
		[bar addItem:top];
		if (flags & 2) windows = menu;
	}
	if (r.bad) return nil;
	if (windows != nil) [NSApp setWindowsMenu:windows];
	return bar;
}

static void install(void) {
	if (menuBar != nil) [NSApp setMainMenu:menuBar];
}

void tiegel_set_menus(void *buf, size_t len) {
	dispatch_async(dispatch_get_main_queue(), ^{
		@autoreleasepool {
			static dispatch_once_t once;
			dispatch_once(&once, ^{
				menuTarget = [[TiegelMenuTarget alloc] init];
				// Re-apply after launch in case anything replaced the menu
				// while the application finished launching.
				[[NSNotificationCenter defaultCenter] addObserverForName:NSApplicationDidFinishLaunchingNotification
					object:nil queue:nil usingBlock:^(NSNotification *n) { install(); }];
			});
			[NSApplication sharedApplication];
			NSMenu *bar = build(buf, len);
			free(buf);
			if (bar == nil) return;
			menuBar = bar;
			install();
		}
	});
}

int tiegel_choose_folder(const char *title, const char *dir, char **out) {
	if ([NSThread isMainThread]) return -1;
	NSString *t = [NSString stringWithUTF8String:title];
	NSString *d = [NSString stringWithUTF8String:dir];
	__block char *result = NULL;
	dispatch_semaphore_t done = dispatch_semaphore_create(0);
	// Run from the run loop rather than the main dispatch queue so that
	// Gio's main-queue work keeps flowing while the panel is modal.
	CFRunLoopPerformBlock(CFRunLoopGetMain(), kCFRunLoopCommonModes, ^{
		@autoreleasepool {
			NSOpenPanel *p = [NSOpenPanel openPanel];
			p.canChooseDirectories = YES;
			p.canChooseFiles = NO;
			p.canCreateDirectories = YES;
			p.allowsMultipleSelection = NO;
			if (t.length > 0) {
				p.title = t;
				p.message = t;
			}
			BOOL isDir = NO;
			if (d.length > 0 && [[NSFileManager defaultManager] fileExistsAtPath:d isDirectory:&isDir] && isDir) {
				p.directoryURL = [NSURL fileURLWithPath:d isDirectory:YES];
			}
			if ([p runModal] == NSModalResponseOK && p.URLs.count > 0) {
				const char *s = p.URLs.firstObject.path.fileSystemRepresentation;
				if (s != NULL) result = strdup(s);
			}
		}
		dispatch_semaphore_signal(done);
	});
	CFRunLoopWakeUp(CFRunLoopGetMain());
	dispatch_semaphore_wait(done, DISPATCH_TIME_FOREVER);
	*out = result;
	return result != NULL ? 1 : 0;
}
