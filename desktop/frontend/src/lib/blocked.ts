// What Claude Code's auto mode refused: its classifier's denial is the call's result, a failed call
// whose output begins with this (Claude Code's own words). /permissions lists them ("Recently
// blocked", as Claude Code's terminal does), from the open chat's turns.
import type { Tool, Turn } from "./turn";

export const classifierDenial = "Permission for this action was denied by the Claude Code auto mode classifier. Reason: ";

/** Why auto mode refused a call (its classifier's reason, without the brackets), or null when the
 * call was not refused by it. */
export function blockedReason(tool: Pick<Tool, "status" | "output">): string | null {
  const out = tool.output ?? "";
  if (!out.startsWith(classifierDenial)) return null;
  const rest = out.slice(classifierDenial.length);
  const end = rest.indexOf(". If you have other tasks");
  const r = (end > 0 ? rest.slice(0, end) : rest.split("\n")[0]).trim();
  return r.replace(/^\[(.*)\]$/s, "$1").trim();
}

export type Blocked = { msg: string; to: string; id: string; name: string; title: string; command: string; kind: string; reason: string; at: number };

/** A call's command, when it ran one. */
export function commandOf(tool: Tool): string {
  const c = tool.input?.command;
  return typeof c === "string" ? c : Array.isArray(c) ? c.join(" ") : "";
}

/** The calls auto mode refused in these turns (message id, turn, the agent it was), newest first. */
export function blockedIn(turns: [string, Turn, string?][]): Blocked[] {
  const out: Blocked[] = [];
  for (const [msg, turn, to = ""] of turns) {
    for (const tool of turn.tools.values()) {
      const reason = blockedReason(tool);
      if (reason === null) continue;
      out.push({ msg, to, id: tool.id, name: tool.name ?? "", title: tool.title ?? "", command: commandOf(tool), kind: tool.kind ?? "", reason, at: tool.ended ?? tool.started });
    }
  }
  return out.sort((a, b) => b.at - a.at);
}

/** A rule that would allow the call, as Claude Code writes them: a command as it is (one line), else
 * its program and subcommand with anything after them; an MCP tool by its name; else the tool's kind. */
export function ruleFor(b: Pick<Blocked, "command" | "title" | "kind"> & { name?: string }): string {
  // An MCP tool is allowed by its name, whatever its arguments say (one may be called "command").
  if (b.name && /^mcp__\w+/.test(b.name)) return b.name;
  if (b.command) {
    const one = b.command.trim();
    if (!/[\n\r]/.test(one) && one.length <= 300) return `Bash(${one})`;
    const words: string[] = [];
    for (const w of one.split(/\s+/)) { if (!/^[\w./@:-]+$/.test(w) || words.length === 2) break; words.push(w); }
    return words.length ? `Bash(${words.join(" ")}:*)` : "Bash";
  }
  if (/^mcp__\w+/.test(b.title)) return b.title.split(/\s/)[0];
  // A tool Claude Code names itself (WebSearch, WebFetch, Grep…): its name is its rule.
  if (b.name && /^[A-Z][A-Za-z]+$/.test(b.name)) return b.name;
  const byKind: Record<string, string> = { read: "Read", edit: "Edit", delete: "Edit", move: "Edit", fetch: "WebFetch", search: "Grep", execute: "Bash" };
  return byKind[b.kind] ?? b.title.split(/\s/)[0] ?? "";
}

/** The message that approves a refused call and asks for it again. The agent reads it (what the
 * person says is part of what auto mode's classifier weighs; it adds no rule, as an approval in
 * Claude Code's terminal does not); the chat shows it as "Permission granted" alone. */
export const grantedMark = "[acpost] Permission granted";
export function approveText(b: Pick<Blocked, "command" | "title">): string {
  const what = b.command || b.title;
  const fence = "`".repeat(Math.max(3, ...[...what.matchAll(/`+/g)].map(m => m[0].length + 1)));
  return `${grantedMark}: the user approves the action auto mode blocked. Run it again:\n\n${fence}\n${what}\n${fence}`;
}

/** Whether a message is such an approval (shown as a mark, not as its text). */
export const isGranted = (body: string | undefined) =>
  !!body && /^\[acpost\] Permission granted: the user approves the action auto mode blocked\. Run it again:\n\n(`{3,})\n[\s\S]*\n\1$/.test(body);
