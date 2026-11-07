//go:build darwin

package main

import (
	"sync"

	"github.com/k1dory/acpost/desktop/preview"
)

// The preview pane: a native view over the window where the interface keeps its box, showing the
// agent's dev server; what goes wrong in the page comes to the interface as "preview:event".

var previewOnce sync.Once

func (n *Node) previewInit() {
	previewOnce.Do(func() {
		preview.OnEvent(func(e preview.Event) { n.app.Event.Emit("preview:event", e) })
	})
}

// PreviewShow lays the pane at x, y (points from the top left of the window's content), w × h.
func (n *Node) PreviewShow(x, y, w, h float64) error {
	n.previewInit()
	if n.win == nil {
		return errPreview
	}
	return preview.Show(n.win.NativeWindow(), x, y, w, h)
}

// GuardNavigation keeps the app's page where it is (called once the page is up): an address it
// would have gone to comes to the interface as "app:navigate", to open where it belongs.
func (n *Node) GuardNavigation() {
	if n.win == nil {
		return
	}
	preview.GuardNavigation(n.win.NativeWindow(), func(url string) { n.app.Event.Emit("app:navigate", url) })
}

// PreviewHide hides it (its page stays loaded).
func (n *Node) PreviewHide() { preview.Hide() }

// PreviewLoad opens a page (http or https).
func (n *Node) PreviewLoad(url string) error {
	n.previewInit()
	return preview.Load(url)
}

func (n *Node) PreviewReload() { preview.Reload() }
func (n *Node) PreviewBack()   { preview.Back() }

var errPreview = previewErr("the preview needs the app's window")

type previewErr string

func (e previewErr) Error() string { return string(e) }
