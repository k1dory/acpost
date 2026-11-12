// Which coding agent an agent is: by what the node says runs behind it, else by its name.
import { describe, it, expect } from "vitest";
import { brandOf, agentBrand, brand, allBrands } from "./brand";

describe("brandOf", () => {
  it("names each CLI by its ACP agentInfo or its command", () => {
    const cases: [string, string][] = [
      ["@zed-industries/claude-code-acp", "claude"], ["claude-agent-acp", "claude"], ["Claude Code", "claude"],
      ["codex-acp", "codex"], ["@zed-industries/codex-acp", "codex"], ["OpenAI Codex", "codex"],
      ["gemini-cli", "gemini"], ["gemini", "gemini"],
      ["copilot", "copilot"], ["GitHub Copilot CLI", "copilot"],
      ["opencode", "opencode"], ["cursor-agent", "cursor"], ["amp-acp", "amp"], ["ampcode", "amp"],
      ["devin", "devin"], ["kiro-cli", "kiro"], ["goose", "goose"], ["qwen-code", "qwen"], ["qwen", "qwen"],
      ["kimi-cli", "kimi"], ["deepseek-cli", "deepseek"], ["cline", "cline"], ["aider", "aider"],
      ["auggie", "auggie"], ["Augment Code", "auggie"], ["codebuddy", "codebuddy"], ["zed", "zed"],
    ];
    for (const [s, want] of cases) expect([s, brandOf(s)]).toEqual([s, want]);
  });

  it("takes the agent before the adapter's maker, and OpenCode and CodeBuddy before Codex", () => {
    expect(brandOf("@zed-industries/claude-code-acp")).toBe("claude");
    expect(brandOf("opencode")).toBe("opencode");
    expect(brandOf("codebuddy-code")).toBe("codebuddy");
  });

  it("does not see a short brand inside another word", () => {
    for (const s of ["example", "sample-agent", "decline", "devinci", "lazed", "agent:helper"]) expect([s, brandOf(s)]).toEqual([s, ""]);
  });

  it("takes the first hint that names a brand", () => {
    expect(brandOf(undefined, "", "node.test", "agent:codex")).toBe("codex");
    expect(brandOf("gemini", "agent:claude")).toBe("gemini");
  });
});

describe("agentBrand", () => {
  const agents = [
    { name: "work", address: "agent:work", node: "mac", product: "gemini-cli" },
    { name: "claude", address: "agent:claude", node: "mac" },
    { name: "x", address: "agent:x@box", node: "box", product: "kimi-cli" },
  ];
  it("draws what the node says runs behind an agent, whatever it is called", () => {
    expect(agentBrand("agent:work", agents)).toBe("gemini");
    expect(agentBrand("agent:work@mac", agents)).toBe("gemini"); // as usage names a local agent
    expect(agentBrand("agent:x@box", agents)).toBe("kimi");
  });
  it("falls back to the name: an agent without a product, one not listed, a cloud agent's brand", () => {
    expect(agentBrand("agent:claude", agents)).toBe("claude");
    expect(agentBrand("agent:codex@elsewhere", agents)).toBe("codex");
    expect(agentBrand("codex", [])).toBe("codex");
    expect(agentBrand("agent:work@other", agents)).toBe("");
    expect(agentBrand("", agents)).toBe("");
    expect(agentBrand(undefined, agents)).toBe("");
  });
});

describe("the brands", () => {
  it("cover the CLIs asked for, each with a title and a tile or a mark", () => {
    const ids = allBrands().map(b => b.id).sort();
    expect(ids).toEqual(["aider", "amp", "auggie", "claude", "cline", "codebuddy", "codex", "copilot", "cursor", "deepseek", "devin", "gemini", "goose", "kimi", "kiro", "opencode", "qwen", "zed"]);
    for (const b of allBrands()) {
      expect(b.title).toBeTruthy();
      expect(b.tile.length).toBeGreaterThan(0);
      expect(b.tile.length).toBeLessThanOrEqual(2);
    }
    expect(allBrands().filter(b => b.mark).map(b => b.id).sort()).toEqual(["claude", "codex", "copilot", "cursor", "gemini"]);
    expect(brand("nope")).toBeUndefined();
  });
});
