// Components in an agent's answer: a ```ui block holds one JSON object {"type": …} (or an array of
// them) that the app draws as a chart, a table, a plan, or a question the person answers. The block
// is the agent's: it is read as data, checked here field by field, and only what is checked is kept
// (every text is shown as text, nothing is loaded, nothing runs). Anything else, or a block still
// being written, is shown as the code it is.

export type Status = "done" | "doing" | "todo" | "failed";
export type Cell = string | number | null;

export type Stats = { type: "stats"; title?: string; items: { label: string; value: string | number; delta?: string | number }[] };
export type Chart = { type: "chart"; title?: string; kind: "bar" | "line" | "area" | "pie"; labels: string[]; series: { name: string; values: number[] }[]; unit?: string };
export type Table = { type: "table"; title?: string; columns: string[]; rows: Cell[][] };
export type Timeline = { type: "timeline"; title?: string; items: { time: string; title: string; text?: string; status?: Status }[] };
export type Kanban = { type: "kanban"; title?: string; columns: { title: string; cards: { title: string; text?: string; tag?: string }[] }[] };
export type Steps = { type: "steps"; title?: string; items: { title: string; status: Status; text?: string }[] };
export type Choices = { type: "choices"; title?: string; question: string; options: string[]; multiple?: boolean };
export type Field = { label: string; type: "text" | "number" | "select" | "checkbox"; options?: string[]; value?: string | number | boolean; placeholder?: string };
export type Form = { type: "form"; title?: string; fields: Field[]; submit?: string };
export type Ui = Stats | Chart | Table | Timeline | Kanban | Steps | Choices | Form;

/** The limits a block is held to: what a reply can sensibly show, and what stays quick to draw. */
export const caps = {
  source: 50_000, components: 8, number: 1e15, title: 120, label: 80, text: 500, cell: 300,
  stats: 12, labels: 60, series: 8, columns: 12, rows: 500, items: 50, kanbanColumns: 8, cards: 40,
  options: 12, fields: 10, selectOptions: 30,
};

class Bad extends Error {}
const fail = (where: string, what: string): never => { throw new Bad(`${where}: ${what}`); };

type Obj = Record<string, unknown>;
function obj(v: unknown, where: string): Obj {
  if (typeof v !== "object" || v === null || Array.isArray(v)) fail(where, "not an object");
  return v as Obj;
}
function str(v: unknown, where: string, max: number): string {
  if (typeof v !== "string") return fail(where, "not a string");
  if (v.length > max) fail(where, `longer than ${max}`);
  return v;
}
function optStr(v: unknown, where: string, max: number): string | undefined {
  return v === undefined ? undefined : str(v, where, max);
}
// A number a chart can scale: JSON reads 1e400 as Infinity, and one near the largest double has
// no room above it for an axis.
function num(v: unknown, where: string): number {
  if (typeof v !== "number" || !Number.isFinite(v)) fail(where, "not a number");
  if (Math.abs(v as number) > caps.number) fail(where, `beyond ±${caps.number}`);
  return v as number;
}
function strOrNum(v: unknown, where: string, max: number): string | number {
  return typeof v === "number" ? num(v, where) : str(v, where, max);
}
function list(v: unknown, where: string, min: number, max: number): unknown[] {
  if (!Array.isArray(v)) return fail(where, "not a list");
  if (v.length < min) fail(where, `fewer than ${min}`);
  if (v.length > max) fail(where, `more than ${max}`);
  return v;
}
function oneOf<T extends string>(v: unknown, where: string, allowed: readonly T[]): T {
  if (!allowed.includes(v as T)) fail(where, `not one of ${allowed.join(", ")}`);
  return v as T;
}
const statuses = ["done", "doing", "todo", "failed"] as const;
const optStatus = (v: unknown, where: string) => v === undefined ? undefined : oneOf(v, where, statuses);

/** Only the fields that are set: an object kept is no bigger than what was checked. */
function defined<T extends Obj>(o: T): T {
  for (const k of Object.keys(o)) if (o[k] === undefined) delete o[k];
  return o;
}

function component(v: unknown, where: string): Ui {
  const o = obj(v, where);
  const type = str(o.type, where + ".type", 20);
  const title = optStr(o.title, where + ".title", caps.title);
  switch (type) {
    case "stats":
      return defined({ type, title, items: list(o.items, where + ".items", 1, caps.stats).map((it, i) => {
        const w = `${where}.items[${i}]`, x = obj(it, w);
        return defined({ label: str(x.label, w + ".label", caps.label), value: strOrNum(x.value, w + ".value", caps.label),
          delta: x.delta === undefined ? undefined : strOrNum(x.delta, w + ".delta", 30) });
      }) });
    case "chart": {
      const kind = oneOf(o.kind, where + ".kind", ["bar", "line", "area", "pie"] as const);
      const labels = list(o.labels, where + ".labels", 1, caps.labels).map((l, i) => str(l, `${where}.labels[${i}]`, caps.label));
      const series = list(o.series, where + ".series", 1, kind === "pie" ? 1 : caps.series).map((s, i) => {
        const w = `${where}.series[${i}]`, x = obj(s, w);
        const values = list(x.values, w + ".values", labels.length, labels.length).map((n, j) => num(n, `${w}.values[${j}]`));
        if (kind === "pie" && (values.some(n => n < 0) || !values.some(n => n > 0))) fail(w + ".values", "a pie needs values of 0 or more, not all 0");
        return { name: str(x.name ?? "", w + ".name", caps.label), values };
      });
      return defined({ type, title, kind, labels, series, unit: optStr(o.unit, where + ".unit", 12) });
    }
    case "table": {
      const columns = list(o.columns, where + ".columns", 1, caps.columns).map((c, i) => str(c, `${where}.columns[${i}]`, caps.label));
      const rows = list(o.rows, where + ".rows", 0, caps.rows).map((r, i) =>
        list(r, `${where}.rows[${i}]`, columns.length, columns.length).map((c, j): Cell =>
          c === null ? null : typeof c === "boolean" ? String(c) : strOrNum(c, `${where}.rows[${i}][${j}]`, caps.cell)));
      return defined({ type, title, columns, rows });
    }
    case "timeline":
      return defined({ type, title, items: list(o.items, where + ".items", 1, caps.items).map((it, i) => {
        const w = `${where}.items[${i}]`, x = obj(it, w);
        return defined({ time: str(x.time, w + ".time", 40), title: str(x.title, w + ".title", caps.title),
          text: optStr(x.text, w + ".text", caps.text), status: optStatus(x.status, w + ".status") });
      }) });
    case "kanban":
      return defined({ type, title, columns: list(o.columns, where + ".columns", 1, caps.kanbanColumns).map((c, i) => {
        const w = `${where}.columns[${i}]`, x = obj(c, w);
        return { title: str(x.title, w + ".title", caps.label), cards: list(x.cards ?? [], w + ".cards", 0, caps.cards).map((k, j) => {
          const v = `${w}.cards[${j}]`, y = typeof k === "string" ? { title: k } : obj(k, v);
          return defined({ title: str(y.title, v + ".title", caps.title), text: optStr(y.text, v + ".text", caps.text), tag: optStr(y.tag, v + ".tag", 30) });
        }) };
      }) });
    case "steps":
      return defined({ type, title, items: list(o.items, where + ".items", 1, caps.items).map((it, i) => {
        const w = `${where}.items[${i}]`, x = typeof it === "string" ? { title: it, status: "todo" } : obj(it, w);
        return defined({ title: str(x.title, w + ".title", caps.title), status: oneOf(x.status ?? "todo", w + ".status", statuses),
          text: optStr(x.text, w + ".text", caps.text) });
      }) });
    case "choices": {
      if (o.multiple !== undefined && typeof o.multiple !== "boolean") fail(where + ".multiple", "not true or false");
      const options = list(o.options, where + ".options", 2, caps.options).map((s, i) => str(s, `${where}.options[${i}]`, caps.title));
      if (new Set(options).size !== options.length) fail(where + ".options", "the same option twice");
      return defined({ type, title, question: str(o.question, where + ".question", caps.text), options, multiple: o.multiple as boolean | undefined });
    }
    case "form":
      return defined({ type, title, submit: optStr(o.submit, where + ".submit", 40), fields: list(o.fields, where + ".fields", 1, caps.fields).map((f, i) => {
        const w = `${where}.fields[${i}]`, x = obj(f, w);
        const kind = oneOf(x.type ?? "text", w + ".type", ["text", "number", "select", "checkbox"] as const);
        const options = kind === "select" ? list(x.options, w + ".options", 1, caps.selectOptions).map((s, j) => str(s, `${w}.options[${j}]`, caps.title)) : undefined;
        let value: string | number | boolean | undefined;
        if (x.value !== undefined) {
          if (kind === "checkbox") { if (typeof x.value !== "boolean") fail(w + ".value", "not true or false"); value = x.value as boolean; }
          else if (kind === "number") value = num(x.value, w + ".value");
          else value = str(x.value, w + ".value", caps.text);
          if (kind === "select" && !options!.includes(value as string)) fail(w + ".value", "not one of its options");
        }
        return defined({ label: str(x.label, w + ".label", caps.label), type: kind, options, value, placeholder: optStr(x.placeholder, w + ".placeholder", caps.label) });
      }) });
    default:
      return fail(where + ".type", `unknown "${type.slice(0, 20)}"`);
  }
}

export type Parsed = { ok: true; items: Ui[] } | { ok: false; why: string };

/** A ```ui block's text, read: its components, or why it is not one (shown as code then). */
export function parseUi(src: string): Parsed {
  if (src.length > caps.source) return { ok: false, why: "too long" };
  let v: unknown;
  try { v = JSON.parse(src); } catch { return { ok: false, why: "not JSON (yet)" }; }
  try {
    const items = Array.isArray(v) ? list(v, "ui", 1, caps.components).map((c, i) => component(c, `ui[${i}]`)) : [component(v, "ui")];
    return { ok: true, items };
  } catch (e) {
    if (e instanceof Bad) return { ok: false, why: e.message };
    return { ok: false, why: "unreadable" };
  }
}

/** A number as a reply shows it: grouped, with no more decimals than it needs. */
export function fmt(n: number): string {
  return n.toLocaleString(undefined, { maximumFractionDigits: Math.abs(n) < 10 ? 2 : 1 });
}

/** The scale of a chart's value axis: from min (or 0) to max (or 0), in about four round steps;
 * at most 11 ticks whatever the numbers (a span too small for round steps has its two ends). */
export function ticks(min: number, max: number): number[] {
  const lo = Math.min(0, min), hi = Math.max(0, max) > lo ? Math.max(0, max) : lo + 1;
  const raw = (hi - lo) / 4, mag = 10 ** Math.floor(Math.log10(raw));
  const step = [1, 2, 2.5, 5, 10].map(m => m * mag).find(s => s >= raw);
  if (step && Number.isFinite(step) && step > 0) {
    const a = Math.floor(lo / step), b = Math.ceil(hi / step);
    if (Number.isFinite(a * step) && Number.isFinite(b * step) && b - a >= 1 && b - a <= 10)
      return Array.from({ length: b - a + 1 }, (_, i) => (a + i) * step);
  }
  return [lo, hi];
}

/** A table's rows ordered by a column: numbers as numbers, text as the person reads it; empty
 * cells last whichever way. */
export function sortRows(rows: Cell[][], col: number, dir: 1 | -1): Cell[][] {
  return rows.map((r, i) => ({ r, i })).sort((a, b) => {
    const x = a.r[col], y = b.r[col];
    if (x === null || x === "") return y === null || y === "" ? a.i - b.i : 1;
    if (y === null || y === "") return -1;
    const c = typeof x === "number" && typeof y === "number" ? x - y : String(x).localeCompare(String(y), undefined, { numeric: true });
    return c ? c * dir : a.i - b.i;
  }).map(e => e.r);
}

/** The rows with a cell that has the text (any case); all of them for no text. */
export function filterRows(rows: Cell[][], q: string): Cell[][] {
  q = q.trim().toLowerCase();
  return q ? rows.filter(r => r.some(c => c !== null && String(c).toLowerCase().includes(q))) : rows;
}

/** Whether a table's column holds numbers (right-aligned, sorted as numbers). */
export const numeric = (rows: Cell[][], col: number) => rows.some(r => typeof r[col] === "number") && rows.every(r => r[col] === null || typeof r[col] === "number");

/** The answer to a question, as the person's next message says it. */
export function choiceAnswer(c: Choices, picked: string[]): string {
  return `> ${c.question.replace(/\n/g, "\n> ")}\n\n${picked.join(", ")}\n\n`;
}

/** A form's answers, one line a field, as the person's next message says them. */
export function formAnswer(f: Form, values: (string | number | boolean)[], yes: string, no: string): string {
  const head = f.title ? `> ${f.title.replace(/\n/g, "\n> ")}\n\n` : "";
  return head + f.fields.map((x, i) => {
    const v = values[i];
    return `- ${x.label}: ${typeof v === "boolean" ? (v ? yes : no) : String(v ?? "")}`;
  }).join("\n") + "\n\n";
}

// What a person did to a block (a sorted table, a ticked choice), kept while the app runs: the same
// block drawn again (the reply re-rendered as it streams, the chat opened again) is as they left it.
const kept = new Map<string, Record<string, unknown>>();
/** The state of the n-th ui block with this text: the same object each time it is asked for. */
export function keepFor(n: number, src: string): Record<string, unknown> {
  const key = n + "\u0000" + src;
  let k = kept.get(key);
  if (!k) {
    if (kept.size >= 300) kept.delete(kept.keys().next().value!); // the oldest first
    kept.set(key, k = {});
  }
  return k;
}
