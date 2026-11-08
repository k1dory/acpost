// How the app names things: agents, directories, sizes (one place, so they read the same everywhere).

/** An agent's address said as its product: agent:claude → Claude Code. */
export function agentLabel(addr?: string): string {
  const n = (addr ?? "").replace(/^agent:/, "");
  return n === "claude" ? "Claude Code" : n === "codex" ? "Codex" : n;
}

/** A directory under the home directory said from ~ (/Users/me/src → ~/src; /Users/meow stays). */
export function tilde(dir: string | undefined, home: string): string {
  if (!dir) return "";
  if (!home || (dir !== home && !dir.startsWith(home.replace(/\/$/, "") + "/"))) return dir;
  return "~" + dir.slice(home.replace(/\/$/, "").length);
}

/** A size: 512 B, 12 KB, 3.4 MB. */
export function bytes(n: number): string {
  return n < 1024 ? `${n} B` : n < 1 << 20 ? `${(n / 1024).toFixed(0)} KB` : `${(n / (1 << 20)).toFixed(1)} MB`;
}
