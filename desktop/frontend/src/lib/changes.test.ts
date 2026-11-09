import { describe, it, expect } from "vitest";
import { changeRoots, mergeChanges, joinFiles } from "./changes";

describe("a chat's changes from its messages' (finding 6)", () => {
  it("the roots are the person's messages to agents, every one of them", () => {
    const msgs = [{ id: "a", depth: 0, to: "agent:x" }, { id: "b", depth: 1, to: "agent:y" }, { id: "c", depth: 0, to: "agent:x" }, { id: "d", depth: 0, to: "user:me" }];
    expect(changeRoots(msgs)).toEqual(["a", "c"]);
  });

  it("a repository two roots changed is listed once, its files summed, its diffs in order", () => {
    const f = (path: string, added: number) => ({ path, status: "M", added, removed: 0 });
    const got = mergeChanges([
      { turns: [{ msg: "a", agent: "x", start: "", at: "2026-01-01T00:00:02Z", repo: "/r", before: "t0", after: "t1" }], repos: [{ repo: "/r", before: "t0", after: "t1", files: [f("x.go", 1)], diff: "D1" }] },
      { turns: [{ msg: "c", agent: "x", start: "", at: "2026-01-01T00:00:01Z", repo: "/r", before: "t1", after: "t2" }], repos: [{ repo: "/r", before: "t1", after: "t2", files: [f("x.go", 2), f("y.go", 1)], diff: "D2" }, { repo: "/s", before: "u0", after: "u1", diff: "S" }] },
    ]);
    expect(got.turns.map(t => t.msg)).toEqual(["c", "a"]);
    expect(got.repos.map(r => r.repo)).toEqual(["/r", "/s"]);
    expect(got.repos[0]).toMatchObject({ before: "t0", after: "t2", diff: "D1\nD2", parts: 2 });
    expect(got.repos[0].files).toEqual([f("x.go", 3), f("y.go", 1)]);
    expect(got.repos[1].diff).toBe("S");
  });

  it("one file's diffs that came apart are one entry", () => {
    const got = joinFiles([{ repo: "/r", path: "a", lines: [1] }, { repo: "/r", path: "b", lines: [2] }, { repo: "/r", path: "a", lines: [3] }]);
    expect(got).toEqual([{ repo: "/r", path: "a", lines: [1, 3] }, { repo: "/r", path: "b", lines: [2] }]);
  });
});
