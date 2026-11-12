import { describe, it, expect } from "vitest";
import { parseUi, caps, ticks, fenceClosed, sortRows, filterRows, numeric, choiceAnswer, formAnswer, keepFor, type Ui } from "./genui";

const ok = (v: unknown): Ui[] => {
  const p = parseUi(JSON.stringify(v));
  if (!p.ok) throw new Error(p.why);
  return p.items;
};
const why = (v: unknown) => {
  const p = parseUi(typeof v === "string" ? v : JSON.stringify(v));
  return p.ok ? "" : p.why;
};

describe("a ui block, checked", () => {
  it("reads each component from a sample", () => {
    expect(ok({ type: "stats", items: [{ label: "Users", value: 1200, delta: "+4%" }] })[0]).toEqual({ type: "stats", items: [{ label: "Users", value: 1200, delta: "+4%" }] });
    expect(ok({ type: "chart", kind: "line", labels: ["a", "b"], series: [{ name: "x", values: [1, 2] }] })[0]).toMatchObject({ kind: "line" });
    expect(ok({ type: "table", columns: ["a", "b"], rows: [[1, "x"], [null, true]] })[0]).toEqual({ type: "table", columns: ["a", "b"], rows: [[1, "x"], [null, "true"]] });
    expect(ok({ type: "timeline", items: [{ time: "10:00", title: "deploy", status: "done" }] })).toHaveLength(1);
    expect(ok({ type: "kanban", columns: [{ title: "To do", cards: ["a", { title: "b", tag: "bug" }] }] })[0]).toMatchObject({ columns: [{ cards: [{ title: "a" }, { title: "b", tag: "bug" }] }] });
    expect(ok({ type: "steps", items: ["plan", { title: "build", status: "doing" }] })[0]).toMatchObject({ items: [{ status: "todo" }, { status: "doing" }] });
    expect(ok({ type: "choices", question: "Which?", options: ["A", "B"], multiple: true })[0]).toMatchObject({ multiple: true });
    expect(ok({ type: "form", fields: [{ label: "Name" }, { label: "Size", type: "select", options: ["S", "M"], value: "M" }, { label: "Go", type: "checkbox", value: true }] })[0])
      .toMatchObject({ fields: [{ type: "text" }, { type: "select" }, { type: "checkbox", value: true }] });
    expect(ok([{ type: "stats", items: [{ label: "a", value: 1 }] }, { type: "steps", items: ["x"] }])).toHaveLength(2);
  });

  it("keeps only the fields it checked", () => {
    const [s] = ok({ type: "stats", onclick: "alert(1)", html: "<img src=x>", items: [{ label: "a", value: 1, style: "x" }] });
    expect(s).toEqual({ type: "stats", items: [{ label: "a", value: 1 }] });
  });

  it("refuses what is not one, saying why", () => {
    expect(why("")).toMatch(/JSON/);
    expect(why('{"type":"stats","items":[{"label":"a"')).toMatch(/JSON/); // still being written
    expect(why({ type: "video", src: "https://x" })).toMatch(/unknown/);
    expect(why({ items: [] })).toMatch(/type/);
    expect(why({ type: "stats", items: [] })).toMatch(/fewer/);
    expect(why({ type: "stats", items: [{ label: 1, value: 1 }] })).toMatch(/label/);
    expect(why({ type: "chart", kind: "radar", labels: ["a"], series: [{ name: "x", values: [1] }] })).toMatch(/kind/);
    expect(why({ type: "chart", kind: "bar", labels: ["a", "b"], series: [{ name: "x", values: [1] }] })).toMatch(/values/);
    expect(why({ type: "chart", kind: "pie", labels: ["a"], series: [{ name: "x", values: [0] }] })).toMatch(/pie/);
    expect(why({ type: "chart", kind: "pie", labels: ["a"], series: [{ name: "x", values: [1] }, { name: "y", values: [1] }] })).toMatch(/more than 1/);
    expect(why({ type: "table", columns: ["a", "b"], rows: [[1]] })).toMatch(/rows\[0\]/);
    expect(why({ type: "table", columns: ["a"], rows: [[{ a: 1 }]] })).toMatch(/rows\[0\]\[0\]/);
    expect(why({ type: "steps", items: [{ title: "x", status: "maybe" }] })).toMatch(/status/);
    expect(why({ type: "choices", question: "q", options: ["a"] })).toMatch(/fewer/);
    expect(why({ type: "choices", question: "q", options: ["a", "a"] })).toMatch(/twice/);
    expect(why({ type: "form", fields: [{ label: "x", type: "select", options: ["a"], value: "b" }] })).toMatch(/options/);
    expect(why({ type: "form", fields: [{ label: "x", type: "file" }] })).toMatch(/type/);
    expect(why([])).toMatch(/fewer/);
    expect(why(null)).toMatch(/object/);
  });

  it("holds a block to its limits", () => {
    expect(why({ type: "stats", title: "x".repeat(caps.title + 1), items: [{ label: "a", value: 1 }] })).toMatch(/longer/);
    expect(why({ type: "table", columns: ["a"], rows: Array.from({ length: caps.rows + 1 }, () => [1]) })).toMatch(/more than/);
    expect(why(Array.from({ length: caps.components + 1 }, () => ({ type: "steps", items: ["x"] })))).toMatch(/more than/);
    expect(why(" ".repeat(caps.source + 1))).toBe("too long");
    expect(why({ type: "chart", kind: "bar", labels: ["a"], series: [{ name: "x", values: [1e400] }] })).toBeTruthy(); // Infinity is no JSON number
  });
});

describe("what the components compute in the page", () => {
  it("puts a chart's axis on round steps from zero", () => {
    expect(ticks(3, 97)).toEqual([0, 25, 50, 75, 100]);
    expect(ticks(-12, 30)).toEqual([-20, 0, 20, 40]);
    expect(ticks(-8, 30)).toEqual([-10, 0, 10, 20, 30]);
    expect(ticks(0, 0)).toEqual([0, 0.25, 0.5, 0.75, 1]);
  });
  it("sorts a table's rows by a column, empty cells last", () => {
    const rows = [["b", 10], ["a", 2], ["c", null], ["a10", 33], ["a9", 1]];
    expect(sortRows(rows, 1, 1).map(r => r[1])).toEqual([1, 2, 10, 33, null]);
    expect(sortRows(rows, 1, -1).map(r => r[1])).toEqual([33, 10, 2, 1, null]);
    expect(sortRows(rows, 0, 1).map(r => r[0])).toEqual(["a", "a9", "a10", "b", "c"]);
    expect(filterRows(rows, " A1 ").map(r => r[0])).toEqual(["a10"]);
    expect(filterRows(rows, "")).toBe(rows);
    expect(numeric(rows, 1)).toBe(true);
    expect(numeric(rows, 0)).toBe(false);
  });
  it("says an answer as the person's message would", () => {
    const c = { type: "choices" as const, question: "Which DB?\nPick one", options: ["Postgres", "SQLite"] };
    expect(choiceAnswer(c, ["SQLite"])).toBe("> Which DB?\n> Pick one\n\nSQLite\n\n");
    const f = { type: "form" as const, title: "Deploy", fields: [{ label: "Host", type: "text" as const }, { label: "Now", type: "checkbox" as const }] };
    expect(formAnswer(f, ["web1", true], "yes", "no")).toBe("> Deploy\n\n- Host: web1\n- Now: yes\n\n");
  });
  it("keeps a block's state for the same block only", () => {
    const a = keepFor("msg_1", 0, "{}");
    a.x = 1;
    expect(keepFor("msg_1", 0, "{}")).toBe(a);
    expect(keepFor("msg_1", 1, "{}")).not.toBe(a);
    expect(keepFor("msg_1", 0, "{ }")).not.toBe(a);
    expect(keepFor("msg_2", 0, "{}")).not.toBe(a); // the same block in another message
  });
});

describe("review round 1", () => {
  it("refuses numbers a chart cannot scale, and draws an axis of a few ticks for any it takes (P1)", () => {
    const chart = (values: number[]) => ({ type: "chart", kind: "bar", labels: values.map((_, i) => "l" + i), series: [{ name: "x", values }] });
    expect(why(chart([1.7e308]))).toMatch(/beyond/);
    expect(why(chart([-1.7e308]))).toMatch(/beyond/);
    expect(why(chart([1, 1e15 + 1e3]))).toMatch(/beyond/);
    expect(why({ type: "stats", items: [{ label: "a", value: 1e300 }] })).toMatch(/beyond/);
    expect(ok(chart([5e-324]))).toHaveLength(1);
    expect(ok(chart([5e-324, 1e15, -1e15, 0]))).toHaveLength(1);
    // The scale for any finite numbers at all: a few ticks, every one finite (no loop to run away).
    const extremes = [0, 1, -1, 5e-324, -5e-324, 1e-300, 1e15, -1e15, 1.7e308, -1.7e308, Number.MAX_VALUE, -Number.MAX_VALUE];
    for (const lo of extremes) for (const hi of extremes) {
      const tk = ticks(Math.min(lo, hi), Math.max(lo, hi));
      expect(tk.length, `${lo}..${hi}`).toBeGreaterThanOrEqual(2);
      expect(tk.length, `${lo}..${hi}`).toBeLessThanOrEqual(11);
      expect(tk.every(Number.isFinite), `${lo}..${hi}: ${tk}`).toBe(true);
      expect(tk[tk.length - 1] > tk[0], `${lo}..${hi}`).toBe(true);
    }
  });

  it("knows a fenced block whose closing fence has not come (P2)", () => {
    expect(fenceClosed("```ui\n{}\n```")).toBe(true);
    expect(fenceClosed("```ui\n{}\n```\n\n")).toBe(true);
    expect(fenceClosed("````ui\n{}\n`````")).toBe(true);
    expect(fenceClosed("~~~ui\n{}\n~~~")).toBe(true);
    expect(fenceClosed("```ui\n{}")).toBe(false);
    expect(fenceClosed("```ui\n{}\n")).toBe(false);
    expect(fenceClosed("```ui")).toBe(false);
    expect(fenceClosed("````ui\n{}\n```")).toBe(false); // shorter than the opening
    expect(fenceClosed("```ui\n{}\n~~~")).toBe(false);
  });
});

describe("review round 1", () => {
  it("refuses numbers a chart cannot scale, and draws an axis of a few ticks for any it takes (P1)", () => {
    const chart = (values: number[]) => ({ type: "chart", kind: "bar", labels: values.map((_, i) => "l" + i), series: [{ name: "x", values }] });
    expect(why(chart([1.7e308]))).toMatch(/beyond/);
    expect(why(chart([-1.7e308]))).toMatch(/beyond/);
    expect(why(chart([1, 1e15 + 1e3]))).toMatch(/beyond/);
    expect(why({ type: "stats", items: [{ label: "a", value: 1e300 }] })).toMatch(/beyond/);
    expect(ok(chart([5e-324]))).toHaveLength(1);
    expect(ok(chart([5e-324, 1e15, -1e15, 0]))).toHaveLength(1);
    // The scale for any finite numbers at all: a few ticks, every one finite (no loop to run away).
    const extremes = [0, 1, -1, 5e-324, -5e-324, 1e-300, 1e15, -1e15, 1.7e308, -1.7e308, Number.MAX_VALUE, -Number.MAX_VALUE];
    for (const lo of extremes) for (const hi of extremes) {
      const tk = ticks(Math.min(lo, hi), Math.max(lo, hi));
      expect(tk.length, `${lo}..${hi}`).toBeGreaterThanOrEqual(2);
      expect(tk.length, `${lo}..${hi}`).toBeLessThanOrEqual(11);
      expect(tk.every(Number.isFinite), `${lo}..${hi}: ${tk}`).toBe(true);
      expect(tk[tk.length - 1] > tk[0], `${lo}..${hi}`).toBe(true);
    }
  });

});

describe("review round 2", () => {
  it("formats numbers with their significant digits (P2)", async () => {
    const { fmt, axisLabel } = await import("./genui");
    expect([0, 0.0001, 0.000123456789, -0.0042, 1.5, 12.5, 1234.5678, 123456.789, 1e14, 1e15, -2.5e-7, 5e-324].map(fmt))
      .toEqual(["0", "0.0001", "0.000123457", "-0.0042", "1.5", "12.5", "1,234.57", "123,457", "100,000,000,000,000", "1e15", "-2.5e-7", "4.94066e-324"]);
    expect([0, 0.25, 0.5].map(v => axisLabel(v, 0.25))).toEqual(["0", "0.25", "0.50"]);
    expect([0, 20, 40].map(v => axisLabel(v, 20))).toEqual(["0", "20", "40"]);
    expect([5e-5, 1e-4].map(v => axisLabel(v, 5e-5))).toEqual(["5e-5", "1e-4"]);
    expect(axisLabel(1, 0)).toBe("1");
  });
});
