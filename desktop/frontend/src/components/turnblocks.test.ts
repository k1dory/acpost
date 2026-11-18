// @vitest-environment happy-dom
// A turn's blocks as the chat lays them out: calls made one after another are one list.
import { describe, it, expect, vi, afterEach } from "vitest";
import { mount, unmount, flushSync } from "svelte";

vi.mock("../lib/api", async () => ({ ...(await import("../lib/errors")), ...(await import("../lib/base64")), Node: new Proxy({}, { get: () => async () => null }), on: () => () => {} }));
vi.mock("@wailsio/runtime", () => ({ Clipboard: { SetText: vi.fn() }, Browser: { OpenURL: vi.fn() }, Events: { On: () => () => {} } }));

import TurnBlocks from "./TurnBlocks.svelte";

const tool = (id: string) => ({ id, title: "Read " + id, kind: "read", status: "completed", started: 1, ended: 20, output: "out of " + id });
const view = (blocks: any[], known: string[]) => ({ blocks, tools: new Map(known.map(id => [id, tool(id)])), requests: new Map() }) as any;

let mounted: any[] = [];
afterEach(() => { for (const m of mounted) unmount(m); mounted = []; document.body.innerHTML = ""; });
const show = (v: any) => { mounted.push(mount(TurnBlocks, { target: document.body, props: { view: v, msg: "m1", msgState: "done", agent: "agent:claude" } })); flushSync(); };
const series = () => [...document.querySelectorAll(".series")].map(s => [...s.querySelectorAll(".tool .what")].map(w => w.textContent));

describe("a turn's tool calls", () => {
  it("made one after another are one list; text between them begins another", () => {
    show(view([{ t: "tool", id: "a" }, { t: "tool", id: "b" }, { t: "text", text: "then" }, { t: "tool", id: "c" }], ["a", "b", "c"]));
    expect(series()).toEqual([["Read a", "Read b"], ["Read c"]]);
    expect(document.body.textContent).toContain("then");
  });
  it("a call the turn has no record of leaves no row, no empty list, and does not break the list it is in", () => {
    show(view([{ t: "tool", id: "x" }, { t: "text", text: "said" }, { t: "tool", id: "a" }, { t: "tool", id: "x" }, { t: "tool", id: "b" }], ["a", "b"]));
    expect(series()).toEqual([["Read a", "Read b"]]);
  });
  it("a row of the list unfolds to what the call printed", () => {
    show(view([{ t: "tool", id: "a" }], ["a"]));
    (document.querySelector(".tool .head") as HTMLButtonElement).click(); flushSync();
    expect(document.querySelector(".tool .out")!.textContent).toBe("out of a");
  });
});
