// Where an agent's time went, turn by turn: each tool call, permission wait and message to another
// agent as a span on the turn's time axis, and the turn split into phases (waiting to start, the
// model working, tools, waiting for a person, other agents). Read from the timing of the turn's
// events — the node's short form of them (GET /v1/traces), or the events themselves.

import type { Ev } from "./turn";

/** An event's timing (internal/client TraceEvent). */
export type Mark = {
  seq: number; at: number; end?: number; kind: string;
  call?: string; request?: string; status?: string; tool?: string; title?: string; command?: string; exit?: number | null;
};

/** A turn's times and events (internal/client TraceMessage). */
export type TMsg = {
  id: string; thread: string; parent?: string; depth: number; from: string; to: string; state: string; error_code?: string;
  created_at: string; updated_at: string; finished_at?: string; events: Mark[]; cut?: boolean;
};

/** A conversation's turns (internal/client TraceThread). */
export type TThread = { thread: string; title: string; updated_at: string; messages: TMsg[]; cut?: boolean };

export type Phase = "queue" | "model" | "tools" | "approval" | "agents";
/** In the order a bar shows them. */
export const phaseOrder: Phase[] = ["queue", "model", "tools", "approval", "agents"];
export type Phases = Record<Phase, number>;
const noPhases = (): Phases => ({ queue: 0, model: 0, tools: 0, approval: 0, agents: 0 });

export type SpanKind = "tool" | "permission" | "agent";
export type SpanState = "running" | "done" | "failed" | "cancelled" | "unknown" | "waiting";
export type Span = {
  id: string;        // unique in the turn: "tool:<call>", "perm:<request>", "msg:<id>"
  kind: SpanKind;
  ref: string;       // the call, the request or the message
  name: string;      // what kind of thing (read, execute, the agent asked)
  label: string;     // what it was (its command, its title)
  start: number; end: number;
  state: SpanState;
  parent?: string;   // the span it ran under
  tool?: string; command?: string; exit?: number;
};

export type TurnTrace = {
  id: string;
  start: number;         // sent
  first: number | null;  // its first event: the agent began
  end: number;           // finished, or now
  running: boolean;
  spans: Span[];
  phases: Phases;
  calls: number;
  errors: number;
  active: number;        // from its first event to its end
  truncated: boolean;    // the node kept no more of its events (limits.max_events_per_message)
};

const finals = new Set(["done", "failed", "cancelled", "expired"]);
export const isFinal = (state: string) => finals.has(state);

/** A time the node gave, in milliseconds; none (or Go's zero time) is 0. */
export function ms(iso: string | undefined): number {
  const v = iso ? Date.parse(iso) : NaN;
  return isFinite(v) && v > 0 ? v : 0;
}

/** A message's events in the node's short form: a run of text or thinking is one mark; a call's
 * marks say its kind, title, status and command. What they carried is not kept. */
export function marksOf(evs: Ev[]): Mark[] {
  const out: Mark[] = [];
  for (const e of evs) {
    const at = Date.parse(e.time);
    if (!isFinite(at)) continue;
    let d: any;
    try { d = JSON.parse(e.data); } catch { d = null; }
    const tail = out[out.length - 1];
    switch (e.kind) {
      case "text": case "thought":
        if (tail?.kind === e.kind) tail.end = at;
        else out.push({ seq: e.seq, at, end: at, kind: e.kind });
        break;
      case "tool_call": case "tool_call_update":
        if (!d?.id) break;
        out.push({ seq: e.seq, at, kind: e.kind, call: String(d.id), status: str(d.status), tool: str(d.kind), title: str(d.title),
          command: commandOf(d.input), exit: typeof d.exit_code === "number" ? d.exit_code : undefined });
        break;
      case "permission_request":
        if (!d?.request_id) break;
        out.push({ seq: e.seq, at, kind: e.kind, request: String(d.request_id), call: str(d.tool_call_id), title: str(d.title), tool: str(d.tool_kind) });
        break;
      case "permission":
        if (!d?.request_id) break;
        out.push({ seq: e.seq, at, kind: e.kind, request: String(d.request_id), status: str(d.decision) });
        break;
      case "notice":
        out.push({ seq: e.seq, at, kind: e.kind, status: str(d?.severity) });
        break;
      case "truncated":
        out.push({ seq: e.seq, at, kind: e.kind });
        break;
    }
  }
  return out;
}

const str = (v: unknown) => (typeof v === "string" && v ? v : undefined);

function commandOf(input: any): string | undefined {
  const c = input && typeof input === "object" ? input.command : undefined;
  if (typeof c === "string") return c || undefined;
  if (Array.isArray(c)) return c.filter(x => typeof x === "string").join(" ") || undefined;
  return undefined;
}

const bareTitles = new Set(["terminal", "tool", "bash", "shell"]);

/** A call's turn on the time axis, a permission wait under the call it was for, and each message
 * the turn sent to another agent under the call that was running when it was sent. */
export function traceTurn(m: Pick<TMsg, "id" | "state" | "created_at" | "updated_at" | "finished_at">, marks: Mark[], children: TMsg[], now: number): TurnTrace {
  const running = !isFinal(m.state);
  const start = ms(m.created_at) || marks[0]?.at || now;
  const first = marks.length ? Math.min(...marks.map(x => x.at)) : null;
  const lastMark = marks.reduce((a, x) => Math.max(a, x.end ?? x.at), 0);
  const end0 = running ? now : (ms(m.finished_at) || ms(m.updated_at) || lastMark || start);
  const end = Math.max(end0, lastMark, start);

  // What an unfinished span came to: the turn's end ended it.
  const left: SpanState = running ? "running" : m.state === "done" ? "done" : m.state === "failed" ? "unknown" : "cancelled";

  const tools = new Map<string, Span & { ended?: number }>();
  const perms = new Map<string, Span & { ended?: number }>();
  for (const x of marks) {
    if ((x.kind === "tool_call" || x.kind === "tool_call_update") && x.call) {
      let s = tools.get(x.call);
      if (!s) {
        s = { id: "tool:" + x.call, kind: "tool", ref: x.call, name: "", label: "", start: x.at, end: x.at, state: left };
        tools.set(x.call, s);
      }
      if (x.tool) s.tool = x.tool;
      if (x.title) s.label = x.title; // the latest says most ("Read a.go" after "Read")
      if (x.command) s.command = x.command;
      if (typeof x.exit === "number") s.exit = x.exit;
      // It ended when it first said so: an update after (late output, the same status again,
      // which the node's short form leaves out) does not move its end; its exit code still counts.
      if ((x.status === "completed" || x.status === "failed") && s.ended === undefined) {
        s.ended = x.at;
        s.state = x.status === "failed" ? "failed" : "done";
      }
      if (typeof s.exit === "number" && s.exit !== 0) s.state = "failed";
    } else if (x.kind === "permission_request" && x.request) {
      perms.set(x.request, { id: "perm:" + x.request, kind: "permission", ref: x.request, name: "approval", label: x.title ?? "", start: x.at, end: x.at,
        state: running ? "waiting" : "unknown", parent: x.call ? "tool:" + x.call : undefined, tool: x.tool });
    } else if (x.kind === "permission" && x.request) {
      const p = perms.get(x.request);
      if (p) { p.ended = x.at; p.state = x.status && /^(allow|approve)/.test(x.status) ? "done" : "failed"; p.label ||= x.status ?? ""; }
    }
  }
  const spans: Span[] = [];
  for (const s of tools.values()) {
    const { ended, ...rest } = s;
    rest.end = ended ?? end;
    rest.name = (s.tool || s.label.split(/[\s:]/)[0] || "tool").toLowerCase();
    if (s.command && (!s.label || bareTitles.has(s.label.trim().toLowerCase()))) rest.label = s.command;
    rest.label ||= rest.name;
    spans.push(rest);
  }
  for (const p of perms.values()) {
    const { ended, ...rest } = p;
    rest.end = ended ?? end;
    if (rest.parent && !tools.has(rest.parent.slice(5))) rest.parent = undefined;
    spans.push(rest);
  }
  for (const c of children) {
    if (c.parent !== m.id) continue;
    const cs = ms(c.created_at) || start;
    const cRunning = !isFinal(c.state);
    const ce = cRunning ? Math.max(now, cs) : Math.max(ms(c.finished_at) || ms(c.updated_at) || cs, cs);
    // Sent from the call that ran then (the latest begun of those running).
    const under = spans.filter(s => s.kind === "tool" && s.start <= cs && s.end >= cs).sort((a, b) => b.start - a.start)[0];
    spans.push({ id: "msg:" + c.id, kind: "agent", ref: c.id, name: c.to.replace(/^agent:/, ""), label: c.to, start: cs, end: ce,
      state: cRunning ? "running" : c.state === "done" ? "done" : c.state === "failed" || c.state === "expired" ? "failed" : "cancelled", parent: under?.id });
  }
  spans.sort((a, b) => a.start - b.start || a.id.localeCompare(b.id));

  const phases = phasesOf(start, first, end, spans);
  const calls = spans.filter(s => s.kind === "tool").length;
  const errors = spans.filter(s => s.kind === "tool" && s.state === "failed").length + (m.state === "failed" || m.state === "expired" ? 1 : 0);
  return { id: m.id, start, first, end, running, spans, phases, calls, errors, active: first === null ? 0 : Math.max(0, end - first),
    truncated: marks.some(x => x.kind === "truncated") };
}

// Where spans overlap, the time is the one a person would blame: waiting for them first, then
// another agent's work, then a tool; the rest is the model's.
const precedence: Exclude<Phase, "queue" | "model">[] = ["approval", "agents", "tools"];
const phaseOf: Record<SpanKind, Exclude<Phase, "queue" | "model">> = { permission: "approval", agent: "agents", tool: "tools" };

/** The turn's time split into phases: before its first event, it waited (queued, starting); after,
 * each moment is the highest of what was going on. The phases sum to the turn's length. */
export function phasesOf(start: number, first: number | null, end: number, spans: Span[]): Phases {
  const out = noPhases();
  const begin = first === null ? end : Math.min(Math.max(first, start), end);
  out.queue = Math.max(0, begin - start);
  if (end <= begin) return out;
  const pts = new Set<number>([begin, end]);
  const iv: { a: number; b: number; p: Phase }[] = [];
  for (const s of spans) {
    const a = Math.max(s.start, begin), b = Math.min(s.end, end);
    if (b <= a) continue;
    iv.push({ a, b, p: phaseOf[s.kind] });
    pts.add(a); pts.add(b);
  }
  const xs = [...pts].sort((a, b) => a - b);
  for (let i = 0; i + 1 < xs.length; i++) {
    const a = xs[i], b = xs[i + 1], mid = (a + b) / 2;
    const on = new Set(iv.filter(v => v.a <= mid && v.b > mid).map(v => v.p));
    out[precedence.find(p => on.has(p)) ?? "model"] += b - a;
  }
  return out;
}

export function addPhases(a: Phases, b: Phases): Phases {
  const o = noPhases();
  for (const p of phaseOrder) o[p] = a[p] + b[p];
  return o;
}

/** The turns a conversation's own messages are (not those it asked), each traced. */
export function threadTurns(th: TThread, now: number): { msg: TMsg; trace: TurnTrace }[] {
  return th.messages.filter(m => m.thread === th.thread && m.to.startsWith("agent:"))
    .map(m => ({ msg: m, trace: traceTurn(m, m.events ?? [], th.messages, now) }));
}

export type ThreadSum = {
  thread: string; title: string; updated: number; turns: number; active: number; calls: number; errors: number;
  phases: Phases; running: boolean; cut: boolean;
};

/** A conversation summed: its turns, how long they were active, their calls and errors, and where
 * the time went. */
export function threadSum(th: TThread, now: number): ThreadSum {
  const turns = threadTurns(th, now);
  let phases = noPhases(), active = 0, calls = 0, errors = 0;
  for (const { trace } of turns) {
    phases = addPhases(phases, trace.phases);
    active += trace.active; calls += trace.calls; errors += trace.errors;
  }
  return { thread: th.thread, title: th.title, updated: ms(th.updated_at), turns: turns.length, active, calls, errors, phases,
    // A turn it asked may still run when its own are over: it is running until they all are.
    running: th.messages.some(m => !isFinal(m.state)), cut: threadCut(th) };
}

/** Whether what a conversation's numbers count is short: the node left turns or events out, or
 * kept no more of a turn's events — any turn it holds, its own or one it asked. */
export function threadCut(th: TThread): boolean {
  return !!th.cut || th.messages.some(m => m.cut || (m.events ?? []).some(e => e.kind === "truncated"));
}

export function sortSums(list: ThreadSum[], by: "recent" | "time"): ThreadSum[] {
  return [...list].sort(by === "time" ? (a, b) => b.active - a.active || b.updated - a.updated : (a, b) => b.updated - a.updated);
}

export type Row = Span & { depth: number };

/** The spans as rows: each under the one it ran under, in the order they began. A filter keeps a
 * span whose name or label has the text (and, hiding short ones, that lasted a second at least),
 * and what it ran under. */
export function rows(spans: Span[], filter: { text?: string; hideShort?: boolean } = {}): Row[] {
  const ids = new Set(spans.map(s => s.id));
  const kids = new Map<string, Span[]>();
  const top: Span[] = [];
  for (const s of spans) {
    if (s.parent && ids.has(s.parent) && s.parent !== s.id) (kids.get(s.parent) ?? kids.set(s.parent, []).get(s.parent)!).push(s);
    else top.push(s);
  }
  const q = (filter.text ?? "").trim().toLowerCase();
  const passes = (s: Span) => (!q || s.name.toLowerCase().includes(q) || s.label.toLowerCase().includes(q)) && (!filter.hideShort || s.end - s.start >= 1000);
  const out: Row[] = [];
  const walk = (s: Span, depth: number, seen: Set<string>): Row[] => {
    if (seen.has(s.id)) return [];
    seen.add(s.id);
    const under = (kids.get(s.id) ?? []).flatMap(k => walk(k, depth + 1, seen));
    return passes(s) || under.length ? [{ ...s, depth }, ...under] : [];
  };
  const seen = new Set<string>();
  for (const s of top) out.push(...walk(s, 0, seen));
  return out;
}

// Programs whose second word says what was done (git status, go test, npm run…).
const twoWords = new Set(["git", "go", "npm", "npx", "pnpm", "yarn", "bun", "cargo", "docker", "kubectl", "gh", "make", "pip", "pip3", "uv",
  "brew", "terraform", "helm", "systemctl", "apt", "apt-get", "swift", "dotnet", "mvn", "gradle", "poetry", "deno", "nix", "acpost", "wails3"]);
const skipWords = new Set(["sudo", "env", "time", "nohup", "command", "exec"]);
const setupWords = new Set(["cd", "export", "source", ".", "set", "pushd"]);

/** What a call is counted as: a shell command by its program (and, for a program of
 * subcommands, the subcommand); any other call by its kind. */
export function commandKey(s: Pick<Span, "command" | "name" | "tool">): string {
  if (!s.command) return s.name || s.tool || "tool";
  const parts = s.command.split(/&&|\|\||;|\|/).map(p => p.trim()).filter(Boolean);
  const words = (p: string) => p.split(/\s+/).filter(w => w && !/^[A-Za-z_][A-Za-z0-9_]*=/.test(w));
  let w = words(parts.find(p => !setupWords.has(words(p)[0] ?? "")) ?? parts[0] ?? "");
  while (w.length > 1 && skipWords.has(w[0])) w = w.slice(1);
  if (!w.length) return s.name || "execute";
  const prog = w[0].replace(/^.*\//, "").replace(/^["']|["']$/g, "");
  const sub = w[1];
  return twoWords.has(prog) && sub && /^[a-z][\w:.-]*$/.test(sub) ? `${prog} ${sub}` : prog;
}

export type CommandRow = { key: string; runs: number; total: number; avg: number; max: number; failures: number };

/** Calls grouped by what they ran: how many, how long in all and on average, the slowest, the
 * failed. The longest in all first. */
export function commandGroups(spans: Span[]): CommandRow[] {
  const by = new Map<string, CommandRow>();
  for (const s of spans) {
    if (s.kind !== "tool") continue;
    const k = commandKey(s);
    const r = by.get(k) ?? by.set(k, { key: k, runs: 0, total: 0, avg: 0, max: 0, failures: 0 }).get(k)!;
    const d = Math.max(0, s.end - s.start);
    r.runs++; r.total += d; r.max = Math.max(r.max, d);
    if (s.state === "failed") r.failures++;
  }
  for (const r of by.values()) r.avg = r.runs ? r.total / r.runs : 0;
  return [...by.values()].sort((a, b) => b.total - a.total || b.runs - a.runs || a.key.localeCompare(b.key));
}

/** Text shown in the detail pane, bounded. */
export function clip(s: string, n = 8000): string {
  return s.length > n ? s.slice(0, n) + `\n… (${s.length - n} more)` : s;
}

/** A value shown as text: a string as it is, anything else as JSON. */
export function shown(v: unknown, n = 8000): string {
  if (v === undefined || v === null || v === "") return "";
  if (typeof v === "string") return clip(v, n);
  try { return clip(JSON.stringify(v, null, 2), n); } catch { return ""; }
}

/** Where the axis has its ticks: about `count` of them, at round steps. */
export function ticks(length: number, count = 6): number[] {
  if (!(length > 0)) return [0];
  const steps = [100, 250, 500, 1000, 2000, 5000, 10_000, 15_000, 30_000, 60_000, 120_000, 300_000, 600_000, 900_000, 1_800_000, 3_600_000, 7_200_000];
  const step = steps.find(s => length / s <= count) ?? Math.ceil(length / count / 3_600_000) * 3_600_000;
  const out: number[] = [];
  for (let x = 0; x <= length; x += step) out.push(x);
  return out;
}
