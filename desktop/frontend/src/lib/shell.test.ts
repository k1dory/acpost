import { describe, it, expect } from "vitest";
import { shellBody, shellNote, fitOutput, shellSent } from "./shell";
import { follows } from "./stick";
import { sideWidth } from "./sidewidth";

describe("a run as the agent is told of it", () => {
  it("is read back as the command, its output and how it ended", () => {
    for (const [cmd, out, end] of [
      ["git status", " M a.go\n?? b\n", { code: 1 }],
      ["echo ok", "ok\n", {}],
      ["true", "", {}],
      ["cat README.md", "text\n```sh\nls\n```\nmore\n", {}],
      ["printf 'a\nb'", "a\nb", { stopped: true }],
      ["sleep 999", "", { timedOut: true }],
      ["ls", "", { error: "fork/exec /bin/zsh:\nno such file" }],
    ] as const) {
      const n = shellNote(shellBody(cmd, out, end))!;
      expect([n.cmd, n.output]).toEqual([cmd, out.replace(/\s+$/, "")]);
      expect(n.failed).toBe(Object.keys(end).length > 0);
    }
    expect(shellNote(shellBody("x", "y", { code: 3 }))!.end).toBe("exit 3");
    expect(shellNote("$ 5 is what I owe you")).toBeNull();
    expect(shellNote("fix the build")).toBeNull();
  });

  it("is within what an agent is sent and the node takes: the start of a long output and, mostly, its end", () => {
    const long = Array.from({ length: 40000 }, (_, i) => `line ${i} — строка`).join("\n");
    const fit = fitOutput(long, 4096);
    expect(new TextEncoder().encode(fit).length).toBeLessThanOrEqual(4096);
    expect(fit.startsWith("line 0 ")).toBe(true);
    expect(fit.endsWith("line 39999 — строка")).toBe(true);
    expect(fit).toMatch(/\[… \d+ bytes left out …\]/);
    expect(fit).not.toContain("�");
    expect(new TextEncoder().encode(shellBody("make", long)).length).toBeLessThanOrEqual(shellSent + 64);
    expect(new TextEncoder().encode(shellBody("make", long, {}, 2048)).length).toBeLessThanOrEqual(2048);
    expect(shellNote(shellBody("make", long, { code: 2 }, 2048))!.cmd).toBe("make");
    // The fences are as long as the output makes them: counted too.
    expect(new TextEncoder().encode(shellBody("cat x", "`".repeat(1000), {}, 1024)).length).toBeLessThanOrEqual(1024);
    // Ended by a signal: read back as a run all the same.
    expect(shellNote(shellBody("kill -TERM $$", "", { code: -1 }))).toMatchObject({ cmd: "kill -TERM $$", end: "exit -1", failed: true });
    expect(shellNote(shellBody("x", "y", { cut: true }))).toMatchObject({ output: "y" });
  });
});

describe("a chat following its end", () => {
  it("follows while the person is at the end, not once they moved up, and again when they are back", () => {
    expect(follows(true, 600, 1000, 1000)).toBe(true);   // the text grew under the view: no one moved
    expect(follows(true, 30, 970, 1000)).toBe(false);    // a little up, text still streaming: left alone
    expect(follows(false, 400, 1200, 1000)).toBe(false); // down, but not to the end
    expect(follows(false, 40, 1560, 1200)).toBe(true);   // down to near the end
    expect(follows(false, 0, 900, 1600)).toBe(true);     // the content shrank: the view is at its end
  });
});

describe("the chat's column beside the IDE", () => {
  it("is as wide as asked, between what a chat needs and what leaves the IDE its place", () => {
    expect(sideWidth(520, 1600)).toBe(520);
    expect(sideWidth(100, 1600)).toBe(320);
    expect(sideWidth(5000, 1600)).toBe(1240);
    expect(sideWidth(900, 600)).toBe(320);
    expect(sideWidth(NaN, 1600)).toBe(400);
  });
});
