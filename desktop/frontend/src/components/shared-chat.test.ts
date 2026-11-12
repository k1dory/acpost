// @vitest-environment happy-dom
// A chat shared by several agents: who the next message goes to is chosen in the composer.
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
const msg = (id: string, to: string, extra: object = {}) =>
  ({ id, from: "user:me", to, thread: "T", depth: 0, state: "done", body: "q", reply: "a", created_at: "2026-11-12T10:00:00Z", updated_at: "2026-11-12T10:00:01Z", ...extra });

async function setup(msgs: any[], agents = ["claude", "codex"]) {
  svelte = await import("svelte");
  const { app } = await import("../lib/state.svelte");
  app.agents = agents.map(name => ({ name, address: "agent:" + name, driver: "acp", queued: 0, running: 0 }));
  app.conn = { ok: true };
  app.newTask();
  fake.Node.Thread.mockResolvedValue(msgs);
  await app.open({ key: "thread:T", kind: "thread", thread: "T", agent: msgs[0]?.to, title: "t", updated: "" } as any);
  const Composer = (await import("./Composer.svelte")).default;
  mounted.push(svelte.mount(Composer, { target: document.body }));
  svelte.flushSync();
  return app;
}
const textarea = () => document.querySelector("textarea") as HTMLTextAreaElement;
function type(text: string) {
  textarea().value = text;
  textarea().dispatchEvent(new Event("input", { bubbles: true }));
  svelte.flushSync();
}
const press = (key: string) => { textarea().dispatchEvent(new KeyboardEvent("keydown", { key, bubbles: true, cancelable: true })); svelte.flushSync(); };
const chip = () => document.querySelector("button.chip.to") as HTMLButtonElement | null;

beforeEach(() => {
  vi.resetModules();
  fake = fakeNode();
  fake.Node.Chats.mockResolvedValue({ items: [] });
  fake.Node.Events.mockResolvedValue([]);
  fake.Node.SendTask.mockImplementation(async (t: any) => msg("m9", t.to, { state: "queued", body: t.body }));
  document.body.innerHTML = "";
});
afterEach(() => { for (const m of mounted) svelte.unmount(m); mounted = []; });

describe("a chat shared by several agents", () => {
  it("the next message goes to whoever was asked last, not to the chat's first agent", async () => {
    const app = await setup([msg("m1", "agent:claude"), msg("m2", "agent:codex"), msg("m3", "agent:claude", { depth: 1, from: "agent:codex" })]);
    expect(app.target).toBe("agent:codex"); // m3 is what an agent asked another, not the person
    expect(chip()!.textContent).toContain("codex");
  });

  it("another agent of this Mac is called in from the chip, and the message goes to it in the same thread", async () => {
    const app = await setup([msg("m1", "agent:claude")]);
    expect(app.targets()).toEqual(["agent:claude", "agent:codex"]);
    chip()!.click();
    svelte.flushSync();
    const items = [...document.querySelectorAll(".menu.pick .mi")] as HTMLButtonElement[];
    expect(items.map(i => i.textContent!.trim())).toEqual(["claude", "codex"]);
    items[1].click();
    svelte.flushSync();
    expect(app.target).toBe("agent:codex");
    type("look at this");
    press("Enter");
    await flush(); await flush();
    expect(fake.Node.SendTask).toHaveBeenCalledTimes(1);
    expect(fake.Node.SendTask.mock.calls[0][0]).toMatchObject({ to: "agent:codex", thread: "T", body: "look at this" });
  });

  it("@name at the start of a message sends it to that agent, without the name", async () => {
    const app = await setup([msg("m1", "agent:claude")]);
    type("@Codex check the tests");
    press("Enter");
    await flush(); await flush();
    expect(fake.Node.SendTask.mock.calls[0][0]).toMatchObject({ to: "agent:codex", thread: "T", body: "check the tests" });
    expect(app.target).toBe("agent:codex");
  });

  it("@ alone offers the chat's agents; a name that is no agent's stays text", async () => {
    const app = await setup([msg("m1", "agent:claude")]);
    type("@co");
    expect([...document.querySelectorAll(".menu .mi.who")].map(i => i.textContent!.trim())).toEqual(["codex"]);
    press("Enter");
    expect(app.target).toBe("agent:codex");
    expect(textarea().value).toBe("");
    expect(fake.Node.SendTask).not.toHaveBeenCalled();
    type("@nobody hello");
    press("Enter");
    await flush(); await flush();
    expect(fake.Node.SendTask.mock.calls[0][0]).toMatchObject({ to: "agent:codex", body: "@nobody hello" });
  });

  it("what a client or another node put in the thread does not choose who the person's next message goes to", async () => {
    const app = await setup([msg("m1", "agent:claude"), msg("m2", "agent:spy@peer", { from: "client:ci" }), msg("m3", "agent:spy@peer", { from: "user:x@peer" })]);
    expect(app.target).toBe("agent:claude");
    expect(app.targets()).toEqual(["agent:claude", "agent:codex"]);
  });

  it("a message with @name whose answer was lost is sent again as the same message: same agent, body and key", async () => {
    await setup([msg("m1", "agent:claude")]);
    fake.Node.SendTask.mockRejectedValueOnce(new Error("lost"));
    type("@codex @claude is the reviewer");
    press("Enter");
    await flush(); await flush(); svelte.flushSync();
    expect(textarea().value).toBe("@codex @claude is the reviewer"); // as typed until the node has it
    press("Enter");
    await flush(); await flush(); svelte.flushSync();
    const [a, b] = fake.Node.SendTask.mock.calls.map(c => c[0]);
    expect(a).toMatchObject({ to: "agent:codex", body: "@claude is the reviewer" });
    expect(b).toEqual(a);
    expect(a.idem).toBeTruthy();
    expect(textarea().value).toBe("");
  });

  it("who was chosen stays with the chat's unsent draft when the chat is left and opened again", async () => {
    const app = await setup([msg("m1", "agent:claude")]);
    app.setTarget("agent:codex");
    type("not sent yet");
    app.newTask();
    await app.open({ key: "thread:T", kind: "thread", thread: "T", agent: "agent:claude", title: "t", updated: "" } as any);
    svelte.flushSync();
    expect(app.target).toBe("agent:codex");
    expect(app.draftOf(app.draftKey()).text).toBe("not sent yet");
  });

  it("with no other agent to choose, @name is the message's own text", async () => {
    await setup([msg("m1", "agent:claude")], ["claude"]);
    type("@claude");
    press("Enter");
    await flush(); await flush();
    type("@claude please explain");
    press("Enter");
    await flush(); await flush();
    expect(fake.Node.SendTask.mock.calls.map(c => c[0].body)).toEqual(["@claude", "@claude please explain"]);
  });

  it("a message whose answer was lost goes again to the same agent with the same key, whoever was asked meanwhile", async () => {
    const app = await setup([msg("m1", "agent:claude")]);
    fake.Node.SendTask.mockRejectedValueOnce(new Error("lost"));
    type("plain");
    press("Enter");
    await flush(); await flush(); svelte.flushSync();
    app.newTask();
    fake.Node.Thread.mockResolvedValue([msg("m1", "agent:claude"), msg("m2", "agent:codex")]); // asked from elsewhere meanwhile
    await app.open({ key: "thread:T", kind: "thread", thread: "T", agent: "agent:claude", title: "t", updated: "" } as any);
    svelte.flushSync();
    expect(app.target).toBe("agent:claude");
    await app.submit(app.draftOf(app.draftKey()));
    const [a, b] = fake.Node.SendTask.mock.calls.map(c => c[0]);
    expect(a).toMatchObject({ to: "agent:claude", body: "plain" });
    expect(b).toEqual(a);
  });

  it("an answer that comes after the person chose another agent does not undo the choice", async () => {
    const app = await setup([msg("m1", "agent:claude")]);
    let ack!: (m: any) => void;
    fake.Node.SendTask.mockImplementationOnce((t: any) => new Promise(r => { ack = () => r(msg("m9", t.to, { state: "queued" })); }));
    type("to claude");
    press("Enter");
    await flush();
    app.setTarget("agent:codex");
    ack(null);
    await flush(); await flush();
    expect(app.target).toBe("agent:codex");
  });

  it("a choice made while a message with @name went stands, even one back to the agent chosen before", async () => {
    const app = await setup([msg("m1", "agent:claude")]);
    let ack!: () => void;
    fake.Node.SendTask.mockImplementationOnce((t: any) => new Promise(r => { ack = () => r(msg("m8", t.to, { state: "queued" })); }));
    type("@codex review");
    press("Enter");
    await flush();
    app.setTarget("agent:codex");
    app.setTarget("agent:claude");
    ack();
    await flush(); await flush();
    expect(app.target).toBe("agent:claude");
  });

  it("a bare @name is this node's agent, never a namesake of another node (named with its node)", async () => {
    const app = await setup([msg("m1", "agent:codex@peer"), msg("m2", "agent:claude")]);
    expect(app.mention("@codex review")).toEqual({ to: "agent:codex", rest: "review" });
    expect(app.mention("@codex@peer review")).toEqual({ to: "agent:codex@peer", rest: "review" });
    type("@co");
    expect([...document.querySelectorAll(".menu .mi.who")].map(i => i.textContent!.trim())).toEqual(["codex@peer", "codex"]);
    type("@codex");
    press("Enter"); // the one named exactly
    expect(app.target).toBe("agent:codex");
  });

  it("an agent named in a message answers next in that thread, though its answer came with the thread closed", async () => {
    const app = await setup([msg("m1", "agent:claude")]);
    type("plain");
    press("Enter");
    await flush(); await flush(); svelte.flushSync();
    let ack!: () => void;
    fake.Node.SendTask.mockImplementationOnce((t: any) => new Promise(r => { ack = () => r(msg("m8", t.to, { state: "queued" })); }));
    type("@codex review");
    press("Enter");
    await flush();
    app.newTask();
    ack();
    await flush(); await flush();
    fake.Node.Thread.mockResolvedValue([msg("m1", "agent:claude"), msg("m9", "agent:claude"), msg("m8", "agent:codex")]);
    await app.open({ key: "thread:T", kind: "thread", thread: "T", agent: "agent:claude", title: "t", updated: "" } as any);
    expect(app.target).toBe("agent:codex");
  });

  for (const how of ["the chip", "@name"]) {
    it(`a session's message whose answer was lost, sent again to another agent chosen by ${how}, is a message to that one`, async () => {
      svelte = await import("svelte");
      const { app } = await import("../lib/state.svelte");
      app.agents = ["claude", "codex"].map(name => ({ name, address: "agent:" + name, driver: "acp", queued: 0, running: 0 }));
      app.conn = { ok: true };
      const S = { key: "session:claude/s1", kind: "session" as const, agent: "agent:claude", session: "s1", title: "", updated: "" };
      const T = { key: "thread:T", kind: "thread" as const, thread: "T", agent: "agent:claude", session: "s1", session_agent: "agent:claude", title: "", updated: "" };
      fake.Node.Chats.mockResolvedValue({ items: [S] });
      await app.loadChats();
      fake.Node.SessionHistory.mockResolvedValue([]);
      await app.open(S as any);
      app.draftOf().text = "go on";
      fake.Node.SendTask.mockRejectedValueOnce(new Error("timeout")); // the node took it; the answer was lost
      expect(await app.submit(app.draftOf())).toBe(false);
      fake.Node.Chats.mockResolvedValue({ items: [T] });              // the session is a thread now
      await app.loadChats();
      fake.Node.Thread.mockResolvedValue([msg("m1", "agent:claude")]);
      await app.open(T as any);
      if (how === "the chip") app.setTarget("agent:codex");
      else app.draftOf().text = "@codex go on";
      expect(await app.submit(app.draftOf())).toBe(true);
      const [first, again] = fake.Node.SendTask.mock.calls.map(c => c[0]);
      expect(first).toMatchObject({ to: "agent:claude", session: "s1", body: "go on" });
      expect(again).toMatchObject({ to: "agent:codex", thread: "T", body: "go on" });
      expect(again.session).toBeUndefined();
      expect(again.idem).not.toBe(first.idem);
    });
  }

  it("a session's message that starts with @name, its answer lost, is sent again as it was: to the session's agent, whole, with its key", async () => {
    svelte = await import("svelte");
    const { app } = await import("../lib/state.svelte");
    app.agents = ["claude", "codex"].map(name => ({ name, address: "agent:" + name, driver: "acp", queued: 0, running: 0 }));
    app.conn = { ok: true };
    const S = { key: "session:claude/s1", kind: "session" as const, agent: "agent:claude", session: "s1", title: "", updated: "" };
    const T = { key: "thread:T", kind: "thread" as const, thread: "T", agent: "agent:claude", session: "s1", session_agent: "agent:claude", title: "", updated: "" };
    fake.Node.Chats.mockResolvedValue({ items: [S] });
    await app.loadChats();
    fake.Node.SessionHistory.mockResolvedValue([]);
    await app.open(S as any);
    app.draftOf().text = "@codex check the tests"; // no one else to name in a session: the text is the message
    fake.Node.SendTask.mockRejectedValueOnce(new Error("timeout"));
    expect(await app.submit(app.draftOf())).toBe(false);
    fake.Node.Chats.mockResolvedValue({ items: [T] });
    await app.loadChats();
    fake.Node.Thread.mockResolvedValue([msg("m1", "agent:claude")]);
    await app.open(T as any);
    expect(await app.submit(app.draftOf())).toBe(true);
    const [first, again] = fake.Node.SendTask.mock.calls.map(c => c[0]);
    expect(first).toMatchObject({ to: "agent:claude", session: "s1", body: "@codex check the tests" });
    expect(again).toEqual(first);
  });

  for (const how of ["Enter", "the send button"]) {
    it(`a session's bare "@codex", its answer lost, goes again as that message by ${how}: no agent is chosen by it`, async () => {
      svelte = await import("svelte");
      const { app } = await import("../lib/state.svelte");
      app.agents = ["claude", "codex"].map(name => ({ name, address: "agent:" + name, driver: "acp", queued: 0, running: 0 }));
      app.conn = { ok: true };
      const S = { key: "session:claude/s1", kind: "session" as const, agent: "agent:claude", session: "s1", title: "", updated: "" };
      const T = { key: "thread:T", kind: "thread" as const, thread: "T", agent: "agent:claude", session: "s1", session_agent: "agent:claude", title: "", updated: "" };
      fake.Node.Chats.mockResolvedValue({ items: [S] });
      await app.loadChats();
      fake.Node.SessionHistory.mockResolvedValue([]);
      await app.open(S as any);
      app.draftOf().text = "@codex";
      fake.Node.SendTask.mockRejectedValueOnce(new Error("timeout"));
      expect(await app.submit(app.draftOf())).toBe(false);
      fake.Node.Chats.mockResolvedValue({ items: [T] });
      await app.loadChats();
      fake.Node.Thread.mockResolvedValue([msg("m1", "agent:claude")]);
      await app.open(T as any);
      const Composer = (await import("./Composer.svelte")).default;
      mounted.push(svelte.mount(Composer, { target: document.body }));
      svelte.flushSync();
      expect(textarea().value).toBe("@codex");
      if (how === "Enter") press("Enter");
      else (document.querySelector("button.send") as HTMLButtonElement).click();
      await flush(); await flush(); svelte.flushSync();
      const [first, again] = fake.Node.SendTask.mock.calls.map(c => c[0]);
      expect(again).toEqual(first);
      expect(first).toMatchObject({ to: "agent:claude", session: "s1", body: "@codex" });
      expect(app.target).toBe("agent:claude");
    });
  }

  it("agents whose names differ by case alone are named as written, by @name and by the menu", async () => {
    const app = await setup([msg("m1", "agent:claude")], ["claude", "reviewer", "Reviewer"]);
    expect(app.mention("@Reviewer inspect")).toEqual({ to: "agent:Reviewer", rest: "inspect" });
    expect(app.mention("@reviewer inspect")).toEqual({ to: "agent:reviewer", rest: "inspect" });
    expect(app.mention("@REVIEWER inspect")).toBeNull();          // which of the two is not said
    expect(app.mention("@Claude hi")).toEqual({ to: "agent:claude", rest: "hi" });
    type("@Reviewer");
    // (each row: the avatar's initial, then the name)
    expect([...document.querySelectorAll(".menu .mi.who")].map(i => i.textContent!.trim().slice(-8))).toEqual(["Reviewer", "reviewer"]);
    press("Enter");
    expect(app.target).toBe("agent:Reviewer");
  });

  it("a session's message still unanswered for is shown going to its agent when its thread is opened, and goes there", async () => {
    svelte = await import("svelte");
    const { app } = await import("../lib/state.svelte");
    app.agents = ["claude", "codex"].map(name => ({ name, address: "agent:" + name, driver: "acp", queued: 0, running: 0 }));
    app.conn = { ok: true };
    const S = { key: "session:claude/s1", kind: "session" as const, agent: "agent:claude", session: "s1", title: "", updated: "" };
    const T = { key: "thread:T", kind: "thread" as const, thread: "T", agent: "agent:claude", session: "s1", session_agent: "agent:claude", title: "", updated: "" };
    fake.Node.Chats.mockResolvedValue({ items: [S] });
    await app.loadChats();
    fake.Node.SessionHistory.mockResolvedValue([]);
    await app.open(S as any);
    app.draftOf().text = "go on";
    fake.Node.SendTask.mockRejectedValueOnce(new Error("timeout"));
    expect(await app.submit(app.draftOf())).toBe(false);
    fake.Node.Chats.mockResolvedValue({ items: [T] });
    await app.loadChats();
    app.newTask();
    fake.Node.Thread.mockResolvedValue([msg("m1", "agent:claude"), msg("m2", "agent:codex")]); // Codex asked there meanwhile
    await app.open(T as any);
    expect(app.target).toBe("agent:claude");
    expect(await app.submit(app.draftOf())).toBe(true);
    const [first, again] = fake.Node.SendTask.mock.calls.map(c => c[0]);
    expect(again).toEqual(first);
    expect(app.target).toBe("agent:claude");
  });

  it("a message sent when there was no one else to name is that message again, though another agent is there by the retry", async () => {
    const app = await setup([msg("m1", "agent:claude")], ["claude"]);
    fake.Node.SendTask.mockRejectedValueOnce(new Error("lost"));
    type("@claude explain this");
    press("Enter");
    await flush(); await flush(); svelte.flushSync();
    app.agents = ["claude", "codex"].map(name => ({ name, address: "agent:" + name, driver: "acp", queued: 0, running: 0 })) as any;
    svelte.flushSync();
    press("Enter");
    await flush(); await flush(); svelte.flushSync();
    const [first, again] = fake.Node.SendTask.mock.calls.map(c => c[0]);
    expect(first).toMatchObject({ to: "agent:claude", body: "@claude explain this" });
    expect(again).toEqual(first);
  });

  it("a chat with one agent to choose from has no chip; a cloud sandbox's chat takes no agent of this Mac", async () => {
    let app = await setup([msg("m1", "agent:claude")], ["claude"]);
    expect(chip()).toBeNull();
    expect(textarea().placeholder).not.toContain("@");
    for (const m of mounted) svelte.unmount(m);
    mounted = [];
    vi.resetModules();
    app = await setup([msg("m1", "agent:sb_1")]);
    expect(app.targets()).toEqual(["agent:sb_1"]);
    expect(chip()).toBeNull();
  });
});
