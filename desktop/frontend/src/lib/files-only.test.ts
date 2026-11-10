// A message of files alone (a screenshot, nothing typed) is a message.
import { describe, it, expect, vi, beforeEach } from "vitest";
import { fakeNode, type Fake } from "../test/fake";

let fake: Fake;
vi.mock("./api", async () => {
  const errors = await import("./errors");
  const Node = new Proxy({}, { get: (_, k: string) => fake.Node[k] });
  return { ...errors, Node, on: (n: string, f: (d: any) => void) => fake.on(n, f), b64: (s: string) => s, unb64: () => new Uint8Array() };
});

beforeEach(() => {
  vi.resetModules();
  fake = fakeNode();
  fake.Node.Chats.mockResolvedValue({ items: [] });
  fake.Node.Agents.mockResolvedValue([]);
});

describe("a message without words", () => {
  it("goes with its files, and leaves the draft empty", async () => {
    const { app } = await import("./state.svelte");
    const { attachment } = await import("./composer.svelte");
    app.agents = [{ name: "claude", address: "agent:claude", driver: "acp", queued: 0, running: 0 }] as any;
    app.conn = { ok: true } as any;
    fake.Node.SendTask.mockResolvedValue({ id: "m1", from: "user:me", to: "agent:claude", thread: "t1", depth: 0, state: "queued", created_at: "", updated_at: "" });
    const d = app.draftOf();
    d.files = [attachment({ name: "shot.png", type: "image/png", size: 4, data: "AAAA" })];
    expect(await app.submit(d)).toBe(true);
    const task = fake.Node.SendTask.mock.calls[0][0];
    expect(task.body).toBe("");
    expect(task.files).toEqual([{ name: "shot.png", type: "image/png", data: "AAAA" }]);
    expect(d.files).toEqual([]);
  });

  it("and without files is not sent", async () => {
    const { app } = await import("./state.svelte");
    const d = app.draftOf();
    d.text = "  \n";
    expect(await app.submit(d)).toBe(false);
    expect(fake.Node.SendTask).not.toHaveBeenCalled();
  });
});
