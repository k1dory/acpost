// What the composer holds for one chat: its text and files, and what a send of them made so far.
// It lives in the app's state, not in the composer: the composer is made anew when a new task
// becomes a chat, and the whole conversation is taken off screen while the node is down.
import { overLimits } from "./draft";
import { b64bytes } from "./base64";
import type { Limits } from "./state.svelte";
import { OpKey } from "./opkey";

/** A file attached; asked: the read that brought it (a send takes the reads asked before it).
 * Kept in the attachment: the $state array gives back proxies, not the objects put in. */
export type Attachment = { name: string; type: string; size: number; data: string; asked?: number };
/** A file to attach (a File, or what a test gives). */
export type FileLike = { name: string; type: string; size: number; arrayBuffer(): Promise<ArrayBuffer> };

/** Reads asked so far (any draft): a send takes the files of the reads asked before it. */
let asked = 0;

/** The files a send made now takes, once the reads asked before it are done (`adding`): those
 * attached then, and those those reads bring. What is attached after it is the next message's. */
export function filesAsOf(d: ComposerDraft): () => Attachment[] {
  const had = new Set(d.files), upto = asked;
  return () => d.files.filter(f => had.has(f) || (f.asked ?? Infinity) <= upto);
}

export class ComposerDraft {
  text = $state("");
  files = $state<Attachment[]>([]);
  busy = $state(false);
  /** The cloud sandbox made for this draft when its send failed after it: the next try goes there
   * (for: the destination it was made for, App.destNow()). */
  sandbox: { for: string; agent: string } | null = null;
  /** The key of the sandbox being made for this draft, from before its first request: a retry
   * after a lost answer gets the sandbox made the first time. */
  sandboxOp = new OpKey();
  /** The message being sent (all of it but its key) and its idempotency key: the same message
   * tried again has the same key, so the node runs it once even when its first answer was lost.
   * content is the message without where it goes; task, the request as it was made. */
  sending: { what: string; key: string; content: string; task: any; dest: string; moved?: boolean } | null = null;
  /** Where this draft went (a new task's, a thread's it merged into): what is still being read
   * into it goes there. */
  movedTo: ComposerDraft | null = null;
  adding: Promise<void> = Promise.resolve();

  /** The draft that holds this one's text now. */
  live(): ComposerDraft {
    let d: ComposerDraft = this;
    for (let i = 0; d.movedTo && i < 64; i++) d = d.movedTo;
    return d;
  }
  /** This draft continues in `to`: reads still going land there, and its adds queue after them. */
  forward(to: ComposerDraft) {
    if (to === this) return;
    this.movedTo = to;
    const mine = this.adding, theirs = to.adding;
    to.adding = Promise.all([mine, theirs]).then(() => {});
  }

  constructor(public key: string) {}

  /** Bumped by forget(): a send that answers after it does not write into the draft's next message. */
  epoch = 0;

  /** What a send made is let go: the next message is a new one (a new task, a sent message). */
  forget() { this.sandbox = null; this.sandboxOp.done(); this.sending = null; this.epoch++; }

  /** Attaches files within the node's limits, one at a time: two drops at once cannot both pass a
   * check made before either was read. A file over the limits is said (fail) and not read; one
   * that cannot be read is said too, and the next is read. `name` gives the name it is attached
   * under, from the names attached right then. A send waits for what is being read (adding). */
  add(list: Iterable<FileLike>, limits: Limits | null, fail: (why: "count" | "size" | "read", file: string) => void,
    name: (f: FileLike, taken: string[]) => string = f => f.name): Promise<void> {
    const files = [...list];
    const ticket = ++asked;
    const at = this.live();
    at.adding = at.adding.then(async () => {
      for (const f of files) {
        if (overLimits(this.live().files, f, limits)) { fail(overLimits(this.live().files, f, limits) as "count" | "size", f.name); return; }
        let data: string;
        try { data = b64bytes(new Uint8Array(await f.arrayBuffer())); } catch { fail("read", f.name); continue; }
        // Read into whichever draft this one is by now (a new task, the thread it moved into),
        // checked again there: that one may have filled up meanwhile.
        const d = this.live();
        const over = overLimits(d.files, f, limits);
        if (over) { fail(over, f.name); return; }
        // Its name, chosen here: against all attached by now, those added just before it included.
        d.files = [...d.files, { name: name(f, d.files.map(x => x.name)), type: f.type || "application/octet-stream", size: f.size, data, asked: ticket }];
      }
    }).catch(() => {}); // what failed was said: the queue goes on
    return at.adding;
  }
}
