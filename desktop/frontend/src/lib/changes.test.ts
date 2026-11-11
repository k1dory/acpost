import { describe, it, expect } from "vitest";
import { changeRoots, mergeChanges, joinFiles } from "./changes";

describe("a chat's changes from its messages' (finding 6)", () => {
  it("the roots are the person's messages to agents, every one of them (a child is its parent's)", () => {
    const msgs = [{ id: "a", to: "agent:x" }, { id: "b", parent: "a", to: "agent:y" }, { id: "c", to: "agent:x" }, { id: "d", to: "user:me" }];
    expect(changeRoots(msgs)).toEqual(["a", "c"]);
  });

  it("a task delegated through the hub (depth 1, its parent on another node) is a root here", () => {
    expect(changeRoots([{ id: "child", parent: "on-another-node", to: "agent:x" }])).toEqual(["child"]);
  });

  it("no root among the messages: the focused one, as before", () => {
    expect(changeRoots([], "m9")).toEqual(["m9"]);
    expect(changeRoots([{ id: "r", to: "user:me" }], "r")).toEqual(["r"]);
    expect(changeRoots([], "")).toEqual([]);
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

import { laterStatus } from "./changes";
import { headerPath, splitDiff } from "./turn";

describe("review round 5: what several answers say of one file", () => {
  const part = (files: any[], diff = "") => ({ turns: [], repos: [{ repo: "/r", before: "a", after: "b", files, diff }] });
  it("a binary file stays binary when two messages' answers are put together", () => {
    const got = mergeChanges([part([{ path: "logo.png", status: "A", added: -1, removed: -1 }]), part([{ path: "logo.png", status: "M", added: -1, removed: -1 }, { path: "a.txt", status: "M", added: 2, removed: 1 }])]);
    const files = got.repos[0].files!;
    expect(files.find(f => f.path === "logo.png")).toEqual({ path: "logo.png", status: "A", added: -1, removed: -1 });
    expect(files.find(f => f.path === "a.txt")).toEqual({ path: "a.txt", status: "M", added: 2, removed: 1 });
  });
  it("a file's status is its last word, a new file staying new while only changed", () => {
    for (const [steps, want] of [["MD", "D"], ["DA", "A"], ["AM", "A"], ["AD", "D"], ["DM", "M"]]) expect([...steps].reduce((was, next) => laterStatus(was, next))).toBe(want);
    const got = mergeChanges([part([{ path: "x", status: "M", added: 1, removed: 0 }]), part([{ path: "x", status: "D", added: 0, removed: 3 }])]);
    expect(got.repos[0].files![0].status).toBe("D");
  });
  it("a diff header's names, quoted each on its own", () => {
    expect(headerPath("a/x.txt b/x.txt")).toBe("x.txt");
    expect(headerPath("a/dir b/file.txt b/dir b/file.txt")).toBe("dir b/file.txt");
    expect(headerPath('"a/\\321\\204.txt" "b/\\321\\204.txt"')).toBe("ф.txt");
    expect(headerPath('a/old.txt "b/\\321\\204.txt"')).toBe("ф.txt"); // a rename to a name git quotes
    expect(headerPath('"a/\\321\\204.txt" b/new.txt')).toBe("new.txt");
    expect(headerPath("a/old.txt b/new.txt")).toBe("new.txt");
    const files = splitDiff('diff --git a/old.txt "b/\\321\\204.txt"\nsimilarity index 80%\nrename from old.txt\nrename to "\\321\\204.txt"\n@@ -1 +1 @@\n-a\n+b\n');
    expect(files).toHaveLength(1);
    expect(files[0].path).toBe("ф.txt");
    expect(files[0].lines.filter(l => l.op === "+" || l.op === "-")).toHaveLength(2);
  });
  it("a rename's new name is the one it says; a quoted name with an emoji is read whole (review round 6)", () => {
    const files = splitDiff("diff --git a/old.txt b/dir b/file.txt\nsimilarity index 90%\nrename from old.txt\nrename to dir b/file.txt\n@@ -1 +1 @@\n-a\n+b\n");
    expect(files[0].path).toBe("dir b/file.txt");
    expect(headerPath('"a/😀\\"x.txt" "b/😀\\"x.txt"')).toBe('😀"x.txt');
  });
});
