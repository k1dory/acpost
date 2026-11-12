// @vitest-environment happy-dom
// ```ui blocks in a reply, drawn for real (happy-dom): each component from a sample, the code shown
// for a block not valid or still being written, and an answer put into the composer's draft.
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
let mount: typeof import("svelte").mount, unmount: typeof import("svelte").unmount, flushSync: typeof import("svelte").flushSync;
import { fakeNode, flush, type Fake } from "../test/fake";

let fake: Fake;
vi.mock("../lib/api", async () => {
  const errors = await import("../lib/errors");
  const b = await import("../lib/base64");
  const Node = new Proxy({}, { get: (_, k: string) => fake.Node[k] });
  return { ...errors, ...b, Node, on: (n: string, f: (d: any) => void) => fake.on(n, f) };
});
vi.mock("@wailsio/runtime", () => ({ Clipboard: { SetText: vi.fn() }, Browser: { OpenURL: vi.fn() }, Events: { On: () => () => {} } }));

let mounted: any[] = [], reply: any = null;
beforeEach(async () => {
  vi.resetModules();
  fake = fakeNode();
  fake.Node.Chats.mockResolvedValue({ items: [] });
  fake.Node.Agents.mockResolvedValue([]);
  fake.Node.Events.mockResolvedValue([]);
  document.body.innerHTML = "";
  ({ mount, unmount, flushSync } = await import("svelte"));
});
afterEach(() => { for (const m of mounted) unmount(m); mounted = []; reply = null; });

const block = (v: unknown) => "Here:\n\n```ui\n" + (typeof v === "string" ? v : JSON.stringify(v)) + "\n```\n\nafter";
// A reply shown; shown again (as it streams on, or as its chat is opened again), in place of the last.
async function show(text: string) {
  const Markdown = (await import("./Markdown.svelte")).default;
  if (reply) { unmount(reply); mounted = mounted.filter(m => m !== reply); }
  let box = document.getElementById("reply");
  if (!box) { box = document.createElement("div"); box.id = "reply"; document.body.append(box); }
  reply = mount(Markdown, { target: box, props: { text } });
  mounted.push(reply);
  flushSync();
  await flush(); flushSync();
}
const q = <T extends Element = HTMLElement>(s: string) => document.querySelector<T>(s)!;
const all = (s: string) => [...document.querySelectorAll<HTMLElement>(s)];
const texts = (s: string) => all(s).map(e => e.textContent!.trim());

describe("a ui block in a reply", () => {
  it("draws stats tiles in place of its code", async () => {
    await show(block({ type: "stats", title: "Week", items: [{ label: "Users", value: 1200, delta: 4 }, { label: "Errors", value: "3", delta: "-2" }] }));
    expect(q(".uib").classList.contains("drawn")).toBe(true);
    expect(q(".gu h4").textContent).toBe("Week");
    expect(texts(".tile .label")).toEqual(["Users", "Errors"]);
    expect(q(".tile .delta").className).toContain("up");
    expect(all(".tile .delta")[1].className).toContain("down");
    expect(document.body.textContent).toContain("after"); // the rest of the reply is there
  });

  it("draws a bar chart with an axis, a legend and the value on hover", async () => {
    await show(block({ type: "chart", kind: "bar", unit: "ms", labels: ["mon", "tue"], series: [{ name: "p50", values: [10, 20] }, { name: "p99", values: [40, 80] }] }));
    expect(all("svg rect.bar")).toHaveLength(4);
    expect(texts(".legend span")).toEqual(["p50", "p99"]);
    expect(texts("svg text.ax")).toContain("mon");
    all("svg rect.bar")[3].dispatchEvent(new Event("mouseenter"));
    flushSync();
    expect(q(".tip").textContent).toBe("tue: p99 80 ms");
  });

  it("draws line, area and pie charts", async () => {
    await show(block([
      { type: "chart", kind: "line", labels: ["a", "b", "c"], series: [{ name: "x", values: [1, 3, 2] }] },
      { type: "chart", kind: "area", labels: ["a", "b"], series: [{ name: "x", values: [1, -1] }] },
      { type: "chart", kind: "pie", labels: ["yes", "no", "none"], series: [{ name: "", values: [3, 1, 0] }] },
    ]));
    const charts = all("div.chart");
    expect(charts).toHaveLength(3);
    expect(charts[0].querySelectorAll("circle")).toHaveLength(3);
    expect(charts[1].querySelector("path[fill-opacity]")).toBeTruthy();
    expect(charts[2].querySelectorAll("path.slice")).toHaveLength(2); // a slice of 0 is not drawn
    charts[2].querySelector("path.slice")!.dispatchEvent(new Event("mouseenter"));
    flushSync();
    expect(charts[2].querySelector(".tip")!.textContent).toBe("yes: 3 (75%)");
  });

  it("draws a table that sorts and filters in the page, kept as it was left", async () => {
    const src = block({ type: "table", columns: ["host", "load"], rows: [["b", 2], ["a", 10], ["c", 1], ["d", 5], ["e", 3], ["f", 4]] });
    await show(src);
    expect(q("td.num").textContent).toBe("2"); // numbers right-aligned
    q<HTMLButtonElement>("th:nth-child(2) button").click();
    flushSync();
    expect(texts("tbody td:first-child")).toEqual(["c", "b", "e", "f", "d", "a"]);
    q<HTMLButtonElement>("th:nth-child(2) button").click();
    flushSync();
    expect(texts("tbody td:first-child")[0]).toBe("a");
    const box = q<HTMLInputElement>(".tbl input");
    box.value = "a"; box.dispatchEvent(new Event("input", { bubbles: true }));
    flushSync();
    expect(texts("tbody td:first-child")).toEqual(["a"]);
    // The reply goes on streaming: the page is drawn again, the table as the person left it.
    await show(src + " and more");
    expect(texts("tbody td:first-child")).toEqual(["a"]);
    expect(q<HTMLInputElement>(".tbl input").value).toBe("a");
    expect(q("th:nth-child(2)").getAttribute("aria-sort")).toBe("descending");
  });

  it("draws a timeline, a kanban board and a plan's steps", async () => {
    await show(block([
      { type: "timeline", items: [{ time: "10:00", title: "built", status: "done" }, { time: "10:05", title: "deploying", status: "doing" }] },
      { type: "kanban", columns: [{ title: "Doing", cards: [{ title: "api", tag: "p1" }] }, { title: "Done", cards: [] }] },
      { type: "steps", items: [{ title: "read", status: "done" }, { title: "write", status: "doing" }, "test"] },
    ]));
    expect(texts(".tl .t")).toEqual(["built", "deploying"]);
    expect(q(".tl .dot.running")).toBeTruthy();
    expect(texts(".kb .ct")).toEqual(["Doing1", "Done0"]);
    expect(q(".kc .chip").textContent).toBe("p1");
    expect(all(".steps li").map(l => l.className.split(" ").find(c => ["done", "doing", "todo", "failed"].includes(c)))).toEqual(["done", "doing", "todo"]);
  });

  it("shows its text as text, never as HTML", async () => {
    await show(block({ type: "steps", title: "<img src=x onerror=alert(1)>", items: ["<script>alert(1)</script>"] }));
    expect(document.querySelector(".gu img, .gu script")).toBeNull();
    expect(q(".gu h4").textContent).toBe("<img src=x onerror=alert(1)>");
  });

  it("shows the code of a block not valid, or still being written", async () => {
    await show("Here:\n\n```ui\n{\"type\":\"stats\",\"items\":[{\"label\":\"Us");
    expect(q(".uib").classList.contains("drawn")).toBe(false);
    expect(q(".uib .lang").textContent).toBe("ui");
    expect(q(".uib code").textContent).toContain('"label"');
    expect(document.querySelector(".gu")).toBeNull();
    // Whole: drawn.
    await show(block({ type: "stats", items: [{ label: "Users", value: 1 }] }));
    expect(q(".gu .tile .label").textContent).toBe("Users");
    // Of a kind not known: code again.
    await show(block({ type: "iframe", src: "https://example.com" }));
    expect(document.querySelector(".gu")).toBeNull();
    expect(q(".uib code").textContent).toContain("iframe");
  });
});

describe("a ui block in a reply that streams", () => {
  it("is code while written, drawn once whole, and stays as the person left it as the text goes on", async () => {
    const Streaming = (await import("../test/Streaming.svelte")).default;
    const src = JSON.stringify({ type: "table", columns: ["host", "load"], rows: [["b", 2], ["a", 10], ["c", 1], ["d", 5], ["e", 3], ["f", 4]] });
    const head = "Here:\n\n```ui\n";
    const r = mount(Streaming, { target: document.body, props: { text: head + src.slice(0, 40) } }) as unknown as { set(t: string): void };
    mounted.push(r);
    flushSync();
    expect(document.querySelector(".gu")).toBeNull();
    expect(q(".uib code").textContent).toContain('"host"');
    r.set(head + src + "\n```\n");
    flushSync(); await flush(); flushSync();
    expect(all(".gu")).toHaveLength(1);
    q<HTMLButtonElement>("th:nth-child(1) button").click();
    flushSync();
    expect(texts("tbody td:first-child")).toEqual(["a", "b", "c", "d", "e", "f"]);
    r.set(head + src + "\n```\n\nThe busiest is");
    flushSync(); await flush(); flushSync();
    expect(all(".gu")).toHaveLength(1); // drawn again, not twice
    expect(texts("tbody td:first-child")).toEqual(["a", "b", "c", "d", "e", "f"]);
    expect(document.body.textContent).toContain("The busiest is");
  });
});

describe("an answer from a reply", () => {
  async function withComposer() {
    const { app } = await import("../lib/state.svelte");
    app.agents = [{ name: "claude", address: "agent:claude", driver: "acp", queued: 0, running: 0 }];
    app.conn = { ok: true };
    app.draft.agent = "agent:claude";
    const Composer = (await import("./Composer.svelte")).default;
    mounted.push(mount(Composer, { target: document.body }));
    flushSync();
    return app;
  }
  const textarea = () => q<HTMLTextAreaElement>("textarea");

  it("of a single choice goes into the draft on a click, and nothing is sent", async () => {
    await withComposer();
    await show(block({ type: "choices", question: "Which DB?", options: ["Postgres", "SQLite"] }));
    all(".opt")[1].click();
    flushSync(); await flush(); flushSync();
    expect(textarea().value).toBe("> Which DB?\n\nSQLite\n\n");
    expect(q(".ask .muted").textContent).toBeTruthy();
    expect(fake.Node.SendTask).not.toHaveBeenCalled();
    expect(fake.Node.Send).not.toHaveBeenCalled();
  });

  it("of several choices goes in, in the options' order, when added", async () => {
    await withComposer();
    await show(block({ type: "choices", question: "Which?", options: ["a", "b", "c"], multiple: true }));
    const add = q<HTMLButtonElement>(".ask .btn");
    expect(add.disabled).toBe(true);
    all(".opt")[2].click(); all(".opt")[0].click();
    flushSync();
    expect(all(".opt").map(o => o.getAttribute("aria-checked"))).toEqual(["true", "false", "true"]);
    add.click();
    flushSync(); await flush(); flushSync();
    expect(textarea().value).toBe("> Which?\n\na, c\n\n");
  });

  it("of a form goes in field by field, after what the person has typed", async () => {
    await withComposer();
    textarea().value = "mine"; textarea().dispatchEvent(new Event("input", { bubbles: true }));
    flushSync();
    await show(block({ type: "form", title: "Deploy", fields: [
      { label: "Host", placeholder: "web1" }, { label: "Replicas", type: "number", value: 2 },
      { label: "Region", type: "select", options: ["eu", "us"] }, { label: "Now", type: "checkbox" }] }));
    const inputs = all(".form input, .form select") as (HTMLInputElement | HTMLSelectElement)[];
    inputs[0].value = "web2"; inputs[0].dispatchEvent(new Event("input", { bubbles: true }));
    inputs[2].value = "us"; inputs[2].dispatchEvent(new Event("change", { bubbles: true }));
    (inputs[3] as HTMLInputElement).checked = true; inputs[3].dispatchEvent(new Event("change", { bubbles: true }));
    flushSync();
    q<HTMLButtonElement>(".form .btn").click();
    flushSync(); await flush(); flushSync();
    expect(textarea().value).toBe("mine\n> Deploy\n\n- Host: web2\n- Replicas: 2\n- Region: us\n- Now: yes\n\n");
    expect(fake.Node.SendTask).not.toHaveBeenCalled();
  });
});
