// node --test ide/reveal.test.mjs
import test from "node:test";
import assert from "node:assert/strict";
import { createRequire } from "node:module";
const { plan, isCommand } = createRequire(import.meta.url)("./extension/reveal.js");

const now = 1_000_000;
const files = { "/w/a.txt": "one\nTWO\nthree\n", "/w/twice.txt": "x\nx\n", "/else/b.txt": "b\n" };
const read = f => files[f] ?? null;
const cmd = o => ({ folder: "/w", path: "a.txt", at: now, old: "two", new: "TWO", ...o });

test("an edit is taken back for the diff's left side", () => {
  assert.deepEqual(plan(cmd(), ["/w"], read, now), { file: "/w/a.txt", before: "one\ntwo\nthree\n", title: "edit" });
  assert.deepEqual(plan(cmd({ path: "/else/b.txt", old: "", new: "b\n", created: true, title: "Claude" }), ["/w"], read, now), { file: "/else/b.txt", before: "", title: "Claude" });
});
test("no diff when the edit is not found once", () => {
  assert.equal(plan(cmd({ new: "gone" }), ["/w"], read, now).before, null);
  assert.equal(plan(cmd({ path: "twice.txt", old: "y", new: "x" }), ["/w"], read, now).before, null);
  assert.equal(plan(cmd({ old: "", new: "" }), ["/w"], read, now).before, null);
});
test("not this window's, old, or not a request: nothing", () => {
  assert.equal(plan(cmd(), ["/other"], read, now), null);
  assert.equal(plan(cmd({ at: now - 31_000 }), ["/w"], read, now), null);
  assert.equal(plan(cmd({ path: "missing.txt" }), ["/w"], read, now), null);
  assert.equal(plan({ folder: "/w" }, ["/w"], read, now), null);
  assert.equal(plan(null, ["/w"], read, now), null);
});
test("a request's name", () => {
  assert.ok(isCommand("1794313922226-0af3.json"));
  for (const n of ["x.json", "1-0a.json.tmp", ".1-0a.json", "../1-0a.json"]) assert.ok(!isCommand(n));
});
