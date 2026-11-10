// Round 2 of the review (codex-second-desk): the app's state against a mocked node.
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { fakeNode, deferred, flush, type Fake } from "../test/fake";
import { overLimits } from "./draft";

let fake: Fake;
vi.mock("./api", async () => {
  const errors = await import("./errors");
  const Node = new Proxy({}, { get: (_, k: string) => fake.Node[k] });
  return { ...errors, Node, on: (n: string, f: (d: any) => void) => fake.on(n, f), b64: (s: string) => s, unb64: () => new Uint8Array() };
});

/** An attachment as the app makes one (attachment(): its identity given). */
let attach: (f: { name: string; type: string; size: number; data: string }) => import("./composer.svelte").Attachment;
async function load() {
  const { app } = await import("./state.svelte");
  attach = (await import("./composer.svelte")).attachment; // the app's own instance: identities do not collide
  app.agents = [{ name: "claude", address: "agent:claude", driver: "acp", queued: 0, running: 0 }];
  app.conn = { ok: true };
  return app;
}
const msg = (id: string, thread: string, state = "queued", to = "agent:claude", extra: any = {}) =>
  ({ id, from: "user:me", to, thread, depth: 0, state, created_at: "", updated_at: "", ...extra });
const evs = (from: number, n: number) => Array.from({ length: n }, (_, i) => ({ seq: from + i, time: "", kind: "text", data: JSON.stringify("x") }));

beforeEach(() => {
  vi.resetModules();
  fake = fakeNode();
  fake.Node.Chats.mockResolvedValue({ items: [] });
  fake.Node.Agents.mockResolvedValue([]);
});
afterEach(() => { vi.useRealTimers(); });

describe("what the app creates is created once (finding 1)", () => {
  it("a cloud task's sandbox is asked with one key until it is made, the answer lost or not", async () => {
    const app = await load();
    app.draft.where = "cloud"; app.draft.cloudAgent = "ref1"; app.draft.repo = "github.com/me/a";
    fake.Node.CreateCloudTask.mockRejectedValueOnce(new Error("lost")).mockResolvedValueOnce({ agent: "agent:sb_1" });
    fake.Node.SendTask.mockResolvedValue(msg("m1", "t1", "queued", "agent:sb_1"));
    expect(await app.send("do it", [])).toBe(false);
    expect(await app.send("do it", [])).toBe(true);
    const keys = fake.Node.CreateCloudTask.mock.calls.map(c => c[3]);
    expect(keys[0]).toMatch(/^[0-9a-f]{32}$/);
    expect(keys[1]).toBe(keys[0]);
  });

  it("another repository, or a new task, is another sandbox with another key", async () => {
    const app = await load();
    app.draft.where = "cloud"; app.draft.cloudAgent = "ref1"; app.draft.repo = "github.com/me/a";
    fake.Node.CreateCloudTask.mockRejectedValue(new Error("lost"));
    await app.send("do it", []);
    app.draft.repo = "github.com/me/b";
    await app.send("do it", []);
    app.newTask();
    await app.send("do it", []);
    const [a, b, c] = fake.Node.CreateCloudTask.mock.calls.map(c => c[3]);
    expect(new Set([a, b, c]).size).toBe(3);
  });

  it("a sandbox asked, a new task started meanwhile, the answer lost: the draft put back keeps the key", async () => {
    const app = await load();
    app.draft.where = "cloud"; app.draft.cloudAgent = "ref1"; app.draft.repo = "github.com/me/a";
    const made = deferred();
    fake.Node.CreateCloudTask.mockReturnValueOnce(made.promise).mockResolvedValueOnce({ agent: "agent:sb_1" });
    fake.Node.SendTask.mockResolvedValue(msg("m1", "t1", "queued", "agent:sb_1"));
    const d = app.draftOf();
    d.text = "do it";
    const going = app.submit(d);
    await flush();
    app.newTask();                        // while the sandbox is being made
    made.reject(new Error("lost"));       // it may have been made
    expect(await going).toBe(false);
    const back = app.draftOf();
    expect(back.text).toBe("do it");      // put back into the new task
    expect(await app.submit(back)).toBe(true);
    const [a, b] = fake.Node.CreateCloudTask.mock.calls.map(c => c[3]);
    expect(b).toBe(a);
  });

  it("put back where it no longer goes (another repository chosen), the draft asks a new sandbox", async () => {
    const app = await load();
    app.draft.where = "cloud"; app.draft.cloudAgent = "ref1"; app.draft.repo = "github.com/me/a";
    const made = deferred();
    fake.Node.CreateCloudTask.mockReturnValueOnce(made.promise).mockResolvedValueOnce({ agent: "agent:sb_2" });
    fake.Node.SendTask.mockResolvedValue(msg("m1", "t1", "queued", "agent:sb_2"));
    const d = app.draftOf();
    d.text = "do it";
    const going = app.submit(d);
    await flush();
    app.newTask();
    app.draft.repo = "github.com/me/b";
    made.reject(new Error("lost"));
    await going;
    await app.submit(app.draftOf());
    const [a, b] = fake.Node.CreateCloudTask.mock.calls.map(c => c[3]);
    expect(b).not.toBe(a);
  });

  it("a handoff asked again after a lost answer carries the first one's key; another handoff a new one", async () => {
    const app = await load();
    const req = { thread: "T", agent: "", session: "", cwd: "/w", cloud_agent: "ref1", note: "" };
    fake.Node.Handoff.mockRejectedValueOnce(new Error("lost")).mockResolvedValue({ chat: { key: "cloud:sb_1" } });
    await expect(app.handoff(req)).rejects.toThrow();
    expect((await app.handoff({ ...req })).key).toBe("cloud:sb_1");
    await app.handoff({ ...req });
    const keys = fake.Node.Handoff.mock.calls.map(c => c[0].idem);
    expect(keys[0]).toMatch(/^[0-9a-f]{32}$/);
    expect(keys[1]).toBe(keys[0]);
    expect(keys[2]).not.toBe(keys[0]); // the first one's answer came: this is a new handoff
  });

  it("a handoff with another note is another handoff", async () => {
    const app = await load();
    const req = { thread: "T", agent: "", session: "", cwd: "/w", cloud_agent: "ref1", note: "" };
    fake.Node.Handoff.mockRejectedValue(new Error("lost"));
    await app.handoff(req).catch(() => {});
    await app.handoff({ ...req, note: "and the tests" }).catch(() => {});
    const keys = fake.Node.Handoff.mock.calls.map(c => c[0].idem);
    expect(keys[1]).not.toBe(keys[0]);
  });
});

describe("one owner per subscription (finding 2)", () => {
  it("A, another chat, A again: the Unfollow of the first names the first, the new Follow another", async () => {
    const app = await load();
    fake.Node.Thread.mockImplementation(async (th: string) => th === "A" ? [msg("m1", "A", "running")] : []);
    const A = { key: "thread:A", kind: "thread" as const, thread: "A", title: "A", updated: "" };
    await app.open(A);
    await app.open({ key: "thread:B", kind: "thread", thread: "B", title: "B", updated: "" });
    await app.open(A);
    const follows = fake.Node.Follow.mock.calls.map(c => c[2]);
    const unfollows = fake.Node.Unfollow.mock.calls.filter(c => c[0] === "m1").map(c => c[1]);
    expect(follows.length).toBe(2);
    expect(follows[1]).toBeGreaterThan(follows[0]); // the newer generation, which the app keeps
    expect(unfollows).toEqual([follows[0]]); // only the first: the node keeps the second, whenever it runs
  });

  it("A, B, A with the first Follow answering last: only the first generation is let go, the second never", async () => {
    const app = await load();
    fake.Node.Thread.mockImplementation(async (th: string) => th === "A" ? [msg("m1", "A", "running")] : []);
    const first = deferred();
    fake.Node.Follow.mockReturnValueOnce(first.promise).mockResolvedValue(undefined);
    const A = { key: "thread:A", kind: "thread" as const, thread: "A", title: "A", updated: "" };
    await app.open(A);
    await app.open({ key: "thread:B", kind: "thread", thread: "B", title: "B", updated: "" });
    await app.open(A);
    await flush();
    first.resolve(undefined); // the first Follow's answer, late
    await flush();
    const [g1, g2] = fake.Node.Follow.mock.calls.map(c => c[2]);
    expect(g2).toBeGreaterThan(g1);
    const gone = fake.Node.Unfollow.mock.calls.filter(c => c[0] === "m1").map(c => c[1]);
    expect(gone.length).toBeGreaterThan(0);
    expect(gone.every(g => g === g1)).toBe(true);
    expect(fake.listeners("events:m1")).toBe(1);
  });

  it("generations rise across a reload of the window (they start from the clock)", async () => {
    const app = await load();
    fake.Node.Thread.mockResolvedValue([msg("m1", "T", "running")]);
    await app.open({ key: "thread:T", kind: "thread", thread: "T", title: "", updated: "" });
    expect(fake.Node.Follow.mock.calls[0][2]).toBeGreaterThanOrEqual(Date.now() * 1000 - 60e9);
  });

  it("a batch of another subscription of the same message is not this one's", async () => {
    const app = await load();
    fake.Node.Thread.mockResolvedValue([msg("m1", "T", "running")]);
    await app.open({ key: "thread:T", kind: "thread", thread: "T", title: "", updated: "" });
    const sub = fake.Node.Follow.mock.calls[0][2];
    fake.emit("events:m1", { id: "m1", gen: sub + 1, events: evs(1, 2) });
    expect(app.turns.m1.last).toBe(0);
    fake.emit("events:m1", { id: "m1", gen: sub, events: evs(1, 2) });
    expect(app.turns.m1.last).toBe(2);
  });
});

describe("nothing is dropped at the end (finding 3)", () => {
  async function running() {
    const app = await load();
    fake.Node.Thread.mockResolvedValue([msg("m1", "T", "running")]);
    await app.open({ key: "thread:T", kind: "thread", thread: "T", title: "", updated: "" });
    const sub = fake.Node.Follow.mock.calls[0][2];
    fake.emit("events:m1", { id: "m1", gen: sub, events: evs(1, 1) });
    return app;
  }

  it("a cancel's answer ends the stream, and the events it still held are read from the last seq", async () => {
    const app = await running();
    fake.Node.Cancel.mockResolvedValue(msg("m1", "T", "cancelled"));
    fake.Node.Events.mockResolvedValue(evs(2, 1)); // the batch still in the app's buffer
    await app.cancel("m1");
    await flush();
    expect(fake.Node.Events).toHaveBeenCalledWith("m1", 1);
    expect(app.turns.m1.last).toBe(2);
    expect(app.msgs[0].state).toBe("cancelled");
  });

  it("a read of them that fails is said on the message", async () => {
    const app = await running();
    fake.Node.Cancel.mockResolvedValue(msg("m1", "T", "cancelled"));
    fake.Node.Events.mockRejectedValue(new Error("down"));
    await app.cancel("m1");
    await flush();
    expect(app.broken.m1).toBeTruthy();
  });

  it("the stream's own final came after its last batch: nothing more is read", async () => {
    const app = await running();
    const sub = fake.Node.Follow.mock.calls[0][2];
    fake.emit("events:m1", { id: "m1", gen: sub, final: msg("m1", "T", "done") });
    await flush();
    expect(fake.Node.Events).not.toHaveBeenCalled();
  });
});

describe("a send waits for the files being read (finding 4)", () => {
  const file = (name: string, read: Promise<ArrayBuffer>) => ({ name, type: "text/plain", size: 2, arrayBuffer: () => read });

  it("a file pasted, then sent at once, goes with the message, to where it was sent", async () => {
    const app = await load();
    app.thread = "T"; app.target = "agent:claude";
    fake.Node.SendTask.mockResolvedValue(msg("m1", "T"));
    fake.Node.Thread.mockResolvedValue([]);
    const d = app.draftOf();
    d.text = "look at this";
    const read = deferred<ArrayBuffer>();
    d.add([file("a.txt", read.promise)], null, () => {});
    const going = app.submit(d);
    await app.open({ key: "thread:B", kind: "thread", thread: "B", title: "B", updated: "" }); // the screen moves on meanwhile
    read.resolve(new TextEncoder().encode("hi").buffer as ArrayBuffer);
    expect(await going).toBe(true);
    const task = fake.Node.SendTask.mock.calls[0][0];
    expect(task.files.map((f: any) => f.name)).toEqual(["a.txt"]);
    expect(task.thread).toBe("T");
    expect(d.files).toEqual([]); // sent: not left for the next message
  });

  it("a file removed after the send, while another is read, still goes: the send's list is the click's", async () => {
    const app = await load();
    app.thread = "T"; app.target = "agent:claude";
    fake.Node.SendTask.mockResolvedValue(msg("m1", "T"));
    const d = app.draftOf();
    d.text = "both";
    d.files = [attach({ name: "a.txt", type: "text/plain", size: 1, data: "YQ==" })];
    const read = deferred<ArrayBuffer>();
    d.add([file("b.txt", read.promise)], null, () => {});
    const going = app.submit(d);
    d.files = d.files.filter(f => f.name !== "a.txt"); // removed after Send
    read.resolve(new ArrayBuffer(1));
    await going;
    expect(fake.Node.SendTask.mock.calls[0][0].files.map((f: any) => f.name)).toEqual(["a.txt", "b.txt"]);
  });

  it("a file that cannot be read is said, and the next one is still read", async () => {
    const { ComposerDraft } = await import("./composer.svelte");
    const d = new ComposerDraft("new");
    const said: string[] = [];
    await d.add([file("bad.txt", Promise.reject(new Error("gone"))), file("ok.txt", Promise.resolve(new ArrayBuffer(2)))], null, (why, f) => said.push(why + ":" + f));
    expect(said).toEqual(["read:bad.txt"]);
    expect(d.files.map(f => f.name)).toEqual(["ok.txt"]);
  });
});

describe("the suite runs the state as the app does", () => {
  it("a $state array gives back proxies (Svelte's client build, not the server's plain objects)", async () => {
    const { ComposerDraft } = await import("./composer.svelte");
    const d = new ComposerDraft("new");
    const a = attach({ name: "a", type: "", size: 0, data: "" });
    d.files = [a];
    expect(d.files[0]).not.toBe(a);
  });
});

describe("an operation started is a frozen request (review round 2)", () => {
  const opts = { agent: "agent:claude", options: [{ id: "m", name: "Model", category: "model", current: "a", values: [{ value: "a", name: "A" }, { value: "b", name: "B" }] }] };

  it("a model chosen while the message's file is read is the next message's: this one goes with the one of the click", async () => {
    const app = await load();
    app.options = { "agent:claude": opts };
    app.thread = "T"; app.target = "agent:claude";
    app.setSetting("agent:claude", "model", "a");
    fake.Node.SendTask.mockResolvedValue(msg("m1", "T"));
    const d = app.draftOf();
    d.text = "look";
    const read = deferred<ArrayBuffer>();
    d.add([{ name: "f.txt", type: "text/plain", size: 1, arrayBuffer: () => read.promise }], null, () => {});
    const going = app.submit(d);
    app.setSetting("agent:claude", "model", "b");
    read.resolve(new ArrayBuffer(1));
    await going;
    expect(fake.Node.SendTask.mock.calls[0][0].model).toBe("a");
  });

  it("the first line edited after a sandbox's answer was lost: the retry asks the same sandbox, name and key", async () => {
    const app = await load();
    app.draft.where = "cloud"; app.draft.cloudAgent = "ref1"; app.draft.repo = "github.com/me/a";
    fake.Node.CreateCloudTask.mockRejectedValueOnce(new Error("lost")).mockResolvedValueOnce({ agent: "agent:sb_1" });
    fake.Node.SendTask.mockResolvedValue(msg("m1", "t1", "queued", "agent:sb_1"));
    const d = app.draftOf();
    d.text = "fix a";
    expect(await app.submit(d)).toBe(false);
    d.text = "fix b, and the tests";
    expect(await app.submit(d)).toBe(true);
    const [a, b] = fake.Node.CreateCloudTask.mock.calls;
    expect(b).toEqual(a);
  });

  it("handoffs of A (answer lost), then B, then A again: A's retry is A's first request, key and all", async () => {
    const app = await load();
    const A = { thread: "A", agent: "", session: "", cwd: "/a", cloud_agent: "ref1", note: "" };
    const B = { thread: "B", agent: "", session: "", cwd: "/b", cloud_agent: "ref1", note: "" };
    fake.Node.Handoff.mockRejectedValueOnce(new Error("lost")).mockResolvedValue({ chat: { key: "cloud:sb" } });
    await app.handoff(A).catch(() => {});
    await app.handoff(B);
    await app.handoff({ ...A });
    const [a1, b, a2] = fake.Node.Handoff.mock.calls.map(c => c[0]);
    expect(a2).toEqual(a1);
    expect(b.idem).not.toBe(a1.idem);
  });

  // Every creator of the app: its first request lost, the retry is the same request, whole.
  const creators: { name: string; run: (app: any) => Promise<unknown>; calls: () => any[][] }[] = [
    { name: "a message", calls: () => fake.Node.SendTask.mock.calls,
      run: async app => { app.thread = "T"; app.target = "agent:claude"; const d = app.draftOf(); d.text = "do it"; await app.submit(d); } },
    { name: "a new task on this Mac", calls: () => fake.Node.SendTask.mock.calls,
      run: async app => { app.draft.cwd = "/w"; const d = app.draftOf(); d.text = "do it"; await app.submit(d); } },
    { name: "a cloud task's sandbox", calls: () => fake.Node.CreateCloudTask.mock.calls,
      run: async app => { app.draft.where = "cloud"; app.draft.cloudAgent = "ref1"; app.draft.repo = "r"; const d = app.draftOf(); d.text = "do it"; await app.submit(d); } },
    { name: "a handoff", calls: () => fake.Node.Handoff.mock.calls,
      run: async app => { await app.handoff({ thread: "T", agent: "", session: "", cwd: "/w", cloud_agent: "ref1", note: "n" }).catch(() => {}); } },
  ];
  for (const c of creators) {
    it(`${c.name}: a retry sends a request deep-equal to the first`, async () => {
      const app = await load();
      app.options = { "agent:claude": opts };
      app.setSetting("agent:claude", "model", "b");
      for (const k of ["SendTask", "CreateCloudTask", "Handoff"]) fake.Node[k].mockRejectedValue(new Error("lost"));
      await c.run(app);
      await c.run(app);
      const [first, retry] = c.calls();
      expect(first).toBeDefined();
      expect(retry).toEqual(first);
    });
  }
});

describe("a send owns its attachments from the click (review round 4)", () => {
  const slow = (name: string, data: string) => {
    const d = deferred<ArrayBuffer>();
    return { d, f: { name, type: "text/plain", size: 1, arrayBuffer: () => d.promise }, done: () => d.resolve(new TextEncoder().encode(data).buffer as ArrayBuffer) };
  };
  async function setupSend() {
    const app = await load();
    const { freeName } = await import("./paste");
    app.thread = "T"; app.target = "agent:claude";
    fake.Node.SendTask.mockResolvedValue(msg("m1", "T"));
    const d = app.draftOf();
    d.text = "these";
    const add = (fs: any[]) => d.add(fs, null, () => {}, (f, taken) => freeName(f.name, taken));
    return { app, d, add, sent: () => fake.Node.SendTask.mock.calls[0][0].files.map((f: any) => f.name) };
  }

  it("two files of one name, the ready one removed after Send while the other is read: both go, named apart", async () => {
    const { app, d, add, sent } = await setupSend();
    d.files = [attach({ name: "a.txt", type: "text/plain", size: 1, data: "MQ==" })];
    const b = slow("a.txt", "2");
    add([b.f]);
    const going = app.submit(d);
    d.files = [];                // the first a.txt removed: the form names the read one a.txt again
    b.done();
    await going;
    expect(sent()).toEqual(["a.txt", "a (2).txt"]);
  });

  it("a file read after Send and removed from the form while the other is read still goes", async () => {
    const { app, d, add, sent } = await setupSend();
    const x = slow("x.txt", "x"), y = slow("y.txt", "y");
    add([x.f, y.f]);
    const going = app.submit(d);
    x.done(); await flush();
    expect(d.files.map(f => f.name)).toEqual(["x.txt"]);
    d.files = [];                // removed while y is read
    y.done();
    await going;
    expect(sent()).toEqual(["x.txt", "y.txt"]);
  });

  it("a file read and removed before Send, in an add() still reading another, does not go", async () => {
    const { app, d, add, sent } = await setupSend();
    const a = slow("a.txt", "a"), b = slow("b.txt", "b");
    add([a.f, b.f]);
    a.done(); await flush();
    d.files = [];                // a.txt removed while b.txt is read
    const going = app.submit(d);
    b.done();
    await going;
    expect(sent()).toEqual(["b.txt"]);
  });

  it("a screenshot pasted and sent before it is read, the answer lost: the retry is the same request, key and all", async () => {
    const app = await load();
    const { pasteName } = await import("./paste");
    app.thread = "T"; app.target = "agent:claude";
    fake.Node.SendTask.mockRejectedValueOnce(new Error("lost")).mockResolvedValue(msg("m1", "T"));
    const d = app.draftOf();
    d.text = "this";
    const img = slow("image.png", "png");
    d.add([{ ...img.f, type: "image/png" }], null, () => {}, (f, taken) => pasteName(f, taken));
    const first = app.submit(d);
    img.done();
    expect(await first).toBe(false);
    expect(await app.submit(d)).toBe(true);
    const [a, b] = fake.Node.SendTask.mock.calls.map(c => c[0]);
    expect(a.files[0].name).toMatch(/^pasted-\d{6}-\d+\.png$/);
    expect(b).toEqual(a);
  });

  for (const first of ["old", "new"]) it(`two drafts merged with their reads going, ${first}.txt read first, the answer lost: the retry is the same request, key and all`, async () => {
    const { app, d, add } = await setupSend();
    fake.Node.SendTask.mockReset();
    fake.Node.SendTask.mockRejectedValueOnce(new Error("lost")).mockResolvedValue(msg("m1", "T"));
    const old = slow("old.txt", "o"), nu = slow("new.txt", "n");
    add([old.f]);
    const other = app.draftOf("thread:X");
    other.add([nu.f], null, () => {});
    (app as any).moveDraft(other, "thread:T");          // merged, both reads still going
    const going = app.submit(d);
    (first === "old" ? old : nu).done(); await flush();
    (first === "old" ? nu : old).done();
    expect(await going).toBe(false);
    expect(await app.submit(d)).toBe(true);
    const [a, b] = fake.Node.SendTask.mock.calls.map(c => c[0]);
    expect(a.files.map((f: any) => f.name)).toEqual(first === "old" ? ["old.txt", "new.txt"] : ["new.txt", "old.txt"]);
    expect(b).toEqual(a);
  });

  it("max_files 2: a.txt ready, b.txt read, Send, the preview's report attached before the read ends: a.txt and b.txt go, the report stays", async () => {
    const { app, d, sent } = await setupSend();
    app.limits = { max_body: 1 << 20, max_files: 2, max_files_size: 1 << 20 };
    d.files = [attach({ name: "a.txt", type: "text/plain", size: 1, data: "YQ==" })];
    const b = slow("b.txt", "b");
    d.add([b.f], app.limits, () => {});
    const going = app.submit(d);
    d.take([{ name: "preview-problems.md", type: "text/markdown", size: 1, data: "cA==" }]); // the next message's
    b.done();
    expect(await going).toBe(true);
    expect(sent()).toEqual(["a.txt", "b.txt"]);
    expect(d.files.map(f => f.name)).toEqual(["preview-problems.md"]);
  });

  it("the send's own files over the click's limits: nothing goes, the draft stays, and why is said", async () => {
    const { app, d } = await setupSend();
    app.limits = { max_body: 1 << 20, max_files: 2, max_files_size: 1 << 20 };
    const x = slow("x.txt", "x"), y = slow("y.txt", "y"), z = slow("z.txt", "z");
    d.add([x.f], app.limits, () => {});
    d.add([y.f, z.f], app.limits, () => {});  // its files' turn comes after the click: the send judges them
    const going = app.submit(d);
    x.done(); y.done(); z.done();
    expect(await going).toBe(false);
    expect(fake.Node.SendTask).not.toHaveBeenCalled();
    expect(d.text).toBe("these");
    expect(app.error).toContain("2");
  });

  it("a file the send has that cannot be read: nothing goes, the draft stays", async () => {
    const { app, d } = await setupSend();
    const bad = { name: "bad.txt", type: "text/plain", size: 1, arrayBuffer: () => Promise.reject(new Error("gone")) };
    const ok = slow("ok.txt", "o");
    d.add([ok.f, bad], null, () => {});
    const going = app.submit(d);
    ok.done();
    expect(await going).toBe(false);
    expect(fake.Node.SendTask).not.toHaveBeenCalled();
    expect(d.text).toBe("these");
  });

  for (const how of ["one add", "two adds"]) it(`a 1 GiB video behind a small file still read, Send (${how}): the video is never read, nothing goes, the draft stays`, async () => {
    const { app, d } = await setupSend();
    app.limits = { max_body: 1 << 20, max_files: 16, max_files_size: 16 << 20 };
    const small = slow("notes.txt", "n");
    let videoRead = false;
    const video = { name: "demo.mov", type: "video/quicktime", size: 1 << 30, arrayBuffer: () => { videoRead = true; return Promise.resolve(new ArrayBuffer(1)); } };
    if (how === "one add") d.add([small.f, video], app.limits, () => {});
    else { d.add([small.f], app.limits, () => {}); d.add([video], app.limits, () => {}); }
    const going = app.submit(d);
    small.done();
    expect(await going).toBe(false);
    expect(videoRead).toBe(false);
    expect(fake.Node.SendTask).not.toHaveBeenCalled();
    expect(d.text).toBe("these");
    expect(app.error).toContain("16");
  });

  it("a file being read at the click, the composer full by the time it is read: it goes with the message", async () => {
    const { app, d, sent } = await setupSend();
    app.limits = { max_body: 1 << 20, max_files: 1, max_files_size: 1 << 20 };
    const x = slow("x.txt", "x");
    d.add([x.f], app.limits, () => {});
    await flush();                                    // its read is under way
    const going = app.submit(d);
    d.take([{ name: "preview-problems.md", type: "text/markdown", size: 1, data: "cA==" }]); // the composer is full
    x.done();
    expect(await going).toBe(true);
    expect(sent()).toEqual(["x.txt"]);
    expect(d.files.map(f => f.name)).toEqual(["preview-problems.md"]);
  });

  it("the click's own list already over the limits (files handed over past them): refused here, not left to the node", async () => {
    const { app, d } = await setupSend();
    app.limits = { max_body: 1 << 20, max_files: 1, max_files_size: 1 << 20 };
    d.take([{ name: "a.md", type: "text/markdown", size: 1, data: "YQ==" }, { name: "b.md", type: "text/markdown", size: 1, data: "Yg==" }]);
    expect(await app.submit(d)).toBe(false);
    expect(fake.Node.SendTask).not.toHaveBeenCalled();
    expect(d.text).toBe("these");
    expect(d.files.length).toBe(2);
  });

  it("15 files, x.txt read, Send, the report attached (the composer full), the send fails: all 16 come back, and without the report the retry is the first request", async () => {
    const { app, d } = await setupSend();
    app.limits = { max_body: 1 << 20, max_files: 16, max_files_size: 1 << 20 };
    fake.Node.SendTask.mockReset();
    fake.Node.SendTask.mockRejectedValueOnce(new Error("lost")).mockResolvedValue(msg("m1", "T"));
    d.take(Array.from({ length: 15 }, (_, i) => ({ name: `f${i}.txt`, type: "text/plain", size: 1, data: btoa("f" + i) })));
    const x = slow("x.txt", "x");
    d.add([x.f], app.limits, () => {});
    const going = app.submit(d);
    d.take([{ name: "preview-problems.md", type: "text/markdown", size: 1, data: "cA==" }]); // the 16th: the composer is full
    x.done();
    expect(await going).toBe(false);
    expect(d.files.map(f => f.name)).toEqual([...Array.from({ length: 15 }, (_, i) => `f${i}.txt`), "x.txt", "preview-problems.md"]);
    expect(app.error).toContain("remove a file");
    d.files = d.files.filter(f => f.name !== "preview-problems.md");
    expect(await app.submit(d)).toBe(true);
    const [a, b] = fake.Node.SendTask.mock.calls.map(c => c[0]);
    expect(a.files.length).toBe(16);
    expect(b).toEqual(a);
  });

  for (const keep of [true, false]) it(`the report sent, attached again, the send failed: names stay unique; ${keep ? "kept, both go" : "the late one removed, the retry is the first request"}`, async () => {
    const { app, d } = await setupSend();
    const report = { name: "preview-problems.md", type: "text/markdown", size: 1, data: "cA==" };
    fake.Node.SendTask.mockReset();
    const first = deferred();
    fake.Node.SendTask.mockReturnValueOnce(first.promise).mockResolvedValue(msg("m1", "T"));
    d.take([report]);
    const going = app.submit(d);
    await flush();
    d.take([{ ...report, data: "cQ==" }]);           // the report again, while the send goes
    first.reject(new Error("refused"));
    expect(await going).toBe(false);
    expect(d.files.map(f => f.name)).toEqual(["preview-problems.md", "preview-problems (2).md"]);
    if (!keep) d.files = d.files.slice(0, 1);
    expect(await app.submit(d)).toBe(true);
    const [a, b] = fake.Node.SendTask.mock.calls.map(c => c[0]);
    if (keep) {
      expect(b.files.map((f: any) => [f.name, f.data])).toEqual([["preview-problems.md", "cA=="], ["preview-problems (2).md", "cQ=="]]);
      expect(b.idem).not.toBe(a.idem);
    } else expect(b).toEqual(a);
  });

  it("a new task's report sent, New task with another report, the send fails: put back into the new task, the first report keeps its name, and without the late one the retry is the first request", async () => {
    const app = await load();
    app.draft.cwd = "/w";
    const first = deferred();
    fake.Node.SendTask.mockReturnValueOnce(first.promise).mockResolvedValue(msg("m1", "T"));
    const d = app.draftOf();
    d.text = "fix these";
    d.take([{ name: "preview-problems.md", type: "text/markdown", size: 1, data: "cA==" }]);
    const going = app.submit(d);
    await flush();
    app.newTask();                            // same agent and folder
    const n = app.draftOf();
    n.take([{ name: "preview-problems.md", type: "text/markdown", size: 1, data: "cQ==" }]);
    first.reject(new Error("lost"));
    expect(await going).toBe(false);
    const back = app.draftOf();
    expect(back.files.map(f => [f.name, f.data])).toEqual([["preview-problems.md", "cA=="], ["preview-problems (2).md", "cQ=="]]);
    back.files = back.files.filter(f => f.data !== "cQ==");
    expect(await app.submit(back)).toBe(true);
    const [a, b] = fake.Node.SendTask.mock.calls.map(c => c[0]);
    expect(b).toEqual(a);
  });

  it("any reads, removals and pastes before and after the click: what goes is the click's files and what the reads of then brought after it, named apart; a retry is the first request", async () => {
    const { pasteName, freeName } = await import("./paste");
    let seed = 11;
    const rnd = (n: number) => { seed = (seed * 1103515245 + 12345) % 2 ** 31; return Math.floor(seed / 2 ** 31 * n); }; // high bits: the low ones of this generator repeat
    const outcomes = { whole: 0, refused: 0 };
    // ACPOST_PROPERTY_RUNS for a longer run by hand (the default 120 take some 4 s on a laptop;
    // the timeout, 60 s, grows with it).
    const runs = Number(process.env.ACPOST_PROPERTY_RUNS) || 120;
    for (let run = 0; run < runs; run++) {
      vi.resetModules();
      fake = fakeNode();
      fake.Node.Chats.mockResolvedValue({ items: [] });
      fake.Node.Agents.mockResolvedValue([]);
      const { app, d: main } = await setupSend();
      // A second draft with reads of its own, merged into the one sent at some point before the
      // click (its reads and the other's then run at once: delivery order is the oracle's too).
      const other = app.draftOf("thread:X");
      let d = rnd(2) ? other : main, merged = false;
      const retry = run % 2 === 0; // half the runs: the first answer lost, nothing done after the click, sent again
      const fails = !retry && run % 3 === 0; // some others: the send fails, with things done after the click
      if (retry) fake.Node.SendTask.mockRejectedValueOnce(new Error("lost"));
      // A third of the runs with the node's limits (small: they are met): the composer judges what
      // it is given, a send judges its own files, and goes whole or not at all.
      const limits = run % 3 === 1 ? { max_body: 1 << 20, max_files: 1 + rnd(4), max_files_size: rnd(2) ? 1 << 20 : 2 + rnd(4) } : null;
      app.limits = limits;
      let n = 0, clicked = false;
      const afterClick: string[] = [];      // files of reads asked before the click, delivered after it
      const formOver: string[] = [];        // a file the composer took past its limits
      // Files of reads asked before the click that came up after it (their size first asked then),
      // in that order, and the reads made after the click of those files (with whether the
      // composer had room for it then).
      const readLate: { v: string; ok: boolean }[] = [];
      let simBudget: { size: number }[] = [];   // the send's budget as the oracle keeps it
      const groups: { vs: string[]; late: boolean; stopped: boolean }[] = [];
      const sizeOf = new Map<string, number>();
      const readStarted = new Set<string>();
      const reads: { s: ReturnType<typeof slow>; v: string; ended: boolean }[] = [];
      const delivered = new Set<string>(); // the namer runs as a read's file is put in the form
      const deliveryOrder: string[] = [];
      const addSome = () => {
        const paste = rnd(2) === 0;
        const late = clicked;
        const group = Array.from({ length: 1 + rnd(3) }, () => {
          const v = "f" + n++;
          const s = slow(paste ? "image.png" : (rnd(2) ? "same.txt" : v + ".txt"), v);
          const r = { s, v, ended: false };
          reads.push(r);
          // Under limits, sizes vary, now and then a file far too big (it must not be read).
          const size = limits ? (rnd(8) === 0 ? 1 << 30 : 1 + rnd(2)) : 1;
          sizeOf.set(v, size);
          return {
            name: s.f.name, type: paste ? "image/png" : "text/plain", v, size,
            arrayBuffer: () => {
              if (!clicked) readStarted.add(v);
              if (clicked && !late) {
                // A read after the click of a file the send has: only if it fits the send's budget
                // (the send admitted it), or, the send refused, the composer has room for it.
                const fits = !overLimits(simBudget, { size }, limits);
                if (fits) simBudget.push({ size });
                readLate.push({ v, ok: fits || !overLimits(main.files, { size }, limits) });
              }
              return s.f.arrayBuffer();
            },
          };
        });
        const g = { vs: group.map(f => f.v), late, stopped: false };
        groups.push(g);
        const mark = (f: any) => {
          delivered.add(f.v); deliveryOrder.push(f.v);
          if (clicked && !late) afterClick.push(f.v);
          if (late && overLimits(main.files, f, limits)) formOver.push(f.v); // the composer's own limit, after the click
          return f;
        };
        d.add(group, limits, () => { if (!clicked) g.stopped = true; }, paste ? (f, t) => pasteName(mark(f), t) : (f, t) => freeName(mark(f).name, t));
      };
      const end = (r: (typeof reads)[number]) => { r.s.done(); r.ended = true; };
      // The drafts' names are unique after every step, whatever put files there.
      const unique = () => { for (const x of [main, other]) expect(new Set(x.files.map(f => f.name)).size).toBe(x.files.length); };
      // A file handed over whole (the preview's report): the same report again is another attachment.
      const takeOne = () => { const k = rnd(2); d.take([{ name: "preview-problems.md", type: "text/markdown", size: 1, data: btoa("report" + k) }]); };
      // Each draft starts with a read going: merged, the two run at once.
      { const keep = d; d = other; addSome(); d = main; addSome(); d = keep; }
      // Before the click: adds, reads ending, files removed (some from an add() still reading).
      for (let step = 0; step < 6; step++) {
        const k = rnd(5);
        if (k === 4) takeOne();
        else if (k === 3 && !merged) { (app as any).moveDraft(other, "thread:T"); d = main; merged = true; }
        else if (k === 3) d = main;
        else if (k === 0) addSome();
        else if (k === 1 && reads.length) end(reads[rnd(reads.length)]);
        else if (d.files.length) d.files = d.files.filter((_, j) => j !== rnd(d.files.length));
        await flush();
        unique();
      }
      if (!merged) { (app as any).moveDraft(other, "thread:T"); d = main; }
      if (!reads.length) addSome();
      const atClick = d.files.map(f => atob(f.data));
      const clickRids = new Set(d.files.map(f => f.rid));
      const clickSizes = d.files.map(f => ({ size: f.size }));
      let formAtSend: { rid: number; data: string }[] = [];
      // A failing send's give-back and a draft merged into the sent one, in either order: before
      // the answer (merged while the send goes) or after it (given back, then merged).
      // Or after it, the sent draft merged into another (as a new task's is, put back).
      const mergeWhen = (["before", "after", "into"] as const)[rnd(3)];
      const mergeIn = () => {
        const x = app.draftOf("thread:Y" + run);
        x.take([{ name: "preview-problems.md", type: "text/markdown", size: 1, data: btoa("merged") }]);
        if (mergeWhen === "into") { (app as any).moveDraft(d, x.key); d = x; }
        else (app as any).moveDraft(x, "thread:T");
      };
      // The node answers once all is done (the composer as it is then is what the answer cleans).
      let answer = () => {};
      if (!retry) fake.Node.SendTask.mockImplementation(() => new Promise((r, rj) => { answer = () => { formAtSend = d.files.map(f => ({ rid: f.rid, data: f.data })); fails ? rj(new Error("refused")) : r(msg("m1", "T")); }; }));
      // Reads under way at the click: the send's first (their files passed the composer's check).
      // (a stopped add() has ended: none of its files is under way)
      const stoppedVs = new Set(groups.filter(g => g.stopped).flatMap(g => g.vs));
      const inFlight = reads.filter(r => readStarted.has(r.v) && !delivered.has(r.v) && !stoppedVs.has(r.v)).map(r => ({ v: r.v, size: sizeOf.get(r.v)! }));
      // Still to come at the click: files of reads asked before it, not read yet, of a read the
      // composer had not stopped (over its limits, it drops the rest of that add()).
      const toCome = groups.filter(g => !g.late && !g.stopped).flatMap(g => g.vs).filter(v => !readStarted.has(v) && !delivered.has(v)).map(v => ({ v, size: sizeOf.get(v)! }));
      simBudget = [...d.files.map(f => ({ size: f.size })), ...inFlight];
      clicked = true;
      let backAt: number[] = []; // the draft's files as the send returned
      const going = app.submit(d).then(ok => { backAt = d.files.map(f => f.rid); return ok; });
      // After the click: none of this is the message's.
      for (let step = 0; step < 6; step++) {
        const k = rnd(4);
        if (!retry && k === 3) takeOne();
        else if (!retry && k === 0) addSome();
        else if (!retry && k === 1 && d.files.length) d.files = d.files.filter((_, j) => j !== rnd(d.files.length));
        else if (reads.length) end(reads[rnd(reads.length)]);
        await flush();
        unique();
      }
      for (const r of reads) end(r);
      for (let i = 0; i < 5; i++) await flush();
      if (fails && mergeWhen === "before") { mergeIn(); unique(); }
      answer();
      const ok = await going;
      expect(formOver).toEqual([]);
      const pendingAtClick = afterClick;
      // The send goes whole or not at all: over the click's limits with all it has (the click's
      // files, the reads under way, those to come), nothing goes; and no file was read after the
      // click that could not fit (unless the composer, the send refused, had room for it).
      const all = [...clickSizes, ...inFlight, ...toCome];
      const over = all.some((f, i) => overLimits(all.slice(0, i), f, limits));
      expect(readLate.filter(r => !r.ok)).toEqual([]);
      if (limits) outcomes[over ? "refused" : "whole"]++;
      if (over) {
        expect(ok).toBe(false);
        expect(fake.Node.SendTask).not.toHaveBeenCalled();
        expect(d.text).toBe("these");
        for (const r of clickRids) expect(backAt.includes(r)).toBe(true); // given back as the send returned, removed since or not
        continue;
      }
      const want = [...atClick, ...pendingAtClick].sort();
      const firstReq = fake.Node.SendTask.mock.calls[0][0];
      const files = firstReq.files ?? [];
      expect(files.map((f: any) => atob(f.data)).sort()).toEqual(want);
      // In order: the click's list, then the reads of then as they were delivered.
      const got = files.map((f: any) => atob(f.data));
      expect(got.slice(0, atClick.length)).toEqual(atClick);
      expect(got.slice(atClick.length)).toEqual(deliveryOrder.filter(v => pendingAtClick.includes(v)));
      expect(new Set(files.map((f: any) => f.name)).size).toBe(files.length);
      if (fails) {
        // It failed: all it owned is back in the draft, its request's files first, then what was
        // attached since (nothing lost); without those, the retry is the first request.
        expect(ok).toBe(false);
        expect(d.files.slice(0, files.length).map(f => atob(f.data))).toEqual(got);
        for (const f of formAtSend) expect(d.files.some(x => x.rid === f.rid)).toBe(true);
        unique();
        if (mergeWhen !== "before") { mergeIn(); unique(); expect(new Set(d.files.map(f => f.name)).size).toBe(d.files.length); }
        // The request's files first still, with its names, whichever came first.
        expect(d.files.slice(0, files.length).map(f => [f.name, atob(f.data)])).toEqual(files.map((f: any) => [f.name, atob(f.data)]));
        // Half the time the later files stay and go too (names apart); else, without them, the
        // retry is the first request.
        const keepLate = rnd(2) === 0;
        if (!keepLate) d.files = d.files.slice(0, files.length);
        const draftNow = d.files.map(f => [f.name, f.data]);
        fake.Node.SendTask.mockReset();
        fake.Node.SendTask.mockResolvedValue(msg("m1", "T"));
        await app.submit(d);
        const again = fake.Node.SendTask.mock.calls[0]?.[0];
        if (keepLate) {
          if (again) expect((again.files ?? []).map((f: any) => [f.name, f.data])).toEqual(draftNow);
          else expect(limits).not.toBeNull(); // not sent only under limits (the draft given back can be over them)
        } else expect(again).toEqual(firstReq);
        continue;
      }
      if (!retry) {
        // What it sent leaves the composer, exactly those: a file attached after the click stays,
        // the same report attached again included.
        const sentHere = (f: { rid: number; data: string }) => clickRids.has(f.rid) || pendingAtClick.includes(atob(f.data));
        expect(d.files.map(f => f.rid)).toEqual(formAtSend.filter(f => !sentHere(f)).map(f => f.rid));
      }
      if (retry) {
        await app.submit(d);
        expect(fake.Node.SendTask.mock.calls[1][0]).toEqual(firstReq);
      }
    }
    expect(outcomes.whole).toBeGreaterThan(0);   // under limits, both ways come up
    expect(outcomes.refused).toBeGreaterThan(0);
  }, Math.max(60_000, 40 * (Number(process.env.ACPOST_PROPERTY_RUNS) || 0))); // many runs, each a fresh app: past vitest's 5 s on a CI runner
});
