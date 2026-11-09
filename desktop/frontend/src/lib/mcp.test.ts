import { describe, it, expect } from "vitest";
import { mcpRequest, type MCPEdit } from "./mcp";

const edit = (o: Partial<MCPEdit>): MCPEdit => ({ isNew: true, name: "wiki", kind: "stdio", command: "npx", args: "", url: "", env: [], headers: [], agents: [], enabled: true, ...o });

describe("mcpRequest (Y19)", () => {
  it("keys a secret by the same trimmed name as the list", () => {
    const r = mcpRequest(edit({ env: [{ name: " TOKEN ", value: "s3cret", kept: false }] }));
    expect("server" in r && r.server.env).toEqual(["TOKEN"]);
    expect("secrets" in r && r.secrets).toEqual({ "env/TOKEN": "s3cret" });
  });
  it("refuses a name given twice", () => {
    const r = mcpRequest(edit({ env: [{ name: "A", value: "1", kept: false }, { name: "A ", value: "2", kept: false }] }));
    expect("error" in r && r.name).toBe("A");
  });
  it("refuses a value with no name, skips an empty row", () => {
    expect("error" in mcpRequest(edit({ headers: [{ name: " ", value: "x", kept: false }] }))).toBe(true);
    const r = mcpRequest(edit({ env: [{ name: "", value: "", kept: false }] }));
    expect("server" in r && r.server.env).toEqual([]);
  });
  it("keeps a kept value unsent unless retyped", () => {
    const r = mcpRequest(edit({ env: [{ name: "K", value: "", kept: true }, { name: "N", value: "new", kept: true }] }));
    expect("secrets" in r && r.secrets).toEqual({ "env/N": "new" });
  });
});
