// The app's VS Code settings that its extension cannot give as defaults (VS Code takes
// application- and machine-wide settings only from the user), written into VS Code's store: its
// IndexedDB, made by VS Code a bit after the page loads on a fresh profile (tried each second for
// a minute, never made here). Then the page is loaded once more to take them.
//
// The user's choice stays: a setting is written only when it is absent or still the value the app
// wrote before; one the app no longer writes (now an extension default) is taken out only while it
// is still the app's. A file VS Code reads but plain JSON does not (comments) is read, never
// rewritten: tried again on the next load. Done once per version of the settings, marked only
// after VS Code's store has taken them.
//
// Go fills in V, OURS, DEFAULTS and OLD (ide_theme.go: ideScriptText).
(() => {
  const V = __V__, OURS = __OURS__, DEFAULTS = __DEFAULTS__, OLD = __OLD__;
  const K = "acpost-ide-settings", SEEDED = "acpost-ide-seeded";
  const FILE = "/User/settings.json", STORE = "vscode-userdata-store";
  let marked, seeded;
  try { marked = localStorage.getItem(K); seeded = localStorage.getItem(SEEDED); } catch (e) { return; }
  if (marked === V) return;
  // What the app wrote before: kept beside the mark; a profile from before that (marked, nothing
  // kept) had every setting written, with the values the defaults and OURS have.
  let prev = {};
  try { prev = seeded ? JSON.parse(seeded) : marked ? { ...DEFAULTS, ...OURS } : {}; } catch (e) {}

  const same = (a, b) => JSON.stringify(a) === JSON.stringify(b);

  // JSON with comments and trailing commas, as VS Code reads its settings; null when not that.
  function parseJSONC(text) {
    // Each pass copies strings whole and changes only what is outside them.
    const pass = (t, f) => {
      let out = "";
      for (let i = 0; i < t.length;) {
        if (t[i] === '"') {
          let j = i + 1;
          while (j < t.length && t[j] !== '"') j += t[j] === "\\" ? 2 : 1;
          out += t.slice(i, j + 1); i = j + 1;
        } else {
          const [kept, next] = f(t, i);
          out += kept; i = next;
        }
      }
      return out;
    };
    const comments = (t, i) => {
      if (t[i] === "/" && t[i + 1] === "/") { const j = t.indexOf("\n", i); return ["", j < 0 ? t.length : j]; }
      if (t[i] === "/" && t[i + 1] === "*") { const j = t.indexOf("*/", i + 2); return [" ", j < 0 ? t.length : j + 2]; }
      return [t[i], i + 1];
    };
    const commas = (t, i) => {
      if (t[i] === ",") {
        let j = i + 1;
        while (j < t.length && /\s/.test(t[j])) j++;
        if (t[j] === "}" || t[j] === "]") return ["", i + 1];
      }
      return [t[i], i + 1];
    };
    try {
      const v = JSON.parse(pass(pass(text, comments), commas));
      return v && typeof v === "object" && !Array.isArray(v) ? v : null;
    } catch (e) { return null; }
  }

  // cur with the app's settings; whether it changed.
  function merge(cur) {
    let changed = false;
    for (const [k, v] of Object.entries(OURS)) {
      if ((!(k in cur) || same(cur[k], prev[k])) && !same(cur[k], v)) { cur[k] = v; changed = true; }
    }
    for (const [k, v] of Object.entries(prev)) {
      if (!(k in OURS) && k in cur && same(cur[k], v)) { delete cur[k]; changed = true; }
    }
    // The app's older way of its colours (now its themes): only the values it wrote go.
    const cc = cur["workbench.colorCustomizations"];
    if (cc && typeof cc === "object" && !Array.isArray(cc)) {
      for (const [t, old] of Object.entries(OLD)) {
        const b = cc[t];
        if (!b || typeof b !== "object") continue;
        for (const [k, v] of Object.entries(old)) if (b[k] === v) { delete b[k]; changed = true; }
        if (!Object.keys(b).length) delete cc[t];
      }
      if (!Object.keys(cc).length) delete cur["workbench.colorCustomizations"];
    }
    return changed;
  }

  const mark = () => {
    try {
      localStorage.setItem(SEEDED, JSON.stringify(OURS));
      localStorage.setItem(K, V);
      return localStorage.getItem(K) === V;
    } catch (e) { return false; }
  };

  let left = 60;
  const again = () => { if (--left > 0) setTimeout(tryIt, 1000); };
  function tryIt() {
    let q;
    try { q = indexedDB.open("vscode-web-db"); } catch (e) { again(); return; }
    q.onupgradeneeded = () => { q.transaction.abort(); }; // not made yet: VS Code makes it
    q.onerror = (e) => { e.preventDefault(); again(); };
    q.onsuccess = () => {
      const db = q.result;
      if (!db.objectStoreNames.contains(STORE)) { db.close(); again(); return; }
      let tx, settled = false;
      const end = (retry) => { if (settled) return; settled = true; db.close(); if (retry) again(); };
      try { tx = db.transaction(STORE, "readwrite"); } catch (e) { end(true); return; }
      tx.onabort = () => end(true);
      const st = tx.objectStore(STORE), g = st.get(FILE);
      g.onsuccess = () => {
        let text, cur = {};
        if (g.result !== undefined) {
          text = typeof g.result === "string" ? g.result : new TextDecoder().decode(g.result);
          cur = parseJSONC(text);
          if (!cur) { tx.oncomplete = () => end(false); return; } // not settings VS Code reads either: left
        }
        if (!merge(cur)) { tx.oncomplete = () => { end(false); mark(); }; return; }
        let plain = text === undefined;
        try { plain = plain || (JSON.parse(text), true); } catch (e) {}
        if (!plain) { tx.oncomplete = () => end(false); return; } // the user's comments: kept, not marked
        st.put(new TextEncoder().encode(JSON.stringify(cur, null, 2)), FILE);
        // Unmarked (storage refused): no reload, it would come again on every load.
        tx.oncomplete = () => { end(false); if (mark()) location.reload(); };
      };
    };
  }
  tryIt();
})();
