// The builder of cloud agents against a mocked node: what is on the screen is one cloud's, and an
// answer that comes late changes only what it was asked for.
import { describe, it, expect, vi, beforeEach } from "vitest";
import { fakeNode, deferred, flush, type Fake } from "../test/fake";

let fake: Fake;
vi.mock("./api", async () => {
  const errors = await import("./errors");
  const Node = new Proxy({}, { get: (_, k: string) => fake.Node[k] });
  return { ...errors, Node, on: (n: string, f: (d: any) => void) => fake.on(n, f), b64: (s: string) => s, unb64: () => new Uint8Array() };
});

const def = (name: string, extra: any = {}) => ({ ref: name, shared: false, revision: 2, name, description: "", policy: "backend-dev", environment: "python-dev", repo: "", instructions: "old", connections: [], ...extra });
const catalogue = (...agents: any[]) => ({ cloud: "cloud-a", agents, environments: [{ ref: "python-dev", name: "python-dev", shared: false, revision: 1 }], connections: [], policies: ["backend-dev"] });

async function builder(cat = catalogue(def("backend"), def("docs"))) {
  fake.Node.CloudInfo.mockResolvedValue({ configured: true, id: "cloud-a", url: "https://a", tenant: "a" });
  fake.Node.CloudBuild.mockResolvedValue(cat);
  fake.Node.CloudAgents.mockResolvedValue({ configured: true, items: [] });
  const { CloudBuilder } = await import("./cloudbuild.svelte");
  const cb = new CloudBuilder();
  await cb.load();
  return cb;
}

beforeEach(() => {
  vi.resetModules();
  fake = fakeNode();
  fake.Node.Chats.mockResolvedValue({ items: [] });
  fake.Node.Agents.mockResolvedValue([]);
});

describe("connecting another cloud", () => {
  it("closes what was open: a delete clicked once in tenant A is not confirmed in tenant B", async () => {
    const cb = await builder();
    cb.editAgent(cb.cat!.agents[0]);
    await cb.remove(); // the first click
    expect(cb.sure).toBe("backend");
    fake.Node.CloudConnect.mockResolvedValue({ restarted: true, info: {} });
    fake.Node.CloudInfo.mockResolvedValue({ configured: true, id: "cloud-b", url: "https://b", tenant: "b" });
    fake.Node.CloudBuild.mockResolvedValue({ ...catalogue(def("backend")), cloud: "cloud-b" });
    expect(await cb.connect("https://b", "sbk_b", "")).toBe(true);
    expect(cb.ed).toBeNull();
    expect(cb.sure).toBe("");
    await cb.remove();
    expect(fake.Node.CloudDeleteAgent).not.toHaveBeenCalled();
    // And B's agent, opened anew, is deleted by two clicks of its own.
    cb.editAgent(cb.cat!.agents[0]);
    await cb.remove();
    expect(fake.Node.CloudDeleteAgent).not.toHaveBeenCalled();
    await cb.remove();
    expect(fake.Node.CloudDeleteAgent).toHaveBeenCalledWith("cloud-b", "backend");
  });

  it("drops a check made in the old cloud, and does not start while something is being written", async () => {
    const cb = await builder();
    cb.editAgent(cb.cat!.agents[0]);
    fake.Node.CloudPlanAgent.mockResolvedValue({ plan: "p1", valid: true, action: "update", revision: 2, next_revision: 3, diff: [], errors: [] });
    cb.ed!.spec.instructions = "new";
    await cb.check();
    expect(cb.checked).toBe(true);
    const dep = deferred();
    fake.Node.CloudDeployAgent.mockReturnValue(dep.promise);
    const deploying = cb.deploy();
    expect(await cb.connect("https://b", "k", "")).toBe(false); // a deploy is on its way
    expect(fake.Node.CloudConnect).not.toHaveBeenCalled();
    dep.resolve({ ref: "backend", revision: 3, action: "update" });
    await deploying;
    cb.editAgent(cb.cat!.agents[0]);
    cb.ed!.spec.instructions = "newer";
    await cb.check();
    fake.Node.CloudConnect.mockResolvedValue({ restarted: false, info: {} });
    await cb.connect("https://b", "k", "");
    await cb.deploy();
    expect(fake.Node.CloudDeployAgent).toHaveBeenCalledTimes(1);
  });
});

describe("a connect that failed", () => {
  it("after the key was saved: the old cloud's catalogue is gone, and what comes back is the new cloud's", async () => {
    const cb = await builder();
    cb.editAgent(cb.cat!.agents[0]);
    await cb.remove(); // armed in A
    const conn = deferred();
    fake.Node.CloudConnect.mockReturnValue(conn.promise);
    const connecting = cb.connect("https://b", "sbk_b", "");
    // While it connects nothing of A is there to act on.
    expect(cb.cat).toBeNull();
    expect(cb.ed).toBeNull();
    cb.newAgent();
    expect(cb.ed).toBeNull();
    // The key is saved, the restart failed: the node is down for now.
    fake.Node.CloudInfo.mockRejectedValue(new Error(JSON.stringify({ code: "node_down", message: "down" })));
    conn.reject(new Error("the node did not start in 20s"));
    expect(await connecting).toBe(false);
    expect(cb.cat).toBeNull();
    expect(cb.err).toContain("did not start");
    await cb.remove();
    await cb.deploy();
    expect(fake.Node.CloudDeleteAgent).not.toHaveBeenCalled();
    // The node comes back, with B: its catalogue, under its own name.
    fake.Node.CloudInfo.mockResolvedValue({ configured: true, id: "cloud-b", url: "https://b", tenant: "b" });
    fake.Node.CloudBuild.mockResolvedValue({ ...catalogue(def("backend")), cloud: "cloud-b" });
    await cb.load();
    cb.editAgent(cb.cat!.agents[0]);
    await cb.remove();
    await cb.remove();
    expect(fake.Node.CloudDeleteAgent).toHaveBeenCalledTimes(1);
    expect(fake.Node.CloudDeleteAgent).toHaveBeenCalledWith("cloud-b", "backend");
  });

  it("the settings left and opened again while it connects show the same builder, still connecting", async () => {
    await builder();
    const { cloud } = await import("./cloudbuild.svelte");
    await cloud.load();
    cloud.editAgent(cloud.cat!.agents[0]);
    await cloud.remove();
    const conn = deferred();
    fake.Node.CloudConnect.mockReturnValue(conn.promise);
    const connecting = cloud.connect("https://b", "sbk_b", "");
    // Opened again: the component takes the app's builder, and does not load over a connect.
    const again = (await import("./cloudbuild.svelte")).cloud;
    expect(again).toBe(cloud);
    expect(again.busy).toBe("connect");
    expect(again.cat).toBeNull();
    // A load that was on its way (or is started anyway) does not bring A back over the connect.
    const info = deferred();
    fake.Node.CloudInfo.mockReturnValueOnce(info.promise);
    const loading = again.load();
    fake.Node.CloudInfo.mockResolvedValue({ configured: true, id: "cloud-b", url: "https://b", tenant: "b" });
    fake.Node.CloudBuild.mockResolvedValue({ ...catalogue(def("backend")), cloud: "cloud-b" });
    conn.resolve({ restarted: true, info: {} });
    await connecting;
    info.resolve({ configured: true, id: "cloud-a", url: "https://a", tenant: "a" });
    await loading;
    expect(again.info?.tenant).toBe("b");
    expect(again.cat?.cloud).toBe("cloud-b");
    await again.remove();
    expect(fake.Node.CloudDeleteAgent).not.toHaveBeenCalled();
  });

  it("a catalogue that turns out another cloud's closes the editor over it", async () => {
    const cb = await builder();
    cb.editAgent(cb.cat!.agents[0]);
    await cb.remove();
    // The node restarted with B by itself.
    fake.Node.CloudInfo.mockResolvedValue({ configured: true, id: "cloud-b", url: "https://b", tenant: "b" });
    fake.Node.CloudBuild.mockResolvedValue({ ...catalogue(def("backend")), cloud: "cloud-b" });
    await cb.load();
    expect(cb.ed).toBeNull();
    expect(cb.sure).toBe("");
  });
});

describe("reading the cloud", () => {
  it("that fails keeps the form and what was typed in it", async () => {
    const cb = await builder();
    cb.editAgent(cb.cat!.agents[0]);
    cb.ed!.spec.instructions = "typed, not saved";
    fake.Node.CloudBuild.mockRejectedValue(new Error("timeout"));
    await cb.load();
    expect(cb.ed?.spec.instructions).toBe("typed, not saved");
    expect(cb.cat).not.toBeNull();
    expect(cb.err).toContain("timeout");
    fake.Node.CloudInfo.mockRejectedValue(new Error(JSON.stringify({ code: "node_down", message: "down" })));
    await cb.load();
    expect(cb.ed?.spec.instructions).toBe("typed, not saved");
    // The cloud itself not answering: the same.
    fake.Node.CloudInfo.mockResolvedValue({ configured: true, id: "cloud-a", url: "https://a", error: "no route", error_code: "unavailable" });
    await cb.load();
    expect(cb.ed?.spec.instructions).toBe("typed, not saved");
    expect(cb.cat?.cloud).toBe("cloud-a");
    expect(cb.info?.error).toBe("no route");
    // What it writes still names its own connection.
    fake.Node.CloudPlanAgent.mockResolvedValue({ plan: "p", valid: true, action: "update", revision: 2, next_revision: 3, diff: [], errors: [] });
    await cb.check();
    expect(fake.Node.CloudPlanAgent.mock.calls[0][0]).toBe("cloud-a");
    // No cloud any more: the form is of none.
    fake.Node.CloudInfo.mockResolvedValue({ configured: false });
    await cb.load();
    expect(cb.ed).toBeNull();
    expect(cb.cat).toBeNull();
  });

  it("never shows one cloud's name over another's catalogue", async () => {
    const cb = await builder();
    // The node changes its cloud between the two reads: A's identity, then B's catalogue.
    fake.Node.CloudInfo.mockResolvedValueOnce({ configured: true, id: "cloud-a", tenant: "a" }).mockResolvedValue({ configured: true, id: "cloud-b", tenant: "b" });
    fake.Node.CloudBuild.mockResolvedValue({ ...catalogue(def("backend")), cloud: "cloud-b" });
    await cb.load();
    expect(cb.info?.tenant).toBe("b");
    expect(cb.cat?.cloud).toBe("cloud-b");
    // One that never agrees is not shown at all.
    fake.Node.CloudInfo.mockResolvedValue({ configured: true, id: "cloud-a", tenant: "a" });
    await cb.load();
    expect(cb.err).not.toBe("");
    expect(cb.info?.tenant).toBe("b");
    expect(cb.cat?.cloud).toBe("cloud-b");
  });
});

describe("a check and a deploy", () => {
  it("a deploy goes with the check of exactly this definition", async () => {
    const cb = await builder();
    cb.editAgent(cb.cat!.agents[0]);
    await cb.deploy();
    expect(fake.Node.CloudDeployAgent).not.toHaveBeenCalled(); // never unchecked
    fake.Node.CloudPlanAgent.mockResolvedValue({ plan: "p1", valid: true, action: "update", revision: 2, next_revision: 3, diff: [], errors: [] });
    cb.ed!.spec.instructions = "new";
    await cb.check();
    cb.ed!.spec.instructions = "changed after the check";
    expect(cb.checked).toBe(false);
    await cb.deploy();
    expect(fake.Node.CloudDeployAgent).not.toHaveBeenCalled();
    await cb.check();
    fake.Node.CloudDeployAgent.mockResolvedValue({ ref: "backend", revision: 3, action: "update" });
    await cb.deploy();
    expect(fake.Node.CloudDeployAgent).toHaveBeenCalledWith("cloud-a", expect.objectContaining({ name: "backend", instructions: "changed after the check" }), "p1");
    expect(cb.ed).toBeNull();
    expect(cb.note).toContain("3");
  });

  it("a catalogue that moved on asks for the check again", async () => {
    const cb = await builder();
    cb.editAgent(cb.cat!.agents[0]);
    fake.Node.CloudPlanAgent.mockResolvedValue({ plan: "p1", valid: true, action: "update", revision: 2, next_revision: 3, diff: [], errors: [] });
    await cb.check();
    fake.Node.CloudDeployAgent.mockRejectedValue(new Error(JSON.stringify({ code: "conflict", message: "the catalogue changed since the check", hint: "check it again" })));
    await cb.deploy();
    expect(cb.ed!.plan).toBeNull();
    expect(cb.checked).toBe(false);
    expect(cb.err).not.toBe("");
  });

  it("a delete answered late closes its own editor, not the one opened meanwhile", async () => {
    const cb = await builder();
    cb.editAgent(cb.cat!.agents[0]);
    const del = deferred();
    fake.Node.CloudDeleteAgent.mockReturnValue(del.promise);
    await cb.remove();
    const removing = cb.remove();
    // Nothing else opens while it is deleted…
    cb.editAgent(cb.cat!.agents[1]);
    expect(cb.ed!.spec.name).toBe("backend");
    // …and an editor that is another one by then (closed and opened by the app) is left alone.
    const other = { cloud: "cloud-a", isNew: false, rev: 2, spec: { ...cb.ed!.spec, name: "docs", instructions: "typed" }, plan: null, planKey: "" };
    cb.ed = other;
    del.resolve(undefined);
    await removing;
    expect(cb.ed?.spec.name).toBe("docs");
    expect(cb.ed?.spec.instructions).toBe("typed");
  });
});

describe("a deploy answered late", () => {
  it("keeps what was typed while it was on its way, as not deployed", async () => {
    const cb = await builder();
    cb.newAgent();
    Object.assign(cb.ed!.spec, { name: "fresh", instructions: "first" });
    fake.Node.CloudPlanAgent.mockResolvedValue({ plan: "p1", valid: true, action: "create", revision: 0, next_revision: 1, diff: [], errors: [] });
    await cb.check();
    const dep = deferred();
    fake.Node.CloudDeployAgent.mockReturnValue(dep.promise);
    const deploying = cb.deploy();
    cb.ed!.spec.instructions = "first, and more";
    dep.resolve({ ref: "fresh", revision: 1, action: "create" });
    await deploying;
    expect(fake.Node.CloudDeployAgent.mock.calls[0][1]).toMatchObject({ instructions: "first" });
    expect(cb.ed).not.toBeNull();
    expect(cb.ed).toMatchObject({ isNew: false, rev: 1, plan: null, spec: { name: "fresh", instructions: "first, and more" } });
    expect(cb.checked).toBe(false);
  });

  it("renamed while it was on its way, the form is a new agent's, not the editor of one of that name", async () => {
    const cb = await builder();
    cb.newAgent();
    Object.assign(cb.ed!.spec, { name: "fresh", instructions: "first" });
    fake.Node.CloudPlanAgent.mockResolvedValue({ plan: "p1", valid: true, action: "create", revision: 0, next_revision: 1, diff: [], errors: [] });
    await cb.check();
    const dep = deferred();
    fake.Node.CloudDeployAgent.mockReturnValue(dep.promise);
    const deploying = cb.deploy();
    cb.ed!.spec.name = "docs"; // an agent the catalogue has
    dep.resolve({ ref: "fresh", revision: 1, action: "create" });
    await deploying;
    expect(cb.ed).toMatchObject({ isNew: true, rev: 0, plan: null, spec: { name: "docs" } });
    await cb.remove();
    await cb.remove();
    expect(fake.Node.CloudDeleteAgent).not.toHaveBeenCalled();
  });
});

describe("a draft", () => {
  const start = async () => {
    const cb = await builder();
    cb.editAgent(cb.cat!.agents[0]);
    cb.ask = "a backend developer"; cb.draftAgent = "agent:claude";
    return cb;
  };
  it("fills the form, and keeps what the person typed while it was written", async () => {
    const cb = await start();
    const res = deferred();
    fake.Node.CloudDraft.mockResolvedValue({ msg: "m1", thread: "t1", state: "queued" });
    fake.Node.CloudDraftResult.mockReturnValueOnce(Promise.resolve({ msg: "m1", thread: "t1", state: "running" })).mockReturnValueOnce(res.promise);
    const writing = cb.writeDraft();
    await flush();
    expect(cb.drafting).toBe(true);
    expect(cb.draft).toEqual({ msg: "m1", thread: "t1" });
    cb.ed!.spec.instructions = "mine";
    res.resolve({ msg: "m1", thread: "t1", state: "done", spec: { name: "other", description: "the shop's backend", policy: "", environment: "", repo: "", instructions: "the agent's", connections: [] } });
    await writing;
    expect(cb.ed!.spec).toMatchObject({ name: "backend", description: "the shop's backend", instructions: "mine", policy: "backend-dev" });
    expect(cb.drafting).toBe(false);
    expect(fake.Node.CloudDraft.mock.calls[0][0]).toMatchObject({ agent: "agent:claude", ask: "a backend developer", current: { name: "backend", instructions: "old" } });
  });

  it("stopped before its task is known, is cancelled when it is", async () => {
    const cb = await start();
    const made = deferred();
    fake.Node.CloudDraft.mockReturnValue(made.promise);
    const writing = cb.writeDraft();
    cb.stopDraft();
    expect(cb.drafting).toBe(false);
    expect(fake.Node.Cancel).not.toHaveBeenCalled();
    made.resolve({ msg: "m9", thread: "t9", state: "queued" });
    await writing;
    expect(fake.Node.Cancel).toHaveBeenCalledWith("m9");
    expect(fake.Node.CloudDraftResult).not.toHaveBeenCalled();
    expect(cb.ed!.spec.instructions).toBe("old");
  });

  it("stopped while written, is cancelled and fills nothing; a cancel that fails is said", async () => {
    const cb = await start();
    const res = deferred();
    fake.Node.CloudDraft.mockResolvedValue({ msg: "m1", thread: "t1", state: "queued" });
    fake.Node.CloudDraftResult.mockReturnValue(res.promise);
    fake.Node.Cancel.mockRejectedValue(new Error("the node is down"));
    const writing = cb.writeDraft();
    await flush();
    cb.stopDraft();
    await flush();
    expect(fake.Node.Cancel).toHaveBeenCalledWith("m1");
    expect(cb.err).toContain("the node is down");
    // Not stopped: still the builder's to stop, and stopped by the next try.
    expect(cb.draftLive).toBe(true);
    fake.Node.Cancel.mockResolvedValue({});
    cb.stopDraft();
    await flush();
    expect(fake.Node.Cancel).toHaveBeenCalledTimes(2);
    expect(cb.draftLive).toBe(false);
    res.resolve({ msg: "m1", thread: "t1", state: "done", spec: { name: "x", description: "", policy: "", environment: "", repo: "", instructions: "late", connections: [] } });
    await writing;
    expect(cb.ed!.spec.instructions).toBe("old");
  });

  it("an answer without a definition is said, and the form stays", async () => {
    const cb = await start();
    fake.Node.CloudDraft.mockResolvedValue({ msg: "m1", thread: "t1", state: "done", error: "the agent's answer had no definition" });
    await cb.writeDraft();
    expect(cb.err).toContain("no definition");
    expect(cb.ed!.spec.instructions).toBe("old");
    expect(cb.drafting).toBe(false);
  });
  it("whose wait failed still runs: closing the editor, or another draft, cancels it", async () => {
    const cb = await start();
    fake.Node.CloudDraft.mockResolvedValue({ msg: "m1", thread: "t1", state: "queued" });
    fake.Node.CloudDraftResult.mockRejectedValue(new Error("timeout"));
    await cb.writeDraft();
    expect(cb.drafting).toBe(false);
    expect(cb.draftLive).toBe(true);
    expect(fake.Node.Cancel).not.toHaveBeenCalled();
    // Another draft: the first task is cancelled, the second is the one waited for.
    fake.Node.CloudDraft.mockResolvedValue({ msg: "m2", thread: "t2", state: "queued" });
    await cb.writeDraft();
    expect(fake.Node.Cancel).toHaveBeenCalledWith("m1");
    cb.close();
    await flush();
    expect(fake.Node.Cancel).toHaveBeenCalledWith("m2");
    expect(cb.draftLive).toBe(false);
    // One that ended is not cancelled.
    cb.editAgent(cb.cat!.agents[0]);
    cb.ask = "x"; cb.draftAgent = "agent:claude";
    fake.Node.CloudDraft.mockResolvedValue({ msg: "m3", thread: "t3", state: "done", spec: { name: "", description: "d", policy: "", environment: "", repo: "", instructions: "", connections: [] } });
    await cb.writeDraft();
    cb.close();
    await flush();
    expect(fake.Node.Cancel).toHaveBeenCalledTimes(2);
  });
  it("asked for and never answered: asked again it is the same task, and a stop finds it by its key", async () => {
    const cb = await start();
    fake.Node.CloudDraft.mockRejectedValueOnce(new Error("connection reset"));
    await cb.writeDraft();
    expect(cb.err).toContain("connection reset");
    expect(cb.draftLive).toBe(true); // there may be a task
    const key = fake.Node.CloudDraft.mock.calls[0][0].key;
    expect(key).toMatch(/^[0-9a-f]{32}$/);
    // The same request again: the same key.
    fake.Node.CloudDraft.mockRejectedValueOnce(new Error("connection reset"));
    await cb.writeDraft();
    expect(fake.Node.CloudDraft.mock.calls[1][0].key).toBe(key);
    // Stop: the task is found by the key and cancelled.
    fake.Node.CloudDraft.mockResolvedValue({ msg: "m7", thread: "t7", state: "running" });
    cb.stopDraft();
    await flush();
    expect(fake.Node.CloudDraft.mock.calls[2][0].key).toBe(key);
    expect(fake.Node.Cancel).toHaveBeenCalledWith("m7");
    expect(cb.draftLive).toBe(false);
    // Another request is another key.
    cb.ask = "something else";
    fake.Node.CloudDraft.mockResolvedValue({ msg: "m8", thread: "t8", state: "done", error: "no definition" });
    await cb.writeDraft();
    expect(fake.Node.CloudDraft.mock.calls[3][0].key).not.toBe(key);
  });

  it("refused by the node leaves nothing to stop", async () => {
    const cb = await start();
    fake.Node.CloudDraft.mockRejectedValue(new Error(JSON.stringify({ code: "unsupported", message: "no read-only mode" })));
    await cb.writeDraft();
    expect(cb.draftLive).toBe(false);
  });
});

describe("a draft whose request may or may not have been taken", () => {
  const start = async () => {
    const cb = await builder();
    cb.editAgent(cb.cat!.agents[0]);
    cb.ask = "a backend developer"; cb.draftAgent = "agent:claude";
    return cb;
  };
  it("asked again after the form changed is another draft: the first is stopped, what was typed stays", async () => {
    const cb = await start();
    fake.Node.CloudDraft.mockRejectedValueOnce(new Error("connection reset"));
    await cb.writeDraft();
    const key = fake.Node.CloudDraft.mock.calls[0][0].key;
    cb.ed!.spec.instructions = "mine, typed after the first try";
    // The retry: the first ask found by its key and cancelled, the second under its own.
    fake.Node.CloudDraft.mockImplementation(async (r: any) => r.key === key
      ? { msg: "m-old", thread: "t", state: "running" }
      : { msg: "m-new", thread: "t2", state: "done", spec: { name: "", description: "d2", policy: "", environment: "", repo: "", instructions: "", connections: [] } });
    await cb.writeDraft();
    await flush();
    const keys = fake.Node.CloudDraft.mock.calls.map(c => c[0].key);
    expect(new Set(keys).size).toBe(2);
    expect(fake.Node.CloudDraft.mock.calls.find(c => c[0].key !== key)![0].current.instructions).toBe("mine, typed after the first try");
    expect(fake.Node.Cancel).toHaveBeenCalledWith("m-old");
    expect(cb.ed!.spec).toMatchObject({ instructions: "mine, typed after the first try", description: "d2" });
    expect(cb.draftLive).toBe(false);
  });

  it("a form with only a repository or a connection is the definition as it is: edited, it is another draft", async () => {
    const cb = await builder({ ...catalogue(), policies: [], environments: [] } as any);
    for (const edit of [(s: any) => { s.repo = "https://git.example/o/b"; }, (s: any) => { s.connections = ["github"]; }]) {
      fake.Node.CloudDraft.mockReset();
      cb.newAgent();
      cb.ask = "a reviewer"; cb.draftAgent = "agent:claude";
      cb.ed!.spec.repo = "https://git.example/o/a";
      fake.Node.CloudDraft.mockRejectedValueOnce(new Error("connection reset"));
      await cb.writeDraft();
      const first = fake.Node.CloudDraft.mock.calls[0][0];
      expect(first.current).toMatchObject({ repo: "https://git.example/o/a" });
      edit(cb.ed!.spec);
      fake.Node.CloudDraft.mockImplementation(async (r: any) => r.key === first.key
        ? { msg: "m-old", thread: "t", state: "running" }
        : { msg: "m-new", thread: "t", state: "done", spec: { name: "x", description: "", policy: "", environment: "", repo: "https://git.example/o/agent", instructions: "", connections: [] } });
      await cb.writeDraft();
      await flush();
      const second = fake.Node.CloudDraft.mock.calls.find(c => c[0].key !== first.key)![0];
      expect(second.current).toMatchObject(JSON.parse(JSON.stringify(cb.ed!.spec.connections.length ? { connections: ["github"] } : {})));
      // The agent's repo fills only a field not changed since this (second) draft was asked for.
      expect(cb.ed!.spec.name).toBe("x");
      cb.close();
      await flush();
    }
  });

  it("stopped while it was asked for, and the answer then lost: found by its key and stopped", async () => {
    const cb = await start();
    const made = deferred();
    fake.Node.CloudDraft.mockReturnValueOnce(made.promise);
    const writing = cb.writeDraft();
    cb.close();
    expect(cb.draftLive).toBe(true);
    fake.Node.CloudDraft.mockResolvedValue({ msg: "m5", thread: "t5", state: "running" });
    made.reject(new Error("connection reset"));
    await writing;
    await flush();
    expect(fake.Node.CloudDraft).toHaveBeenCalledTimes(2);
    expect(fake.Node.CloudDraft.mock.calls[1][0].key).toBe(fake.Node.CloudDraft.mock.calls[0][0].key);
    expect(fake.Node.Cancel).toHaveBeenCalledWith("m5");
    expect(cb.draftLive).toBe(false);
  });

  it("two of them unresolved: a stop goes after both", async () => {
    const cb = await start();
    fake.Node.CloudDraft.mockRejectedValue(new Error("connection reset"));
    await cb.writeDraft(); // A: lost
    cb.ask = "something else";
    await cb.writeDraft(); // B: lost, and A's search by its key lost too
    await flush();
    const keyA = fake.Node.CloudDraft.mock.calls[0][0].key;
    const keyB = fake.Node.CloudDraft.mock.calls.find(c => c[0].ask === "something else")![0].key;
    expect(cb.draftLive).toBe(true);
    fake.Node.CloudDraft.mockImplementation(async (r: any) => ({ msg: r.key === keyA ? "mA" : "mB", thread: "t", state: "running" }));
    cb.stopDraft();
    await flush();
    expect(fake.Node.Cancel).toHaveBeenCalledWith("mA");
    expect(fake.Node.Cancel).toHaveBeenCalledWith("mB");
    expect(keyA).not.toBe(keyB);
    expect(cb.draftLive).toBe(false);
  });
});

describe("a read's error", () => {
  it("goes once the cloud is read, and another's stays", async () => {
    const cb = await builder();
    fake.Node.CloudBuild.mockRejectedValueOnce(new Error("timeout"));
    await cb.load();
    expect(cb.err).toContain("timeout");
    await cb.load();
    expect(cb.err).toBe("");
    cb.err = "the deploy failed";
    await cb.load();
    expect(cb.err).toBe("the deploy failed");
  });
});
