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
    if (s[i] !== "\\") { // a whole character (two units of one outside the basic plane)
      const ch = String.fromCodePoint(s.codePointAt(i)!);
      bytes.push(...enc.encode(ch));
      i += ch.length - 1;
      continue;
    }
    const oct = /^[0-3][0-7]{2}/.exec(s.slice(i + 1));
    if (oct) { bytes.push(parseInt(oct[0], 8)); i += 3; continue; }
    const c = s[i + 1];
    if (c !== undefined && c in simple) { bytes.push(simple[c]); i++; continue; }
    bytes.push(92);
  }
  return new TextDecoder().decode(new Uint8Array(bytes));
}

/** The file a diff's header is of (its new name): what follows "diff --git ", two names, each
 * as it is or as git quotes it (not ASCII, a quotation mark: "b/…"), one quoted or both. Not
 * quoted, the name may have " b/" in it: the same name twice is read by its halves; a rename's
 * second name starts at the last " b/". */
export function headerPath(rest: string): string {
  const quoted = /(?:^| )"b\/((?:[^"\\]|\\.)*)"$/.exec(rest);
  if (quoted) return gitUnquote(quoted[1]);
  const n = (rest.length - 5) / 2;
  if (Number.isInteger(n) && n > 0 && rest.startsWith("a/") && rest.slice(2 + n, 2 + n + 3) === " b/" && rest.slice(2, 2 + n) === rest.slice(2 + n + 3)) return rest.slice(2, 2 + n);
  const i = rest.lastIndexOf(" b/");
  return i >= 0 ? rest.slice(i + 3) : rest;
}

/** A git unified diff split into files. */
export function splitDiff(diff: string): { path: string; lines: { op: string; text: string; a?: number; b?: number }[] }[] {
  const files: { path: string; lines: { op: string; text: string; a?: number; b?: number }[] }[] = [];
  let cur: (typeof files)[number] | null = null;
  let a = 0, b = 0;
  for (const line of diff.split("\n")) {
    if (line.startsWith("diff --git ")) {
      cur = { path: headerPath(line.slice(11)), lines: [] };
      files.push(cur);
      continue;
    }
    // A rename says its new name outright (the header's two names may not be told apart).
    if (cur && line.startsWith("rename to ")) {
      const to = line.slice(10), q = /^"((?:[^"\\]|\\.)*)"$/.exec(to);
      cur.path = q ? gitUnquote(q[1]) : to;
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

// What the node puts before a message's text in a prompt (internal/node: acpSlot.prompt, readerNote,
// knowledgeFor), to the letter: a test of the node's holds these to its own.
const promptLines = "Your final answer in this turn is sent back to the sender as the reply. To ask another agent, run `acpost send --to agent:<name> --body \"…\" --wait`. To attach files to your reply, run `acpost attach FILE…`.\n\n";
export const readerNote = "[acpost] The user reads your answers in the acpost app, which renders Markdown, code blocks, Mermaid and ```ui components. When a flow, an architecture, a sequence of calls or a state machine is clearer as a picture, draw it in a ```mermaid block (flowchart TD/LR or sequenceDiagram; every label in double quotes, A[\"api (HTTP)\"] -->|\"calls\"| B[\"store\"]; no HTML; about 15 nodes at most). Do not draw what a sentence says as well. When data, a plan or a question for the user is clearer as a component than as prose, put one strict JSON object (or an array of up to 8) in a ```ui block, each with \"type\" and an optional \"title\": stats {items:[{label,value,delta?}]}; chart {kind:\"bar\"|\"line\"|\"area\"|\"pie\",labels:[…],series:[{name,values:[a number a label]}],unit?}; table {columns:[…],rows:[[…]]} (the user sorts and filters it); timeline {items:[{time,title,text?,status?}]}; kanban {columns:[{title,cards:[{title,text?,tag?}]}]}; steps {items:[{title,status:\"done\"|\"doing\"|\"todo\"|\"failed\"}]}; choices {question,options:[…],multiple?}; form {fields:[{label,type:\"text\"|\"number\"|\"select\"|\"checkbox\",options?}]}. What the user picks or fills in comes back in their next message. Plain text in every field, no HTML.\n\n";
// The note sessions started before the app drew components were given: their prompts read back
// lose it too.
const readerNoteBefore = "[acpost] The user reads your answers in the acpost app, which renders Markdown, code blocks and Mermaid. When a flow, an architecture, a sequence of calls or a state machine is clearer as a picture, draw it in a ```mermaid block (flowchart TD/LR or sequenceDiagram; every label in double quotes, A[\"api (HTTP)\"] -->|\"calls\"| B[\"store\"]; no HTML; about 15 nodes at most). Do not draw what a sentence says as well.\n\n";
const knowledgeHead = "[acpost] What the user keeps as knowledge for this work (follow it unless the task says otherwise):\n\n";
const knowledgeEnd = "\n\n[acpost] The task:\n\n";
const attachedFiles = /^\[acpost\] Attached files:\n(?:- [^\n]*\n)+\n/;
const messageHead = /^\[acpost\] Message msg_[0-9A-Za-z]+ from [^\n]*\.\n/;

// A line of the attached-files list (acpSlot.prompt): "- NAME (TYPE[, N bytes]): WHERE". The name may
// have brackets and colons of its own, and so may the type's parameters: the type is found by its
// shape (type/subtype), at the first place it has one (a name has no slash).
const attachedLine = /^- ([^/]*?) \(([\w.+!#$&^-]+\/[\w.+!#$&^-]+(?:;.*?)?)(?:, \d+ bytes)?\): (?:in this prompt|a link, .*|.* \(a copy until this turn ends\))$/;

/** The name of a file in a line of the attached-files list; a line not read is given whole (without
 * its dash): the file is not lost from what is shown. */
export function attachedName(line: string): string {
  const m = attachedLine.exec(line);
  return m ? m[1] : line.replace(/^- /, "");
}

/** What the person wrote in a prompt acpost sent: the prompt without what the node put before it
 * (the files it lists, whose message it is and how to answer, the note on where the answer is
 * read, the person's knowledge). A session read back (History) has its prompts whole. Only the
 * node's own lines, whole and where it puts them, are taken off: any other text is as it is. */
const sharedHead = /^\[acpost #([0-9a-f]{8})\] This chat is shared with other agents: you see only your own turns of it\. Since your last turn here/;

export function ownBody(text: string): string {
  const files = attachedFiles.exec(text);
  let rest = files ? text.slice(files[0].length) : text;
  const head = messageHead.exec(rest);
  if (!head || !rest.startsWith(promptLines, head[0].length)) return text;
  rest = rest.slice(head[0].length + promptLines.length);
  for (const n of [readerNote, readerNoteBefore]) if (rest.startsWith(n)) { rest = rest.slice(n.length); break; }
  if (rest.startsWith(knowledgeHead)) {
    const end = rest.indexOf(knowledgeEnd, knowledgeHead.length - 2);
    if (end >= 0) rest = rest.slice(end + knowledgeEnd.length);
  }
  // What the chat's other agents said, told before the message (a shared chat): up to the node's
  // closing line, which carries the mark of its opening one (made after the message was written:
  // nothing told there, and nothing in the message, spells it).
  const shared = sharedHead.exec(rest);
  if (shared) {
    const close = `\n[acpost #${shared[1]}] That is what the others said. The message to you:\n\n`;
    const end = rest.indexOf(close);
    if (end >= 0) rest = rest.slice(end + close.length);
  }
  // The files that went with it are named (a message of a screenshot alone has no other text):
  // their content is not replayed.
  const names = files ? files[0].split("\n").filter(l => l.startsWith("- ")).map(attachedName) : [];
  if (names.length) rest = (rest.trim() ? rest.replace(/\s+$/, "") + "\n\n" : "") + names.map(n => "📎 " + n).join("\n");
  return rest;
}
