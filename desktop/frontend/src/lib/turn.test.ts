import { describe, it, expect } from "vitest";
import { Turn, toolState, type Tool } from "./turn";

const tool = (o: Partial<Tool> = {}): Tool => ({ id: "t", started: 0, ...o });

describe("toolState (Y22)", () => {
  it("an unfinished call of a cancelled turn is cancelled, not done", () => expect(toolState(tool(), "cancelled")).toBe("cancelled"));
  it("an unfinished call of an expired turn is expired", () => expect(toolState(tool(), "expired")).toBe("expired"));
  it("an unfinished call of a failed turn has an unknown result", () => expect(toolState(tool(), "failed")).toBe("unknown"));
  it("an unfinished call of a turn done ended with it", () => expect(toolState(tool(), "done")).toBe("done"));
  it("a call of a running turn runs or waits", () => {
    expect(toolState(tool(), "running")).toBe("running");
    expect(toolState(tool({ status: "pending" }), "running")).toBe("pending");
  });
  it("a call's own end wins", () => {
    expect(toolState(tool({ status: "completed" }), "cancelled")).toBe("done");
    expect(toolState(tool({ exit_code: 2 }), "done")).toBe("failed");
  });
});

describe("Turn", () => {
  it("skips events already seen (a stream asked again repeats them)", () => {
    const t = new Turn();
    t.add([{ seq: 1, time: "", kind: "text", data: '"a"' }, { seq: 2, time: "", kind: "text", data: '"b"' }]);
    t.add([{ seq: 2, time: "", kind: "text", data: '"b"' }, { seq: 3, time: "", kind: "text", data: '"c"' }]);
    expect(t.blocks).toEqual([{ t: "text", text: "abc" }]);
  });
});

describe("Turn with events it cannot use (P3)", () => {
  const ev = (seq: number, kind: string, data: any) => ({ seq, time: "", kind, data: JSON.stringify(data) });
  it("a plan with a null entry does not throw nor lose the rest of the batch", () => {
    const t = new Turn();
    expect(() => t.add([ev(1, "plan", [null, "pending: write it"]), ev(2, "text", "after")])).not.toThrow();
    expect(t.plan).toEqual([{ status: "pending", content: "write it" }]);
    expect(t.blocks.at(-1)).toEqual({ t: "text", text: "after" });
    expect(t.last).toBe(2);
  });
  it("notes a gap in the events and an update of a call whose start was not seen", () => {
    const t = new Turn();
    t.add([ev(1, "text", "a"), ev(4, "tool_call_update", { id: "c1", status: "completed" })]);
    expect(t.problems.length).toBe(2);
    expect(t.problems[0]).toContain("2–3");
    expect(t.problems[1]).toContain("c1");
  });
  it("shows that the node kept no more events", () => {
    const t = new Turn().add([ev(1, "truncated", "later events were not kept (limits.max_events_per_message)")]);
    expect(t.blocks[0]).toMatchObject({ t: "notice", severity: "warning" });
  });
});
