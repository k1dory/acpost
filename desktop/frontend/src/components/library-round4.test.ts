// @vitest-environment happy-dom
// Review round 4: an attachment opened from its message, chats opened in quick succession.
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
let mount: typeof import("svelte").mount, unmount: typeof import("svelte").unmount, flushSync: typeof import("svelte").flushSync;
import { fakeNode, deferred, flush, type Fake } from "../test/fake";

let fake: Fake;
vi.mock("../lib/api", async () => {
  const errors = await import("../lib/errors");
  const b = await import("../lib/base64");
  const Node = new Proxy({}, { get: (_, k: string) => fake.Node[k] });
  return { ...errors, ...b, Node, on: (n: string, f: (d: any) => void) => fake.on(n, f) };
});
vi.mock("@wailsio/runtime", () => ({ Clipboard: { SetText: vi.fn() }, Browser: { OpenURL: vi.fn() }, Events: { On: () => () => {} } }));

const now = new Date().toISOString();
let mounted: any[] = [];
beforeEach(() => {
  vi.resetModules();
  fake = fakeNode();
  vi.stubGlobal("IntersectionObserver", undefined);
  for (const [k, v] of Object.entries({ Chats: { items: [] }, Status: { ok: true }, Home: "/Users/me", Agents: [], Thread: [], Events: [] })) fake.Node[k].mockResolvedValue(v);
  document.body.innerHTML = "";
});
afterEach(() => { for (const m of mounted) unmount(m); mounted = []; vi.unstubAllGlobals(); });

const settle = async () => { for (let i = 0; i < 3; i++) { await flush(); flushSync(); } };
const click = (el: Element | null) => { el!.dispatchEvent(new MouseEvent("click", { bubbles: true, cancelable: true })); flushSync(); };
async function load() {
  ({ mount, unmount, flushSync } = await import("svelte"));
  return (await import("../lib/state.svelte")).app;
}

describe("review round 4", () => {
  it("reads the links of an attachment opened from its message from the chat's directory", async () => {
    const app = await load();
    const m = { id: "a1", from: "agent:claude", to: "user:me", thread: "a1", depth: 0, state: "done", created_at: now, updated_at: now,
      meta: { acp_cwd: "/project-a" }, files: [{ name: "report.md", type: "text/markdown", size: 9, reply: true }] } as any;
    app.msgs = [m];
    fake.Node.MessageFile.mockResolvedValue({ name: "report.md", type: "text/markdown", size: 9, data: btoa("See [notes](notes.md).") });
    fake.Node.LocalFile.mockImplementation(async (path: string) => ({ name: "notes.md", type: "text/markdown", size: 5, path, data: btoa("notes") }));
    mounted.push(mount((await import("../App.svelte")).default, { target: document.body }));
    await settle();
    app.msgs = [m];
    const host = document.createElement("div");
    document.body.append(host);
    mounted.push(mount((await import("./Message.svelte")).default, { target: host, props: { m } }));
    const panel = document.createElement("div");
    document.body.append(panel);
    mounted.push(mount((await import("./Artifacts.svelte")).default, { target: panel }));
    await settle();
    click(host.querySelector("button.art")); // the attachment, from the message
    await settle();
    expect(app.artifact).toMatchObject({ msg: "a1", name: "report.md", base: "/project-a" });
    click(panel.querySelector(".md-wrap a"));
    await settle();
    expect(app.artifact?.path).toBe("/project-a/notes.md");
    expect(document.querySelector(".toast")).toBeNull();
  });

  it("opens only the chat clicked last, and none once the library is gone", async () => {
    const app = await load();
    app.conn = { ok: true } as any;
    fake.Node.Artifacts.mockResolvedValue({ items: [
      { kind: "file", name: "a.md", size: 1, msg: "ma", thread: "TA", title: "A", at: now },
      { kind: "file", name: "b.md", size: 1, msg: "mb", thread: "TB", title: "B", at: now },
    ], next: "" });
    const first = deferred<any>(), second = deferred<any>();
    fake.Node.Chats.mockReturnValueOnce(first.promise).mockReturnValueOnce(second.promise);
    const lib = mount((await import("./Library.svelte")).default, { target: document.body });
    mounted.push(lib);
    await settle();
    const chats = () => [...document.querySelectorAll(".lc button.chat")];
    click(chats()[0]); // A
    click(chats()[1]); // then B
    second.resolve({ items: [] });
    await settle();
    expect(app.chat?.key).toBe("thread:TB");
    first.resolve({ items: [] }); // A's answer, late
    await settle();
    expect(app.chat?.key).toBe("thread:TB");

    // A click whose answer comes after the library is gone.
    app.view = "artifacts";
    const third = deferred<any>();
    fake.Node.Chats.mockReturnValueOnce(third.promise);
    click(chats()[0]);
    unmount(lib); mounted = [];
    third.resolve({ items: [] });
    await settle();
    expect(app.chat?.key).toBe("thread:TB");
    expect(app.view).toBe("artifacts");
  });
});
