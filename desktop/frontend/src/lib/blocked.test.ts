import { describe, it, expect } from "vitest";
import { Turn } from "./turn";
import { blockedReason, blockedIn, ruleFor, approveText, classifierDenial } from "./blocked";

const denial = classifierDenial + "[Destructive or Unwanted Actions]. If you have other tasks that don't depend on this action, continue working on those. IMPORTANT: …";
const ev = (seq: number, kind: string, data: unknown, time = "2026-11-20T10:00:0" + seq + "Z") => ({ seq, time, kind, data: JSON.stringify(data) });

describe("auto mode's refusals", () => {
  it("are failed calls whose result is the classifier's denial; its reason without brackets", () => {
    expect(blockedReason({ status: "failed", output: denial })).toBe("Destructive or Unwanted Actions");
    expect(blockedReason({ status: "failed", output: "exit 1: permission denied" })).toBeNull();
    expect(blockedReason({ status: "completed", output: undefined })).toBeNull();
  });

  it("are listed from a chat's turns, newest first, with the command each would have run", () => {
    const a = new Turn().add([
      ev(1, "tool_call", { id: "c1", title: "ssh host 'sudo rm x'", kind: "execute", status: "pending", input: { command: "ssh host 'sudo rm x'" } }),
      ev(2, "tool_call_update", { id: "c1", status: "failed", output: denial }),
      ev(3, "tool_call", { id: "c2", title: "ls", kind: "execute", status: "pending", input: { command: "ls" } }),
      ev(4, "tool_call_update", { id: "c2", status: "failed", output: "No such file" }),
    ]);
    const b = new Turn().add([ev(5, "tool_call", { id: "m1", title: "mcp__forgejo__delete_branch", kind: "other", status: "failed", output: denial })]);
    const got = blockedIn([["msg_a", a], ["msg_b", b]]);
    expect(got.map(x => [x.msg, x.id, x.command, x.reason])).toEqual([
      ["msg_b", "m1", "", "Destructive or Unwanted Actions"],
      ["msg_a", "c1", "ssh host 'sudo rm x'", "Destructive or Unwanted Actions"],
    ]);
  });

  it("suggest the rule that would allow them, as Claude Code writes rules", () => {
    expect(ruleFor({ command: "git push origin main", title: "", kind: "execute" })).toBe("Bash(git push origin main)");
    expect(ruleFor({ command: "cat <<EOF > a\nx\nEOF", title: "", kind: "execute" })).toBe("Bash(cat:*)");
    expect(ruleFor({ command: "", title: "mcp__forgejo__delete_branch", kind: "other" })).toBe("mcp__forgejo__delete_branch");
    expect(ruleFor({ command: "", name: "mcp__forgejo__delete_branch", title: "Delete branch main", kind: "delete" })).toBe("mcp__forgejo__delete_branch");
    expect(ruleFor({ command: "ls", name: "mcp__ssh__exec", title: "exec", kind: "execute" })).toBe("mcp__ssh__exec");
    expect(ruleFor({ command: "", title: "Fetch https://x", kind: "fetch" })).toBe("WebFetch");
  });

  it("are approved by a message that names the action whole", () => {
    expect(approveText({ command: "rm -rf build", title: "" })).toBe("I approve this action that auto mode blocked; run it again:\n\n```\nrm -rf build\n```");
    expect(approveText({ command: "echo ```", title: "" })).toContain("````\necho ```\n````");
  });
});
