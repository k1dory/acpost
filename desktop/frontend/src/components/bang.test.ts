// @vitest-environment happy-dom
// "!command" in the composer runs the person's command in the chat's directory; it is not a message.
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
async function setup() {
  svelte = await import("svelte");
  const { app } = await import("../lib/state.svelte");
  app.agents = [{ name: "claude", address: "agent:claude", driver: "acp", queued: 0, running: 0 }];
  app.conn = { ok: true };
  app.chat = { key: "thread:T", kind: "thread", thread: "T", agent: "agent:claude", cwd: "/work/repo", title: "t", updated: "" } as any;
  app.thread = "T"; app.target = "agent:claude";
  const Composer = (await import("./Composer.svelte")).default;
  const ShellRuns = (await import("./ShellRuns.svelte")).default;
  mounted.push(svelte.mount(ShellRuns, { target: document.body }), svelte.mount(Composer, { target: document.body }));
  svelte.flushSync();
  return app;
}
const textarea = () => document.querySelector("textarea") as HTMLTextAreaElement;
function type(text: string) { textarea().value = text; textarea().dispatchEvent(new Event("input", { bubbles: true })); svelte.flushSync(); }
const enter = () => { textarea().dispatchEvent(new KeyboardEvent("keydown", { key: "Enter", bubbles: true, cancelable: true })); svelte.flushSync(); };

beforeEach(() => { vi.resetModules(); fake = fakeNode(); document.body.innerHTML = ""; });
afterEach(() => { for (const m of mounted) svelte.unmount(m); mounted = []; });

describe("! in the composer", () => {
  it("runs the command in the chat's directory and shows what it printed; nothing is sent to the agent", async () => {
    const app = await setup();
    let done!: (r: any) => void;
    fake.Node.Shell.mockImplementation(() => new Promise(r => { done = r; }));
    type("!git status --short");
    expect(document.querySelector(".comp.bang")).toBeTruthy();
    enter();
    await flush(); svelte.flushSync();
    expect(fake.Node.Shell.mock.calls[0].slice(1)).toEqual(["git status --short", "/work/repo"]);
    expect(fake.Node.SendTask).not.toHaveBeenCalled();
    expect(textarea().value).toBe("");
    expect(document.querySelector(".sh")!.textContent).toContain("git status --short");
    // While it runs it can be stopped.
    ([...document.querySelectorAll(".sh button")].find(b => b.textContent === "Stop") as HTMLButtonElement).click();
    expect(fake.Node.ShellStop).toHaveBeenCalledWith(fake.Node.Shell.mock.calls[0][0]);
    done({ output: " M a.go\n", code: 1, ms: 40 });
    await flush(); svelte.flushSync();
    expect(document.querySelector(".sh .out")!.textContent).toBe(" M a.go\n");
    expect(document.querySelector(".sh .chip.crit")!.textContent).toBe("1");
    // Into the message only when the person puts it there.
    ([...document.querySelectorAll(".sh button")].find(b => b.textContent === "Add to message") as HTMLButtonElement).click();
    svelte.flushSync();
    expect(textarea().value).toBe("$ git status --short\n```\n M a.go\n```\n(exit 1)\n");
    expect(app.shellsHere()).toHaveLength(1);
  });

  it("a bare ! runs nothing; a message that starts with a space and ! is a message; a run that fails says why", async () => {
    const app = await setup();
    type("!  ");
    enter();
    expect(fake.Node.Shell).not.toHaveBeenCalled();
    fake.Node.SendTask.mockResolvedValue({ id: "m1", from: "user:me", to: "agent:claude", thread: "T", depth: 0, state: "queued", created_at: "", updated_at: "" });
    type(" !important: read this");
    enter();
    await flush(); await flush();
    expect(fake.Node.SendTask.mock.calls[0][0].body).toBe("!important: read this");
    fake.Node.Shell.mockRejectedValue(new Error("fork/exec /bin/zsh: no such file"));
    type("!ls");
    enter();
    await flush(); svelte.flushSync();
    expect(document.querySelector(".sh .out.err")!.textContent).toContain("no such file");
    // Another chat does not show this one's runs.
    app.thread = "U"; app.chat = null;
    svelte.flushSync();
    expect(app.shellsHere()).toHaveLength(0);
  });
});
