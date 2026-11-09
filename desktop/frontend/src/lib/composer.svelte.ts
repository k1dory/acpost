// What the composer holds for one chat: its text and files, and what a send of them made so far.
// It lives in the app's state, not in the composer: the composer is made anew when a new task
// becomes a chat, and the whole conversation is taken off screen while the node is down.
import { overLimits } from "./draft";
import { b64bytes } from "./base64";
import type { Limits } from "./state.svelte";
import { newId } from "./ids";
import { freeName } from "./paste";

/** A file attached. rid: the read that brought it (its own, of all reads). */
export type Attachment = { name: string; type: string; size: number; data: string; rid?: number };
/** A file to attach (a File, or what a test gives). */
export type FileLike = { name: string; type: string; size: number; arrayBuffer(): Promise<ArrayBuffer> };
type Namer = (f: { name: string; type: string }, taken: string[]) => string;

/** One add(): the files it read, in order, as they were read (whatever the form does with them
 * after). done: settled once all are read (or refused). */
type ReadJob = { results: Attachment[]; done: Promise<void> };

let rids = 0;

/** The files a send takes, owned by it from the click: the form's list then, and what the reads
 * still going then bring after it (each read delivers to the send too, not only to the form; what
 * they brought before the click is in that list, or was removed from it). A read's file keeps the
 * name it was given once, when read (a pasted picture's pasted-…-n is not made again); one that
 * the send's own list has already is told apart there (freeName). The form after the click is not
 * asked. wait() is when those reads end. */
export function sendFiles(d: ComposerDraft): { wait: Promise<void>; files: () => Attachment[] } {
  const had = [...d.files], jobs = [...d.reading].map(j => ({ j, from: j.results.length }));
  return {
    wait: Promise.all(jobs.map(({ j }) => j.done)).then(() => {}),
    files: () => {
      const out = had.map(f => ({ ...f }));
      for (const { j, from } of jobs) for (const r of j.results.slice(from)) {
        out.push({ ...r, name: freeName(r.name, out.map(x => x.name)) });
      }
      return out;
    },
  };
}

/** A cloud sandbox asked for (for: the destination, App.destNow()). */
export type SandboxReq = { for: string; agent: string; repo: string; name: string; idem: string };

/** What a send of a draft made so far. */
export class SendOp {
  /** The cloud sandbox made for the draft when its send failed after it: the next try goes there
   * (for: the destination it was made for, App.destNow()). */
  sandbox: { for: string; agent: string } | null = null;
  /** The sandbox being made: its whole request, frozen at the first try (catalogue agent,
   * repository, name, key). A retry after a lost answer sends it as it was: the node finds the
   * sandbox by its name and key, and a name made again from edited text is another machine. */
  sandboxReq: SandboxReq | null = null;
  /** The message being sent (all of it but its key) and its idempotency key: the same message
   * tried again has the same key, so the node runs it once even when its first answer was lost.
   * content is the message without where it goes; task, the request as it was made. */
  sending: { what: string; key: string; content: string; task: any; dest: string; moved?: boolean } | null = null;

  /** Something was made or asked that a retry must reuse. */
  pending(): boolean { return !!(this.sending || this.sandbox || this.sandboxReq); }
  /** Where what it made goes (a new task's destination), if it says. */
  dest(): string | undefined { return this.sending?.dest || this.sandbox?.for || this.sandboxReq?.for; }

  /** The sandbox request for destination `dest`: the one frozen for it, or a new one made now. */
  sandboxFor(dest: string, make: () => Omit<SandboxReq, "for" | "idem">): SandboxReq {
    if (this.sandboxReq?.for !== dest) this.sandboxReq = { ...make(), for: dest, idem: newId() };
    return this.sandboxReq;
  }
}

export class ComposerDraft {
  text = $state("");
  files = $state<Attachment[]>([]);
  busy = $state(false);
  /** What a send of this draft made so far, all of it in one: a draft that moves (into a thread,
   * back into a new task) takes it whole, so no part of it is left behind. */
  op = new SendOp();
  get sandbox() { return this.op.sandbox; }
  set sandbox(v) { this.op.sandbox = v; }
  get sending() { return this.op.sending; }
  set sending(v) { this.op.sending = v; }
  /** Where this draft went (a new task's, a thread's it merged into): what is still being read
   * into it goes there. */
  movedTo: ComposerDraft | null = null;
  adding: Promise<void> = Promise.resolve();
  /** The reads still going into this draft (a send at the click takes them). */
  reading = new Set<ReadJob>();

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
    for (const j of this.reading) to.reading.add(j);
    this.reading.clear();
  }

  constructor(public key: string) {}

  /** Bumped by forget(): a send that answers after it does not write into the draft's next message. */
  epoch = 0;

  /** What a send made is let go: the next message is a new one (a new task, a sent message). */
  forget() { this.op = new SendOp(); this.epoch++; }

  /** Attaches files within the node's limits, one at a time: two drops at once cannot both pass a
   * check made before either was read. A file over the limits is said (fail) and not read; one
   * that cannot be read is said too, and the next is read. `name` gives the name it is attached
   * under, from the names attached right then. A send waits for what is being read (adding). */
  add(list: Iterable<FileLike>, limits: Limits | null, fail: (why: "count" | "size" | "read", file: string) => void,
    name: Namer = f => f.name): Promise<void> {
    const files = [...list];
    const at = this.live();
    const job: ReadJob = { results: [], done: Promise.resolve() };
    at.reading.add(job);
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
        const a: Attachment = { name: name(f, d.files.map(x => x.name)), type: f.type || "application/octet-stream", size: f.size, data, rid: ++rids };
        job.results.push({ ...a });
        d.files = [...d.files, a];
      }
    }).catch(() => {}) // what failed was said: the queue goes on
      .finally(() => { for (let x: ComposerDraft | null = this; x; x = x.movedTo) x.reading.delete(job); });
    job.done = at.adding;
    return at.adding;
  }
}
