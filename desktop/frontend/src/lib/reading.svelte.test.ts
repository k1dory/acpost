// A file still being read into a draft is something to send, and the interface sees it change
// (the send button comes on, and is not the stop button, from the paste itself).
import { describe, it, expect } from "vitest";
import { flushSync } from "svelte";
import { ComposerDraft } from "./composer.svelte";
import { deferred, flush } from "../test/fake";

describe("a draft's content", () => {
  it("is seen as it changes: a read begun, its file there, the file removed", async () => {
    const d = new ComposerDraft("new");
    const seen: boolean[] = [];
    const stop = $effect.root(() => { $effect(() => { seen.push(d.hasContent); }); });
    flushSync();
    const read = deferred<ArrayBuffer>();
    d.add([{ name: "shot.png", type: "image/png", size: 2, arrayBuffer: () => read.promise } as any], null, () => {});
    flushSync();
    expect(seen).toEqual([false, true]); // the paste itself, before a byte is read
    read.resolve(new ArrayBuffer(2));
    await flush(); await flush();
    flushSync();
    expect(d.files.length).toBe(1);
    expect(d.reading.size).toBe(0);
    expect(d.hasContent).toBe(true);
    d.files = [];
    flushSync();
    expect(seen[seen.length - 1]).toBe(false);
    d.text = "  ";
    flushSync();
    expect(d.hasContent).toBe(false);
    stop();
  });
});
