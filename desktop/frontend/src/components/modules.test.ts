// @vitest-environment happy-dom
// Modules: views the person adds (a page beside the chat), chosen where Agents and IDE are.
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { fakeNode, flush, type Fake } from "../test/fake";

let fake: Fake;
vi.mock("../lib/api", async () => {
  const errors = await import("../lib/errors");
  const b = await import("../lib/base64");
  const Node = new Proxy({}, { get: (_, k: string) => fake.Node[k] });
  return { ...errors, ...b, Node, on: (n: string, f: (d: any) => void) => fake.on(n, f) };
});
vi.mock("@wailsio/runtime", () => ({ Clipboard: { SetText: vi.fn() }, Browser: { OpenURL: vi.fn() }, Events: { On: () => () => {} } }));

let svelte: typeof import("svelte");
let mounted: any[] = [];
const wiki = { id: "mod_1", name: "Wiki", url: "https://wiki.example.com/" };

async function setup() {
  svelte = await import("svelte");
  const { app } = await import("../lib/state.svelte");
  app.conn = { ok: true };
  return app;
}
beforeEach(() => {
  vi.resetModules();
  fake = fakeNode();
  fake.Node.Menu.mockResolvedValue(-2); // no window: the page's own menu
  document.body.innerHTML = "";
});
afterEach(() => { for (const m of mounted) svelte.unmount(m); mounted = []; });

describe("modules", () => {
  it("are listed in the view switcher after Agents and IDE, by their own names, and open as views", async () => {
    const app = await setup();
    fake.Node.Modules.mockResolvedValue([wiki]);
    await app.loadModules();
    const ViewSwitch = (await import("./ViewSwitch.svelte")).default;
    mounted.push(svelte.mount(ViewSwitch, { target: document.body }));
    svelte.flushSync();
    (document.querySelector("button.trig") as HTMLButtonElement).click();
    await flush(); svelte.flushSync();
    const items = [...document.querySelectorAll(".menu .mi")] as HTMLButtonElement[];
    expect(items.map(i => i.querySelector(".l")!.textContent)).toEqual(["Agents view", "IDE view", "Wiki", "Add a module…"]);
    items[2].click();
    svelte.flushSync();
    expect(app.view).toBe("mod:mod_1");
    expect(app.module).toEqual(wiki);
    expect(app.beside).toBe(true);
    expect(document.querySelector("button.trig")!.textContent).toContain("Wiki");
  });

  it("\"Add a module…\" opens the settings at Modules; a module saved there is asked of the app and listed", async () => {
    const app = await setup();
    fake.Node.Modules.mockResolvedValue([]);
    await app.loadModules();
    const ViewSwitch = (await import("./ViewSwitch.svelte")).default;
    mounted.push(svelte.mount(ViewSwitch, { target: document.body }));
    svelte.flushSync();
    (document.querySelector("button.trig") as HTMLButtonElement).click();
    await flush(); svelte.flushSync();
    ([...document.querySelectorAll(".menu .mi")].at(-1) as HTMLButtonElement).click();
    expect(app.view).toBe("settings");
    const Settings = (await import("./Settings.svelte")).default;
    mounted.push(svelte.mount(Settings, { target: document.body }));
    svelte.flushSync();
    expect(document.querySelector("h2")!.textContent).toBe("Modules");
    expect(app.settingsAt).toBe(""); // once: the settings opened later are at their first section
    ([...document.querySelectorAll("button.btn.primary")].find(b => b.textContent!.includes("New module")) as HTMLButtonElement).click();
    svelte.flushSync();
    const [name, url] = [...document.querySelectorAll(".form input")] as HTMLInputElement[];
    name.value = "Wiki"; name.dispatchEvent(new Event("input", { bubbles: true }));
    url.value = "wiki.example.com"; url.dispatchEvent(new Event("input", { bubbles: true }));
    svelte.flushSync();
    fake.Node.PutModule.mockResolvedValue(wiki);
    fake.Node.Modules.mockResolvedValue([wiki]);
    ([...document.querySelectorAll(".form button.btn.primary")].at(-1) as HTMLButtonElement).click();
    await flush(); await flush(); svelte.flushSync();
    expect(fake.Node.PutModule).toHaveBeenCalledWith({ id: "", name: "Wiki", url: "wiki.example.com" });
    expect(app.modules).toEqual([wiki]);
  });

  it("the module shown is laid over its box by its id; removed, the app is back at the chat", async () => {
    const app = await setup();
    fake.Node.Modules.mockResolvedValue([wiki]);
    await app.loadModules();
    app.view = "mod:mod_1";
    vi.stubGlobal("requestAnimationFrame", (f: FrameRequestCallback) => { f(0); return 0; });
    vi.stubGlobal("ResizeObserver", class { observe() {} disconnect() {} });
    const proto = (globalThis as any).HTMLElement.prototype;
    const was = proto.getBoundingClientRect;
    proto.getBoundingClientRect = () => ({ left: 0, top: 50, width: 800, height: 600, right: 800, bottom: 650, x: 0, y: 50, toJSON() {} });
    const ModuleView = (await import("./ModuleView.svelte")).default;
    mounted.push(svelte.mount(ModuleView, { target: document.body, props: { m: wiki } }));
    svelte.flushSync();
    await flush();
    proto.getBoundingClientRect = was;
    vi.unstubAllGlobals();
    expect(fake.Node.ModuleShow).toHaveBeenCalled();
    expect(fake.Node.ModuleShow.mock.calls.at(-1)!.slice(0, 5)).toEqual(["mod_1", 0, 50, 800, 600]);
    fake.Node.Modules.mockResolvedValue([]);
    await app.loadModules();
    expect(app.view).toBe("chat");
  });
});
