// @vitest-environment happy-dom
// The chat's artifacts panel, after its viewer became a piece of its own (ArtifactView).
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

let mounted: any[] = [];
beforeEach(() => { vi.resetModules(); fake = fakeNode(); document.body.innerHTML = ""; });
afterEach(() => { for (const m of mounted) unmount(m); mounted = []; });

describe("the chat's artifacts panel", () => {
  it("lists the replies' files and the files opened, and shows the one picked", async () => {
    ({ mount, unmount, flushSync } = await import("svelte"));
    const { app } = await import("../lib/state.svelte");
    app.msgs = [{ id: "m1", from: "user:me", to: "agent:claude", thread: "m1", depth: 0, state: "done", created_at: "", updated_at: "",
      files: [{ name: "in.txt", type: "text/plain", size: 1 }, { name: "out.md", type: "text/markdown", size: 4, reply: true }] }] as any;
    app.openedArtifacts = [{ name: "page.html", path: "/w/page.html", base: "" }];
    fake.Node.MessageFile.mockResolvedValue({ name: "out.md", type: "text/markdown", size: 7, data: btoa("# Out") });
    fake.Node.LocalFile.mockResolvedValue({ name: "page.html", type: "text/html", size: 9, path: "/w/page.html", data: btoa("<p>hi</p>") });
    const Artifacts = (await import("./Artifacts.svelte")).default;
    mounted.push(mount(Artifacts, { target: document.body }));
    flushSync(); await flush(); flushSync();

    const its = [...document.querySelectorAll(".it")];
    expect(its.map(b => b.textContent?.trim())).toEqual(["out.md", "page.html"]); // the message's own file is not the agent's
    expect(fake.Node.MessageFile).toHaveBeenCalledWith("m1", "out.md");
    expect(document.querySelector(".md-wrap")?.textContent).toContain("Out");

    (its[1] as HTMLButtonElement).click();
    flushSync(); await flush(); flushSync();
    expect(fake.Node.LocalFile).toHaveBeenCalledWith("/w/page.html", "");
    const frame = document.querySelector("iframe.page") as HTMLIFrameElement;
    expect(frame.getAttribute("sandbox")).not.toContain("allow-scripts"); // a page's scripts wait to be allowed
  });
});
