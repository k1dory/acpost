import { describe, it, expect } from "vitest";
import { parseUi, caps, ticks, sortRows, filterRows, numeric, choiceAnswer, formAnswer, keepFor, type Ui } from "./genui";

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
    const a = keepFor(0, "{}");
    a.x = 1;
    expect(keepFor(0, "{}")).toBe(a);
    expect(keepFor(1, "{}")).not.toBe(a);
    expect(keepFor(0, "{ }")).not.toBe(a);
  });
});
