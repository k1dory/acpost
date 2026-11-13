// The preview pane, natively: see preview_darwin.go.

#import <Cocoa/Cocoa.h>
#import <WebKit/WebKit.h>
#include <arpa/inet.h>

#include "_cgo_export.h"

@interface APPreview : NSObject <WKScriptMessageHandler, WKNavigationDelegate>
@end

static WKWebView *apView = nil;
static WKWebView *apIDE = nil;     // the IDE pane (VS Code's own web server): no collector, its own page
static NSString *apIDEPending = nil;
static APPreview *apDelegate = nil;
static NSString *apScript = nil;
static NSString *apPending = nil;   // a page asked for before the view was made
static NSTimeInterval apWindow = 0; // the second being counted, and how many came in it
static int apInWindow = 0;
static const NSUInteger apMaxField = 4000;
static const int apMaxPerSecond = 40;

// apSend hands an event to Go: every string in it cut to apMaxField (whatever made it: a page's
// report, or WebKit's navigation with a page's title).
static void apSend(NSDictionary *in) {
	NSMutableDictionary *d = [NSMutableDictionary dictionaryWithCapacity:in.count];
	for (id k in in) {
		id v = in[k];
		if ([v isKindOfClass:[NSString class]] && [(NSString *)v length] > apMaxField) v = [(NSString *)v substringToIndex:apMaxField];
		d[k] = v;
	}
	if (![NSJSONSerialization isValidJSONObject:d]) return;
	NSData *j = [NSJSONSerialization dataWithJSONObject:d options:0 error:nil];
	if (!j) return;
	NSString *s = [[NSString alloc] initWithData:j encoding:NSUTF8StringEncoding];
	goPreviewEvent((char *)[s UTF8String]);
}

@implementation APPreview
// What a page may report (anything else it posts is dropped: navigation is told by WebKit, not
// the page); its fields, cut; at most apMaxPerSecond a second.
- (void)userContentController:(WKUserContentController *)c didReceiveScriptMessage:(WKScriptMessage *)m {
	static NSSet *kinds = nil;
	if (!kinds) kinds = [NSSet setWithArray:@[@"exception", @"rejection", @"console.error", @"console.warn", @"resource", @"fetch", @"xhr", @"csp"]];
	static NSSet *fields = nil;
	if (!fields) fields = [NSSet setWithArray:@[@"kind", @"message", @"source", @"line", @"col", @"stack", @"url", @"at"]];
	if (![m.body isKindOfClass:[NSDictionary class]]) return;
	NSDictionary *b = m.body;
	if (![b[@"kind"] isKindOfClass:[NSString class]] || ![kinds containsObject:b[@"kind"]]) return;
	NSTimeInterval now = [NSDate timeIntervalSinceReferenceDate];
	if (now - apWindow >= 1) { apWindow = now; apInWindow = 0; }
	if (++apInWindow > apMaxPerSecond) return;
	NSMutableDictionary *x = [NSMutableDictionary dictionary];
	for (NSString *k in fields) {
		id v = b[k];
		if ([v isKindOfClass:[NSString class]]) x[k] = [(NSString *)v length] > apMaxField ? [(NSString *)v substringToIndex:apMaxField] : v;
		else if ([v isKindOfClass:[NSNumber class]]) x[k] = v;
	}
	x[@"main_frame"] = @(m.frameInfo.isMainFrame);
	apSend(x);
}
- (void)webView:(WKWebView *)w didFinishNavigation:(WKNavigation *)n {
	apSend(@{@"kind": @"nav", @"url": w.URL.absoluteString ?: @"", @"title": w.title ?: @""});
}
- (void)webView:(WKWebView *)w didFailProvisionalNavigation:(WKNavigation *)n withError:(NSError *)e {
	apSend(@{@"kind": @"load", @"message": e.localizedDescription ?: @"", @"url": [e.userInfo[NSURLErrorFailingURLErrorKey] absoluteString] ?: @""});
}
- (void)webView:(WKWebView *)w didFailNavigation:(WKNavigation *)n withError:(NSError *)e {
	apSend(@{@"kind": @"load", @"message": e.localizedDescription ?: @"", @"url": w.URL.absoluteString ?: @""});
}
@end

void apInit(const char *script) {
	NSString *s = [NSString stringWithUTF8String:script];
	dispatch_async(dispatch_get_main_queue(), ^{ apScript = s; });
}

static void apEnsure(NSView *host) {
	if (!apView) {
		WKWebViewConfiguration *cfg = [WKWebViewConfiguration new];
		WKUserContentController *uc = [WKUserContentController new];
		apDelegate = [APPreview new];
		[uc addScriptMessageHandler:apDelegate name:@"acpostPreview"];
		[uc addUserScript:[[WKUserScript alloc] initWithSource:apScript ?: @"" injectionTime:WKUserScriptInjectionTimeAtDocumentStart forMainFrameOnly:NO]];
		cfg.userContentController = uc;
		// Sites looked at leave no cookies or storage behind: the default store is the app's, shared with the IDE pane.
		cfg.websiteDataStore = [WKWebsiteDataStore nonPersistentDataStore];
		apView = [[WKWebView alloc] initWithFrame:NSZeroRect configuration:cfg];
		apView.navigationDelegate = apDelegate;
		if ([apView respondsToSelector:@selector(setInspectable:)]) [apView setValue:@YES forKey:@"inspectable"]; // Safari's Web Inspector (macOS 13.3+)
	}
	if (apPending) {
		NSURL *nu = [NSURL URLWithString:apPending];
		apPending = nil;
		if (nu) [apView loadRequest:[NSURLRequest requestWithURL:nu]];
	}
	if (apView.superview != host) {
		[apView removeFromSuperview];
		[host addSubview:apView positioned:NSWindowAbove relativeTo:nil];
	}
}

void apShow(void *nswindow, double x, double y, double w, double h) {
	dispatch_async(dispatch_get_main_queue(), ^{
		NSWindow *win = (__bridge NSWindow *)nswindow;
		NSView *host = win.contentView;
		if (!host) return;
		apEnsure(host);
		CGFloat H = host.bounds.size.height;
		apView.frame = NSMakeRect(x, host.isFlipped ? y : H - y - h, w, h);
		apView.hidden = NO;
	});
}

void apHide(void) {
	dispatch_async(dispatch_get_main_queue(), ^{ if (apView) apView.hidden = YES; });
}

void apLoad(const char *url) {
	NSString *u = [NSString stringWithUTF8String:url];
	dispatch_async(dispatch_get_main_queue(), ^{
		if (!apView) { apPending = u; return; } // loaded once the view is made
		NSURL *nu = [NSURL URLWithString:u];
		if (nu) [apView loadRequest:[NSURLRequest requestWithURL:nu]];
	});
}

void apReload(void) { dispatch_async(dispatch_get_main_queue(), ^{ [apView reload]; }); }
void apBack(void) { dispatch_async(dispatch_get_main_queue(), ^{ if (apView.canGoBack) [apView goBack]; }); }

// ---- The app's own page never navigates away ----
// Wails (v3.1, macOS) lets its webview go wherever a link, a form or window.open sends it: the
// app would become that site. A proxy in front of its navigation delegate cancels navigations of
// the main frame (or a new window) to anything but the app's own origin, and hands the address to
// Go, which opens it where it belongs; everything else goes to Wails' delegate as before.

@interface APNavGuard : NSProxy <WKNavigationDelegate>
@property (strong) id inner;
@end

static BOOL apSameOrigin(NSURL *a, NSURL *b) {
	if (!a || !b) return NO;
	return [a.scheme isEqualToString:b.scheme] && ((!a.host && !b.host) || [a.host isEqualToString:b.host]) && ((!a.port && !b.port) || [a.port isEqual:b.port]);
}

static BOOL apVeto(WKWebView *w, WKNavigationAction *a) {
	BOOL main = a.targetFrame == nil || a.targetFrame.isMainFrame;
	NSURL *u = a.request.URL;
	if (!main || !u) return NO;
	if ([u.scheme isEqualToString:@"about"] || apSameOrigin(u, w.URL)) return NO;
	if (!w.URL) return NO; // the first load: the app's own page
	goNavigation((char *)[u.absoluteString UTF8String]);
	return YES;
}

@implementation APNavGuard
- (instancetype)initWithInner:(id)inner { _inner = inner; return self; }
- (BOOL)respondsToSelector:(SEL)s {
	if (s == @selector(webView:decidePolicyForNavigationAction:decisionHandler:) ||
	    s == @selector(webView:decidePolicyForNavigationAction:preferences:decisionHandler:)) return YES;
	return [_inner respondsToSelector:s];
}
- (BOOL)conformsToProtocol:(Protocol *)p { return [_inner conformsToProtocol:p]; }
- (NSMethodSignature *)methodSignatureForSelector:(SEL)s { return [_inner methodSignatureForSelector:s]; }
- (void)forwardInvocation:(NSInvocation *)inv { [inv invokeWithTarget:_inner]; }
- (void)webView:(WKWebView *)w decidePolicyForNavigationAction:(WKNavigationAction *)a decisionHandler:(void (^)(WKNavigationActionPolicy))h {
	if (apVeto(w, a)) { h(WKNavigationActionPolicyCancel); return; }
	if ([_inner respondsToSelector:_cmd]) [_inner webView:w decidePolicyForNavigationAction:a decisionHandler:h];
	else h(WKNavigationActionPolicyAllow);
}
- (void)webView:(WKWebView *)w decidePolicyForNavigationAction:(WKNavigationAction *)a preferences:(WKWebpagePreferences *)p decisionHandler:(void (^)(WKNavigationActionPolicy, WKWebpagePreferences *))h {
	if (apVeto(w, a)) { h(WKNavigationActionPolicyCancel, p); return; }
	if ([_inner respondsToSelector:_cmd]) [_inner webView:w decidePolicyForNavigationAction:a preferences:p decisionHandler:h];
	else if ([_inner respondsToSelector:@selector(webView:decidePolicyForNavigationAction:decisionHandler:)])
		[_inner webView:w decidePolicyForNavigationAction:a decisionHandler:^(WKNavigationActionPolicy pol) { h(pol, p); }];
	else h(WKNavigationActionPolicyAllow, p);
}
@end

static APNavGuard *apGuard = nil;

static WKWebView *apFindWebView(NSView *v) {
	if ([v isKindOfClass:[WKWebView class]] && v != apView && v != (NSView *)apIDE) return (WKWebView *)v;
	for (NSView *s in v.subviews) { WKWebView *w = apFindWebView(s); if (w) return w; }
	return nil;
}

void apGuardNavigation(void *nswindow) {
	dispatch_async(dispatch_get_main_queue(), ^{
		NSWindow *win = (__bridge NSWindow *)nswindow;
		WKWebView *w = apFindWebView(win.contentView);
		if (!w || [(id)w.navigationDelegate isKindOfClass:[APNavGuard class]] || w.navigationDelegate == (id)apGuard) return;
		apGuard = [[APNavGuard alloc] initWithInner:w.navigationDelegate];
		w.navigationDelegate = (id<WKNavigationDelegate>)apGuard;
	});
}


// ---- The IDE pane ----
// VS Code (code serve-web, on 127.0.0.1) in a view of its own over the window, where the
// interface keeps the IDE's box. It is a page like any: keys, clipboard, its storage persist with
// the app's (WebKit's default store for the bundle).

// Scripts put into VS Code's page — at its start (English, the app's look linked in) and at its
// end (the app's settings for it) — written by the Go side (ide_theme.go).
static NSString *apIDEStart = nil, *apIDESeed = nil; // set by the Go side before the pane is made

void apIDESetScripts(const char *start, const char *end) {
	NSString *a = [NSString stringWithUTF8String:start], *b = [NSString stringWithUTF8String:end];
	dispatch_async(dispatch_get_main_queue(), ^{ apIDEStart = a; apIDESeed = b; });
}

static void apIDELoadURL(NSString *u) {
	NSURL *nu = [NSURL URLWithString:u];
	if (!nu) return;
	// VS Code's server picks the language by the request: English (the web version's other
	// languages come half translated).
	NSMutableURLRequest *rq = [NSMutableURLRequest requestWithURL:nu];
	[rq setValue:@"en-US,en;q=0.9" forHTTPHeaderField:@"Accept-Language"];
	// VS Code's server takes its language cookie first (one it set for another language would
	// win over the header): set to English before the page is asked for.
	NSHTTPCookie *c = [NSHTTPCookie cookieWithProperties:@{
		NSHTTPCookieName: @"vscode.nls.locale", NSHTTPCookieValue: @"en", NSHTTPCookiePath: @"/",
		NSHTTPCookieDomain: nu.host ?: @"127.0.0.1", NSHTTPCookieExpires: [NSDate dateWithTimeIntervalSinceNow:400 * 24 * 3600]}];
	WKWebView *v = apIDE;
	if (!c) { [v loadRequest:rq]; return; }
	[v.configuration.websiteDataStore.httpCookieStore setCookie:c completionHandler:^{ [v loadRequest:rq]; }];
}

static int apIDEDark = -1; // the app's theme for the pane (-1: not said yet)

void apIDEAppearance(int dark) {
	dispatch_async(dispatch_get_main_queue(), ^{
		apIDEDark = dark;
		if (apIDE) apIDE.appearance = [NSAppearance appearanceNamed:dark ? NSAppearanceNameDarkAqua : NSAppearanceNameAqua];
	});
}

void apIDEShow(void *nswindow, double x, double y, double w, double h, int focus) {
	dispatch_async(dispatch_get_main_queue(), ^{
		NSWindow *win = (__bridge NSWindow *)nswindow;
		NSView *host = win.contentView;
		if (!host) return;
		if (!apIDE) {
			WKWebViewConfiguration *cfg = [WKWebViewConfiguration new];
			WKUserContentController *uc = [WKUserContentController new];
			if (apIDEStart) [uc addUserScript:[[WKUserScript alloc] initWithSource:apIDEStart injectionTime:WKUserScriptInjectionTimeAtDocumentStart forMainFrameOnly:YES]];
			if (apIDESeed) [uc addUserScript:[[WKUserScript alloc] initWithSource:apIDESeed injectionTime:WKUserScriptInjectionTimeAtDocumentEnd forMainFrameOnly:YES]];
			cfg.userContentController = uc;
			apIDE = [[WKWebView alloc] initWithFrame:NSZeroRect configuration:cfg];
			[apIDE setValue:@NO forKey:@"drawsBackground"]; // the app's colour under it until VS Code paints
			if (apIDEDark >= 0) apIDE.appearance = [NSAppearance appearanceNamed:apIDEDark ? NSAppearanceNameDarkAqua : NSAppearanceNameAqua];
			if ([apIDE respondsToSelector:@selector(setInspectable:)]) [apIDE setValue:@YES forKey:@"inspectable"];
			if (apIDEPending) {
				NSString *u = apIDEPending;
				apIDEPending = nil;
				apIDELoadURL(u);
			}
		}
		if (apIDE.superview != host) {
			[apIDE removeFromSuperview];
			[host addSubview:apIDE positioned:NSWindowAbove relativeTo:nil];
		}
		CGFloat H = host.bounds.size.height;
		apIDE.frame = NSMakeRect(x, host.isFlipped ? y : H - y - h, w, h);
		apIDE.hidden = NO;
		if (focus) [win makeFirstResponder:apIDE]; // only when opened, not each time it is placed
	});
}

void apIDEHide(void) {
	dispatch_async(dispatch_get_main_queue(), ^{ if (apIDE) apIDE.hidden = YES; });
}

void apIDELoad(const char *url) {
	NSString *u = [NSString stringWithUTF8String:url];
	dispatch_async(dispatch_get_main_queue(), ^{
		if (!apIDE) { apIDEPending = u; return; }
		apIDELoadURL(u);
	});
}

void apIDEReload(void) { dispatch_async(dispatch_get_main_queue(), ^{ if (apIDE) [apIDE reload]; }); }

// ---- Modules: pages of the person's own choosing, beside the chat ----
// Each module has a pane of its own, made when first shown and kept (its page stays as it was left
// when another view is shown). A page like any in a browser: none of the app's own calls reach it
// (no message handler, no script of the app's), its storage is WebKit's default store.
static NSMutableDictionary<NSString *, WKWebView *> *apMods = nil;
static NSMutableDictionary<NSString *, NSString *> *apModURL = nil; // the address each was opened at

static NSString *apModOf(WKWebView *w) {
	for (NSString *k in apMods) if (apMods[k] == w) return k;
	return nil;
}

// What a page of a browser has and a bare web view has not: its dialogs (alert, confirm, prompt),
// choosing files to upload, and a link meant for a new window (opened in the pane itself). And
// what became of a load, told to the interface (a page that did not load is a blank pane).
@interface APModule : NSObject <WKNavigationDelegate, WKUIDelegate>
@end

static void apModTell(WKWebView *w, NSString *kind, NSString *message) {
	NSString *mid = apModOf(w);
	if (!mid) return;
	if (message.length > apMaxField) message = [message substringToIndex:apMaxField];
	goModuleEvent((char *)[mid UTF8String], (char *)[kind UTF8String], (char *)[(message ?: @"") UTF8String]);
}

// apLocalHost is whether a plain http page may be read from host: this Mac or a numbered address
// of a private network (10.x, 172.16-31.x, 192.168.x, 127.x), as modules.go's localHost has it.
static BOOL apLocalHost(NSString *host) {
	if (!host.length) return NO;
	if ([host caseInsensitiveCompare:@"localhost"] == NSOrderedSame) return YES;
	struct in_addr a4;
	if (inet_pton(AF_INET, host.UTF8String, &a4) == 1) {
		uint32_t a = ntohl(a4.s_addr);
		return (a >> 24) == 127 || (a >> 24) == 10 || (a >> 20) == 0xAC1 || (a >> 16) == 0xC0A8;
	}
	struct in6_addr a6;
	return inet_pton(AF_INET6, host.UTF8String, &a6) == 1 && IN6_IS_ADDR_LOOPBACK(&a6);
}

@implementation APModule
// A plain http page is read only from this Mac and its network: the transport security is opened
// to local addresses for that, and a page there may not lead the pane to a plain one elsewhere.
- (void)webView:(WKWebView *)w decidePolicyForNavigationAction:(WKNavigationAction *)a decisionHandler:(void (^)(WKNavigationActionPolicy))done {
	NSURL *u = a.request.URL;
	if ([u.scheme.lowercaseString isEqualToString:@"http"] && !apLocalHost(u.host)) {
		if (!a.targetFrame || a.targetFrame.isMainFrame) apModTell(w, @"load", @"plain http is for this Mac and its network; elsewhere https");
		done(WKNavigationActionPolicyCancel);
		return;
	}
	done(WKNavigationActionPolicyAllow);
}
- (void)webView:(WKWebView *)w didFinishNavigation:(WKNavigation *)n { apModTell(w, @"nav", @""); }
- (void)webView:(WKWebView *)w didFailProvisionalNavigation:(WKNavigation *)n withError:(NSError *)e {
	if (e.code == NSURLErrorCancelled) return; // another load took its place
	apModTell(w, @"load", e.localizedDescription);
}
- (void)webView:(WKWebView *)w didFailNavigation:(WKNavigation *)n withError:(NSError *)e {
	if (e.code == NSURLErrorCancelled) return;
	apModTell(w, @"load", e.localizedDescription);
}
- (WKWebView *)webView:(WKWebView *)w createWebViewWithConfiguration:(WKWebViewConfiguration *)c forNavigationAction:(WKNavigationAction *)a windowFeatures:(WKWindowFeatures *)f {
	NSString *s = a.request.URL.scheme.lowercaseString;
	if ([s isEqualToString:@"http"] || [s isEqualToString:@"https"]) [w loadRequest:a.request];
	return nil;
}
- (void)webView:(WKWebView *)w runJavaScriptAlertPanelWithMessage:(NSString *)m initiatedByFrame:(WKFrameInfo *)f completionHandler:(void (^)(void))done {
	NSAlert *al = [NSAlert new];
	al.messageText = w.URL.host ?: @"";
	al.informativeText = m ?: @"";
	if (w.window) [al beginSheetModalForWindow:w.window completionHandler:^(NSModalResponse r) { done(); }];
	else done();
}
- (void)webView:(WKWebView *)w runJavaScriptConfirmPanelWithMessage:(NSString *)m initiatedByFrame:(WKFrameInfo *)f completionHandler:(void (^)(BOOL))done {
	NSAlert *al = [NSAlert new];
	al.messageText = w.URL.host ?: @"";
	al.informativeText = m ?: @"";
	[al addButtonWithTitle:@"OK"];
	[al addButtonWithTitle:@"Cancel"];
	if (w.window) [al beginSheetModalForWindow:w.window completionHandler:^(NSModalResponse r) { done(r == NSAlertFirstButtonReturn); }];
	else done(NO);
}
- (void)webView:(WKWebView *)w runJavaScriptTextInputPanelWithPrompt:(NSString *)m defaultText:(NSString *)d initiatedByFrame:(WKFrameInfo *)f completionHandler:(void (^)(NSString *))done {
	NSAlert *al = [NSAlert new];
	al.messageText = w.URL.host ?: @"";
	al.informativeText = m ?: @"";
	NSTextField *tf = [[NSTextField alloc] initWithFrame:NSMakeRect(0, 0, 260, 24)];
	tf.stringValue = d ?: @"";
	al.accessoryView = tf;
	[al addButtonWithTitle:@"OK"];
	[al addButtonWithTitle:@"Cancel"];
	if (w.window) [al beginSheetModalForWindow:w.window completionHandler:^(NSModalResponse r) { done(r == NSAlertFirstButtonReturn ? tf.stringValue : nil); }];
	else done(nil);
}
- (void)webView:(WKWebView *)w runOpenPanelWithParameters:(WKOpenPanelParameters *)p initiatedByFrame:(WKFrameInfo *)f completionHandler:(void (^)(NSArray<NSURL *> *))done {
	NSOpenPanel *op = [NSOpenPanel openPanel];
	op.allowsMultipleSelection = p.allowsMultipleSelection;
	op.canChooseDirectories = p.allowsDirectories;
	op.canChooseFiles = YES;
	if (w.window) [op beginSheetModalForWindow:w.window completionHandler:^(NSModalResponse r) { done(r == NSModalResponseOK ? op.URLs : nil); }];
	else done(nil);
}
@end

static APModule *apModDelegate = nil;

void apModShow(void *nswindow, const char *cid, const char *curl, double x, double y, double w, double h, int dark) {
	NSString *mid = [NSString stringWithUTF8String:cid], *u = [NSString stringWithUTF8String:curl];
	dispatch_async(dispatch_get_main_queue(), ^{
		NSWindow *win = (__bridge NSWindow *)nswindow;
		NSView *host = win.contentView;
		if (!host) return;
		if (!apMods) { apMods = [NSMutableDictionary dictionary]; apModURL = [NSMutableDictionary dictionary]; }
		WKWebView *v = apMods[mid];
		BOOL made = NO;
		if (!v) {
			v = [[WKWebView alloc] initWithFrame:NSZeroRect configuration:[WKWebViewConfiguration new]];
			if ([v respondsToSelector:@selector(setInspectable:)]) [v setValue:@YES forKey:@"inspectable"];
			v.allowsBackForwardNavigationGestures = YES;
			if (!apModDelegate) apModDelegate = [APModule new];
			v.navigationDelegate = apModDelegate;
			v.UIDelegate = apModDelegate;
			apMods[mid] = v;
			made = YES;
		}
		v.appearance = [NSAppearance appearanceNamed:dark ? NSAppearanceNameDarkAqua : NSAppearanceNameAqua];
		for (NSString *k in apMods) if (apMods[k] != v) apMods[k].hidden = YES;
		if (v.superview != host) {
			[v removeFromSuperview];
			[host addSubview:v positioned:NSWindowAbove relativeTo:nil];
		}
		CGFloat H = host.bounds.size.height;
		v.frame = NSMakeRect(x, host.isFlipped ? y : H - y - h, w, h);
		v.hidden = NO;
		// Opened at its address once, and again when the address was changed (not each time shown).
		if (u.length && ![apModURL[mid] isEqualToString:u]) {
			NSURL *nu = [NSURL URLWithString:u];
			if (nu) { apModURL[mid] = u; [v loadRequest:[NSURLRequest requestWithURL:nu]]; }
		}
		if (made) [win makeFirstResponder:v];
	});
}

void apModHide(void) {
	dispatch_async(dispatch_get_main_queue(), ^{ for (NSString *k in apMods) apMods[k].hidden = YES; });
}

// Back at the module's own address (wherever its page went since).
void apModReload(const char *cid) {
	NSString *mid = [NSString stringWithUTF8String:cid];
	dispatch_async(dispatch_get_main_queue(), ^{
		WKWebView *v = apMods[mid];
		NSURL *nu = apModURL[mid] ? [NSURL URLWithString:apModURL[mid]] : nil;
		if (v && nu) [v loadRequest:[NSURLRequest requestWithURL:nu]];
	});
}

void apModForget(const char *cid) {
	NSString *mid = [NSString stringWithUTF8String:cid];
	dispatch_async(dispatch_get_main_queue(), ^{
		[apMods[mid] removeFromSuperview];
		[apMods removeObjectForKey:mid];
		[apModURL removeObjectForKey:mid];
	});
}

// ---- A menu of the system's ----
// Shown at x, y (window points from the top left of the content), over everything — the native
// panes too, which an HTML menu would be under. Blocks until it closes; the item chosen, or -1.
@interface APMenuTarget : NSObject
@property (nonatomic) NSInteger chosen;
- (void)pick:(NSMenuItem *)it;
@end
@implementation APMenuTarget
- (void)pick:(NSMenuItem *)it { self.chosen = it.tag; }
@end

int apPopup(void *nswindow, double x, double y, const char **titles, const char **keys, int n, int checked) {
	__block int out = -1;
	void (^run)(void) = ^{
		NSWindow *win = (__bridge NSWindow *)nswindow;
		NSView *host = win.contentView;
		if (!host) return;
		APMenuTarget *t = [APMenuTarget new];
		t.chosen = -1;
		NSMenu *m = [[NSMenu alloc] initWithTitle:@""];
		m.autoenablesItems = NO;
		for (int i = 0; i < n; i++) {
			NSMenuItem *it = [[NSMenuItem alloc] initWithTitle:[NSString stringWithUTF8String:titles[i]] action:@selector(pick:) keyEquivalent:@""];
			NSString *k = [NSString stringWithUTF8String:keys[i]];
			if (k.length) { // "I" with ⇧⌘, as the app's own key
				it.keyEquivalent = [k lowercaseString];
				it.keyEquivalentModifierMask = NSEventModifierFlagCommand | NSEventModifierFlagShift;
			}
			it.target = t;
			it.tag = i;
			it.state = i == checked ? NSControlStateValueOn : NSControlStateValueOff;
			[m addItem:it];
		}
		CGFloat H = host.bounds.size.height;
		NSPoint p = NSMakePoint(x, host.isFlipped ? y : H - y);
		[m popUpMenuPositioningItem:nil atLocation:p inView:host];
		out = (int)t.chosen;
	};
	if ([NSThread isMainThread]) run(); else dispatch_sync(dispatch_get_main_queue(), run);
	return out;
}
