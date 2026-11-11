import { describe, it, expect } from "vitest";
import { editsToReveal, revealOf } from "./ide";

const now = Date.parse("2026-11-11T12:00:00Z");
const ev = (seq: number, kind: string, d: any, ago = 1000) => ({ seq, kind, time: new Date(now - ago).toISOString(), data: JSON.stringify(d) });
const edit = (path: string, o = "a", n = "b") => ({ path, added: 1, removed: 1, old: o, new: n });

describe("what the IDE is asked to show of a turn's events", () => {
  it("a call's edits, once it completed, once", () => {
    const seen = new Set<string>();
    expect(editsToReveal("m", [ev(1, "tool_call", { id: "c1", status: "in_progress", edits: [edit("/w/a")] })], seen, now)).toEqual([]);
    expect(editsToReveal("m", [ev(2, "tool_call_update", { id: "c1", status: "completed", edits: [edit("/w/a")] })], seen, now)).toEqual([{ path: "/w/a", old: "a", new: "b" }]);
    expect(editsToReveal("m", [ev(3, "tool_call_update", { id: "c1", status: "completed", edits: [edit("/w/a")] })], seen, now)).toEqual([]);
    // Another message's call of the same id is its own.
    expect(editsToReveal("m2", [ev(1, "tool_call", { id: "c1", status: "completed", edits: [edit("/w/z")] })], seen, now)).toHaveLength(1);
  });
  it("not a failed call, a call without edits, or one that completed a while ago (a chat opened on a running turn)", () => {
    const seen = new Set<string>();
    expect(editsToReveal("m", [
      ev(1, "tool_call", { id: "f", status: "failed", edits: [edit("/w/a")] }),
      ev(2, "tool_call", { id: "r", status: "completed" }),
      ev(3, "tool_call", { id: "old", status: "completed", edits: [edit("/w/a")] }, 60_000),
      ev(4, "text", { text: "hi" }),
      { seq: 5, kind: "tool_call", time: "", data: "{" },
    ], seen, now)).toEqual([]);
    // The old one is not shown later either.
    expect(editsToReveal("m", [ev(6, "tool_call_update", { id: "old", status: "completed", edits: [edit("/w/a")] })], seen, now)).toEqual([]);
  });
  it("a file's last edit, three files a call at most; a cut edit without its text", () => {
    const seen = new Set<string>();
    const got = editsToReveal("m", [ev(1, "tool_call", { id: "c", status: "completed", edits: [edit("/1", "a", "b"), edit("/1", "b", "c"), edit("/2"), edit("/3"), edit("/4")] })], seen, now);
    expect(got.map(r => r.path)).toEqual(["/1", "/2", "/3"]);
    expect(got[0]).toEqual({ path: "/1", old: "b", new: "c" });
    expect(revealOf({ path: "/big", added: 9, removed: 0, old: "x", new: "y", cut: true })).toEqual({ path: "/big", old: "", new: "" });
  });
});
