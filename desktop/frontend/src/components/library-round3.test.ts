// @vitest-environment happy-dom
// Review round 3: ~/ links, opening the same file again, an attachment with no directory.
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
let mount: typeof import("svelte").mount, unmount: typeof import("svelte").unmount, flushSync: typeof import("svelte").flushSync;
import { fakeNode, flush, type Fake } from "../test/fake";

let fake: Fake;
vi.mock("../lib/api", async () => {
  const errors = await import("../lib/errors");
  const b = await import("../lib/base64");
  const Node = new Proxy({}, { get: (_, k: string) => fake.Node[k] });
  return { ...errors, ...b, Node, on: (n: string, f: (d: any) => void) => fake.on(n, f) };
});
vi.mock("@wailsio/runtime", () => ({ Clipboard: { SetText: vi.fn() }, Browser: { OpenURL: vi.fn() }, Events: { On: () => () => {} } }));

const now = new Date().toISOString();
const md = (path: string, text: string) => ({ name: path.split("/").pop(), type: "text/markdown", size: text.length, path, data: btoa(text) });
const selectedChat = [{ id: "a1", from: "user:me", to: "agent:claude", thread: "a1", depth: 0, state: "done", created_at: "", updated_at: "", meta: { acp_cwd: "/project-a" } }] as any;

let mounted: any[] = [];
beforeEach(() => {
  vi.resetModules();
  fake = fakeNode();
  vi.stubGlobal("IntersectionObserver", undefined);
  for (const [k, v] of Object.entries({ Chats: { items: [] }, Status: { ok: true }, Home: "/Users/me", Agents: [], Thread: [] })) fake.Node[k].mockResolvedValue(v);
  document.body.innerHTML = "";
});
afterEach(() => { for (const m of mounted) unmount(m); mounted = []; vi.unstubAllGlobals(); });

const settle = async () => { for (let i = 0; i < 3; i++) { await flush(); flushSync(); } };
const click = (el: Element | null) => { el!.dispatchEvent(new MouseEvent("click", { bubbles: true, cancelable: true })); flushSync(); };
async function load() {
  ({ mount, unmount, flushSync } = await import("svelte"));
  return (await import("../lib/state.svelte")).app;
}
async function panel() {
  const el = document.createElement("div");
  document.body.append(el);
  mounted.push(mount((await import("./Artifacts.svelte")).default, { target: el }));
  await settle();
  return el;
}

describe("artifact references, review round 3", () => {
  it("opens ~/… in the home directory, not under the chat's", async () => {
    const app = await load();
    app.home = "/Users/me";
    app.openArtifact({ name: "report.md", path: "~/report.md", base: "/project" });
    expect(app.artifact?.path).toBe("/Users/me/report.md");
    app.openArtifact({ name: "~", path: "~", base: "/project" });
    expect(app.artifact?.path).toBe("/Users/me");
    const { normalizeRef } = await import("../lib/artifactref");
    expect(normalizeRef({ name: "r", path: "~/r.md", base: "/project" }).path).toBe("~/r.md"); // home not known: the app's side expands it
  });

  it("reads a file again when it is opened again: changed since, or failed before", async () => {
    const app = await load();
    fake.Node.LocalFile.mockResolvedValueOnce(md("/w/a.md", "first")).mockRejectedValueOnce(new Error("busy")).mockResolvedValueOnce(md("/w/a.md", "third"));
    const el = await panel();
    app.openArtifact({ name: "a.md", path: "/w/a.md" });
    await settle();
    expect(el.querySelector(".md-wrap")?.textContent).toContain("first");
    app.openArtifact({ name: "a.md", path: "a.md", base: "/w" }); // the same file, overwritten
    await settle();
    expect(el.querySelector(".err")?.textContent).toContain("busy");
    app.openArtifact({ name: "a.md", path: "/w/a.md" }); // tried again
    await settle();
    expect(el.querySelector(".md-wrap")?.textContent).toContain("third");
    expect(fake.Node.LocalFile).toHaveBeenCalledTimes(3);
    expect(app.openedArtifacts.length).toBe(1);
  });

  it("does not open a relative link of an attachment with no directory against another chat's", async () => {
    const app = await load();
    fake.Node.Artifacts.mockResolvedValue({ items: [{ kind: "file", name: "r.md", type: "text/markdown", size: 9, msg: "m9", thread: "T9", at: now }], next: "" });
    fake.Node.MessageFile.mockResolvedValue({ name: "r.md", type: "text/markdown", size: 9, data: btoa("See [notes](notes.md) and `docs/x.md`.") });
    mounted.push(mount((await import("../App.svelte")).default, { target: document.body }));
    await settle();
    app.msgs = selectedChat; // another project's chat is the one selected
    app.view = "artifacts";
    await settle();
    click(document.querySelector(".lib .lc .hit"));
    await settle();
    expect(document.querySelector(".lib .av")?.getAttribute("data-artifact-base")).toBe("");
    click(document.querySelector(".lib .av .md-wrap a"));
    click(document.querySelector(".lib .av .md-wrap code.pl"));
    await settle();
    expect(app.openedArtifacts).toEqual([]);
    expect(fake.Node.LocalFile).not.toHaveBeenCalled();
    expect(app.view).toBe("artifacts");
    expect(document.querySelector(".toast")?.textContent).toContain("no directory of its own");
  });

  it("still reads a chat's own attachments' links from that chat's directory", async () => {
    const app = await load();
    app.msgs = [{ ...selectedChat[0], files: [{ name: "r.md", type: "text/markdown", size: 9, reply: true }] }];
    fake.Node.MessageFile.mockResolvedValue({ name: "r.md", type: "text/markdown", size: 9, data: btoa("See [notes](notes.md).") });
    fake.Node.LocalFile.mockResolvedValue(md("/project-a/notes.md", "notes"));
    mounted.push(mount((await import("../App.svelte")).default, { target: document.body }));
    const el = await panel();
    click(el.querySelector(".md-wrap a"));
    expect(app.artifact?.path).toBe("/project-a/notes.md");
  });
});
