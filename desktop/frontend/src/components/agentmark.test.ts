// @vitest-environment happy-dom
// AgentMark draws the CLI behind an agent: a mark in the text's colour (both themes), a lettered tile
// for a brand without one, nothing for an unknown agent; the node's product wins over the name.
import { describe, it, expect, vi, beforeEach } from "vitest";
import { fakeNode, type Fake } from "../test/fake";

let fake: Fake;
vi.mock("../lib/api", async () => {
  const errors = await import("../lib/errors");
  const b = await import("../lib/base64");
  const Node = new Proxy({}, { get: (_, k: string) => fake.Node[k] });
  return { ...errors, ...b, Node, on: (n: string, f: (d: any) => void) => fake.on(n, f) };
});
vi.mock("@wailsio/runtime", () => ({ Clipboard: { SetText: vi.fn() }, Browser: { OpenURL: vi.fn() }, Events: { On: () => () => {} } }));

async function draw(of: string, agents: any[] = [], extra: Record<string, any> = {}) {
  const svelte = await import("svelte");
  const { app } = await import("../lib/state.svelte");
  app.agents = agents;
  const AgentMark = (await import("./AgentMark.svelte")).default;
  svelte.mount(AgentMark, { target: document.body, props: { of, size: 14, ...extra } });
  svelte.flushSync();
  return document.querySelector(".mark") as HTMLElement | null;
}

beforeEach(() => { vi.resetModules(); fake = fakeNode(); document.body.innerHTML = ""; });

describe("AgentMark", () => {
  it("draws Codex as a mark in the text's colour, not a fixed-colour picture", async () => {
    const m = (await draw("agent:codex"))!;
    expect(m.classList.contains("codex")).toBe(true);
    expect(m.querySelector("img")).toBeNull();
    const svg = m.querySelector("svg")!;
    expect(svg.getAttribute("stroke")).toBe("currentColor");
    expect(svg.querySelectorAll("path").length).toBe(6);
    expect(m.title).toBe("Codex");
  });

  it("keeps Claude's spark and its busy animation", async () => {
    const m = (await draw("agent:claude", [], { busy: true }))!;
    expect(m.querySelector("img")).not.toBeNull();
    expect(m.classList.contains("busy")).toBe(true);
  });

  it("draws the CLI the node says runs behind an agent, whatever it is called", async () => {
    const m = (await draw("agent:work", [{ name: "work", address: "agent:work", driver: "acp", queued: 0, running: 0, product: "gemini-cli" }]))!;
    expect(m.classList.contains("gemini")).toBe(true);
    expect(m.title).toBe("Gemini CLI");
  });

  it("draws a brand without a mark as its initials on a tile", async () => {
    const m = (await draw("agent:q", [{ name: "q", address: "agent:q", driver: "acp", queued: 0, running: 0, product: "qwen-code" }]))!;
    expect(m.classList.contains("tile")).toBe(true);
    expect(m.textContent?.trim()).toBe("Q");
    expect(m.style.getPropertyValue("--h")).toBe("255");
  });

  it("draws nothing for an agent it does not know", async () => {
    expect(await draw("agent:helper")).toBeNull();
  });
});
