// What the IDE is asked to show of an agent's edits: the files a tool call that just completed
// edited, each once, with the edit's text when the event has it whole.
import type { Ev, Edit } from "./turn";

export type Reveal = { path: string; old: string; new: string };

/** A tool call's edits are shown once it completed; an event older than this (a chat opened on a
 * turn already under way: its earlier calls) is not. */
export const revealFresh = 20_000;
/** Files shown for one call at most (a call that edits many: the first ones). */
export const revealMax = 3;

/** The edits to show for these events of message msg: those of calls that completed now, not
 * shown before (seen keeps "message:call"), and after since — when its events began to be followed
 * (what a turn did before its chat was opened is replayed first: that is not shown). The last edit
 * of each file: it is what the file has. */
export function editsToReveal(msg: string, evs: Ev[], seen: Set<string>, now: number, since = 0): Reveal[] {
  const out: Reveal[] = [];
  for (const e of evs) {
    if (e.kind !== "tool_call" && e.kind !== "tool_call_update") continue;
    let d: any;
    try { d = JSON.parse(e.data); } catch { continue; }
    if (!d?.id || d.status !== "completed" || !Array.isArray(d.edits) || !d.edits.length) continue;
    const key = msg + ":" + d.id;
    if (seen.has(key)) continue;
    seen.add(key);
    const at = Date.parse(e.time);
    if (!(now - at < revealFresh) || at < since) continue; // old, before the chat was opened, or no time: not now
    const last = new Map<string, Edit>();
    for (const ed of d.edits as Edit[]) if (typeof ed?.path === "string" && ed.path) last.set(ed.path, ed);
    for (const ed of [...last.values()].slice(0, revealMax)) out.push(revealOf(ed));
  }
  return out;
}

/** An edit as the IDE is asked to show it: its text only when whole (a cut one cannot be found in
 * the file: the file alone is shown). */
export function revealOf(ed: Edit): Reveal {
  return ed.cut ? { path: ed.path, old: "", new: "" } : { path: ed.path, old: ed.old ?? "", new: ed.new ?? "" };
}

/** Whether an agent's files are this Mac's: an agent of this node (not one of another node, not a
 * cloud sandbox, whose paths are of another machine). Only its edits are shown in the IDE. */
export function localFiles(agent: string): boolean {
  return agent.startsWith("agent:") && !agent.includes("@") && !agent.startsWith("agent:sb_");
}
