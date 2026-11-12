import { describe, it, expect } from "vitest";
import { EventFeed } from "./feed";
import { deferred, flush } from "../test/fake";

const ev = (seq: number) => ({ seq, time: new Date(1_000_000 + seq).toISOString(), kind: "text", data: "{}" });

describe("EventFeed", () => {
  it("a live batch that comes while the history is read waits for it: nothing before it is skipped", async () => {
    const store = [1, 2, 3, 4].map(ev);
    const first = deferred<any[]>();
    const asked: number[] = [];
    const seen: number[] = [];
    const f = new EventFeed(after => { asked.push(after); return asked.length === 1 ? first.promise : Promise.resolve(store.filter(e => e.seq > after)); }, fresh => seen.push(...fresh.map(e => e.seq)));
    const done = f.sync();
    f.live([ev(4)]);            // the stream is ahead of the read
    expect(f.raw).toEqual([]);  // not applied: 1–3 are not there yet
    first.resolve(store.slice(0, 2));
    await done;
    expect(f.raw.map(e => e.seq)).toEqual([1, 2, 3, 4]);
    expect(seen).toEqual([1, 2, 3, 4]);
    expect(asked[0]).toBe(0);
  });

  it("applies a batch that follows the history at once, and reads what a gap leaves out", async () => {
    let store = [1, 2].map(ev);
    const f = new EventFeed(after => Promise.resolve(store.filter(e => e.seq > after)), () => {});
    await f.sync();
    f.live([ev(3)]);
    expect(f.last).toBe(3);
    store = [1, 2, 3, 4, 5, 6].map(ev);
    f.live([ev(6)]);            // 4 and 5 are the node's
    await flush(); await flush();
    expect(f.raw.map(e => e.seq)).toEqual([1, 2, 3, 4, 5, 6]);
  });

  it("stopped, it reads and applies nothing more", async () => {
    const d = deferred<any[]>();
    const f = new EventFeed(() => d.promise, () => { throw new Error("applied"); });
    const p = f.sync();
    f.stop();
    d.resolve([ev(1)]);
    await p;
    f.live([ev(1)]);
    expect(f.raw).toEqual([]);
  });
});
