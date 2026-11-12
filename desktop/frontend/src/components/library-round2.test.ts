// @vitest-environment happy-dom
// Review round 2: following relative links from documents of two projects, and one after another.
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
// Files of this Mac: each document links on by a relative path.
const disk: Record<string, string> = {
  "/project-a/report.md": "See [notes](docs/notes.md).",
  "/project-b/report.md": "See [notes](docs/notes.md).",
  "/project-a/docs/notes.md": "Then [more](more.md).",
  "/project-b/docs/notes.md": "Then [more](more.md).",
  "/project-a/docs/more.md": "The end.",
};
const doc = (path: string) => ({ kind: "change", name: "report.md", type: "text/markdown", size: 9, path, msg: "m" + path, thread: "T", at: now });

let mounted: any[] = [];
beforeEach(() => {
  vi.resetModules();
  fake = fakeNode();
  vi.stubGlobal("IntersectionObserver", undefined);
  for (const [k, v] of Object.entries({ Chats: { items: [] }, Status: { ok: true }, Home: "/Users/me", Agents: [], Thread: [] })) fake.Node[k].mockResolvedValue(v);
  fake.Node.Artifacts.mockResolvedValue({ items: [doc("/project-a/report.md"), doc("/project-b/report.md")], next: "" });
  // As the app's side does: a relative path from its base.
  fake.Node.LocalFile.mockImplementation(async (path: string, base: string) => {
    const p = path.startsWith("/") ? path : base + "/" + path;
    if (!(p in disk)) throw new Error("no such file " + p);
    return { name: p.split("/").pop(), type: "text/markdown", size: disk[p].length, path: p, data: btoa(disk[p]) };
  });
  document.body.innerHTML = "";
});
afterEach(() => { for (const m of mounted) unmount(m); mounted = []; vi.unstubAllGlobals(); });

const settle = async () => { for (let i = 0; i < 3; i++) { await flush(); flushSync(); } };
const click = (el: Element | null) => { el!.dispatchEvent(new MouseEvent("click", { bubbles: true, cancelable: true })); flushSync(); };

async function showApp() {
  ({ mount, unmount, flushSync } = await import("svelte"));
  const { app } = await import("../lib/state.svelte");
  mounted.push(mount((await import("../App.svelte")).default, { target: document.body }));
  await settle();
  app.view = "artifacts";
  await settle();
  return app;
}
// From the library, a document, then its link.
async function follow(app: any, card: number) {
  app.view = "artifacts";
  await settle();
  click(document.querySelectorAll(".lib .lc .hit")[card]);
  await settle();
  click(document.querySelector(".lib .av .md-wrap a"));
  await settle();
}

describe("artifact references, review round 2", () => {
  it("keeps two projects' docs/notes.md apart in the chat's panel, and one file reached twice as one", async () => {
    const app = await showApp();
    await follow(app, 0);
    await follow(app, 1);
    await follow(app, 0); // the first one again
    expect(app.openedArtifacts.map((a: any) => a.path)).toEqual(["/project-a/docs/notes.md", "/project-b/docs/notes.md"]);
    expect(app.artifact?.path).toBe("/project-a/docs/notes.md");

    const panel = document.createElement("div");
    document.body.append(panel);
    mounted.push(mount((await import("./Artifacts.svelte")).default, { target: panel })); // threw each_key_duplicate
    await settle();
    const its = [...panel.querySelectorAll(".it")];
    expect(its.map(b => b.getAttribute("title"))).toEqual(["/project-a/docs/notes.md", "/project-b/docs/notes.md"]);
    expect(its.map(b => b.classList.contains("on"))).toEqual([true, false]);
  });

  it("follows relative links one after another from where each document is", async () => {
    const app = await showApp();
    await follow(app, 0);
    expect(app.artifact?.path).toBe("/project-a/docs/notes.md");
    const panel = document.createElement("div");
    document.body.append(panel);
    mounted.push(mount((await import("./Artifacts.svelte")).default, { target: panel }));
    await settle();
    expect(panel.querySelector(".md-wrap")?.textContent).toContain("Then");
    click(panel.querySelector(".md-wrap a")); // more.md, from /project-a/docs
    await settle();
    expect(app.artifact?.path).toBe("/project-a/docs/more.md");
    expect(panel.querySelector(".md-wrap")?.textContent).toContain("The end.");
  });
});
