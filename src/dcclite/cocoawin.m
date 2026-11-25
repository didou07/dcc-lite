//go:build darwin && native

#import <Cocoa/Cocoa.h>
#import <WebKit/WebKit.h>
#include <stdlib.h>
#include <string.h>

#include "cocoawin.h"

// Implemented in Go (native_darwin.go).
extern char *dccGoHandle(char *op, char *raw, char **err);
extern void dccGoMouse(char *kind, int x, int y);
extern void dccGoAlive(void);
extern void dccGoLog(char *level, char *text);

@class DCCVideoView;

static NSWindow *g_win;
static NSView *g_container;
static WKWebView *g_view;
static DCCVideoView *g_video;
static NSString *g_origin;
static volatile unsigned long g_video_ptr;
static volatile int g_view_w, g_view_h;
static BOOL g_cursor_hidden;
static double g_last_move;

static void dcc_log(const char *level, NSString *text) {
	dccGoLog((char *)level, (char *)[text UTF8String]);
}

static BOOL inside_uri(NSString *uri) {
	if (!uri || !g_origin || [g_origin length] == 0 || ![uri hasPrefix:g_origin]) return NO;
	if ([uri length] == [g_origin length]) return YES;
	unichar c = [uri characterAtIndex:[g_origin length]];
	return c == '/' || c == '?' || c == '#';
}

static BOOL is_web_link(NSString *uri) {
	return [uri hasPrefix:@"http://"] || [uri hasPrefix:@"https://"] || [uri hasPrefix:@"mailto:"];
}

static void open_outside(NSString *uri) {
	NSURL *u = [NSURL URLWithString:uri];
	if (!u || ![[NSWorkspace sharedWorkspace] openURL:u]) {
		dcc_log("warn", [NSString stringWithFormat:@"could not open the link in the default browser: %@", uri]);
	}
}

// Ends [NSApp run] cleanly (from the main thread).
static void stop_app(void) {
	if (g_cursor_hidden) { [NSCursor unhide]; g_cursor_hidden = NO; }
	[NSApp stop:nil];
	NSEvent *ev = [NSEvent otherEventWithType:NSEventTypeApplicationDefined location:NSZeroPoint modifierFlags:0
		timestamp:0 windowNumber:0 context:nil subtype:0 data1:0 data2:0];
	[NSApp postEvent:ev atStart:YES];
}

static void send_mouse(NSEvent *e, const char *kind) {
	NSPoint p = [g_container convertPoint:[e locationInWindow] fromView:nil];
	dccGoMouse((char *)kind, (int)p.x, (int)p.y);
}

@interface DCCVideoView : NSView
@end

@implementation DCCVideoView
- (BOOL)isOpaque { return YES; }
- (BOOL)acceptsFirstResponder { return NO; }
- (BOOL)acceptsFirstMouse:(NSEvent *)e { return YES; }
- (void)drawRect:(NSRect)r { [[NSColor blackColor] setFill]; NSRectFill(r); }
- (NSView *)hitTest:(NSPoint)p {
	if ([self isHidden]) return nil;
	NSPoint q = [self convertPoint:p fromView:[self superview]];
	return NSPointInRect(q, [self bounds]) ? self : nil;
}
- (void)updateTrackingAreas {
	for (NSTrackingArea *a in [[self trackingAreas] copy]) [self removeTrackingArea:a];
	[self addTrackingArea:[[NSTrackingArea alloc] initWithRect:[self bounds]
		options:NSTrackingMouseMoved | NSTrackingActiveInKeyWindow | NSTrackingInVisibleRect owner:self userInfo:nil]];
	[super updateTrackingAreas];
}
- (void)mouseDown:(NSEvent *)e {
	if ([e modifierFlags] & NSEventModifierFlagControl) send_mouse(e, "menu");
	else if ([e clickCount] == 2) send_mouse(e, "dbl");
}
- (void)rightMouseDown:(NSEvent *)e { send_mouse(e, "menu"); }
- (void)mouseMoved:(NSEvent *)e {
	double now = [[NSProcessInfo processInfo] systemUptime];
	if (now - g_last_move < 0.04) return; // at most 25 a second
	g_last_move = now;
	send_mouse(e, "move");
}
- (void)scrollWheel:(NSEvent *)e {
	double dy = -[e scrollingDeltaY];
	if (![e hasPreciseScrollingDeltas]) dy *= 40;
	if ((int)dy != 0) dccGoMouse((char *)"wheel", 0, (int)dy);
}
@end

@interface DCCContainer : NSView
@end

@implementation DCCContainer
- (BOOL)isFlipped { return YES; }
- (void)setFrameSize:(NSSize)s {
	[super setFrameSize:s];
	g_view_w = (int)s.width;
	g_view_h = (int)s.height;
}
@end

static NSString *unique_download_path(NSString *name) {
	NSString *dir = NSSearchPathForDirectoriesInDomains(NSDownloadsDirectory, NSUserDomainMask, YES).firstObject;
	if (!dir) dir = NSHomeDirectory();
	NSString *base = [name length] ? [name lastPathComponent] : @"download";
	NSString *stem = [base stringByDeletingPathExtension], *ext = [base pathExtension];
	NSString *path = [dir stringByAppendingPathComponent:base];
	for (int i = 1; [[NSFileManager defaultManager] fileExistsAtPath:path] && i < 1000; i++) {
		NSString *n = [ext length] ? [NSString stringWithFormat:@"%@ (%d).%@", stem, i, ext] : [NSString stringWithFormat:@"%@ (%d)", stem, i];
		path = [dir stringByAppendingPathComponent:n];
	}
	return path;
}

@interface DCCDelegate : NSObject <NSApplicationDelegate, NSWindowDelegate, WKNavigationDelegate, WKUIDelegate, WKDownloadDelegate, WKScriptMessageHandlerWithReply>
@end

@implementation DCCDelegate

- (NSApplicationTerminateReply)applicationShouldTerminate:(NSApplication *)sender {
	stop_app();
	return NSTerminateCancel;
}

- (void)windowWillClose:(NSNotification *)n { stop_app(); }

- (void)userContentController:(WKUserContentController *)ucc didReceiveScriptMessage:(WKScriptMessage *)message replyHandler:(void (^)(id, NSString *))reply {
	if (![[message frameInfo] isMainFrame] || !inside_uri([[[message webView] URL] absoluteString])) {
		reply(nil, @"not allowed from this page");
		return;
	}
	if (![[message body] isKindOfClass:[NSString class]]) {
		reply(nil, @"bad request");
		return;
	}
	NSString *body = [message body];
	NSRange nl = [body rangeOfString:@"\n"];
	if (nl.location == NSNotFound) {
		reply(nil, @"bad request");
		return;
	}
	NSString *op = [body substringToIndex:nl.location];
	NSString *raw = [body substringFromIndex:nl.location + 1];
	char *err = NULL;
	char *out = dccGoHandle((char *)[op UTF8String], (char *)[raw UTF8String], &err);
	if (err) {
		reply(nil, [NSString stringWithUTF8String:err] ?: @"error");
		free(err);
	} else {
		NSString *s = out ? [NSString stringWithUTF8String:out] : nil;
		reply(s ?: @"{}", nil);
	}
	free(out);
}

- (void)webView:(WKWebView *)wv decidePolicyForNavigationAction:(WKNavigationAction *)action decisionHandler:(void (^)(WKNavigationActionPolicy))handler {
	NSString *uri = [[[action request] URL] absoluteString];
	if ([action shouldPerformDownload]) { handler(WKNavigationActionPolicyDownload); return; }
	if (!uri) { handler(WKNavigationActionPolicyAllow); return; }
	if ([action targetFrame] == nil) {
		handler(WKNavigationActionPolicyCancel);
		if (inside_uri(uri)) [wv loadRequest:[action request]];
		else if (is_web_link(uri)) open_outside(uri);
		return;
	}
	if (inside_uri(uri) || [uri hasPrefix:@"blob:"] || [uri hasPrefix:@"data:"] || [uri hasPrefix:@"about:"]) {
		handler(WKNavigationActionPolicyAllow);
		return;
	}
	if ([action navigationType] == WKNavigationTypeLinkActivated) {
		handler(WKNavigationActionPolicyCancel);
		if (is_web_link(uri)) open_outside(uri);
		return;
	}
	handler(WKNavigationActionPolicyAllow);
}

- (void)webView:(WKWebView *)wv decidePolicyForNavigationResponse:(WKNavigationResponse *)resp decisionHandler:(void (^)(WKNavigationResponsePolicy))handler {
	NSURLResponse *r = [resp response];
	NSString *cd = [r isKindOfClass:[NSHTTPURLResponse class]] ? [(NSHTTPURLResponse *)r valueForHTTPHeaderField:@"Content-Disposition"] : nil;
	if (![resp canShowMIMEType] || (cd && [[cd lowercaseString] hasPrefix:@"attachment"])) handler(WKNavigationResponsePolicyDownload);
	else handler(WKNavigationResponsePolicyAllow);
}

- (void)webView:(WKWebView *)wv navigationAction:(WKNavigationAction *)a didBecomeDownload:(WKDownload *)d { [d setDelegate:self]; }
- (void)webView:(WKWebView *)wv navigationResponse:(WKNavigationResponse *)r didBecomeDownload:(WKDownload *)d { [d setDelegate:self]; }

- (void)download:(WKDownload *)d decideDestinationUsingResponse:(NSURLResponse *)resp suggestedFilename:(NSString *)name completionHandler:(void (^)(NSURL *))handler {
	NSString *path = unique_download_path(name);
	dcc_log("info", [NSString stringWithFormat:@"download saved to %@", path]);
	handler([NSURL fileURLWithPath:path]);
}

- (void)download:(WKDownload *)d didFailWithError:(NSError *)err resumeData:(NSData *)data {
	dcc_log("warn", [NSString stringWithFormat:@"a download failed: %@", [err localizedDescription]]);
}

- (WKWebView *)webView:(WKWebView *)wv createWebViewWithConfiguration:(WKWebViewConfiguration *)c forNavigationAction:(WKNavigationAction *)a windowFeatures:(WKWindowFeatures *)f {
	NSString *uri = [[[a request] URL] absoluteString];
	if (uri && ([uri hasPrefix:@"http://"] || [uri hasPrefix:@"https://"]) && !inside_uri(uri)) open_outside(uri);
	return nil; // no new window is ever made
}

- (void)webView:(WKWebView *)wv didFailProvisionalNavigation:(WKNavigation *)n withError:(NSError *)err {
	dcc_log("warn", [NSString stringWithFormat:@"the page failed to load: %@", [err localizedDescription]]);
}

- (void)webViewWebContentProcessDidTerminate:(WKWebView *)wv {
	dcc_log("warn", @"the web process ended; reloading the page");
	[wv reload];
}


- (void)webView:(WKWebView *)wv runJavaScriptAlertPanelWithMessage:(NSString *)message initiatedByFrame:(WKFrameInfo *)f completionHandler:(void (^)(void))handler {
	NSAlert *a = [[NSAlert alloc] init];
	[a setMessageText:message ?: @""];
	[a addButtonWithTitle:@"OK"];
	[a runModal];
	handler();
}

- (void)webView:(WKWebView *)wv runJavaScriptConfirmPanelWithMessage:(NSString *)message initiatedByFrame:(WKFrameInfo *)f completionHandler:(void (^)(BOOL))handler {
	NSAlert *a = [[NSAlert alloc] init];
	[a setMessageText:message ?: @""];
	[a addButtonWithTitle:@"OK"];
	[a addButtonWithTitle:@"Cancel"];
	handler([a runModal] == NSAlertFirstButtonReturn);
}

- (void)webView:(WKWebView *)wv runJavaScriptTextInputPanelWithPrompt:(NSString *)prompt defaultText:(NSString *)def initiatedByFrame:(WKFrameInfo *)f completionHandler:(void (^)(NSString *))handler {
	NSAlert *a = [[NSAlert alloc] init];
	[a setMessageText:prompt ?: @""];
	[a addButtonWithTitle:@"OK"];
	[a addButtonWithTitle:@"Cancel"];
	NSTextField *field = [[NSTextField alloc] initWithFrame:NSMakeRect(0, 0, 320, 24)];
	[field setStringValue:def ?: @""];
	[a setAccessoryView:field];
	handler([a runModal] == NSAlertFirstButtonReturn ? [field stringValue] : nil);
}

- (void)webView:(WKWebView *)wv runOpenPanelWithParameters:(WKOpenPanelParameters *)p initiatedByFrame:(WKFrameInfo *)f completionHandler:(void (^)(NSArray<NSURL *> *))handler {
	NSOpenPanel *op = [NSOpenPanel openPanel];
	[op setAllowsMultipleSelection:[p allowsMultipleSelection]];
	[op setCanChooseDirectories:[p allowsDirectories]];
	[op beginSheetModalForWindow:g_win completionHandler:^(NSModalResponse r) {
		handler(r == NSModalResponseOK ? [op URLs] : nil);
	}];
}
@end

static DCCDelegate *g_delegate;

void dcc_cocoa_box(int x, int y, int w, int h, int show) {
	dispatch_async(dispatch_get_main_queue(), ^{
		if (!g_video) return;
		int bw = w < 2 ? 2 : w, bh = h < 2 ? 2 : h;
		[g_video setFrame:NSMakeRect(x < 0 ? 0 : x, y < 0 ? 0 : y, bw, bh)];
		[g_video setHidden:!show];
	});
}

void dcc_cocoa_fullscreen(int on) {
	dispatch_async(dispatch_get_main_queue(), ^{
		if (!g_win) return;
		BOOL full = ([g_win styleMask] & NSWindowStyleMaskFullScreen) != 0;
		if ((on != 0) != full) [g_win toggleFullScreen:nil];
	});
}

void dcc_cocoa_cursor(int hide) {
	dispatch_async(dispatch_get_main_queue(), ^{
		if (hide && !g_cursor_hidden) { [NSCursor hide]; g_cursor_hidden = YES; }
		else if (!hide && g_cursor_hidden) { [NSCursor unhide]; g_cursor_hidden = NO; }
	});
}

void dcc_cocoa_quit(void) {
	dispatch_async(dispatch_get_main_queue(), ^{ stop_app(); });
}

void dcc_cocoa_ping(void) {
	dispatch_async(dispatch_get_main_queue(), ^{ dccGoAlive(); });
}

unsigned long dcc_cocoa_video_view(void) { return g_video_ptr; }
void dcc_cocoa_view_size(int *w, int *h) { *w = g_view_w; *h = g_view_h; }

static NSString *js_quote(NSString *s) {
	NSString *t = [s stringByReplacingOccurrencesOfString:@"\\" withString:@"\\\\"];
	t = [t stringByReplacingOccurrencesOfString:@"\"" withString:@"\\\""];
	return [NSString stringWithFormat:@"\"%@\"", t];
}

static void add_item(NSMenu *m, NSString *title, SEL action, NSString *key, NSEventModifierFlags mods) {
	NSMenuItem *it = [m addItemWithTitle:title action:action keyEquivalent:key];
	if (mods) [it setKeyEquivalentModifierMask:mods];
}

static void build_menus(void) {
	NSMenu *bar = [[NSMenu alloc] init];
	NSMenuItem *appItem = [[NSMenuItem alloc] init];
	[bar addItem:appItem];
	NSMenu *app = [[NSMenu alloc] initWithTitle:@"DCC Lite"];
	add_item(app, @"Hide DCC Lite", @selector(hide:), @"h", 0);
	[app addItem:[NSMenuItem separatorItem]];
	add_item(app, @"Quit DCC Lite", @selector(terminate:), @"q", 0);
	[appItem setSubmenu:app];

	NSMenuItem *editItem = [[NSMenuItem alloc] init];
	[bar addItem:editItem];
	NSMenu *edit = [[NSMenu alloc] initWithTitle:@"Edit"];
	add_item(edit, @"Undo", @selector(undo:), @"z", 0);
	add_item(edit, @"Redo", @selector(redo:), @"z", NSEventModifierFlagCommand | NSEventModifierFlagShift);
	[edit addItem:[NSMenuItem separatorItem]];
	add_item(edit, @"Cut", @selector(cut:), @"x", 0);
	add_item(edit, @"Copy", @selector(copy:), @"c", 0);
	add_item(edit, @"Paste", @selector(paste:), @"v", 0);
	add_item(edit, @"Select All", @selector(selectAll:), @"a", 0);
	[editItem setSubmenu:edit];

	NSMenuItem *winItem = [[NSMenuItem alloc] init];
	[bar addItem:winItem];
	NSMenu *win = [[NSMenu alloc] initWithTitle:@"Window"];
	add_item(win, @"Minimize", @selector(performMiniaturize:), @"m", 0);
	add_item(win, @"Enter Full Screen", @selector(toggleFullScreen:), @"f", NSEventModifierFlagCommand | NSEventModifierFlagControl);
	[winItem setSubmenu:win];
	[NSApp setWindowsMenu:win];
	[NSApp setMainMenu:bar];
}

int dcc_cocoa_run(const char *url, const char *origin, const char *datadir, const char *title, int w, int h) {
	@autoreleasepool {
		[NSApplication sharedApplication];
		[NSApp setActivationPolicy:NSApplicationActivationPolicyRegular];
		g_delegate = [[DCCDelegate alloc] init];
		[NSApp setDelegate:g_delegate];
		build_menus();
		g_origin = [NSString stringWithUTF8String:origin];

		WKWebViewConfiguration *cfg = [[WKWebViewConfiguration alloc] init];
		WKUserContentController *ucc = [[WKUserContentController alloc] init];
		[ucc addScriptMessageHandlerWithReply:g_delegate contentWorld:[WKContentWorld pageWorld] name:@"dccLive"];
		NSString *q = js_quote(g_origin);
		NSString *bridge = [NSString stringWithFormat:
			@"window.__dccGui = %@;\n"
			@"if (location.origin === %@) {\n"
			@"  window.dccLive = function (op, args) {\n"
			@"    return window.webkit.messageHandlers.dccLive.postMessage(op + '\\n' + (args || '{}'));\n"
			@"  };\n"
			@"}\n"
			@"window.dccLiveGtk = true;\n", q, q];
		[ucc addUserScript:[[WKUserScript alloc] initWithSource:bridge injectionTime:WKUserScriptInjectionTimeAtDocumentStart forMainFrameOnly:YES]];
		[cfg setUserContentController:ucc];
		if (getenv("DCC_MAC_DEVTOOLS")) [[cfg preferences] setValue:@YES forKey:@"developerExtrasEnabled"];

		NSRect frame = NSMakeRect(0, 0, w, h);
		g_view = [[WKWebView alloc] initWithFrame:frame configuration:cfg];
		if (!g_view) return -2;
		[g_view setNavigationDelegate:g_delegate];
		[g_view setUIDelegate:g_delegate];
		[g_view setAutoresizingMask:NSViewWidthSizable | NSViewHeightSizable];

		g_container = [[DCCContainer alloc] initWithFrame:frame];
		[g_container addSubview:g_view];
		g_video = [[DCCVideoView alloc] initWithFrame:NSMakeRect(0, 0, 2, 2)];
		[g_video setWantsLayer:YES];
		[[g_video layer] setBackgroundColor:[[NSColor blackColor] CGColor]];
		[g_video setHidden:YES]; // out of sight until the page asks for it
		[g_container addSubview:g_video positioned:NSWindowAbove relativeTo:g_view];
		g_video_ptr = (unsigned long)(__bridge void *)g_video;

		g_win = [[NSWindow alloc] initWithContentRect:frame
			styleMask:NSWindowStyleMaskTitled | NSWindowStyleMaskClosable | NSWindowStyleMaskMiniaturizable | NSWindowStyleMaskResizable
			backing:NSBackingStoreBuffered defer:NO];
		[g_win setReleasedWhenClosed:NO];
		[g_win setTitle:[NSString stringWithUTF8String:title]];
		[g_win setMinSize:NSMakeSize(960, 620)];
		[g_win setCollectionBehavior:NSWindowCollectionBehaviorFullScreenPrimary];
		[g_win setAcceptsMouseMovedEvents:YES];
		[g_win setDelegate:g_delegate];
		[g_win setContentView:g_container];
		[g_win center];
		g_view_w = (int)[g_container bounds].size.width;
		g_view_h = (int)[g_container bounds].size.height;

		[g_view loadRequest:[NSURLRequest requestWithURL:[NSURL URLWithString:[NSString stringWithUTF8String:url]]]];
		[g_win makeKeyAndOrderFront:nil];
		[NSApp activateIgnoringOtherApps:YES];
		dcc_log("info", [NSString stringWithFormat:@"picture view ready (%p)", (__bridge void *)g_video]);
		[NSApp run];
		g_win = nil;
		g_video = nil;
		g_video_ptr = 0;
		g_view = nil;
	}
	return 0;
}
