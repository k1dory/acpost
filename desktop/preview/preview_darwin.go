//go:build darwin

// Package preview is the app's preview pane: a WKWebView of its own laid over the app's window
// (Wails gives a window one webview), showing a page — the agent's dev server — and reporting
// what goes wrong in it: exceptions with their stacks, rejected promises, console errors and
// warnings, resources and requests that failed, CSP violations, pages that did not load. A script
// the pane puts into every page (and frame) of it, and only there, collects them and hands them
// over a native channel, not the network.
package preview

/*
#cgo CFLAGS: -fobjc-arc
#cgo LDFLAGS: -framework Cocoa -framework WebKit
#include <stdlib.h>

void apInit(const char *script);
void apShow(void *nswindow, double x, double y, double w, double h);
void apHide(void);
void apLoad(const char *url);
void apReload(void);
void apBack(void);
void apGuardNavigation(void *nswindow);
void apIDEShow(void *nswindow, double x, double y, double w, double h, int focus);
void apIDEHide(void);
void apIDELoad(const char *url);
void apIDEReload(void);
void apIDEAppearance(int dark);
void apIDESetSeed(const char *script);
int apPopup(void *nswindow, double x, double y, const char **titles, const char **keys, int n, int checked);

*/
import "C"

import (
	"encoding/json"
	"errors"
	"net/url"
	"sync"
	"unsafe"
)

// Event is what the pane saw: a page loaded (kind "nav"), or something that went wrong.
type Event struct {
	Kind      string  `json:"kind"` // nav, load, exception, rejection, console.error, console.warn, resource, fetch, xhr, csp
	Message   string  `json:"message,omitempty"`
	Source    string  `json:"source,omitempty"`
	Line      float64 `json:"line,omitempty"`
	Col       float64 `json:"col,omitempty"`
	Stack     string  `json:"stack,omitempty"`
	URL       string  `json:"url,omitempty"`
	Title     string  `json:"title,omitempty"`
	At        float64 `json:"at,omitempty"`
	MainFrame bool    `json:"main_frame,omitempty"`
}

var (
	mu      sync.Mutex
	inited  bool
	handler func(Event)
	events  = make(chan Event, 256) // to the handler, in order; when it lags, the newest are dropped
)

func init() {
	go func() {
		for e := range events {
			mu.Lock()
			f := handler
			mu.Unlock()
			if f != nil {
				f(e)
			}
		}
	}()
}

// OnEvent sets what is told of each event (from the main thread: quick).
func OnEvent(f func(Event)) {
	mu.Lock()
	handler = f
	mu.Unlock()
}

func setup() {
	mu.Lock()
	defer mu.Unlock()
	if inited {
		return
	}
	inited = true
	cs := C.CString(collector)
	C.apInit(cs)
	C.free(unsafe.Pointer(cs))
}

// Show lays the pane over window at x, y (from the top left of its content), w × h points.
func Show(window unsafe.Pointer, x, y, w, h float64) error {
	if window == nil {
		return errors.New("the preview needs the app's window (not the browser)")
	}
	setup()
	C.apShow(window, C.double(x), C.double(y), C.double(w), C.double(h))
	return nil
}

// Hide hides the pane (its page stays).
func Hide() { C.apHide() }

// Load opens a page: http or https only (a dev server, a staging site).
func Load(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return errors.New("an http or https address, such as http://localhost:3000")
	}
	cs := C.CString(u.String())
	C.apLoad(cs)
	C.free(unsafe.Pointer(cs))
	return nil
}

func Reload() { C.apReload() }
func Back()   { C.apBack() }

// IDEShow lays the IDE pane over window (as Show does the preview).
func IDEShow(window unsafe.Pointer, x, y, w, h float64, focus bool) error {
	if window == nil {
		return errors.New("the IDE needs the app's window (not the browser)")
	}
	f := 0
	if focus {
		f = 1
	}
	C.apIDEShow(window, C.double(x), C.double(y), C.double(w), C.double(h), C.int(f))
	return nil
}

// IDEAppearance sets the IDE pane light or dark (VS Code follows it).
func IDEAppearance(dark bool) {
	d := 0
	if dark {
		d = 1
	}
	C.apIDEAppearance(C.int(d))
}

// IDEReload loads the IDE's page again.
func IDEReload() { C.apIDEReload() }

// IDEHide hides it (VS Code stays loaded).
func IDEHide() { C.apIDEHide() }

// IDELoad opens an address in it: VS Code's, on this Mac only.
func IDELoad(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "http" || (u.Hostname() != "127.0.0.1" && u.Hostname() != "localhost") {
		return errors.New("the IDE opens VS Code on this Mac only")
	}
	cs := C.CString(u.String())
	C.apIDELoad(cs)
	C.free(unsafe.Pointer(cs))
	return nil
}

var navigate func(string)

// GuardNavigation keeps window's own page where it is: a navigation of it elsewhere (a link, a
// form, window.open) is cancelled and its address given to f.
func GuardNavigation(window unsafe.Pointer, f func(url string)) {
	if window == nil {
		return
	}
	mu.Lock()
	navigate = f
	mu.Unlock()
	C.apGuardNavigation(window)
}

//export goNavigation
func goNavigation(s *C.char) {
	u := C.GoString(s)
	mu.Lock()
	f := navigate
	mu.Unlock()
	if f != nil {
		go f(u)
	}
}

//export goPreviewEvent
func goPreviewEvent(s *C.char) {
	var e Event
	if json.Unmarshal([]byte(C.GoString(s)), &e) != nil {
		return
	}
	select { // off the main thread, never blocking it
	case events <- e:
	default:
	}
}

// collector runs in each document of the pane, before the page's own scripts. It keeps nothing
// global and sends only what went wrong (cut to a few KiB each).
const collector = `(() => {
  const h = window.webkit && window.webkit.messageHandlers && window.webkit.messageHandlers.acpostPreview;
  if (!h) return;
  const cut = (s) => String(s == null ? "" : s).slice(0, 4000);
  const str = (v) => { try { if (v instanceof Error) return v.name + ": " + v.message + (v.stack ? "\n" + v.stack : ""); if (typeof v === "string") return v; return JSON.stringify(v); } catch (_) { return String(v); } };
  const post = (d) => { try { d.url = location.href; d.at = Date.now(); for (const k in d) if (typeof d[k] === "string") d[k] = cut(d[k]); h.postMessage(d); } catch (_) {} };
  addEventListener("error", (e) => {
    if (e instanceof ErrorEvent) post({ kind: "exception", message: e.message, source: e.filename, line: e.lineno, col: e.colno, stack: (e.error && e.error.stack) || "" });
    else { const t = e.target; if (t && t !== window && (t.src || t.href)) post({ kind: "resource", message: "failed to load " + String(t.tagName || "").toLowerCase(), source: t.src || t.href }); }
  }, true);
  addEventListener("unhandledrejection", (e) => post({ kind: "rejection", message: str(e.reason) }));
  addEventListener("securitypolicyviolation", (e) => post({ kind: "csp", message: e.violatedDirective + " blocked " + e.blockedURI, source: e.sourceFile, line: e.lineNumber }));
  for (const lvl of ["error", "warn"]) {
    const orig = console[lvl];
    console[lvl] = function (...a) { post({ kind: "console." + lvl, message: a.map(str).join(" ") }); return orig.apply(this, a); };
  }
  const of = window.fetch;
  if (of) window.fetch = function (...a) {
    const src = String((a[0] && a[0].url) || a[0]);
    return of.apply(this, a).then((r) => { if (!r.ok) post({ kind: "fetch", message: r.status + " " + r.statusText, source: r.url || src }); return r; },
      (err) => { post({ kind: "fetch", message: str(err), source: src }); throw err; });
  };
  const XO = XMLHttpRequest.prototype.open, XS = XMLHttpRequest.prototype.send, url = new WeakMap();
  XMLHttpRequest.prototype.open = function (m, u) { url.set(this, String(u)); return XO.apply(this, arguments); };
  XMLHttpRequest.prototype.send = function () {
    this.addEventListener("loadend", () => { if (this.status === 0 || this.status >= 400) post({ kind: "xhr", message: this.status + " " + this.statusText, source: url.get(this) }); });
    return XS.apply(this, arguments);
  };
})();`

// Popup shows a menu of the system's at x, y (window points) over everything in the window, and
// gives the item chosen, or -1. keys[i], when not empty, is shown as ⇧⌘ and that letter.
func Popup(window unsafe.Pointer, x, y float64, titles, keys []string, checked int) int {
	if window == nil || len(titles) == 0 || len(titles) != len(keys) {
		return -1
	}
	n := len(titles)
	ct := C.malloc(C.size_t(n) * C.size_t(unsafe.Sizeof(uintptr(0))))
	ck := C.malloc(C.size_t(n) * C.size_t(unsafe.Sizeof(uintptr(0))))
	defer C.free(ct)
	defer C.free(ck)
	ts := unsafe.Slice((**C.char)(ct), n)
	ks := unsafe.Slice((**C.char)(ck), n)
	for i := range titles {
		ts[i] = C.CString(titles[i])
		ks[i] = C.CString(keys[i])
	}
	defer func() {
		for i := range ts {
			C.free(unsafe.Pointer(ts[i]))
			C.free(unsafe.Pointer(ks[i]))
		}
	}()
	return int(C.apPopup(window, C.double(x), C.double(y), (**C.char)(ct), (**C.char)(ck), C.int(n), C.int(checked)))
}

// IDESeed is a script put into VS Code's page when the pane is made (call it before IDEShow).
func IDESeed(script string) {
	cs := C.CString(script)
	C.apIDESetSeed(cs)
	C.free(unsafe.Pointer(cs))
}
