// The preview pane, natively: see preview_darwin.go.

#import <Cocoa/Cocoa.h>
#import <WebKit/WebKit.h>

#include "_cgo_export.h"

@interface APPreview : NSObject <WKScriptMessageHandler, WKNavigationDelegate>
@end

static WKWebView *apView = nil;
static APPreview *apDelegate = nil;
static NSString *apScript = nil;
static NSString *apPending = nil;   // a page asked for before the view was made
static NSTimeInterval apWindow = 0; // the second being counted, and how many came in it
static int apInWindow = 0;
static const NSUInteger apMaxField = 4000;
static const int apMaxPerSecond = 40;

static void apSend(NSDictionary *d) {
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
