// @vitest-environment happy-dom
// Round 2, 7: the IDE pane's reload goes through the pane's one queue, naming the address it is
// for; the IDE view mounted for real (happy-dom), the bindings mocked.
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { fakeNode, deferred, flush, type Fake } from "../test/fake";
import { Pane, paneOwners } from "./idePane";

let fake: Fake;
vi.mock("../lib/api", async () => {
  const errors = await import("../lib/errors");
  const Node = new Proxy({}, { get: (_, k: string) => fake.Node[k] });
  return { ...errors, Node, on: (n: string, f: (d: any) => void) => fake.on(n, f) };
});
vi.mock("@wailsio/runtime", () => ({ Clipboard: { SetText: vi.fn() }, Browser: { OpenURL: vi.fn() }, Events: { On: () => () => {} } }));

// A native side whose calls stay out until let go.
function native() {
  const log: string[] = [], out: (() => void)[] = [];
  const call = (what: string) => { log.push(what); return new Promise<void>(res => out.push(res)); };
  return {
    log, out,
    calls: { show: (u: string) => call("show " + u), hide: () => call("hide"), reload: (u: string) => call("reload " + u) },
    async next() { out.shift()!(); await flush(); },
  };
}
const R = { x: 0, y: 0, w: 100, h: 100 };

describe("the pane's queue", () => {
  it("sends a reload after the call out, with its address", async () => {
    const n = native(), p = new Pane(n.calls);
    p.show("A", R);
    const r = p.reload("A");
    expect(n.log).toEqual(["show A"]);
    await n.next();
    expect(n.log).toEqual(["show A", "reload A"]);
    await n.next();
    expect(await r).toBe(true);
  });
  it("sends a state asked after a reload first (the native side drops a reload no longer shown)", async () => {
    const n = native(), p = new Pane(n.calls);
    p.show("A", R);
    p.reload("A");
    p.show("B", R);
    while (n.out.length) await n.next();
    expect(n.log).toEqual(["show A", "show B", "reload A"]);
  });
  it("drops the reload of a view that left, and of an owner no longer the pane's", async () => {
    const n = native(), owner = paneOwners(n.calls);
    const a = owner();
    a.show("A", R);
    const r = a.reload("A");
    a.close();
    const b = owner();
    b.show("B", R);
    expect(await a.reload("A")).toBe(false);
    while (n.out.length) await n.next();
    expect(await r).toBe(false);
    expect(n.log).toEqual(["show A", "show B"]); // the hide replaced by the show after it
  });
  it("drops the reload of an owner a newer one took the pane from before it closed", async () => {
    const n = native(), owner = paneOwners(n.calls);
    const a = owner();
    a.show("U", R);
    const r = a.reload("U");
    const b = owner();
    a.close();
    b.show("U", R);
    while (n.out.length) await n.next();
    expect(await r).toBe(false);
    expect(n.log).toEqual(["show U", "show U"]);
  });
});

describe("the IDE view", () => {
  beforeEach(() => {
    vi.resetModules();
    fake = fakeNode();
    document.body.innerHTML = "";
    vi.stubGlobal("matchMedia", () => ({ matches: false, addEventListener() {}, removeEventListener() {} }));
    vi.stubGlobal("ResizeObserver", class { observe() {} disconnect() {} });
    vi.spyOn(Element.prototype, "getBoundingClientRect").mockReturnValue({ left: 0, top: 0, width: 800, height: 600, x: 0, y: 0, right: 800, bottom: 600, toJSON() {} } as DOMRect);
  });
  afterEach(() => { vi.unstubAllGlobals(); vi.restoreAllMocks(); });

  it("reloads through the pane's queue: after the show out, for the address shown", async () => {
    const { mount, unmount, flushSync } = await import("svelte");
    const { app } = await import("../lib/state.svelte");
    app.home = "/h";
    const U = "http://127.0.0.1:1/?folder=%2Fh&tkn=x";
    fake.Node.IDEStart.mockResolvedValue(U);
    const show = deferred<void>();
    fake.Node.IDEShow.mockReturnValueOnce(show.promise);
    const IDE = (await import("./IDE.svelte")).default;
    const c = mount(IDE, { target: document.body });
    flushSync();
    await flush(); await new Promise(r => requestAnimationFrame(r)); await flush();
    expect(fake.Node.IDEShow).toHaveBeenCalledTimes(1);   // out: the first show, U loaded
    (document.querySelector("button.icon") as HTMLButtonElement).click();
    await flush(); await flush();
    expect(fake.Node.IDEStart).toHaveBeenCalledTimes(2);
    expect(fake.Node.IDEReload).not.toHaveBeenCalled();  // waits for the show out
    show.resolve();
    await flush(); await flush();
    expect(fake.Node.IDEReload).toHaveBeenCalledTimes(1);
    expect(fake.Node.IDEReload).toHaveBeenCalledWith(U);
    unmount(c);
  });
});
