import { describe, it, expect } from "vitest";
import { agentLabel, tilde, bytes } from "./names";

describe("names", () => {
  it("says agents as their product", () => {
    expect(agentLabel("agent:claude")).toBe("Claude Code");
    expect(agentLabel("agent:codex")).toBe("Codex");
    expect(agentLabel("agent:other")).toBe("other");
  });
  it("says a directory under home from ~, not one that only starts like it", () => {
    expect(tilde("/Users/me/src", "/Users/me")).toBe("~/src");
    expect(tilde("/Users/me", "/Users/me")).toBe("~");
    expect(tilde("/Users/meow/x", "/Users/me")).toBe("/Users/meow/x");
  });
  it("says sizes", () => {
    expect(bytes(512)).toBe("512 B");
    expect(bytes(2048)).toBe("2 KB");
    expect(bytes(3 << 20)).toBe("3.0 MB");
  });
});
