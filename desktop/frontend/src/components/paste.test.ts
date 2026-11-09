// @vitest-environment happy-dom
// Pasting into the real composer (happy-dom), the bindings mocked.
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { fakeNode, deferred, flush, type Fake } from "../test/fake";

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
async function setup() {
  svelte = await import("svelte");
  const { app } = await import("../lib/state.svelte");
  app.agents = [{ name: "claude", address: "agent:claude", driver: "acp", queued: 0, running: 0 }];
  app.conn = { ok: true };
  app.thread = "T"; app.target = "agent:claude";
  const Composer = (await import("./Composer.svelte")).default;
  mounted.push(svelte.mount(Composer, { target: document.body }));
  svelte.flushSync();
  return app;
}
const img = () => new File([new Uint8Array([1, 2, 3])], "image.png", { type: "image/png" });
/** A paste on the textarea, as WebKit sends one; returns whether the default was prevented. */
function paste(files: File[], text = ""): boolean {
  const ev = new Event("paste", { bubbles: true, cancelable: true });
  Object.defineProperty(ev, "clipboardData", {
    value: { files, items: files.map(f => ({ kind: "file", type: f.type, getAsFile: () => f })), getData: (t: string) => (t === "text/plain" ? text : "") },
  });
  document.querySelector("textarea")!.dispatchEvent(ev);
  return ev.defaultPrevented;
}
const settle = async (app: any) => { await app.draftOf().live().adding; await flush(); svelte.flushSync(); };

beforeEach(() => {
  vi.resetModules();
  fake = fakeNode();
  fake.Node.Chats.mockResolvedValue({ items: [] });
  fake.Node.Agents.mockResolvedValue([]);
  fake.Node.Events.mockResolvedValue([]);
  document.body.innerHTML = "";
});
afterEach(() => { for (const m of mounted) svelte.unmount(m); mounted = []; });

describe("pasting into the composer", () => {
  it("an image is attached under a name of its own, the default paste prevented", async () => {
    const app = await setup();
    expect(paste([img()])).toBe(true);
    await settle(app);
    const files = app.draftOf().files;
    expect(files.length).toBe(1);
    expect(files[0].name).toMatch(/^pasted-\d{6}-\d+\.png$/);
    expect(document.querySelector(".files")?.textContent).toContain(files[0].name);
  });

  it("two pastes: two files, two names", async () => {
    const app = await setup();
    paste([img()]);
    paste([img()]); // before the first is read
    await settle(app);
    const names = app.draftOf().files.map(f => f.name);
    expect(names.length).toBe(2);
    expect(new Set(names).size).toBe(2);
  });

  it("plain text: nothing attached, the default paste left alone", async () => {
    const app = await setup();
    expect(paste([], "some text")).toBe(false);
    await settle(app);
    expect(app.draftOf().files).toEqual([]);
  });

  it("over max_files: the same error as a drop", async () => {
    const app = await setup();
    app.limits = { max_body: 1 << 20, max_files: 1, max_files_size: 1 << 20 };
    app.draftOf().files = [{ name: "a.txt", type: "text/plain", size: 1, data: "YQ==" }];
    paste([img()]);
    await settle(app);
    expect(app.draftOf().files.length).toBe(1);
    expect(app.error).toBe("A message takes at most 1 files");
  });

  it("while a message is being sent: the image stays in the draft, the sent text leaves", async () => {
    const app = await setup();
    const d = app.draftOf();
    d.text = "look";
    const sent = deferred();
    fake.Node.SendTask.mockReturnValue(sent.promise);
    const going = app.submit(d);
    paste([img()]);
    sent.resolve({ id: "m1", from: "user:me", to: "agent:claude", thread: "T", depth: 0, state: "queued", created_at: "", updated_at: "" });
    expect(await going).toBe(true);
    await settle(app);
    expect(app.draftOf()).toBe(d);
    expect(d.text).toBe("");
    expect(d.files.map(f => f.name)).toEqual([expect.stringMatching(/^pasted-/)]);
    expect(fake.Node.SendTask.mock.calls[0][0].files).toEqual([]); // not sent with the message before it
  });
});
