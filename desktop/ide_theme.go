//go:build darwin

package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sync"

	"github.com/k1dory/acpost/desktop/preview"
)

// The IDE dressed as the app: VS Code's light and dark themes with the app's colours (its tokens,
// frontend/src/app.css), its monospace font, the activity bar on top, VS Code's own AI chat off
// (the agents' chat is beside it), no minimap or tips. Written into VS Code's settings (its page's
// store) when these change — the user's other settings stay; ours win over theirs only then.

type palette struct{ page, side, card, line, lineSoft, ink, ink2, muted, hover, sel, accent, btn, btnInk, warn, crit, good string }

var (
	ideLight = palette{"#f6f6f3", "#fbfbf9", "#ffffff", "#e4e3dd", "#efeee9", "#0b0b0b", "#3d3c38", "#898781", "#f4f3ef", "#eeede8", "#2f5bd3", "#0b0b0b", "#ffffff", "#c98500", "#c23434", "#1f8a3b"}
	ideDark  = palette{"#0f0f0e", "#141413", "#1a1a18", "#2b2a27", "#222220", "#f2f1ec", "#c9c7bf", "#8c8a83", "#22221f", "#2a2926", "#7d9bf0", "#f2f1ec", "#0b0b0b", "#e0a42a", "#ef6a6a", "#4cc26a"}
)

func (p palette) colors() map[string]string {
	return map[string]string{
		"foreground": p.ink2, "descriptionForeground": p.muted, "focusBorder": p.accent + "99", "widget.border": p.line,
		"icon.foreground": p.ink2, "textLink.foreground": p.accent, "textLink.activeForeground": p.accent,
		"editor.background": p.page, "editor.foreground": p.ink, "editorLineNumber.foreground": p.muted + "99",
		"editorLineNumber.activeForeground": p.ink2, "editor.lineHighlightBackground": p.hover, "editor.lineHighlightBorder": p.hover,
		"editor.selectionBackground": p.accent + "33", "editorCursor.foreground": p.ink, "editorIndentGuide.background1": p.lineSoft,
		"editorWidget.background": p.card, "editorWidget.border": p.line, "editorGutter.background": p.page,
		"editorGroup.border": p.line, "editorGroupHeader.tabsBackground": p.side, "editorGroupHeader.tabsBorder": p.line,
		"tab.activeBackground": p.page, "tab.inactiveBackground": p.side, "tab.border": p.line, "tab.activeBorderTop": p.ink,
		"tab.activeForeground": p.ink, "tab.inactiveForeground": p.muted, "tab.hoverBackground": p.hover,
		"activityBar.background": p.side, "activityBar.foreground": p.ink, "activityBar.inactiveForeground": p.muted,
		"activityBar.border": p.line, "activityBar.activeBorder": p.ink, "activityBarTop.foreground": p.ink,
		"activityBarTop.inactiveForeground": p.muted, "activityBarTop.activeBorder": p.ink,
		"activityBarBadge.background": p.accent, "activityBarBadge.foreground": "#ffffff",
		"sideBar.background": p.side, "sideBar.foreground": p.ink2, "sideBar.border": p.line,
		"sideBarTitle.foreground": p.ink, "sideBarSectionHeader.background": p.side, "sideBarSectionHeader.border": p.lineSoft,
		"titleBar.activeBackground": p.side, "titleBar.inactiveBackground": p.side, "titleBar.activeForeground": p.ink2,
		"titleBar.border": p.line, "commandCenter.background": p.card, "commandCenter.border": p.line,
		"statusBar.background": p.side, "statusBar.foreground": p.muted, "statusBar.border": p.line,
		"statusBar.noFolderBackground": p.side, "statusBar.debuggingBackground": p.warn, "statusBarItem.remoteBackground": p.side,
		"statusBarItem.remoteForeground": p.muted,
		"panel.background":               p.page, "panel.border": p.line, "panelTitle.activeForeground": p.ink, "panelTitle.activeBorder": p.ink,
		"panelTitle.inactiveForeground": p.muted, "terminal.background": p.page, "terminal.foreground": p.ink,
		"list.activeSelectionBackground": p.sel, "list.activeSelectionForeground": p.ink, "list.inactiveSelectionBackground": p.sel,
		"list.hoverBackground": p.hover, "list.focusOutline": p.accent + "66", "list.highlightForeground": p.accent,
		"input.background": p.card, "input.border": p.line, "input.foreground": p.ink, "input.placeholderForeground": p.muted,
		"dropdown.background": p.card, "dropdown.border": p.line, "quickInput.background": p.card,
		"menu.background": p.card, "menu.border": p.line, "menu.selectionBackground": p.sel, "menu.selectionForeground": p.ink,
		"button.background": p.btn, "button.foreground": p.btnInk, "button.hoverBackground": p.btn + "dd",
		"button.secondaryBackground": p.sel, "button.secondaryForeground": p.ink, "badge.background": p.sel, "badge.foreground": p.ink,
		"scrollbarSlider.background": p.muted + "33", "scrollbarSlider.hoverBackground": p.muted + "55",
		"notifications.background": p.card, "notifications.border": p.line, "editorError.foreground": p.crit,
		"editorWarning.foreground": p.warn, "gitDecoration.modifiedResourceForeground": p.warn,
		"gitDecoration.untrackedResourceForeground": p.good, "gitDecoration.deletedResourceForeground": p.crit,
		"progressBar.background": p.accent, "welcomePage.background": p.page,
	}
}

func ideSettings() map[string]any {
	return map[string]any{
		"window.autoDetectColorScheme":                 true,
		"workbench.preferredDarkColorTheme":            "Default Dark Modern",
		"workbench.preferredLightColorTheme":           "Default Light Modern",
		"workbench.colorCustomizations":                map[string]any{"[Default Dark Modern]": ideDark.colors(), "[Default Light Modern]": ideLight.colors()},
		"workbench.iconTheme":                          "vs-minimal",
		"workbench.activityBar.location":               "top",
		"workbench.layoutControl.enabled":              false,
		"workbench.startupEditor":                      "none",
		"workbench.tips.enabled":                       false,
		"workbench.secondarySideBar.defaultVisibility": "hidden",
		"chat.disableAIFeatures":                       true,
		"chat.commandCenter.enabled":                   false,
		"window.commandCenter":                         false,
		"editor.fontFamily":                            "'JetBrains Mono', 'SF Mono', Menlo, monospace",
		"editor.fontSize":                              13,
		"editor.lineHeight":                            1.6,
		"editor.minimap.enabled":                       false,
		"editor.renderLineHighlight":                   "line",
		"terminal.integrated.fontFamily":               "'JetBrains Mono', 'SF Mono', Menlo, monospace",
		"terminal.integrated.fontSize":                 12,
		"telemetry.telemetryLevel":                     "off",
	}
}

// The script, made once: it writes the settings into VS Code's store (its IndexedDB, made by VS
// Code a bit after the page loads on a fresh profile: tried each second for a minute, never made
// here), merged over what is there, then loads the page once more to take them. Done once per
// version of them (marked in the page's own storage); a settings file VS Code cannot read as
// plain JSON (the user's, with comments) is left as it is.
var ideSeedOnce sync.Once

func ideSeed() {
	ideSeedOnce.Do(func() {
		b, _ := json.Marshal(ideSettings())
		sum := sha256.Sum256(b)
		ver, _ := json.Marshal(hex.EncodeToString(sum[:8]))
		preview.IDESeed(`(() => {
const V = ` + string(ver) + `, ours = ` + string(b) + `, K = 'acpost-ide-settings';
try { if (localStorage.getItem(K) === V) return; } catch (e) { return; }
let left = 60;
const again = () => { if (--left > 0) setTimeout(tryIt, 1000); };
const done = () => { try { localStorage.setItem(K, V); } catch (e) {} };
function tryIt() {
  const q = indexedDB.open('vscode-web-db');
  q.onupgradeneeded = () => { q.transaction.abort(); };
  q.onerror = (e) => { e.preventDefault(); again(); };
  q.onsuccess = () => {
    const db = q.result;
    if (!db.objectStoreNames.contains('vscode-userdata-store')) { db.close(); again(); return; }
    const tx = db.transaction('vscode-userdata-store', 'readwrite'), st = tx.objectStore('vscode-userdata-store');
    const g = st.get('/User/settings.json');
    g.onsuccess = () => {
      let cur = {};
      if (g.result !== undefined) {
        try { cur = JSON.parse(typeof g.result === 'string' ? g.result : new TextDecoder().decode(g.result)); } catch (e) { done(); return; }
        if (!cur || typeof cur !== 'object' || Array.isArray(cur)) { done(); return; }
      }
      st.put(new TextEncoder().encode(JSON.stringify(Object.assign(cur, ours), null, 2)), '/User/settings.json');
      tx.oncomplete = () => { db.close(); done(); location.reload(); };
    };
  };
}
tryIt();
})();`)
	})
}
