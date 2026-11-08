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
