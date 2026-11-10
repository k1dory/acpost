// A message of files alone (a screenshot, nothing typed) is a message.
import { describe, it, expect, vi, beforeEach } from "vitest";
import { fakeNode, deferred, flush, type Fake } from "../test/fake";
import { afterSent } from "./draft";

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

  it("whose only file could not be read is not sent, and the draft is free again", async () => {
    const { app } = await import("./state.svelte");
    app.agents = [{ name: "claude", address: "agent:claude", driver: "acp", queued: 0, running: 0 }] as any;
    const d = app.draftOf();
    const read = deferred<ArrayBuffer>();
    d.add([{ name: "shot.png", type: "image/png", size: 2, arrayBuffer: () => read.promise } as any], null, () => {});
    const going = app.submit(d);
    read.reject(new Error("gone"));
    expect(await going).toBe(false);
    expect(fake.Node.SendTask).not.toHaveBeenCalled();
    expect(fake.Node.CreateCloudTask).not.toHaveBeenCalled();
    expect(d.busy).toBe(false);
  });

  it("leaves what is typed while it goes exactly as typed", async () => {
    expect(afterSent("    nextIndentedLine()", "")).toBe("    nextIndentedLine()");
    expect(afterSent("", "")).toBe("");
    expect(afterSent("\n    code()", "\n")).toBe("\n    code()");
    expect(afterSent("\n", "\n")).toBe("");
    expect(afterSent("sent more", "sent")).toBe("more");
  });
});
