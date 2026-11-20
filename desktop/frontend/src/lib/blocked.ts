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
  if (b.command) {
    const one = b.command.trim();
    if (!/[\n\r]/.test(one) && one.length <= 300) return `Bash(${one})`;
    const words: string[] = [];
    for (const w of one.split(/\s+/)) { if (!/^[\w./@:-]+$/.test(w) || words.length === 2) break; words.push(w); }
    return words.length ? `Bash(${words.join(" ")}:*)` : "Bash";
  }
  if (b.name && /^mcp__\w+/.test(b.name)) return b.name; // the tool as Claude Code names it
  if (/^mcp__\w+/.test(b.title)) return b.title.split(/\s/)[0];
  const byKind: Record<string, string> = { read: "Read", edit: "Edit", delete: "Edit", move: "Edit", fetch: "WebFetch", search: "Grep", execute: "Bash" };
  return byKind[b.kind] ?? b.title.split(/\s/)[0] ?? "";
}

/** The message that approves a refused call and asks for it again: what the person says is part of
 * what auto mode's classifier weighs (an approval in Claude Code's terminal adds no rule either). */
export function approveText(b: Pick<Blocked, "command" | "title">, head = "I approve this action that auto mode blocked; run it again:"): string {
  const what = b.command || b.title;
  const fence = what.includes("```") ? "````" : "```";
  return `${head}\n\n${fence}\n${what}\n${fence}`;
}
