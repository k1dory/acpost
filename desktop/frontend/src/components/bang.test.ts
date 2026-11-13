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
    ([...document.querySelectorAll(".sh button")].find(b => b.textContent === "Add to message") as HTMLButtonElement).click();
    svelte.flushSync();
    expect(textarea().value).toBe("$ ls\n(not run: fork/exec /bin/zsh: no such file)\n");
    type("");
    // Another chat does not show this one's runs.
    app.thread = "U"; app.chat = null;
    svelte.flushSync();
    expect(app.shellsHere()).toHaveLength(0);
  });

  it("what was run in a new task is shown in the thread it becomes, and not in the next new task", async () => {
    svelte = await import("svelte");
    const { app } = await import("../lib/state.svelte");
    app.agents = [{ name: "claude", address: "agent:claude", driver: "acp", queued: 0, running: 0 }];
    app.conn = { ok: true };
    app.newTask();
    app.draft.agent = "agent:claude";
    fake.Node.Shell.mockResolvedValue({ output: "ok\n", code: 0, ms: 5 });
    await app.runShell("echo ok");
    expect(app.shellsHere()).toHaveLength(1);
    fake.Node.SendTask.mockResolvedValue({ id: "m1", from: "user:me", to: "agent:claude", thread: "T9", depth: 0, state: "queued", created_at: "", updated_at: "" });
    fake.Node.Chats.mockResolvedValue({ items: [] });
    const d = app.draftOf();
    d.text = "first message";
    expect(await app.submit(d)).toBe(true);
    expect(app.thread).toBe("T9");
    expect(app.shellsHere().map(s => s.cmd)).toEqual(["echo ok"]);
    app.newTask();
    expect(app.shellsHere()).toHaveLength(0);
  });

  it("a run's output put into a draft that was a command makes it a message: Enter sends, nothing is run", async () => {
    const app = await setup();
    fake.Node.Shell.mockResolvedValue({ output: "`touch /tmp/x`\n", code: 0, ms: 1 });
    type("!echo first");
    enter();
    await flush(); svelte.flushSync();
    fake.Node.Shell.mockClear();
    type("!echo intended");
    ([...document.querySelectorAll(".sh button")].find(b => b.textContent === "Add to message") as HTMLButtonElement).click();
    svelte.flushSync();
    expect(textarea().value.startsWith(" !echo intended\n$ echo first")).toBe(true);
    expect(document.querySelector(".comp.bang")).toBeNull();
    fake.Node.SendTask.mockResolvedValue({ id: "m1", from: "user:me", to: "agent:claude", thread: "T", depth: 0, state: "queued", created_at: "", updated_at: "" });
    enter();
    await flush(); await flush();
    expect(fake.Node.Shell).not.toHaveBeenCalled();
    expect(fake.Node.SendTask).toHaveBeenCalledTimes(1);
    // A quotation into an empty draft that starts with "!" is a message too.
    app.quote = "!not a command";
    svelte.flushSync();
    expect(textarea().value).toBe(" !not a command");
  });

  it("a new task's runs stay with it while its first message goes, though another new task is started", async () => {
    svelte = await import("svelte");
    const { app } = await import("../lib/state.svelte");
    app.agents = [{ name: "claude", address: "agent:claude", driver: "acp", queued: 0, running: 0 }];
    app.conn = { ok: true };
    app.newTask();
    app.draft.agent = "agent:claude";
    fake.Node.Shell.mockResolvedValue({ output: "ok\n", code: 0, ms: 5 });
    await app.runShell("echo ok");
    let ack!: (m: any) => void;
    fake.Node.SendTask.mockImplementation(() => new Promise(r => { ack = r; }));
    fake.Node.Chats.mockResolvedValue({ items: [] });
    fake.Node.Thread.mockResolvedValue([]);
    const d = app.draftOf();
    d.text = "first message";
    const going = app.submit(d);
    await flush();
    app.newTask();                       // before the node answered
    expect(app.shellsHere()).toHaveLength(0);
    ack({ id: "m1", from: "user:me", to: "agent:claude", thread: "T9", depth: 0, state: "queued", created_at: "", updated_at: "" });
    expect(await going).toBe(true);
    expect(app.shellsHere()).toHaveLength(0);
    await app.open({ key: "thread:T9", kind: "thread", thread: "T9", agent: "agent:claude", title: "", updated: "" } as any);
    expect(app.shellsHere().map(s => s.cmd)).toEqual(["echo ok"]);
  });

  it("a message given back into a draft where a command was typed makes it a message: nothing of it is run", async () => {
    svelte = await import("svelte");
    const { app } = await import("../lib/state.svelte");
    app.agents = [{ name: "claude", address: "agent:claude", driver: "acp", queued: 0, running: 0 }];
    app.conn = { ok: true };
    app.newTask();
    app.draft.agent = "agent:claude";
    let fail!: (e: any) => void;
    fake.Node.SendTask.mockImplementation(() => new Promise((_, rej) => { fail = rej; }));
    const a = app.draftOf();
    a.text = "printf from_playbook";
    const going = app.submit(a);
    await flush();
    app.newTask();
    app.draftOf().text = "!echo intended";
    fail(new Error("down"));
    expect(await going).toBe(false);
    const text = app.draftOf().text;
    expect(text).toContain("printf from_playbook");
    expect(text.startsWith("!")).toBe(false);
  });

  it("a session whose draft moved to its thread shows and keeps its runs under that thread", async () => {
    svelte = await import("svelte");
    const { app } = await import("../lib/state.svelte");
    app.agents = [{ name: "claude", address: "agent:claude", driver: "acp", queued: 0, running: 0 }];
    app.conn = { ok: true };
    const S = { key: "session:claude/s1", kind: "session" as const, agent: "agent:claude", session: "s1", title: "", updated: "" };
    const T = { key: "thread:T1", kind: "thread" as const, thread: "T1", agent: "agent:claude", session: "s1", session_agent: "agent:claude", title: "", updated: "" };
    fake.Node.Chats.mockResolvedValue({ items: [S] });
    await app.loadChats();
    fake.Node.SessionHistory.mockResolvedValue([]);
    await app.open(S as any);
    fake.Node.Shell.mockResolvedValue({ output: "one\n", code: 0, ms: 1 });
    await app.runShell("echo one");
    app.draftOf().text = "go on";
    fake.Node.SendTask.mockRejectedValueOnce(new Error("timeout")); // taken by the node, the answer lost
    expect(await app.submit(app.draftOf())).toBe(false);
    fake.Node.Chats.mockResolvedValue({ items: [T] });              // the session is a thread now; the screen still shows the session
    await app.loadChats();
    expect(app.shellsHere().map(s => s.cmd)).toEqual(["echo one"]);
    await app.runShell("echo two");
    fake.Node.Thread.mockResolvedValue([]);
    await app.open(T as any);
    expect(app.shellsHere().map(s => s.cmd)).toEqual(["echo one", "echo two"]);
  });

  it("text that starts with ! but was not typed here as it stands is a message, however it came to be there", async () => {
    const app = await setup();
    fake.Node.SendTask.mockResolvedValue({ id: "m1", from: "user:me", to: "agent:claude", thread: "T", depth: 0, state: "queued", created_at: "", updated_at: "" });
    // Put there by the app (what is left after a send, a draft given back, anything): not typed.
    app.draftOf().text = "!printf from-playbook";
    svelte.flushSync();
    expect(document.querySelector(".comp.bang")).toBeNull();
    enter();
    await flush(); await flush();
    expect(fake.Node.Shell).not.toHaveBeenCalled();
    expect(fake.Node.SendTask.mock.calls[0][0].body).toBe("!printf from-playbook");
    // Typed, then changed by the app: not a command either. Typed and left as typed: a command.
    type("!echo a");
    app.draftOf().text = "!echo a; rm -rf x";
    svelte.flushSync();
    expect(document.querySelector(".comp.bang")).toBeNull();
    type("!echo typed");                      // typed over what the app put: still not theirs alone
    expect(document.querySelector(".comp.bang")).toBeNull();
    type("");                                 // emptied: what is typed now is theirs
    type("!echo typed");
    expect(document.querySelector(".comp.bang")).toBeTruthy();
    fake.Node.Shell.mockResolvedValue({ output: "", code: 0, ms: 1 });
    enter();
    await flush();
    expect(fake.Node.Shell.mock.calls[0][1]).toBe("echo typed");
  });

  it("the same text as a command run before, put back by the app, is a message", async () => {
    const app = await setup();
    fake.Node.Shell.mockResolvedValue({ output: "", code: 0, ms: 1 });
    type("!echo x >> log");
    enter();
    await flush(); svelte.flushSync();
    expect(fake.Node.Shell).toHaveBeenCalledTimes(1);
    app.draftOf().text = "!echo x >> log";   // a send given back, a draft joined: not typed now
    svelte.flushSync();
    expect(textarea().value).toBe("!echo x >> log");
    expect(document.querySelector(".comp.bang")).toBeNull();
    fake.Node.SendTask.mockResolvedValue({ id: "m1", from: "user:me", to: "agent:claude", thread: "T", depth: 0, state: "queued", created_at: "", updated_at: "" });
    enter();
    await flush(); await flush();
    expect(fake.Node.Shell).toHaveBeenCalledTimes(1);
    expect(fake.Node.SendTask).toHaveBeenCalledTimes(1);
  });

  it("Enter that accepts an input method's candidate neither runs nor sends; the next Enter runs once", async () => {
    await setup();
    fake.Node.Shell.mockResolvedValue({ output: "", code: 0, ms: 1 });
    type("!echo 日本");
    for (const init of [{ isComposing: true }, { keyCode: 229 }]) {
      textarea().dispatchEvent(new KeyboardEvent("keydown", { key: "Enter", bubbles: true, cancelable: true, ...init } as any));
      svelte.flushSync();
    }
    await flush();
    expect(fake.Node.Shell).not.toHaveBeenCalled();
    expect(fake.Node.SendTask).not.toHaveBeenCalled();
    expect(textarea().value).toBe("!echo 日本");
    enter();
    await flush();
    expect(fake.Node.Shell).toHaveBeenCalledTimes(1);
    expect(fake.Node.Shell.mock.calls[0][1]).toBe("echo 日本");
  });

  it("runs the command to the letter; where the chat's files are not this Mac's it runs nothing and keeps the draft", async () => {
    const app = await setup();
    fake.Node.Shell.mockResolvedValue({ output: "", code: 0, ms: 1 });
    type("!printf '<%s>' foo\\ ");
    enter();
    await flush();
    expect(fake.Node.Shell.mock.calls[0][1]).toBe("printf '<%s>' foo\\ ");
    fake.Node.Shell.mockClear();
    for (const set of [
      () => { app.conn = { ok: true, url: "https://node.example:7443" } as any; },
      () => { app.conn = { ok: true, url: "unix:///x/acpost.sock" } as any; app.chat = { ...app.chat!, agent: "agent:claude@far" } as any; },
      () => { app.chat = { ...app.chat!, agent: "agent:sb_1" } as any; },
    ]) {
      set();
      type("!rm -rf build");
      enter();
      await flush(); svelte.flushSync();
      expect(fake.Node.Shell).not.toHaveBeenCalled();
      expect(textarea().value).toBe("!rm -rf build");
      expect(app.error).toContain("this Mac");
      app.error = "";
    }
  });

  it("a new task set for the cloud runs nothing; one set for this Mac runs in its chosen directory, whatever chat was open before", async () => {
    const app = await setup();
    fake.Node.Shell.mockResolvedValue({ output: "", code: 0, ms: 1 });
    app.target = "agent:sb_1";              // a cloud chat was open before
    app.newTask();
    app.draft.where = "cloud"; app.draft.cwd = "/work/local";
    svelte.flushSync();
    type("!rm -rf build");
    enter();
    await flush(); svelte.flushSync();
    expect(fake.Node.Shell).not.toHaveBeenCalled();
    expect(app.error).toContain("cloud");
    app.error = "";
    app.draft.where = "mac"; app.draft.agent = "agent:claude";
    svelte.flushSync();
    type("!ls");
    enter();
    await flush();
    expect(fake.Node.Shell.mock.calls[0].slice(1)).toEqual(["ls", "/work/local"]);
  });

  it("a chat whose directory is not known here runs nothing (not in the home directory)", async () => {
    const app = await setup();
    app.chat = { key: "thread:T", kind: "thread", thread: "T", agent: "agent:claude", title: "t", updated: "" } as any; // no cwd
    svelte.flushSync();
    type("!rm -rf build");
    enter();
    await flush(); svelte.flushSync();
    expect(fake.Node.Shell).not.toHaveBeenCalled();
    expect(textarea().value).toBe("!rm -rf build");
    expect(app.error).toContain("not known");
  });

  it("a command typed while an earlier message was going is still a command when that message is acknowledged", async () => {
    await setup();
    let ack!: (m: any) => void;
    fake.Node.SendTask.mockImplementation(() => new Promise(r => { ack = r; }));
    fake.Node.Chats.mockResolvedValue({ items: [] });
    type("an ordinary message");
    enter();
    await flush();
    type("!echo next");
    ack({ id: "m1", from: "user:me", to: "agent:claude", thread: "T", depth: 0, state: "queued", created_at: "", updated_at: "" });
    await flush(); await flush(); svelte.flushSync();
    expect(textarea().value).toBe("!echo next");
    expect(document.querySelector(".comp.bang")).toBeTruthy();
    fake.Node.Shell.mockResolvedValue({ output: "", code: 0, ms: 1 });
    enter();
    await flush();
    expect(fake.Node.Shell.mock.calls[0][1]).toBe("echo next");
  });

  it("a command typed in a new task is still a command when the person comes back to a new task", async () => {
    svelte = await import("svelte");
    const { app } = await import("../lib/state.svelte");
    app.agents = [{ name: "claude", address: "agent:claude", driver: "acp", queued: 0, running: 0 }];
    app.conn = { ok: true };
    app.home = "/Users/me";
    app.newTask();
    app.draft.agent = "agent:claude";
    const Composer = (await import("./Composer.svelte")).default;
    let c = svelte.mount(Composer, { target: document.body });
    svelte.flushSync();
    type("!printf retained");
    svelte.unmount(c);
    fake.Node.Thread.mockResolvedValue([]);
    await app.open({ key: "thread:T", kind: "thread", thread: "T", agent: "agent:claude", cwd: "/w", title: "t", updated: "" } as any);
    app.newTask();
    c = svelte.mount(Composer, { target: document.body });
    mounted.push(c);
    svelte.flushSync();
    expect(textarea().value).toBe("!printf retained");
    expect(document.querySelector(".comp.bang")).toBeTruthy();
    fake.Node.Shell.mockResolvedValue({ output: "", code: 0, ms: 1 });
    enter();
    await flush();
    expect(fake.Node.Shell.mock.calls[0].slice(1)).toEqual(["printf retained", "/Users/me"]);
    expect(fake.Node.SendTask).not.toHaveBeenCalled();
  });

  it("what the app left in the field does not become a command by being edited", async () => {
    const app = await setup();
    let ack!: (m: any) => void;
    fake.Node.SendTask.mockImplementationOnce(() => new Promise(r => { ack = r; }));
    fake.Node.Chats.mockResolvedValue({ items: [] });
    type("Review");
    enter();
    await flush();
    app.draftOf().text = "Review\n!printf from_playbook";   // a playbook chosen while it went
    ack({ id: "m1", from: "user:me", to: "agent:claude", thread: "T", depth: 0, state: "queued", created_at: "", updated_at: "" });
    await flush(); await flush(); svelte.flushSync();
    expect(textarea().value.trim()).toBe("!printf from_playbook");
    type(textarea().value + " ");                           // an ordinary edit of it
    expect(document.querySelector(".comp.bang")).toBeNull();
    fake.Node.SendTask.mockResolvedValue({ id: "m2", from: "user:me", to: "agent:claude", thread: "T", depth: 0, state: "queued", created_at: "", updated_at: "" });
    enter();
    await flush(); await flush();
    expect(fake.Node.Shell).not.toHaveBeenCalled();
  });

  it("a command typed in a thread's own draft stays one when an emptied draft is joined to it", async () => {
    svelte = await import("svelte");
    const { app } = await import("../lib/state.svelte");
    const { ComposerDraft } = await import("../lib/composer.svelte");
    const there = app.draftOf("thread:T7");
    there.type("!echo typed-here");
    const from = new ComposerDraft("new");
    (app as any).moveDraft(from, "thread:T7");
    expect(app.draftOf("thread:T7").text).toBe("!echo typed-here");
    expect(app.draftOf("thread:T7").own).toBe(true);
    const other = new ComposerDraft("new");
    other.type("typed elsewhere");
    (app as any).moveDraft(other, "thread:T7");               // two texts joined: not a command
    expect(app.draftOf("thread:T7").own).toBe(false);
  });

  it("a shared chat's files stay where its first agent is, whoever the next message goes to", async () => {
    const app = await setup();
    app.msgs = [{ id: "T", from: "user:me", to: "agent:claude", thread: "T", depth: 0, state: "done", created_at: "", updated_at: "" }] as any;
    app.target = "agent:codex@far";                           // another agent called into the chat
    fake.Node.Shell.mockResolvedValue({ output: "", code: 0, ms: 1 });
    type("!pwd");
    enter();
    await flush();
    expect(fake.Node.Shell.mock.calls[0].slice(1)).toEqual(["pwd", "/work/repo"]);
  });

  it("a thread of another node's agent whose shown history begins with a local participant runs nothing", async () => {
    const app = await setup();
    // The thread's first message is not on screen (a long thread shows its latest); the chat is the remote agent's.
    app.chat = { key: "thread:T", kind: "thread", thread: "T", agent: "agent:claude@far", title: "t", updated: "" } as any;
    app.msgs = [{ id: "m900", from: "user:me", to: "agent:codex", thread: "T", depth: 0, state: "done", meta: { acp_cwd: "/work/local" }, created_at: "", updated_at: "" }] as any;
    svelte.flushSync();
    type("!rm -rf build");
    enter();
    await flush(); svelte.flushSync();
    expect(fake.Node.Shell).not.toHaveBeenCalled();
    expect(app.error).toContain("this Mac");
    // And a root an agent made (depth above zero) is still the root.
    app.error = "";
    app.chat = { key: "thread:T", kind: "thread", thread: "T", agent: "agent:claude", cwd: "/work/repo", title: "t", updated: "" } as any;
    app.msgs = [{ id: "T", from: "agent:lead", to: "agent:claude", thread: "T", depth: 1, state: "done", meta: { acp_cwd: "/work/root" }, created_at: "", updated_at: "" },
      { id: "m2", from: "user:me", to: "agent:codex@far", thread: "T", depth: 0, state: "done", created_at: "", updated_at: "" }] as any;
    fake.Node.Shell.mockResolvedValue({ output: "", code: 0, ms: 1 });
    type("");
    type("!pwd");
    enter();
    await flush();
    expect(fake.Node.Shell.mock.calls[0].slice(1)).toEqual(["pwd", "/work/repo"]); // as the node lists the chat
    // Not listed (no directory in the chat's entry): its first message's.
    app.chat = { ...app.chat!, cwd: "" } as any;
    type("!pwd");
    enter();
    await flush();
    expect(fake.Node.Shell.mock.calls[1].slice(1)).toEqual(["pwd", "/work/root"]);
  });

  it("undo that brings back what the app put in the field does not make it the person's", async () => {
    const app = await setup();
    app.draftOf().text = " !printf from_playbook";            // a playbook, put in as a message
    svelte.flushSync();
    type("");                                                // select all, delete
    const undo = (v: string, kind: string) => { textarea().value = v; textarea().dispatchEvent(new InputEvent("input", { bubbles: true, inputType: kind })); svelte.flushSync(); };
    undo(" !printf from_playbook", "historyUndo");
    type("!printf from_playbook");                           // the protective space removed
    expect(document.querySelector(".comp.bang")).toBeNull();
    fake.Node.SendTask.mockResolvedValue({ id: "m1", from: "user:me", to: "agent:claude", thread: "T", depth: 0, state: "queued", created_at: "", updated_at: "" });
    enter();
    await flush(); await flush();
    expect(fake.Node.Shell).not.toHaveBeenCalled();
    undo("!ls", "historyRedo");
    expect(document.querySelector(".comp.bang")).toBeNull();
  });

  it("runs where the node lists the chat (its session's directory), not where a later thread of the task asked", async () => {
    const app = await setup();
    app.chat = { key: "thread:T", kind: "thread", thread: "T", agent: "agent:claude", cwd: "/A", title: "t", updated: "" } as any;
    app.msgs = [{ id: "T", from: "user:me", to: "agent:claude", thread: "T", depth: 0, state: "done", meta: { acp_cwd: "/B" }, created_at: "", updated_at: "" }] as any;
    fake.Node.Shell.mockResolvedValue({ output: "", code: 0, ms: 1 });
    type("!pwd");
    enter();
    await flush();
    expect(fake.Node.Shell.mock.calls[0].slice(1)).toEqual(["pwd", "/A"]);
  });

  it("a chat listed in a directory that is not a whole path runs nothing and keeps the draft", async () => {
    const app = await setup();
    for (const cwd of [".", "repo/sub", "./x"]) {
      app.chat = { key: "thread:T", kind: "thread", thread: "T", agent: "agent:claude", cwd, title: "t", updated: "" } as any;
      svelte.flushSync();
      type("");
      type("!pwd");
      enter();
      await flush(); svelte.flushSync();
      expect(fake.Node.Shell).not.toHaveBeenCalled();
      expect(textarea().value).toBe("!pwd");
      app.error = "";
    }
  });
});
