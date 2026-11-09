import { describe, it, expect } from "vitest";
import { Surface, surfaceOwners } from "./surface";
import { deferred, flush } from "../test/fake";

// A native view that records the calls as they reach it, each answered when the test says.
function view() {
  const calls: { s: any; d: ReturnType<typeof deferred<void>> }[] = [];
  return { calls, apply: (s: any) => { const d = deferred<void>(); calls.push({ s, d }); return d.promise; } };
}

describe("one queue for a native view (finding 10)", () => {
  it("a hide asked while a show is out goes after it, never beside it", async () => {
    const v = view();
    const q = new Surface(v.apply);
    const shown = q.set("show");
    const hidden = q.set("hide");
    await flush();
    expect(v.calls.map(c => c.s)).toEqual(["show"]); // the hide waits for the show to return
    v.calls[0].d.resolve();
    await flush();
    expect(v.calls.map(c => c.s)).toEqual(["show", "hide"]);
    v.calls[1].d.resolve();
    expect(await shown).toBe(true);
    expect(await hidden).toBe(true);
  });

  it("states asked while a call is out: only the last is sent", async () => {
    const v = view();
    const q = new Surface(v.apply);
    q.set(1);
    const a = q.set(2), b = q.set(3);
    await flush();
    v.calls[0].d.resolve(); await flush();
    v.calls[1]?.d.resolve(); await flush();
    expect(v.calls.map(c => c.s)).toEqual([1, 3]);
    expect(await a).toBe(false);
    expect(await b).toBe(true);
  });

  it("an owner that left does not hide the view its successor shows", async () => {
    const v = view();
    const owner = surfaceOwners<string>(v.apply, "hidden");
    const old = owner();
    old.set("old");
    const now = owner();
    now.set("new");
    expect(await old.close()).toBe(false);
    for (let i = 0; i < 3; i++) { v.calls.forEach(c => c.d.resolve()); await flush(); }
    expect(v.calls.map(c => c.s)).toEqual(["old", "new"]);
    old.set("late");
    await flush();
    expect(v.calls.length).toBe(2);
  });

  it("a failed call is the caller's error, and the queue goes on", async () => {
    const v = view();
    const q = new Surface(v.apply);
    const a = q.set("x");
    v.calls[0].d.reject(new Error("no window"));
    await expect(a).rejects.toThrow("no window");
    const b = q.set("y");
    await flush();
    v.calls[1].d.resolve();
    expect(await b).toBe(true);
  });
});
