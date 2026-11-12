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
async function show(text: string, scope?: string) {
  const Markdown = (await import("./Markdown.svelte")).default;
  if (reply) { unmount(reply); mounted = mounted.filter(m => m !== reply); }
  let box = document.getElementById("reply");
  if (!box) { box = document.createElement("div"); box.id = "reply"; document.body.append(box); }
  reply = mount(Markdown, { target: box, props: scope === undefined ? { text } : { text, scope } });
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
    await show(src, "msg_1");
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
    // The message shown again (its chat opened again): the table as the person left it.
    await show(src + " and more", "msg_1");
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
    expect(document.querySelector(".uib")).toBeNull();
    expect(q(".code .lang").textContent).toBe("ui");
    expect(q(".code code").textContent).toContain('"label"');
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
    expect(q(".code code").textContent).toContain('"host"');
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

  it("of a single choice goes into the draft when added, and nothing is sent", async () => {
    await withComposer();
    await show(block({ type: "choices", question: "Which DB?", options: ["Postgres", "SQLite"] }));
    all(".opt")[1].click();
    flushSync(); await flush(); flushSync();
    expect(textarea().value).toBe(""); // a pick alone writes nothing
    q<HTMLButtonElement>(".ask .btn").click();
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

describe("review round 1", () => {
  const streaming = async (text: string) => {
    const Streaming = (await import("../test/Streaming.svelte")).default;
    const r = mount(Streaming, { target: document.body, props: { text } }) as unknown as { set(t: string): void };
    mounted.push(r);
    flushSync(); await flush(); flushSync();
    return async (t: string) => { r.set(t); flushSync(); await flush(); flushSync(); };
  };

  it("draws a chart of extreme numbers with a few finite ticks, and shows one of numbers out of range as code (P1)", async () => {
    await show(block({ type: "chart", kind: "line", labels: ["a", "b", "c"], series: [{ name: "x", values: [5e-324, 1e15, -1e15] }] }));
    const axis = all("div.chart svg text.ax").filter(e => !["a", "b", "c"].includes(e.textContent!));
    expect(axis.length).toBeGreaterThanOrEqual(2);
    expect(axis.length).toBeLessThanOrEqual(11);
    expect(q("div.chart").innerHTML).not.toMatch(/NaN|Infinity/);
    await show(block({ type: "chart", kind: "bar", labels: ["a"], series: [{ name: "x", values: [1.7e308] }] }));
    expect(document.querySelector(".gu")).toBeNull();
    expect(q(".uib code").textContent).toContain("1.7e+308");
  });

  it("keeps what was done to a block per message: the same form in another message starts empty (P2)", async () => {
    const form = block({ type: "form", fields: [{ label: "Host" }] });
    const fill = (v: string) => { const i = q<HTMLInputElement>(".form input"); i.value = v; i.dispatchEvent(new Event("input", { bubbles: true })); flushSync(); };
    await show(form, "msg_A");
    fill("web1");
    q<HTMLButtonElement>(".form .btn").click();
    flushSync();
    await show(form, "msg_B");
    expect(q<HTMLInputElement>(".form input").value).toBe("");
    expect(document.querySelector(".form .muted")).toBeNull(); // not "added" here
    await show(form, "msg_A"); // the first opened again: as it was left
    expect(q<HTMLInputElement>(".form input").value).toBe("web1");
    expect(q(".form .muted")).toBeTruthy();
    // Without a scope (a page that names no place): nothing carried over from another showing.
    await show(form);
    fill("x");
    await show(form);
    expect(q<HTMLInputElement>(".form input").value).toBe("");
  });

  it("replaces a choice's earlier answer in the draft rather than adding a second (P2)", async () => {
    const { app } = await import("../lib/state.svelte");
    app.agents = [{ name: "claude", address: "agent:claude", driver: "acp", queued: 0, running: 0 }];
    app.conn = { ok: true };
    app.draft.agent = "agent:claude";
    mounted.push(mount((await import("./Composer.svelte")).default, { target: document.body }));
    flushSync();
    await show(block({ type: "choices", question: "Which DB?", options: ["Postgres", "SQLite"] }), "msg_C");
    const add = () => q<HTMLButtonElement>(".ask .btn");
    all(".opt")[1].click(); flushSync();
    add().click(); flushSync(); await flush(); flushSync();
    expect(add().disabled).toBe(true); // added, nothing changed since
    const ta = q<HTMLTextAreaElement>("textarea");
    ta.value = "first\n" + ta.value + "thanks"; ta.dispatchEvent(new Event("input", { bubbles: true })); flushSync();
    all(".opt")[0].click(); flushSync();
    add().click(); flushSync(); await flush(); flushSync();
    expect(ta.value).toBe("first\n> Which DB?\n\nPostgres\n\nthanks");
    expect(fake.Node.SendTask).not.toHaveBeenCalled();
  });

  it("keeps a component, its focus and its selection, as the reply streams on (P2)", async () => {
    const src = JSON.stringify({ type: "table", columns: ["host"], rows: [["a"], ["b"], ["c"], ["d"], ["e"], ["f"]] });
    const set = await streaming("Here:\n\n```ui\n" + src + "\n```\n");
    const input = q<HTMLInputElement>(".tbl input");
    input.focus();
    input.value = "abc"; input.dispatchEvent(new Event("input", { bubbles: true }));
    input.setSelectionRange(1, 2);
    flushSync();
    // Never out of the page: a browser would take the focus and the selection off it.
    const removed: Node[] = [];
    const watch = new MutationObserver(rs => { for (const r of rs) removed.push(...r.removedNodes); });
    watch.observe(document.body, { childList: true, subtree: true });
    const focus = vi.spyOn(input, "focus");
    await set("Here:\n\n```ui\n" + src + "\n```\n\nThe busiest");
    await set("Here:\n\n```ui\n" + src + "\n```\n\nThe busiest is a.");
    await flush();
    watch.disconnect();
    expect(removed.filter(n => n.contains(input))).toEqual([]);
    expect(focus).not.toHaveBeenCalled(); // nothing to put back
    expect(q(".tbl input")).toBe(input); // the same element, never mounted again
    expect(document.activeElement).toBe(input);
    expect([input.selectionStart, input.selectionEnd]).toEqual([1, 2]);
    expect(input.value).toBe("abc");
  });

  it("keeps the focus of a component whose block moved into a new part of the page (P2)", async () => {
    // A block in a list: the list is new with each item streamed in, the block moved into it.
    // (happy-dom's DOMPurify unwraps the outer list; this text is the test's own, shown unsanitized
    // here to have the page a browser has.)
    vi.doMock("../lib/sanitize", () => ({ cleanHtml: (h: string) => h, cleanSvg: (h: string) => h }));
    const src = JSON.stringify({ type: "form", fields: [{ label: "Host" }] });
    const item = "- one\n\n  ```ui\n  " + src + "\n  ```\n";
    const set = await streaming(item);
    expect(q("ul .uib .form")).toBeTruthy();
    const input = q<HTMLInputElement>(".form input");
    input.focus();
    input.value = "web"; input.dispatchEvent(new Event("input", { bubbles: true }));
    input.setSelectionRange(0, 3);
    flushSync();
    // Its block's node is new: the field is moved there, and the focus put back with its selection
    // (happy-dom keeps the focus of a detached field; a browser does not).
    const focus = vi.spyOn(input, "focus"), select = vi.spyOn(input, "setSelectionRange");
    await set(item + "- two\n");
    expect(focus).toHaveBeenCalledTimes(1);
    expect(select).toHaveBeenCalledWith(0, 3, expect.any(String));
    expect(document.activeElement).toBe(input);
    expect(q("ul li:nth-child(2)").textContent!.trim()).toBe("two");
    vi.doUnmock("../lib/sanitize");
    expect(q(".form input")).toBe(input);
    expect(document.activeElement).toBe(input);
    expect([input.selectionStart, input.selectionEnd]).toEqual([0, 3]);
    expect(all(".gu")).toHaveLength(1);
  });

  it("stays code until its closing fence comes, even when its JSON is whole (P2)", async () => {
    const src = JSON.stringify({ type: "stats", items: [{ label: "Users", value: 1 }] });
    const set = await streaming("Here:\n\n```ui\n" + src);
    expect(document.querySelector(".gu")).toBeNull();
    expect(q(".code code").textContent).toBe(src);
    await set("Here:\n\n```ui\n" + src + "\n");
    expect(document.querySelector(".gu")).toBeNull();
    await set("Here:\n\n```ui\n" + src + "\n```");
    expect(q(".gu .tile .label").textContent).toBe("Users");
  });
});

describe("review round 2", () => {
  const harness = async (text: string, scope: string) => {
    const Streaming = (await import("../test/Streaming.svelte")).default;
    const r = mount(Streaming, { target: document.body, props: { text, scope } }) as unknown as { set(t: string, sc?: string): void };
    mounted.push(r);
    flushSync(); await flush(); flushSync();
    return async (t: string, sc?: string) => { r.set(t, sc); flushSync(); await flush(); flushSync(); };
  };
  const fill = (v: string) => { const i = q<HTMLInputElement>(".form input"); i.value = v; i.dispatchEvent(new Event("input", { bubbles: true })); flushSync(); };
  async function composer() {
    const { app } = await import("../lib/state.svelte");
    app.agents = [{ name: "claude", address: "agent:claude", driver: "acp", queued: 0, running: 0 }];
    app.conn = { ok: true };
    app.draft.agent = "agent:claude";
    mounted.push(mount((await import("./Composer.svelte")).default, { target: document.body }));
    flushSync();
    return q<HTMLTextAreaElement>("textarea");
  }
  const typeIn = (ta: HTMLTextAreaElement, v: string) => { ta.value = v; ta.dispatchEvent(new Event("input", { bubbles: true })); flushSync(); };

  it("gives one Markdown's pages with the same block a state each, as a wiki shows page after page (P2)", async () => {
    const form = block({ type: "form", fields: [{ label: "Host" }] });
    const set = await harness(form, "wiki:repo:one");
    fill("web1");
    const first = q<HTMLInputElement>(".form input");
    await set(form, "wiki:repo:two"); // the same text, another page
    expect(q<HTMLInputElement>(".form input")).not.toBe(first);
    expect(q<HTMLInputElement>(".form input").value).toBe("");
    expect(all(".gu")).toHaveLength(1);
    fill("web2");
    await set(form, "wiki:repo:one"); // back: as it was left there
    expect(q<HTMLInputElement>(".form input").value).toBe("web1");
    await set(form, "wiki:repo:two");
    expect(q<HTMLInputElement>(".form input").value).toBe("web2");
  });

  it("lets an answer deleted from the draft be added again, also after the message is shown again (P2)", async () => {
    const ta = await composer();
    const src = block({ type: "choices", question: "Which?", options: ["a", "b"] });
    await show(src, "msg_D");
    all(".opt")[0].click(); flushSync();
    const add = () => q<HTMLButtonElement>(".ask .btn");
    add().click(); flushSync(); await flush(); flushSync();
    expect(ta.value).toBe("> Which?\n\na\n\n");
    expect(add().disabled).toBe(true);
    expect(document.querySelector(".ask .muted")).toBeTruthy();
    typeIn(ta, ""); // the person deletes it
    expect(add().disabled).toBe(false);
    expect(document.querySelector(".ask .muted")).toBeNull();
    await show(src, "msg_D"); // the message shown again
    expect(add().disabled).toBe(false);
    add().click(); flushSync(); await flush(); flushSync();
    expect(ta.value).toBe("> Which?\n\na\n\n");
    // A form the same way.
    typeIn(ta, "");
    await show(block({ type: "form", fields: [{ label: "Host" }] }), "msg_E");
    fill("web1");
    q<HTMLButtonElement>(".form .btn").click(); flushSync(); await flush(); flushSync();
    expect(q<HTMLButtonElement>(".form .btn").disabled).toBe(true);
    typeIn(ta, "something else");
    expect(q<HTMLButtonElement>(".form .btn").disabled).toBe(false);
    q<HTMLButtonElement>(".form .btn").click(); flushSync(); await flush(); flushSync();
    expect(ta.value).toBe("something else\n- Host: web1\n\n");
  });

  it("shows small, large, negative and mixed numbers with their digits in charts, tables and stats (P2)", async () => {
    await show(block([
      { type: "chart", kind: "bar", labels: ["one", "two"], series: [{ name: "x", values: [0.0001, 0.0002] }] },
      { type: "chart", kind: "line", labels: ["a", "b"], series: [{ name: "x", values: [-0.5, 0.75] }] },
      { type: "table", columns: ["v"], rows: [[0.000123], [123456.789], [-0.0042], [1e14], [12.5]] },
      { type: "stats", items: [{ label: "p", value: 0.00042, delta: -0.00001 }, { label: "q", value: 987654321 }] },
    ]));
    const [small, mixed] = all("div.chart");
    const axis = (c: HTMLElement) => [...c.querySelectorAll("text.ax")].map(e => e.textContent!).filter(s => !/^[a-z]+$/.test(s));
    expect(axis(small)).toEqual(["0", "5e-5", "1e-4", "1.5e-4", "2e-4"]);
    expect(new Set(axis(small)).size).toBe(axis(small).length); // no two labels alike
    small.querySelectorAll("rect.bar")[0].dispatchEvent(new Event("mouseenter"));
    flushSync();
    expect(small.querySelector(".tip")!.textContent).toBe("one: 0.0001");
    expect(axis(mixed)).toEqual(["-0.5", "0", "0.5", "1.0"]); // a step of 0.5: one decimal each
    expect(texts("tbody td")).toEqual(["0.000123", "123,457", "-0.0042", "100,000,000,000,000", "12.5"]);
    expect(texts(".tile .value")).toEqual(["0.00042", "987,654,321"]);
    expect(q(".tile .delta").textContent).toBe("-1e-5");
  });
});

describe("review round 3", () => {
  async function composer() {
    const { app } = await import("../lib/state.svelte");
    app.agents = [{ name: "claude", address: "agent:claude", driver: "acp", queued: 0, running: 0 }];
    app.conn = { ok: true };
    app.draft.agent = "agent:claude";
    mounted.push(mount((await import("./Composer.svelte")).default, { target: document.body }));
    flushSync();
    return q<HTMLTextAreaElement>("textarea");
  }
  const settle = async () => { flushSync(); await flush(); flushSync(); };

  it("finds an answer in the draft only whole (P2)", async () => {
    const { answerAt } = await import("./genui/draft");
    expect(answerAt("> Q\n\nJavaScript\n\n", "> Q\n\nJava\n\n")).toBe(-1);
    expect(answerAt("> Q\n\nJava\n\n", "> Q\n\nJava\n\n")).toBe(0);
    expect(answerAt("mine\n> Q\n\nJava", "> Q\n\nJava\n\n")).toBe(5);
    expect(answerAt("x> Q\n\nJava", "> Q\n\nJava")).toBe(-1);
    expect(answerAt("- n: 10", "- n: 1")).toBe(-1);
    expect(answerAt("anything", "  \n")).toBe(-1);
  });

  it("lets a shorter choice replace a longer one it begins (P2)", async () => {
    const ta = await composer();
    await show(block({ type: "choices", question: "Language?", options: ["JavaScript", "Java"] }), "msg_F");
    const add = () => q<HTMLButtonElement>(".ask .btn");
    all(".opt")[0].click(); flushSync();
    add().click(); await settle();
    expect(ta.value).toBe("> Language?\n\nJavaScript\n\n");
    all(".opt")[1].click(); flushSync();
    expect(add().disabled).toBe(false); // "Java" is not in the draft: "JavaScript" is
    add().click(); await settle();
    expect(ta.value).toBe("> Language?\n\nJava\n\n");
  });

  it("lets a form change 10 to 1 (P2)", async () => {
    const ta = await composer();
    await show(block({ type: "form", fields: [{ label: "n", type: "number", value: 10 }] }), "msg_G");
    q<HTMLButtonElement>(".form .btn").click(); await settle();
    expect(ta.value).toBe("- n: 10\n\n");
    const i = q<HTMLInputElement>(".form input");
    i.value = "1"; i.dispatchEvent(new Event("input", { bubbles: true })); flushSync();
    expect(q<HTMLButtonElement>(".form .btn").disabled).toBe(false);
    q<HTMLButtonElement>(".form .btn").click(); await settle();
    expect(ta.value).toBe("- n: 1\n\n");
  });
});
