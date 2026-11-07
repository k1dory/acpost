//go:build darwin

package main

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"net/url"
	"path/filepath"
	"sync"

	"github.com/k1dory/acpost/desktop/preview"
)

// The IDE dressed as the app. Its look lives in the app's VS Code extension (ide/extension: the
// colour themes made from the app's tokens, Lucide icons for VS Code's, the app's fonts and the
// workbench CSS); here are the settings that turn it on and the scripts the pane puts into VS
// Code's page.

func ideSettings() map[string]any {
	mono := "'JetBrains Mono', 'SF Mono', Menlo, monospace"
	return map[string]any{
		"window.autoDetectColorScheme":                     true, // the pane's appearance is the app's theme
		"workbench.preferredDarkColorTheme":                "acpost Dark",
		"workbench.preferredLightColorTheme":               "acpost Light",
		"workbench.productIconTheme":                       "acpost",
		"workbench.iconTheme":                              "vs-minimal",
		"workbench.activityBar.location":                   "default", // on the left: VS Code then has no title bar of its own
		"window.menuBarVisibility":                         "compact",
		"window.commandCenter":                             false,
		"workbench.layoutControl.enabled":                  false,
		"workbench.startupEditor":                          "none",
		"workbench.tips.enabled":                           false,
		"workbench.welcomePage.walkthroughs.openOnInstall": false,
		"workbench.secondarySideBar.defaultVisibility":     "hidden",
		"workbench.tree.indent":                            14,
		"workbench.tree.renderIndentGuides":                "none",
		"chat.disableAIFeatures":                           true, // the agents' chat is the app's, beside the IDE
		"chat.commandCenter.enabled":                       false,
		"update.showReleaseNotes":                          false,
		"extensions.ignoreRecommendations":                 true,
		"security.workspace.trust.enabled":                 false, // folders the user's own agents work in
		"editor.fontFamily":                                mono,
		"editor.fontSize":                                  13,
		"editor.lineHeight":                                1.6,
		"editor.minimap.enabled":                           false,
		"editor.renderLineHighlight":                       "line",
		"terminal.integrated.fontFamily":                   mono,
		"terminal.integrated.fontSize":                     12,
		"telemetry.telemetryLevel":                         "off",
	}
}

// ideOldColors are the colours the app once wrote into VS Code's settings (before its themes):
// taken out of them again where they are still the app's.
//
//go:embed ide/old-colors.json
var ideOldColors []byte

var ideScriptsOnce sync.Once

// ideScripts hands the pane its two scripts (before it is made; once):
//
//   - at the page's start: VS Code in English (its other languages come half translated: the
//     language pack it fetches lags its version), and the app's look linked in — the fonts and
//     the CSS from the extension, served by VS Code itself (its CSP takes fonts and styles only
//     from its own server);
//   - at its end: the settings written into VS Code's store (its IndexedDB, made by VS Code a bit
//     after the page loads on a fresh profile: tried each second for a minute, never made here),
//     merged over what is there, then the page loaded once more to take them. Once per version of
//     them and of the extension (marked in the page's own storage; a mark that cannot be kept:
//     written, not reloaded, so it never loops); a settings file VS Code cannot read as plain JSON
//     (the user's, with comments) is left as it is.
func ideScripts(extDir, extHash string) {
	ideScriptsOnce.Do(func() { preview.IDEScripts(ideScriptText(extDir, extHash)) })
}

func ideScriptText(extDir, extHash string) (start, seed string) {
	{
		res := func(p string) string {
			return "/vscode-remote-resource?path=" + url.QueryEscape(filepath.Join(extDir, p)) + "&v=" + extHash[:12]
		}
		var faces string
		for _, s := range []string{"latin", "cyrillic"} {
			faces += "@font-face{font-family:'Onest';src:url(" + res("fonts/onest-"+s+".woff2") + ") format('woff2');font-weight:100 900;font-display:block}"
			for _, w := range []string{"400", "500"} {
				faces += "@font-face{font-family:'JetBrains Mono';src:url(" + res("fonts/jetbrains-mono-"+s+"-"+w+".woff2") + ") format('woff2');font-weight:" + w + ";font-display:block}"
			}
		}
		js := func(v any) string { b, _ := json.Marshal(v); return string(b) }
		start = `(() => {
try { localStorage.setItem('vscode.nls.locale', 'en'); localStorage.removeItem('vscode.nls.languagePackExtensionId'); } catch (e) {}
const root = document.head || document.documentElement;
const f = document.createElement('style'); f.id = 'acpost-fonts'; f.textContent = ` + js(faces) + `; root.appendChild(f);
const l = document.createElement('link'); l.id = 'acpost'; l.rel = 'stylesheet'; l.href = ` + js(res("workbench.css")) + `; root.appendChild(l);
})();`

		b, _ := json.Marshal(ideSettings())
		ver := js(extHash[:16] + ":" + shortHash(b))
		seed = `(() => {
const V = ` + ver + `, ours = ` + string(b) + `, OLD = ` + string(bytes.TrimSpace(ideOldColors)) + `, K = 'acpost-ide-settings';
try { if (localStorage.getItem(K) === V) return; } catch (e) { return; }
let left = 60;
const again = () => { if (--left > 0) setTimeout(tryIt, 1000); };
const done = () => { try { localStorage.setItem(K, V); return localStorage.getItem(K) === V; } catch (e) { return false; } };
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
      // The app's older way of its colours (now its themes): only the values it wrote go; what the
      // user set or changed there stays.
      const cc = cur['workbench.colorCustomizations'];
      if (cc && typeof cc === 'object' && !Array.isArray(cc)) {
        for (const [t, old] of Object.entries(OLD)) {
          const b = cc[t];
          if (!b || typeof b !== 'object') continue;
          for (const [k, v] of Object.entries(old)) if (b[k] === v) delete b[k];
          if (!Object.keys(b).length) delete cc[t];
        }
        if (!Object.keys(cc).length) delete cur['workbench.colorCustomizations'];
      }
      st.put(new TextEncoder().encode(JSON.stringify(Object.assign(cur, ours), null, 2)), '/User/settings.json');
      tx.oncomplete = () => { db.close(); if (done()) location.reload(); }; // unmarked: no reload (it would come again on every load)
    };
  };
}
tryIt();
})();`
	}
	return start, seed
}
