// An MCP server as the settings edit it, made into what the node takes: the server (with the names
// of its variables and headers) and the secret values, keyed by the same names.

export type MCP = { name: string; kind: "stdio" | "http"; command?: string; args?: string[]; url?: string; env?: string[]; headers?: string[]; agents?: string[]; enabled: boolean };
export type Pair = { name: string; value: string; kept: boolean };
export type MCPEdit = { isNew: boolean; name: string; kind: "stdio" | "http"; command: string; args: string; url: string; env: Pair[]; headers: Pair[]; agents: string[]; enabled: boolean };

/** The request, or why it cannot be made (an English text for t()). A name is trimmed once, for
 * both the list and the secret's key: " TOKEN " is TOKEN in both, or the node finds no value. */
export function mcpRequest(e: MCPEdit): { server: MCP; secrets: Record<string, string> } | { error: string; name?: string } {
  const secrets: Record<string, string> = {};
  const names = (list: Pair[], prefix: "env" | "header") => {
    const out: string[] = [];
    for (const p of list) {
      const name = p.name.trim();
      if (!name) {
        if (p.value) return { error: "A value has no name" };
        continue; // an empty row
      }
      if (out.includes(name)) return { error: "The name {name} is there twice", name };
      out.push(name);
      if (!p.kept || p.value) secrets[prefix + "/" + name] = p.value;
    }
    return out;
  };
  const env = names(e.env, "env");
  if (!Array.isArray(env)) return env;
  const headers = names(e.headers, "header");
  if (!Array.isArray(headers)) return headers;
  return {
    server: {
      name: e.name.trim(), kind: e.kind, command: e.command.trim(), url: e.url.trim(), enabled: e.enabled, agents: e.agents,
      args: e.args.split("\n").map(s => s.trim()).filter(Boolean), env, headers,
    },
    secrets,
  };
}
