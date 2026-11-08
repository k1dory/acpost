//go:build darwin

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// I4/Z7, I5: the interface's pane calls (frontend/src/components/idePane.ts), run by node against
// a fake native side whose calls answer when the test says.
func TestIDEPaneOrder(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("no node")
	}
	mod, err := filepath.Abs("frontend/src/components/idePane.ts")
	if err != nil {
		t.Fatal(err)
	}
	f := filepath.Join(t.TempDir(), "pane.mjs")
	if err := os.WriteFile(f, []byte(`import assert from "node:assert/strict";
import { Pane, paneRect, paneOwners } from `+"\""+mod+"\""+`;

// A native side whose calls stay out until let go (in order or not).
function fake() {
  const log = [], out = [];
  const call = (what) => new Promise((res, rej) => out.push({ what, res, rej }));
  return {
    log, out,
    calls: {
      show: (u, r) => { log.push("show " + u + " " + r.h); return call("show " + u); },
      hide: () => { log.push("hide"); return call("hide"); },
    },
    async next(fail) { const c = out.shift(); fail ? c.rej(new Error("no window")) : c.res(); await new Promise(r => setTimeout(r, 0)); },
  };
}
const R = (h) => ({ x: 0, y: 0, w: 100, h });

{ // One call out at a time; what was asked meanwhile is replaced by what is asked after it.
  const n = fake(), p = new Pane(n.calls);
  const a = p.show("A", R(1)), b = p.show("B", R(2)), c = p.show("C", R(3));
  assert.deepEqual(n.log, ["show A 1"]);
  assert.equal(await b, false);
  await n.next();
  assert.equal(await a, true);
  assert.deepEqual(n.log, ["show A 1", "show C 3"]);
  await n.next();
  assert.equal(await c, true);
}
{ // A hide asked while a show is out comes after it: the pane ends hidden.
  const n = fake(), p = new Pane(n.calls);
  p.show("A", R(1)); const h = p.hide();
  await n.next(); await n.next();
  assert.equal(await h, true);
  assert.deepEqual(n.log, ["show A 1", "hide"]);
}
{ // Closed (the view gone): a late show is never sent, the last call is the hide.
  const n = fake(), p = new Pane(n.calls);
  p.show("A", R(1)); p.close(); const late = p.show("B", R(2));
  assert.equal(await late, false);
  await n.next(); await n.next();
  assert.deepEqual(n.log, ["show A 1", "hide"]);
  assert.equal(n.out.length, 0);
}
{ // A failed call is the caller's error; the next one is still sent.
  const n = fake(), p = new Pane(n.calls);
  const a = p.show("A", R(1)).then(() => "", (e) => e.message); const b = p.show("B", R(2));
  await n.next(true);
  assert.equal(await a, "no window");
  await n.next();
  assert.equal(await b, true);
}
{ // I5: a toast over the box's bottom shortens the pane (shown, not hidden); one beside it does not.
  const box = { x: 300, y: 50, w: 600, h: 700 };
  assert.deepEqual(paneRect(box, 1, { x: 400, y: 680, w: 300, h: 50 }), { x: 300, y: 50, w: 600, h: 622 });
  assert.deepEqual(paneRect(box, 1, { x: 10, y: 680, w: 200, h: 50 }), box);
  assert.deepEqual(paneRect(box, 2, null), { x: 600, y: 100, w: 1200, h: 1400 });
  assert.equal(paneRect({ x: 0, y: 0, w: 3, h: 100 }, 1, null), null);
}
{ // Review 3: a view leaving with a show out, and the view coming back at once: the pane ends shown.
  for (const closeFirst of [true, false]) {
    const n = fake(), owner = paneOwners(n.calls);
    const a = owner();
    a.show("A", R(1));                 // out
    let b;
    if (closeFirst) { a.close(); b = owner(); } else { b = owner(); a.close(); }
    b.show("B", R(2));
    a.show("late", R(9));              // the old view's late call: dropped
    while (n.out.length) await n.next();
    assert.equal(n.log.at(-1), "show B 2", closeFirst + ": " + n.log.join(", "));
    assert.ok(!n.log.includes("show late 9"));
  }
}
console.log("ok");
`), 0o600); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command(node, f).CombinedOutput(); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
}
