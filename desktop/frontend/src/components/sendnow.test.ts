// @vitest-environment happy-dom
// A message waiting behind the agent's turn: "Send now" stops that turn, so the agent takes it at once.
import { describe, it, expect, vi, beforeEach } from "vitest";
import { fakeNode, flush, type Fake } from "../test/fake";

let fake: Fake;
vi.mock("../lib/api", async () => {
  const errors = await import("../lib/errors");
  const b = await import("../lib/base64");
  const Node = new Proxy({}, { get: (_, k: string) => fake.Node[k] });
  return { ...errors, ...b, Node, on: (n: string, f: (d: any) => void) => fake.on(n, f) };
});
vi.mock("@wailsio/runtime", () => ({ Clipboard: { SetText: vi.fn() }, Browser: { OpenURL: vi.fn() }, Events: { On: () => () => {} } }));

const at = "2026-11-20T10:00:00Z";
const msg = (id: string, to: string, state: string) => ({ id, thread: "t", from: "user:me", to, depth: 0, state, body: id, created_at: at, updated_at: at });

beforeEach(() => { vi.resetModules(); fake = fakeNode(); document.body.innerHTML = ""; });

describe("a queued message", () => {
  async function shown(msgs: any[], m: any) {
    const svelte = await import("svelte");
    const { app } = await import("../lib/state.svelte");
    app.msgs = msgs;
    const Message = (await import("./Message.svelte")).default;
    svelte.mount(Message, { target: document.body, props: { m } });
    svelte.flushSync();
    return { svelte, app };
  }
  const now = () => document.querySelector<HTMLButtonElement>("button.now");

  it("behind the agent's running turn is sent now by stopping that turn, not another agent's", async () => {
    fake.Node.Cancel.mockImplementation(async (id: string) => ({ ...msg(id, "agent:claude", "cancelled") }));
    const q = msg("b", "agent:claude", "queued");
    const { svelte } = await shown([msg("a", "agent:claude", "running"), msg("c", "agent:codex", "running"), q], q);
    expect(now()).not.toBeNull();
    now()!.click();
    svelte.flushSync(); await flush();
    expect(fake.Node.Cancel.mock.calls.map(c => c[0])).toEqual(["a"]);
  });

  it("with no turn of its agent running has nothing to stop: no Send now", async () => {
    const q = msg("b", "agent:claude", "queued");
    await shown([msg("c", "agent:codex", "running"), q], q);
    expect(now()).toBeNull();
  });

  it("only the next of the agent's queue has it: a later one would start the one before it", async () => {
    const b = { ...msg("b", "agent:claude", "queued"), created_at: "2026-11-20T10:00:01Z" };
    const c = { ...msg("c", "agent:claude", "queued"), created_at: "2026-11-20T10:00:02Z" };
    await shown([msg("a", "agent:claude", "running"), b, c], c);
    expect(now()).toBeNull();
  });

  it("an approval is shown as a mark, not its text, and is not edited", async () => {
    const { approveText } = await import("../lib/blocked");
    const q = { ...msg("g", "agent:claude", "queued"), body: approveText({ command: "ansible-playbook x.yml", title: "" }) };
    await shown([q], q);
    expect(document.querySelector(".granted")!.textContent).toContain("Permission granted");
    expect(document.body.textContent).not.toContain("ansible-playbook");
    expect([...document.querySelectorAll(".eacts .btn")].map(b => b.textContent)).not.toContain("Edit");
  });
});
