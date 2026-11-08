//go:build darwin

package main

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"net/url"
	"path/filepath"
	"strings"
	"sync"

	"github.com/k1dory/acpost/desktop/preview"
)

// The IDE dressed as the app. Its look lives in the app's VS Code extension (ide/extension: the
// colour themes made from the app's tokens, Lucide icons for VS Code's, the app's fonts and the
// workbench CSS); here are the settings that turn it on and the scripts the pane puts into VS
// Code's page.

// ideSettings are the app's VS Code settings its extension cannot give as defaults: the
// application- and machine-wide ones (VS Code drops those from an extension's
// configurationDefaults; their scopes checked in the pinned VS Code). Written into VS Code's own
// settings by the page's seed script; the rest are the extension's configurationDefaults
// (ide/extension/package.json), under the user's own values.
func ideSettings() map[string]any {
	return map[string]any{
		"window.menuBarVisibility":                         "compact",
		"update.showReleaseNotes":                          false,
		"workbench.welcomePage.walkthroughs.openOnInstall": false,
		"security.workspace.trust.enabled":                 false, // folders the user's own agents work in
		"telemetry.telemetryLevel":                         "off",
	}
}

// ideDefaults are the extension's configurationDefaults.
func ideDefaults() map[string]any {
	var m struct {
		Contributes struct {
			ConfigurationDefaults map[string]any
		}
	}
	b, _ := ideExtFS.ReadFile("ide/extension/package.json")
	_ = json.Unmarshal(b, &m)
	return m.Contributes.ConfigurationDefaults
}

//go:embed ide/page/seed.js
var ideSeedJS string

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
//   - at its end: the app's application-wide settings written into VS Code's store
//     (ide/page/seed.js says how; the others are the extension's defaults).
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
		// The empty editor's mark: the app's (from the extension; the CSS cannot name it itself —
		// its address is the extension's file by a query, which a relative url() would lose).
		lp := " .part.editor .editor-group-watermark .letterpress"
		faces += ".monaco-workbench" + lp + "{background-image:url(" + res("media/letterpress-dark.svg") + ")!important}" +
			".monaco-workbench.vs" + lp + ",.monaco-workbench.hc-light" + lp + "{background-image:url(" + res("media/letterpress-light.svg") + ")!important}"
		js := func(v any) string { b, _ := json.Marshal(v); return string(b) }
		start = `(() => {
try { localStorage.setItem('vscode.nls.locale', 'en'); localStorage.removeItem('vscode.nls.languagePackExtensionId'); } catch (e) {}
const root = document.head || document.documentElement;
const f = document.createElement('style'); f.id = 'acpost-fonts'; f.textContent = ` + js(faces) + `; root.appendChild(f);
const l = document.createElement('link'); l.id = 'acpost'; l.rel = 'stylesheet'; l.href = ` + js(res("workbench.css")) + `; root.appendChild(l);
})();`

		ours, _ := json.Marshal(ideSettings())
		defs, _ := json.Marshal(ideDefaults())
		seed = strings.NewReplacer(
			"__V__", js(shortHash(append(ours, defs...))),
			"__OURS__", string(ours),
			"__DEFAULTS__", string(defs),
			"__OLD__", string(bytes.TrimSpace(ideOldColors)),
		).Replace(ideSeedJS)
	}
	return start, seed
}
