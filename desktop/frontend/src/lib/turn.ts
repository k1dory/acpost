// A message's events as the conversation shows them: the agent's text, its thinking, its tool
// calls (the latest event of a call replaces the earlier), permission requests and their
// decisions, the plan, usage, notices — in the order they came.

export type Ev = { seq: number; time: string; kind: string; data: string };

export type Edit = { path: string; added: number; removed: number; old?: string; new?: string; cut?: boolean };
export type Tool = {
  id: string; title?: string; kind?: string; status?: string; input?: any; locations?: string[];
  output?: string; exit_code?: number; edits?: Edit[]; description?: string;
  started: number; ended?: number;
};
export type Option = { option_id: string; name: string; kind: string };
export type Request = {
  request_id: string; kind: string; title?: string; message?: string; text?: string; tool_kind?: string;
  tool_call_id?: string; raw_input?: any; options?: Option[]; url?: string;
  decision?: string; by?: string; at: number;
};
export type Usage = {
  turn?: string;
  input?: number; output?: number; cache_read?: number; cache_write?: number; thought?: number; total?: number;
  cost?: number; currency?: string; context_used?: number; context_size?: number; model?: string;
};

/** The turn a usage event's id belongs to: a delta (turn#2, turn#3, …: what the turn's count grew
 * by after it was published) names its turn before the "#"; any other id is a turn of its own. */
export function usageTurn(id: string | undefined): { turn: string; delta: boolean } {
  const s = id ?? "";
  const i = s.lastIndexOf("#");
  const k = i > 0 ? s.slice(i + 1) : "";
  if (/^[1-9][0-9]*$/.test(k) && Number(k) >= 2) return { turn: s.slice(0, i), delta: true };
  return { turn: s, delta: false };
}

/** A usage event folded into what the turn has: a delta of the same turn adds to it (tokens and
 * cost; the context as the delta says it); anything else is the turn's usage as it now stands. */
export function foldUsage(prev: Usage | null, d: Usage): Usage {
  const at = usageTurn(d?.turn);
  if (!prev || !at.delta || usageTurn(prev.turn).turn !== at.turn) return d;
  const sum = (a?: number, b?: number) => (a === undefined && b === undefined ? undefined : (a ?? 0) + (b ?? 0));
  return {
    ...prev,
    turn: at.turn,
    input: sum(prev.input, d.input), output: sum(prev.output, d.output),
    cache_read: sum(prev.cache_read, d.cache_read), cache_write: sum(prev.cache_write, d.cache_write),
    thought: sum(prev.thought, d.thought), total: sum(prev.total, d.total),
    cost: sum(prev.cost, d.cost), currency: prev.currency || d.currency, model: prev.model || d.model,
    context_used: d.context_used || prev.context_used, context_size: d.context_size || prev.context_size,
  };
}

export type Block =
  | { t: "text"; text: string }
  | { t: "thought"; text: string; start: number; end: number }
  | { t: "tool"; id: string }
  | { t: "request"; id: string }
  | { t: "plan"; entries: { status: string; content: string }[] }
  | { t: "notice"; text: string; severity?: string }
  | { t: "changes"; data: any };

export class Turn {
  blocks: Block[] = [];
  tools = new Map<string, Tool>();
  requests = new Map<string, Request>();
  usage: Usage | null = null;
  plan: { status: string; content: string }[] = [];
  last = 0;

  add(evs: Ev[]): this {
    for (const e of evs) {
      if (e.seq <= this.last) continue;
      if (this.last && e.seq > this.last + 1) this.problems.push(`events ${this.last + 1}–${e.seq - 1} not seen`);
      this.last = e.seq;
      this.one(e);
    }
    return this;
  }

  /** Events that could not be applied, and calls whose start was not seen (a gap in the stream). */
  problems: string[] = [];

  private one(e: Ev) {
    try { this.apply(e); } catch (x) { this.problems.push(`event ${e.seq} (${e.kind}): ${(x as Error)?.message ?? x}`); }
  }

  private apply(e: Ev) {
    let d: any;
    try { d = JSON.parse(e.data); } catch { d = e.data; }
    const at = Date.parse(e.time) || Date.now();
    const tail = this.blocks[this.blocks.length - 1];
    // A thought lasts until what the agent does next.
    if (tail?.t === "thought" && e.kind !== "thought") tail.end = Math.max(tail.end, at);
    switch (e.kind) {
      case "text": {
        const s = typeof d === "string" ? d : d?.text ?? "";
        if (tail?.t === "text") tail.text += s; else this.blocks.push({ t: "text", text: s });
        break;
      }
      case "thought": {
        const s = typeof d === "string" ? d : d?.text ?? "";
        if (tail?.t === "thought") { tail.text += s; tail.end = at; }
        else this.blocks.push({ t: "thought", text: s, start: at, end: at });
        break;
      }
      case "tool_call":
      case "tool_call_update": {
        if (!d?.id) break;
        const prev = this.tools.get(d.id);
        if (!prev) {
          if (e.kind === "tool_call_update") this.problems.push(`event ${e.seq}: an update of call ${d.id}, whose start was not seen`);
          this.tools.set(d.id, { ...d, started: at });
          this.blocks.push({ t: "tool", id: d.id });
        } else {
          const merged: Tool = { ...prev, ...strip(d) };
          if (d.status === "completed" || d.status === "failed") merged.ended = at;
          this.tools.set(d.id, merged);
        }
        break;
      }
      case "permission_request": {
        if (!d?.request_id) break;
        this.requests.set(d.request_id, { ...d, at });
        this.blocks.push({ t: "request", id: d.request_id });
        break;
      }
      case "permission": {
        const r = this.requests.get(d?.request_id);
        if (r) { r.decision = d.decision; r.by = d.by; }
        break;
      }
      case "plan": {
        // Entries are "status: text" strings; anything else in the list is left out (one bad
        // entry must not throw and lose the rest of the batch).
        const entries = (Array.isArray(d) ? d : []).filter((s: unknown): s is string => typeof s === "string").map((s: string) => {
          const i = s.indexOf(": ");
          return i > 0 ? { status: s.slice(0, i), content: s.slice(i + 2) } : { status: "", content: s };
        });
        this.plan = entries;
        // One plan per turn, where it first appeared, kept current.
        const at = this.blocks.find(b => b.t === "plan") as { t: "plan"; entries: typeof entries } | undefined;
        if (at) at.entries = entries; else this.blocks.push({ t: "plan", entries });
        break;
      }
      case "usage":
        this.usage = foldUsage(this.usage, d); // a turn's delta adds to it, never replaces it
        break;
      case "notice":
        this.blocks.push({ t: "notice", text: typeof d === "string" ? d : d?.text ?? d?.title ?? "", severity: d?.severity });
        break;
      case "changes":
        this.blocks.push({ t: "changes", data: d });
        break;
      case "truncated": // the node kept no more events of this message (limits.max_events_per_message)
        this.blocks.push({ t: "notice", text: typeof d === "string" ? d : "later events were not kept", severity: "warning" });
        break;
    }
  }

  /** A copy for rendering: the turn is mutated in place as events come. */
  view() {
    return {
      blocks: this.blocks.map(b => ({ ...b })) as Block[],
      tools: new Map([...this.tools].map(([k, v]) => [k, { ...v }])),
      requests: new Map([...this.requests].map(([k, v]) => [k, { ...v }])),
      usage: this.usage ? { ...this.usage } : null,
    };
  }

  /** Requests still waiting for a person. */
  waiting(): Request[] {
    return [...this.requests.values()].filter(r => !r.decision);
  }
}

function strip(d: any) {
  const o: any = {};
  for (const [k, v] of Object.entries(d)) if (v !== undefined && v !== null && v !== "") o[k] = v;
  return o;
}

const bare = new Set(["terminal", "edit", "read", "write", "bash", "tool", "execute", "fetch", "search"]);

/** What the agent set out to do with a call — not "tool call". */
export function intent(t: Tool): string {
  if (t.description) return t.description;
  if (t.title && !bare.has(t.title.trim().toLowerCase())) return t.title;
  const i = t.input ?? {};
  if (typeof i.command === "string") return i.command;
  if (Array.isArray(i.command)) return i.command.join(" ");
  for (const k of ["file_path", "path", "url", "query", "pattern"]) if (typeof i[k] === "string") return i[k];
  if (t.locations?.length) return t.locations.join(", ");
  return t.title || t.kind || "tool";
}

export function toolName(t: Tool): string {
  return (t.kind || t.title?.split(/[\s:]/)[0] || "tool").toLowerCase();
}

export type ToolState = "running" | "done" | "failed" | "pending" | "cancelled" | "expired" | "unknown";

/** A call's state, its message's state ("queued", "running", "done", …) telling what an unfinished
 * call came to: a turn done ended its calls with it; one cancelled or expired stopped them; one that
 * failed leaves their result unknown. */
export function toolState(t: Tool, msgState: string): ToolState {
  if (t.status === "failed" || (t.exit_code !== undefined && t.exit_code !== 0)) return "failed";
  if (t.status === "completed") return "done";
  switch (msgState) {
    case "done": return "done";
    case "cancelled": return "cancelled";
    case "expired": return "expired";
    case "failed": return "unknown";
  }
  return t.status === "pending" ? "pending" : "running";
}

export function editTotals(t: Tool): { files: number; added: number; removed: number } {
  const e = t.edits ?? [];
  return { files: new Set(e.map(x => x.path)).size, added: e.reduce((a, x) => a + (x.added || 0), 0), removed: e.reduce((a, x) => a + (x.removed || 0), 0) };
}

/** A unified diff of one edit's old and new text, line by line (LCS on small inputs). */
export function lineDiff(oldText = "", newText = ""): { op: " " | "+" | "-"; text: string; a?: number; b?: number }[] {
  const a = oldText.split("\n"), b = newText.split("\n");
  if (a.length * b.length > 400_000) {
    return [...a.map(t => ({ op: "-" as const, text: t })), ...b.map(t => ({ op: "+" as const, text: t }))];
  }
  const m = a.length, n = b.length;
  const L: Uint32Array[] = Array.from({ length: m + 1 }, () => new Uint32Array(n + 1));
  for (let i = m - 1; i >= 0; i--) for (let j = n - 1; j >= 0; j--) L[i][j] = a[i] === b[j] ? L[i + 1][j + 1] + 1 : Math.max(L[i + 1][j], L[i][j + 1]);
  const out: { op: " " | "+" | "-"; text: string; a?: number; b?: number }[] = [];
  let i = 0, j = 0;
  while (i < m && j < n) {
    if (a[i] === b[j]) { out.push({ op: " ", text: a[i], a: ++i, b: ++j }); }
    else if (L[i + 1][j] >= L[i][j + 1]) out.push({ op: "-", text: a[i], a: ++i });
    else out.push({ op: "+", text: b[j], b: ++j });
  }
  while (i < m) out.push({ op: "-", text: a[i], a: ++i });
  while (j < n) out.push({ op: "+", text: b[j], b: ++j });
  return out;
}

/** A path as git quotes it in a diff's header (C style: \\, \", \t, \n and bytes in octal, the
 * name's UTF-8), read back. */
export function gitUnquote(s: string): string {
  const bytes: number[] = [];
  const enc = new TextEncoder();
  const simple: Record<string, number> = { a: 7, b: 8, t: 9, n: 10, v: 11, f: 12, r: 13, '"': 34, "\\": 92 };
  for (let i = 0; i < s.length; i++) {
    if (s[i] !== "\\") { bytes.push(...enc.encode(s[i])); continue; }
    const oct = /^[0-3][0-7]{2}/.exec(s.slice(i + 1));
    if (oct) { bytes.push(parseInt(oct[0], 8)); i += 3; continue; }
    const c = s[i + 1];
    if (c !== undefined && c in simple) { bytes.push(simple[c]); i++; continue; }
    bytes.push(92);
  }
  return new TextDecoder().decode(new Uint8Array(bytes));
}

/** A git unified diff split into files. */
export function splitDiff(diff: string): { path: string; lines: { op: string; text: string; a?: number; b?: number }[] }[] {
  const files: { path: string; lines: { op: string; text: string; a?: number; b?: number }[] }[] = [];
  let cur: (typeof files)[number] | null = null;
  let a = 0, b = 0;
  for (const line of diff.split("\n")) {
    if (line.startsWith("diff --git ")) {
      // A name git quotes (not ASCII, a quotation mark: "a/…" "b/…") is read as it is written.
      const q = /^diff --git "a\/(?:[^"\\]|\\.)*" "b\/((?:[^"\\]|\\.)*)"$/.exec(line);
      const m = q ? null : / b\/(.*)$/.exec(line);
      cur = { path: q ? gitUnquote(q[1]) : m ? m[1] : line.slice(11), lines: [] };
      files.push(cur);
      continue;
    }
    if (!cur || line.startsWith("index ") || line.startsWith("--- ") || line.startsWith("+++ ") || line.startsWith("new file") || line.startsWith("deleted file") || line.startsWith("similarity") || line.startsWith("rename ")) continue;
    const h = /^@@ -(\d+)(?:,\d+)? \+(\d+)(?:,\d+)? @@(.*)$/.exec(line);
    if (h) { a = +h[1]; b = +h[2]; cur.lines.push({ op: "@", text: line }); continue; }
    if (line === "") continue; // the end of the text (a line of a diff has its mark at least)
    const op = line[0];
    if (op === "+") cur.lines.push({ op, text: line.slice(1), b: b++ });
    else if (op === "-") cur.lines.push({ op, text: line.slice(1), a: a++ });
    else if (op === "\\") continue;
    else cur.lines.push({ op: " ", text: line.slice(1), a: a++, b: b++ });
  }
  return files;
}
