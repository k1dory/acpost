// @vitest-environment happy-dom
// /permissions: Claude Code's rules, from its settings files; typed in the composer it opens them
// (the agent has no /permissions through ACP), and a rule added or removed edits its file.
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { fakeNode, flush, type Fake } from "../test/fake";

let fake: Fake;
vi.mock("../lib/api", async () => {
  const errors = await import("../lib/errors");
  const b = await import("../lib/base64");
  const Node = new Proxy({}, { get: (_, k: string) => fake.Node[k] });
  return { ...errors, ...b, Node, on: (n: string, f: (d: any) => void) => fake.on(n, f) };
});
vi.mock("@wailsio/runtime", () => ({ Clipboard: { SetText: vi.fn() }, Browser: { OpenURL: vi.fn() }, Events: { On: () => () => {} } }));

const src = (source: string, o: any = {}) => ({ source, path: `/p/${source}.json`, exists: true, allow: [], ask: [], deny: [], dirs: [], ...o });
let mounted: any[] = [];
beforeEach(() => { vi.resetModules(); fake = fakeNode(); document.body.innerHTML = ""; });
afterEach(async () => { const { unmount } = await import("svelte"); for (const m of mounted) unmount(m); mounted = []; });
const settle = async (svelte: any) => { svelte.flushSync(); await flush(); svelte.flushSync(); };

describe("/permissions", () => {
  it("typed for Claude Code opens its rules for the chat's folder; nothing is sent", async () => {
    const svelte = await import("svelte");
    const { app } = await import("../lib/state.svelte");
    app.agents = [{ name: "claude", address: "agent:claude", driver: "acp", queued: 0, running: 0, product: "claude-agent-acp" }];
    app.conn = { ok: true };
    app.draft.agent = "agent:claude";
    app.draft.cwd = "/work/proj";
    const Composer = (await import("./Composer.svelte")).default;
    mounted.push(svelte.mount(Composer, { target: document.body }));
    svelte.flushSync();
    const ta = document.querySelector("textarea")!;
    ta.value = "/permissions"; ta.dispatchEvent(new Event("input", { bubbles: true }));
    svelte.flushSync();
    ta.dispatchEvent(new KeyboardEvent("keydown", { key: "Enter", bubbles: true }));
    await settle(svelte);
    expect(app.permissionsFor).toBe("/work/proj");
    expect(ta.value).toBe("");
    expect(fake.Node.SendTask).not.toHaveBeenCalled();
  });

  it("is not offered for an agent that is not Claude Code", async () => {
    const { app } = await import("../lib/state.svelte");
    app.agents = [{ name: "codex", address: "agent:codex", driver: "acp", queued: 0, running: 0, product: "codex-acp" }];
    expect(app.isClaude("agent:codex")).toBe(false);
  });

  it("lists each tab's rules with where they are kept; adds to the file chosen, removes from the rule's own", async () => {
    const svelte = await import("svelte");
    fake.Node.PermissionRules.mockResolvedValue([src("user", { allow: ["WebSearch"] }), src("project"), src("local", { allow: ["Bash(ls:*)"], deny: ["Read(./.env)"] })]);
    const Permissions = (await import("./Permissions.svelte")).default;
    mounted.push(svelte.mount(Permissions, { target: document.body, props: { folder: "/work/proj" } }));
    await settle(svelte);
    expect(fake.Node.PermissionRules).toHaveBeenCalledWith("/work/proj");
    const rules = () => [...document.querySelectorAll(".rule .r")].map(e => e.textContent);
    expect(rules()).toEqual(["WebSearch", "Bash(ls:*)"]);
    (document.querySelectorAll(".tab")[3] as HTMLButtonElement).click(); // Deny
    svelte.flushSync();
    expect(rules()).toEqual(["Read(./.env)"]);

    const input = document.querySelector<HTMLInputElement>(".add input")!;
    input.value = " Bash(rm:*) "; input.dispatchEvent(new Event("input", { bubbles: true }));
    const sel = document.querySelector<HTMLSelectElement>(".add select")!;
    sel.value = "user"; sel.dispatchEvent(new Event("change", { bubbles: true }));
    svelte.flushSync();
    document.querySelector<HTMLFormElement>(".add")!.dispatchEvent(new Event("submit", { bubbles: true, cancelable: true }));
    await settle(svelte);
    expect(fake.Node.PermissionEdit).toHaveBeenCalledWith("/work/proj", "user", "deny", " Bash(rm:*) ", true);

    (document.querySelector(".rule button") as HTMLButtonElement).click();
    await settle(svelte);
    expect(fake.Node.PermissionEdit).toHaveBeenLastCalledWith("/work/proj", "local", "deny", "Read(./.env)", false);
  });

  it("opens on what auto mode blocked in the chat; a block is allowed with a rule, or approved with a message", async () => {
    const svelte = await import("svelte");
    const { app } = await import("../lib/state.svelte");
    const { Turn } = await import("../lib/turn");
    const { classifierDenial } = await import("../lib/blocked");
    fake.Node.PermissionRules.mockResolvedValue([src("user"), src("local")]);
    fake.Node.SendTask.mockResolvedValue({ id: "m2", thread: "t2", from: "user:me", to: "agent:claude", depth: 0, state: "queued", body: "", created_at: "", updated_at: "" });
    fake.Node.Events.mockResolvedValue([]);
    app.agents = [{ name: "claude", address: "agent:claude", driver: "acp", queued: 0, running: 0, product: "claude-agent-acp" }];
    app.conn = { ok: true };
    app.draft.agent = "agent:claude"; app.draft.cwd = "/work/proj";
    const turn = new Turn().add([{ seq: 1, time: "2026-11-20T10:00:00Z", kind: "tool_call", data: JSON.stringify({ id: "c1", title: "git push", kind: "execute", status: "failed", input: { command: "git push --force" }, output: classifierDenial + "[Destructive]. If you have other tasks…" }) }]);
    app.msgs = [{ id: "m1", thread: "m1", from: "user:me", to: "agent:claude", depth: 0, state: "done", body: "x", created_at: "", updated_at: "" } as any];
    app.turns = { m1: turn } as any;
    app.openPermissions();
    expect(app.permissionsTab).toBe("recent");
    const Permissions = (await import("./Permissions.svelte")).default;
    mounted.push(svelte.mount(Permissions, { target: document.body, props: { folder: app.permissionsFor! } }));
    await settle(svelte);
    expect(document.querySelector(".blk .r")!.textContent).toBe("git push --force");
    expect(document.querySelector(".blk .why")!.textContent).toContain("Destructive");

    const [allow, approve] = [...document.querySelectorAll<HTMLButtonElement>(".blk .btn")];
    allow.click(); svelte.flushSync();
    expect(document.querySelector<HTMLInputElement>(".add input")!.value).toBe("Bash(git push --force)");
    expect(document.querySelector(".tab.on")!.textContent).toContain("Allow");

    (document.querySelectorAll<HTMLButtonElement>(".tab")[0]).click(); svelte.flushSync();
    document.querySelectorAll<HTMLButtonElement>(".blk .btn")[1].click();
    for (let i = 0; i < 5 && app.permissionsFor !== null; i++) await settle(svelte);
    expect(fake.Node.SendTask).toHaveBeenCalledTimes(1);
    expect(fake.Node.SendTask.mock.calls[0][0].body).toContain("git push --force");
    expect(app.permissionsFor).toBeNull();
  });
});
