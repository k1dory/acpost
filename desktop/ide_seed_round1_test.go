//go:build darwin

package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Z9/I10: the settings seed (ide/page/seed.js as the page gets it), run by node against a fake
// IndexedDB and localStorage.
func TestIDESeed(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("no node")
	}
	_, seed := ideScriptText("/x/acpost.acpost-ide-1.0.0", strings.Repeat("ab", 32))
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "seed.js"), []byte(seed), 0o600); err != nil {
		t.Fatal(err)
	}
	ours, _ := json.Marshal(ideSettings())
	defs, _ := json.Marshal(ideDefaults())
	harness := `import assert from "node:assert/strict";
import fs from "node:fs";
import vm from "node:vm";
const SEED = fs.readFileSync(process.argv[2], "utf8");
const OURS = ` + string(ours) + `, DEFAULTS = ` + string(defs) + `;
const FILE = "/User/settings.json";

// One page load: the seed run against db (its store a Map, or null: not made yet) and storage.
// abort: the first n transactions abort.
function load({ db, storage, abort = 0 }) {
  const ev = (f) => setImmediate(f);
  let reloads = 0, txs = 0;
  const idb = {
    open() {
      const q = {};
      ev(() => {
        if (!db) { q.transaction = { abort() {} }; q.onupgradeneeded?.(); q.onerror?.({ preventDefault() {} }); return; }
        q.result = {
          objectStoreNames: { contains: (n) => n === "vscode-userdata-store" },
          close() {},
          transaction() {
            txs++;
            const tx = {}, aborting = txs <= abort, writes = [];
            const finish = () => ev(() => {
              if (aborting) { tx.onabort?.(); return; }
              for (const [k, v] of writes) db.set(k, v);
              tx.oncomplete?.();
            });
            tx.objectStore = () => ({
              get(k) { const r = {}; ev(() => { r.result = db.get(k); r.onsuccess?.(); finish(); }); return r; },
              put(v, k) { writes.push([k, v]); },
            });
            return tx;
          },
        };
        q.onsuccess();
      });
      return q;
    },
  };
  const localStorage = {
    getItem: (k) => (storage.has(k) ? storage.get(k) : null),
    setItem: (k, v) => storage.set(k, String(v)),
  };
  const ctx = vm.createContext({ indexedDB: idb, localStorage, TextEncoder, TextDecoder, JSON, Object,
    setTimeout: (f) => setImmediate(f), location: { reload: () => reloads++ } });
  vm.runInContext(SEED, ctx);
  return new Promise((res) => setTimeout(() => res({ reloads, txs }), 100));
}
const read = (db) => { const v = db.get(FILE); return v === undefined ? undefined : JSON.parse(new TextDecoder().decode(v)); };
const put = (db, o) => db.set(FILE, typeof o === "string" ? new TextEncoder().encode(o) : new TextEncoder().encode(JSON.stringify(o)));

{ // A fresh profile: the app's settings written, marked, one reload; the next load does nothing.
  const db = new Map(), storage = new Map();
  let r = await load({ db, storage });
  assert.deepEqual(read(db), OURS);
  assert.equal(r.reloads, 1);
  r = await load({ db, storage });
  assert.equal(r.reloads, 0); assert.equal(r.txs, 0);
}
{ // A profile the old seed wrote (every setting; marked, nothing kept): what the app wrote and is
  // now a default goes, what the user changed or added stays.
  const db = new Map(), storage = new Map([["acpost-ide-settings", "old"]]);
  put(db, { ...DEFAULTS, ...OURS, "editor.fontSize": 15, "files.autoSave": "afterDelay" });
  const r = await load({ db, storage });
  assert.deepEqual(read(db), { ...OURS, "editor.fontSize": 15, "files.autoSave": "afterDelay" });
  assert.equal(r.reloads, 1);
}
{ // The user's value of one of the app's settings stays when the app's settings change.
  const db = new Map(), storage = new Map([["acpost-ide-settings", "older"], ["acpost-ide-seeded", JSON.stringify(OURS)]]);
  put(db, { ...OURS, "window.menuBarVisibility": "classic" });
  await load({ db, storage });
  assert.equal(read(db)["window.menuBarVisibility"], "classic");
}
{ // Comments: read, never rewritten, not marked (tried again next load); with nothing to change: marked.
  const db = new Map(), storage = new Map();
  const text = '{\n  // mine\n  "editor.fontSize": 15, "a": "x,}//", /* b */\n}';
  put(db, text);
  let r = await load({ db, storage });
  assert.equal(new TextDecoder().decode(db.get(FILE)), text);
  assert.equal(storage.has("acpost-ide-settings"), false); assert.equal(r.reloads, 0);
  const with_ = '{ // mine\n' + JSON.stringify(OURS).slice(1, -1) + ', "a": "//",\n}';
  put(db, with_);
  r = await load({ db, storage });
  assert.equal(new TextDecoder().decode(db.get(FILE)), with_);
  assert.equal(storage.has("acpost-ide-settings"), true); assert.equal(r.reloads, 0);
}
{ // A transaction that aborts is tried again; marked only once one has gone through.
  const db = new Map(), storage = new Map();
  const r = await load({ db, storage, abort: 2 });
  assert.equal(r.txs, 3);
  assert.deepEqual(read(db), OURS);
  assert.equal(r.reloads, 1); assert.equal(storage.has("acpost-ide-settings"), true);
}
{ // Every transaction aborts: never marked, never reloaded.
  const db = new Map(), storage = new Map();
  const r = await load({ db, storage, abort: 1000 });
  assert.equal(storage.has("acpost-ide-settings"), false); assert.equal(r.reloads, 0);
  assert.equal(db.has(FILE), false);
}
{ // Not made yet: waited for, nothing written.
  const storage = new Map();
  const r = await load({ db: null, storage });
  assert.equal(r.reloads, 0); assert.equal(storage.has("acpost-ide-settings"), false);
}
console.log("ok");
`
	h := filepath.Join(dir, "harness.mjs")
	os.WriteFile(h, []byte(harness), 0o600)
	if out, err := exec.Command(node, h, filepath.Join(dir, "seed.js")).CombinedOutput(); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
}

// Z9: the extension's defaults are settings VS Code takes as defaults (not application- or
// machine-wide), and no setting is both a default and seeded.
func TestIDEDefaultsNotAppScoped(t *testing.T) {
	defs, ours := ideDefaults(), ideSettings()
	if len(defs) < 20 {
		t.Fatalf("defaults: %v", defs)
	}
	for k := range ours {
		if _, ok := defs[k]; ok {
			t.Errorf("%s both a default and seeded", k)
		}
	}
	// Scopes as the pinned VS Code has them (checked there; see ideSettings).
	for _, k := range []string{"window.menuBarVisibility", "update.showReleaseNotes", "security.workspace.trust.enabled",
		"telemetry.telemetryLevel", "workbench.welcomePage.walkthroughs.openOnInstall"} {
		if _, ok := defs[k]; ok {
			t.Errorf("%s is application- or machine-wide: VS Code drops it from defaults", k)
		}
	}
}
