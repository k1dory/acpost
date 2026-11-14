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
/** An attachment as the app makes one (attachment(): its identity given). */
let attach: (f: { name: string; type: string; size: number; data: string }) => import("../lib/composer.svelte").Attachment;
async function setup() {
  svelte = await import("svelte");
  const { app } = await import("../lib/state.svelte");
  attach = (await import("../lib/composer.svelte")).attachment; // the app's own instance: identities do not collide
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

  it("a named file pasted twice while the first is still read: two names (R1.1)", async () => {
    const app = await setup();
    const slow = (text: string) => {
      const f = new File([text], "report.txt", { type: "text/plain" });
      const r = Promise.withResolvers<ArrayBuffer>();
      Object.defineProperty(f, "arrayBuffer", { value: () => r.promise });
      return { f, r };
    };
    const a = slow("one"), b = slow("two");
    paste([a.f]);
    paste([b.f]);
    a.r.resolve(new TextEncoder().encode("one").buffer);
    b.r.resolve(new TextEncoder().encode("two").buffer);
    await settle(app);
    expect(app.draftOf().files.map(f => f.name)).toEqual(["report.txt", "report (2).txt"]);
  });

  it("a paste and a drop of one name: two names (R1.1)", async () => {
    const app = await setup();
    paste([new File(["one"], "report.txt", { type: "text/plain" })]);
    const drop = new Event("drop", { bubbles: true, cancelable: true });
    Object.defineProperty(drop, "dataTransfer", { value: { files: [new File(["two"], "report.txt", { type: "text/plain" })] } });
    document.querySelector(".comp")!.dispatchEvent(drop);
    await settle(app);
    expect(app.draftOf().files.map(f => f.name)).toEqual(["report.txt", "report (2).txt"]);
  });

  // A drag of files from the Finder, as the webview reports it over an element.
  function drag(type: string, on: EventTarget, files: File[] = [], types = ["Files"]) {
    const e = new Event(type, { bubbles: true, cancelable: true });
    Object.defineProperty(e, "dataTransfer", { value: { files, types, dropEffect: "none" } });
    on.dispatchEvent(e);
    return e;
  }

  it("files dropped anywhere in the window are attached, and the window says so while they are over it", async () => {
    const app = await setup();
    const far = document.body;                       // not the composer: the conversation, the sidebar
    expect(document.querySelector(".dropzone")).toBeNull();
    expect(drag("dragenter", far).defaultPrevented).toBe(true);
    expect(drag("dragover", far).defaultPrevented).toBe(true);   // without it the webview refuses the drop
    await settle(app);
    expect(document.querySelector(".dropzone")?.textContent).toContain("Drop files to attach them");
    const e = drag("drop", far, [new File(["x"], "notes.txt", { type: "text/plain" })]);
    await settle(app);
    expect(e.defaultPrevented).toBe(true);           // not opened in the app's place
    expect(app.draftOf().files.map(f => f.name)).toEqual(["notes.txt"]);
    expect(document.querySelector(".dropzone")).toBeNull();
  });

  it("a drag that leaves the window takes the notice with it; one passing between elements does not", async () => {
    const app = await setup();
    const comp = document.querySelector(".comp")!;
    drag("dragenter", document.body);
    drag("dragenter", comp);                         // into a child: an enter, then the leave of the parent
    drag("dragleave", document.body);
    await settle(app);
    expect(document.querySelector(".dropzone")).not.toBeNull();
    drag("dragleave", comp);
    await settle(app);
    expect(document.querySelector(".dropzone")).toBeNull();
    expect(app.draftOf().files).toEqual([]);
  });

  it("a drag without files (text, a card) is left to whatever it is for", async () => {
    const app = await setup();
    expect(drag("dragenter", document.body, [], ["text/plain"]).defaultPrevented).toBe(false);
    expect(drag("dragover", document.body, [], ["text/plain"]).defaultPrevented).toBe(false);
    expect(drag("drop", document.body, [], ["text/plain"]).defaultPrevented).toBe(false);
    await settle(app);
    expect(document.querySelector(".dropzone")).toBeNull();
    expect(app.draftOf().files).toEqual([]);
  });

  it("an address ending in the image's name pastes as text (R1.3)", async () => {
    const app = await setup();
    expect(paste([img()], "https://example.com/image.png")).toBe(false);
    await settle(app);
    expect(app.draftOf().files).toEqual([]);
  });

  it("text that is the name with spaces and blank lines pastes as text (R2)", async () => {
    const app = await setup();
    expect(paste([img()], "  image.png\n\n")).toBe(false);
    await settle(app);
    expect(app.draftOf().files).toEqual([]);
  });

  it("a file whose name begins with a space, with exactly that name as text, is attached (R2)", async () => {
    const app = await setup();
    const f = new File(["x"], " report.txt", { type: "text/plain" });
    expect(paste([f], " report.txt")).toBe(true);
    await settle(app);
    expect(app.draftOf().files.map(x => x.name)).toEqual([" report.txt"]);
  });

  it("a dotfile keeps its name (R1.4)", async () => {
    const app = await setup();
    paste([new File(["A=1"], ".env", { type: "text/plain" })]);
    await settle(app);
    expect(app.draftOf().files.map(f => f.name)).toEqual([".env"]);
  });

  it("over max_files: the same error as a drop", async () => {
    const app = await setup();
    app.limits = { max_body: 1 << 20, max_files: 1, max_files_size: 1 << 20 };
    app.draftOf().files = [attach({ name: "a.txt", type: "text/plain", size: 1, data: "YQ==" })];
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
