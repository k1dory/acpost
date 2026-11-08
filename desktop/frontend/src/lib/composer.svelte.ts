// What the composer holds for one chat: its text and files, and what a send of them made so far.
// It lives in the app's state, not in the composer: the composer is made anew when a new task
// becomes a chat, and the whole conversation is taken off screen while the node is down.
import { overLimits } from "./draft";
import { b64bytes } from "./base64";
import type { Limits } from "./state.svelte";

export type Attachment = { name: string; type: string; size: number; data: string };
/** A file to attach (a File, or what a test gives). */
export type FileLike = { name: string; type: string; size: number; arrayBuffer(): Promise<ArrayBuffer> };

export class ComposerDraft {
  text = $state("");
  files = $state<Attachment[]>([]);
  busy = $state(false);
  /** The cloud sandbox made for this draft when its send failed after it: the next try goes there. */
  sandbox: { for: string; agent: string } | null = null;
  /** The message being sent (all of it but its key) and its idempotency key: the same message
   * tried again has the same key, so the node runs it once even when its first answer was lost.
   * content is the message without where it goes; task, the request as it was made. */
  sending: { what: string; key: string; content: string; task: any; moved?: boolean } | null = null;
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
  forget() { this.sandbox = null; this.sending = null; this.epoch++; }

  /** Attaches files within the node's limits, one at a time: two drops at once cannot both pass a
   * check made before either was read. A file over the limits is said (fail) and not read. */
  add(list: Iterable<FileLike>, limits: Limits | null, fail: (why: "count" | "size") => void): Promise<void> {
    const files = [...list];
    const at = this.live();
    at.adding = at.adding.then(async () => {
      for (const f of files) {
        if (overLimits(this.live().files, f, limits)) { fail(overLimits(this.live().files, f, limits) as "count" | "size"); return; }
        const data = b64bytes(new Uint8Array(await f.arrayBuffer()));
        // Read into whichever draft this one is by now (a new task, the thread it moved into).
        const d = this.live();
        d.files = [...d.files, { name: f.name, type: f.type || "application/octet-stream", size: f.size, data }];
      }
    }).catch(() => {});
    return at.adding;
  }
}
