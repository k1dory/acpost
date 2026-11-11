// @vitest-environment happy-dom
// The chat header's icon-only buttons draw their icon at headerIcon (lib/ui.ts), in a square button.
import { describe, it, expect, vi } from "vitest";
import { fakeNode, type Fake } from "../test/fake";

let fake: Fake;
vi.mock("../lib/api", async () => {
  const errors = await import("../lib/errors");
  const b = await import("../lib/base64");
  const Node = new Proxy({}, { get: (_, k: string) => fake.Node[k] });
  return { ...errors, ...b, Node, on: (n: string, f: (d: any) => void) => fake.on(n, f) };
});
vi.mock("@wailsio/runtime", () => ({ Clipboard: { SetText: vi.fn() }, Browser: { OpenURL: vi.fn() }, Events: { On: () => () => {} } }));

describe("chat header icons", () => {
  it("the fork, panel and cloud buttons draw their icon at headerIcon", async () => {
    fake = fakeNode();
    fake.Node.SessionHistory.mockResolvedValue([]);
    const svelte = await import("svelte");
    const { app } = await import("../lib/state.svelte");
    const { headerIcon } = await import("../lib/ui");
    app.conn = { ok: true };
    app.cloudState = "ok";
    app.cloudAgents = [{ ref: "r", name: "R", revision: 1 }];
    app.chat = { key: "session:claude/s1", kind: "session", agent: "agent:claude", session: "s1", cwd: "/x", repo: "github.com/me/a", title: "t", updated: "" };
    const Conversation = (await import("./Conversation.svelte")).default;
    const c = svelte.mount(Conversation, { target: document.body });
    svelte.flushSync();
    const buttons = [...document.querySelectorAll("header button.btn.icon")];
    expect(buttons.length).toBeGreaterThanOrEqual(2); // fork and panel (cloud when a hand-off is offered)
    for (const b of buttons) {
      const svg = b.querySelector("svg")!;
      expect(svg.getAttribute("width")).toBe(String(headerIcon));
      expect(b.getAttribute("aria-label")).toBeTruthy();
    }
    expect(headerIcon).toBeGreaterThanOrEqual(16);
    svelte.unmount(c);
  });

  it("beside the IDE (no sidebar there): a new task and another chat are had from the header", async () => {
    fake = fakeNode();
    fake.Node.SessionHistory.mockResolvedValue([]);
    const svelte = await import("svelte");
    const { app } = await import("../lib/state.svelte");
    app.conn = { ok: true };
    app.chat = { key: "session:claude/s1", kind: "session", agent: "agent:claude", session: "s1", cwd: "/x", title: "t", updated: "" };
    const Conversation = (await import("./Conversation.svelte")).default;
    const c = svelte.mount(Conversation, { target: document.body });
    const labels = () => [...document.querySelectorAll("header button")].map(b => b.getAttribute("aria-label"));
    const fork = "Fork: the conversation so far, in a new chat", panel = "Right panel (⌘\\)";
    app.view = "chat"; svelte.flushSync();
    expect(labels()).not.toContain("New task");
    expect(labels()).toEqual(expect.arrayContaining([fork, panel]));
    app.view = "ide"; svelte.flushSync();
    expect(labels()).toContain("New task");
    // Beside the IDE the right panel is hidden (its button would do nothing), and the fork goes too.
    expect(labels()).not.toContain(fork);
    expect(labels()).not.toContain(panel);
    expect(document.querySelector("header")!.classList.contains("beside")).toBe(true); // the hand-off form is laid within the column
    app.view = "chat"; svelte.flushSync();
    expect(labels()).toEqual(expect.arrayContaining([fork, panel]));
    expect(document.querySelector("header")!.classList.contains("beside")).toBe(false);
    app.view = "ide"; svelte.flushSync();
    (document.querySelector('header button[aria-label="Another chat (⌘K)"]') as HTMLButtonElement).click();
    expect(app.palette).toBe(true);
    app.palette = false;
    (document.querySelector('header button[aria-label="New task"]') as HTMLButtonElement).click();
    svelte.flushSync();
    expect(app.chat).toBe(null);
    expect(app.view).toBe("ide"); // the new task is written beside the IDE
    app.view = "chat";
    svelte.unmount(c);
  });
});
