// The node's name never brands an agent: only what runs behind it, else the agent's own name
// (review round 1).
import { describe, it, expect } from "vitest";
import { agentBrand } from "./brand";

describe("agentBrand and node names", () => {
  it("does not take a brand word from the node", () => {
    expect(agentBrand("agent:helper@claude-mac", [])).toBe("");
    expect(agentBrand("agent:codex@claude-mac", [])).toBe("codex");
    expect(agentBrand("agent:claude@codex-box", [])).toBe("claude");
    expect(agentBrand("agent:x@gemini-host", [{ name: "x", address: "agent:x@gemini-host", product: "kimi-cli" }])).toBe("kimi");
    expect(agentBrand("agent:work@cursor", [{ name: "work", address: "agent:work", node: "cursor" }])).toBe("");
  });
  it("keeps a plain brand name working (a cloud agent's)", () => {
    expect(agentBrand("codex", [])).toBe("codex");
    expect(agentBrand("Claude Code", [])).toBe("claude");
    expect(agentBrand("gemini-cli", [])).toBe("gemini");
  });
});
