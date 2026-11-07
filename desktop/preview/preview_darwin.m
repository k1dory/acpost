// The preview pane, natively: see preview_darwin.go.

#import <Cocoa/Cocoa.h>
#import <WebKit/WebKit.h>

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

void apIDEShow(void *nswindow, double x, double y, double w, double h, int focus) {
	dispatch_async(dispatch_get_main_queue(), ^{
		NSWindow *win = (__bridge NSWindow *)nswindow;
		NSView *host = win.contentView;
		if (!host) return;
		if (!apIDE) {
			WKWebViewConfiguration *cfg = [WKWebViewConfiguration new];
			apIDE = [[WKWebView alloc] initWithFrame:NSZeroRect configuration:cfg];
			if ([apIDE respondsToSelector:@selector(setInspectable:)]) [apIDE setValue:@YES forKey:@"inspectable"];
			if (apIDEPending) {
				NSURL *nu = [NSURL URLWithString:apIDEPending];
				apIDEPending = nil;
				if (nu) [apIDE loadRequest:[NSURLRequest requestWithURL:nu]];
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
		NSURL *nu = [NSURL URLWithString:u];
		if (nu) [apIDE loadRequest:[NSURLRequest requestWithURL:nu]];
	});
}
