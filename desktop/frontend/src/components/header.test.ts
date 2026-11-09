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
});
