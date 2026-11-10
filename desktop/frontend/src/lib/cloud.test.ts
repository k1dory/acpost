import { describe, expect, it } from "vitest";
import { localDrafters, cleanSpec, emptySpec, mergeDraft, specKey, specMissing, withCurrent, type Catalogue } from "./cloud";

const cat: Catalogue = {
  agents: [], policies: ["backend-dev"],
  environments: [{ ref: "python-dev", name: "python-dev", shared: false, revision: 3 }, { ref: "shared/base", name: "base", shared: true, revision: 1 }],
  connections: [{ ref: "github", name: "github", shared: false, revision: 1 }],
};

describe("a cloud agent's definition", () => {
  it("is sent without what the cloud ignores", () => {
    expect(cleanSpec({ ...emptySpec(), name: " a ", connections: ["github", " github ", ""] })).toEqual({ ...emptySpec(), name: "a", connections: ["github"] });
  });
  it("keeps its check only while it is the one checked", () => {
    const s = { ...emptySpec(), name: "a", instructions: "x" };
    expect(specKey(s)).toBe(specKey({ ...s, instructions: " x\n" }));
    expect(specKey(s)).not.toBe(specKey({ ...s, instructions: "y" }));
  });
  it("says the first thing missing", () => {
    expect(specMissing(emptySpec())).toMatch(/name/);
    expect(specMissing({ ...emptySpec(), name: "Backend" })).toMatch(/name/);
    expect(specMissing({ ...emptySpec(), name: "a-" })).toMatch(/name/);
    expect(specMissing({ ...emptySpec(), name: "a" })).toMatch(/policy/);
    expect(specMissing({ ...emptySpec(), name: "a", policy: "p" })).toMatch(/environment/);
    expect(specMissing({ ...emptySpec(), name: "a1-b", policy: "p", environment: "e" })).toBe("");
  });
  it("keeps a value the lists no longer have", () => {
    const items = [{ value: "a", label: "a" }];
    expect(withCurrent(items, "a")).toBe(items);
    expect(withCurrent(items, "")).toBe(items);
    expect(withCurrent(items, "shared/base@1")[0]).toEqual({ value: "shared/base@1", label: "shared/base@1" });
  });
});

describe("a drafted definition", () => {
  const form = { ...emptySpec(), name: "backend", policy: "backend-dev", environment: "python-dev", instructions: "old", connections: ["github"] };
  it("fills a new agent's form, and drops what the cloud does not have", () => {
    const r = mergeDraft(emptySpec(), { name: "docs", description: "d", policy: "prod-admin", environment: "shared/base@1", repo: "", instructions: "new", connections: ["github", "jira"] }, cat, true);
    expect(r.spec).toEqual({ name: "docs", description: "d", policy: "", environment: "shared/base@1", repo: "", instructions: "new", connections: ["github"] });
    expect(r.dropped).toEqual(["policy prod-admin", "connection jira"]);
  });
  it("keeps an existing agent's name and what the draft left empty", () => {
    const r = mergeDraft(form, { ...emptySpec(), name: "renamed", instructions: "new" }, cat, false);
    expect(r.spec).toEqual({ ...form, instructions: "new" });
    expect(r.dropped).toEqual([]);
  });
  it("leaves what the person changed while it was written", () => {
    const start = { ...form };
    const now = { ...form, instructions: "mine, typed meanwhile", connections: [] };
    const r = mergeDraft(now, { ...emptySpec(), description: "d", policy: "backend-dev", instructions: "the agent's", connections: ["github"] }, cat, false, start);
    expect(r.spec).toEqual({ ...now, description: "d" });
  });
});

describe("who can write a draft", () => {
  it("this node's ACP agents, which carry the node's name like any other", () => {
    const agents = [
      { address: "agent:claude", driver: "acp", node: "mac" },
      { address: "agent:codex", driver: "acp", node: "mac" },
      { address: "agent:sb_1", driver: "sandbox", node: "mac" },
      { address: "agent:claude@hub", driver: "acp", node: "hub" },
    ];
    expect(localDrafters(agents).map(a => a.address)).toEqual(["agent:claude", "agent:codex"]);
  });
});
