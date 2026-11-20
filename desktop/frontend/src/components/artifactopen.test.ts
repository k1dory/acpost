// @vitest-environment happy-dom
// "Open" opens what was clicked: another file chosen while the IDE starts is not opened (nor run).
import { describe, it, expect, vi, beforeEach } from "vitest";
import { fakeNode, flush, deferred, type Fake } from "../test/fake";

let fake: Fake;
vi.mock("../lib/api", async () => {
  const errors = await import("../lib/errors");
  const b = await import("../lib/base64");
  const Node = new Proxy({}, { get: (_, k: string) => fake.Node[k] });
  return { ...errors, ...b, Node, on: (n: string, f: (d: any) => void) => fake.on(n, f) };
});
vi.mock("@wailsio/runtime", () => ({ Clipboard: { SetText: vi.fn() }, Browser: { OpenURL: vi.fn() }, Events: { On: () => () => {} } }));

beforeEach(() => { vi.resetModules(); fake = fakeNode(); document.body.innerHTML = ""; });

describe("Open on a text file", () => {
  it("opens the file clicked, not one chosen while the IDE was starting", async () => {
    const svelte = await import("svelte");
    const { app } = await import("../lib/state.svelte");
    const { reactive } = await import("../test/props.svelte");
    app.draft.cwd = "/work";
    const file = (path: string) => ({ name: path.split("/").pop(), type: "text/plain", size: 2, path, data: btoa("hi") });
    fake.Node.LocalFile.mockImplementation(async (p: string) => file(p));
    const start = deferred();
    fake.Node.IDEStart.mockReturnValue(start.promise);
    const props = reactive({ cur: { name: "report.txt", path: "/work/report.txt" } as any });
    const ArtifactView = (await import("./ArtifactView.svelte")).default;
    svelte.mount(ArtifactView, { target: document.body, props });
    svelte.flushSync(); await flush(); svelte.flushSync();
    const open = [...document.querySelectorAll<HTMLButtonElement>(".bar .btn")].find(b => b.textContent === "Open")!;
    open.click();
    await flush();
    props.cur = { name: "install.command", path: "/work/install.command" };
    svelte.flushSync(); await flush();
    start.reject(new Error("the IDE did not start"));
    await flush(); await flush();
    // The IDE did not show it: nothing is opened (nor run) in its stead.
    expect(fake.Node.OpenLocal).not.toHaveBeenCalled();
    expect(fake.Node.OpenArtifact).not.toHaveBeenCalled();
  });

  it("not shown in the IDE, says why; its own app is a click of its own", async () => {
    const svelte = await import("svelte");
    const { app } = await import("../lib/state.svelte");
    app.draft.cwd = "/work";
    fake.Node.LocalFile.mockResolvedValue({ name: "run.command", type: "text/plain", size: 2, path: "/work/run.command", data: btoa("hi") });
    fake.Node.IDEStart.mockRejectedValue(new Error("the IDE is another acpost's"));
    const ArtifactView = (await import("./ArtifactView.svelte")).default;
    svelte.mount(ArtifactView, { target: document.body, props: { cur: { name: "run.command", path: "/work/run.command" } } });
    svelte.flushSync(); await flush(); svelte.flushSync();
    const btn = (label: string) => [...document.querySelectorAll<HTMLButtonElement>(".bar .btn")].find(b => b.textContent === label);
    btn("Open")!.click();
    await flush(); await flush(); svelte.flushSync();
    expect(fake.Node.OpenLocal).not.toHaveBeenCalled();
    expect(document.querySelector(".ideerr")!.textContent).toContain("another acpost");
    btn("Open in its own app")!.click();
    await flush();
    expect(fake.Node.OpenLocal).toHaveBeenCalledWith("/work/run.command", "");
  });
});
