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
   * tried again has the same key, so the node runs it once even when its first answer was lost. */
  sending: { what: string; key: string } | null = null;
  private adding: Promise<void> = Promise.resolve();

  constructor(public key: string) {}

  /** Bumped by forget(): a send that answers after it does not write into the draft's next message. */
  epoch = 0;

  /** What a send made is let go: the next message is a new one (a new task, a sent message). */
  forget() { this.sandbox = null; this.sending = null; this.epoch++; }

  /** Attaches files within the node's limits, one at a time: two drops at once cannot both pass a
   * check made before either was read. A file over the limits is said (fail) and not read. */
  add(list: Iterable<FileLike>, limits: Limits | null, fail: (why: "count" | "size") => void): Promise<void> {
    const files = [...list];
    this.adding = this.adding.then(async () => {
      for (const f of files) {
        const over = overLimits(this.files, f, limits);
        if (over) { fail(over); return; }
        const data = b64bytes(new Uint8Array(await f.arrayBuffer()));
        this.files = [...this.files, { name: f.name, type: f.type || "application/octet-stream", size: f.size, data }];
      }
    }).catch(() => {});
    return this.adding;
  }
}
