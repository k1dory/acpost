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
const catalogue = (...agents: any[]) => ({ agents, environments: [{ ref: "python-dev", name: "python-dev", shared: false, revision: 1 }], connections: [], policies: ["backend-dev"] });

async function builder(cat = catalogue(def("backend"), def("docs"))) {
  fake.Node.CloudInfo.mockResolvedValue({ configured: true, url: "https://a", tenant: "a" });
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
    fake.Node.CloudInfo.mockResolvedValue({ configured: true, url: "https://b", tenant: "b" });
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
    expect(fake.Node.CloudDeleteAgent).toHaveBeenCalledWith("backend");
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
    expect(fake.Node.CloudDeployAgent).toHaveBeenCalledWith(expect.objectContaining({ name: "backend", instructions: "changed after the check" }), "p1");
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
    const other = { isNew: false, rev: 2, spec: { ...cb.ed!.spec, name: "docs", instructions: "typed" }, plan: null, planKey: "" };
    cb.ed = other;
    del.resolve(undefined);
    await removing;
    expect(cb.ed?.spec.name).toBe("docs");
    expect(cb.ed?.spec.instructions).toBe("typed");
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
});
