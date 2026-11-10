// What the composer holds for one chat: its text and files, and what a send of them made so far.
// It lives in the app's state, not in the composer: the composer is made anew when a new task
// becomes a chat, and the whole conversation is taken off screen while the node is down.
import { overLimits } from "./draft";
import { b64bytes } from "./base64";
import type { Limits } from "./state.svelte";
import { newId } from "./ids";
import { freeName } from "./paste";

/** A file attached. rid: its identity, given once as it enters a composer (attachment()), kept in
 * every copy (a send's): what a send sent leaves the composer by it, never by name or bytes (the
 * same file attached again is another attachment). Also the order files were delivered in. */
export type Attachment = { name: string; type: string; size: number; data: string; readonly rid: number };

let rids = 0;
/** The one way an attachment is made: with an identity of its own. */
export function attachment(f: { name: string; type: string; size: number; data: string }): Attachment {
  return { name: f.name, type: f.type, size: f.size, data: f.data, rid: ++rids };
}
/** A file to attach (a File, or what a test gives). */
export type FileLike = { name: string; type: string; size: number; arrayBuffer(): Promise<ArrayBuffer> };
type Namer = (f: { name: string; type: string }, taken: string[]) => string;

/** One add(): the files it read, in order, as they were read (whatever the form does with them
 * after). done: settled once all are read (or refused). */
/** A send's say on one more file of its reads, before it is read (its size known): true if it fits
 * the send's budget, fixed at the click. */
type Admit = (f: { name: string; size: number }) => boolean;
type ReadJob = { results: Attachment[]; failed: string[]; sends: Set<Admit>; reading: { name: string; size: number } | null; done: Promise<void> };

/** Why a send's files cannot go: over the node's limits, or a file its reads could not read. */
export type FilesRefused = { why: "count" | "size" | "read"; file: string };

/** The files a send takes, owned by it from the click: the form's list then, and what the reads
 * still going then bring after it (each read delivers to the send too, not only to the form; what
 * they brought before the click is in that list, or was removed from it). A read's file keeps the
 * name it was given once, when read (a pasted picture's pasted-…-n is not made again); one that
 * the send's own list has already is told apart there. The limits are the click's and are judged
 * against the send's own list: a read the send has is read for it whatever room the form has
 * after the click (captured), and what does not fit, or could not be read, refuses the whole send
 * (refused): nothing goes in part. The form after the click is not asked. wait() is when those
 * reads end; done() lets the reads go (the form judges them again). */
export function sendFiles(d: ComposerDraft, limits: Limits | null): {
  wait: Promise<void>; files: () => { files: Attachment[]; refused?: FilesRefused }; done: () => void;
  /** All the send owns, refused or not: the click's files and all its reads brought. */
  all: () => Attachment[];
} {
  const had = [...d.files], jobs = [...d.reading].map(j => ({ j, from: j.results.length, failed: j.failed.length }));
  // The budget: the click's files, then each file of its reads as it comes up, judged by what is
  // known of it before it is read (its size): one that cannot fit refuses the send unread.
  const budget: { size: number }[] = [];
  let refused: FilesRefused | undefined;
  const admit: Admit = f => {
    if (refused) return false;
    const over = overLimits(budget, f, limits);
    if (over) { refused = { why: over, file: f.name }; return false; }
    budget.push({ size: f.size });
    return true;
  };
  // The click's files first: a list already over the limits (drafts merged, a file handed over)
  // is refused here, not left to the node.
  for (const f of had) admit(f);
  // A file whose read is under way at the click (it passed the form's check before it) is the
  // send's too: in its budget first.
  for (const { j } of jobs) { if (j.reading) admit(j.reading); j.sends.add(admit); }
  return {
    wait: Promise.all(jobs.map(({ j }) => j.done)).then(() => {}),
    files: () => {
      if (refused) return { files: [], refused };
      const failed = jobs.flatMap(({ j, failed }) => j.failed.slice(failed));
      if (failed.length) return { files: [], refused: { why: "read", file: failed[0] } };
      const reads = jobs.flatMap(({ j, from }) => j.results.slice(from)).sort((a, b) => a.rid - b.rid);
      const fit: Attachment[] = had.map(f => ({ ...f }));
      for (const r of reads) {
        const over = overLimits(fit, r, limits);
        if (over) { return { files: [], refused: { why: over, file: r.name } }; }
        fit.push(r);
      }
      return { files: withReads(had, reads) };
    },
    done: () => { for (const { j } of jobs) j.sends.delete(admit); },
    all: () => withReads(had, jobs.flatMap(({ j, from }) => j.results.slice(from)).sort((a, b) => a.rid - b.rid)),
  };
}

/** A send that did not go gives the draft back all it owned (its request's files, by identity,
 * in its order and with its names), before what was attached after the click: nothing it had is
 * lost, and the same files make the same request again. */
export function giveBack(d: ComposerDraft, owned: Attachment[]) {
  const mine = new Set(owned.map(f => f.rid));
  d.files = [...owned.map(f => ({ ...f })), ...d.files.filter(f => !mine.has(f.rid))];
}

/** A list of files with the files reads brought after it, as both the composer and a send build
 * it: in the order they were delivered (rid; reads of two drafts merged run at once, so not in
 * the order of their add()), each told apart from the names before it. One rule, so the same
 * files make the same request, and a retry has the first one's key. */
export function withReads(list: Attachment[], reads: Attachment[]): Attachment[] {
  const out = list.map(f => ({ ...f }));
  for (const r of [...reads].sort((a, b) => a.rid - b.rid)) out.push({ ...r, name: freeName(r.name, out.map(x => x.name)) });
  return out;
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

  /** Attaches files handed over whole by another part of the app (the preview's problems): each
   * a new attachment, with its own identity; one of a name already attached replaces that one. */
  take(list: { name: string; type: string; size: number; data: string }[]) {
    const names = new Set(list.map(f => f.name));
    this.files = [...this.files.filter(f => !names.has(f.name)), ...list.map(attachment)];
  }

  /** Attaches files within the node's limits, one at a time: two drops at once cannot both pass a
   * check made before either was read. A file over the limits is said (fail) and not read; one
   * that cannot be read is said too, and the next is read. `name` gives the name it is attached
   * under, from the names attached right then. A send waits for what is being read (adding). */
  add(list: Iterable<FileLike>, limits: Limits | null, fail: (why: "count" | "size" | "read", file: string) => void,
    name: Namer = f => f.name): Promise<void> {
    const files = [...list];
    const at = this.live();
    const job: ReadJob = { results: [], failed: [], sends: new Set(), reading: null, done: Promise.resolve() };
    at.reading.add(job);
    at.adding = at.adding.then(async () => {
      for (const f of files) {
        // A read a send has is the send's to judge, against its own budget, before it is read: the
        // form's room after the click does not refuse it. One no send takes (none has it, or it
        // does not fit the send's) is the form's, as before: over its limits, it is not read.
        const sends = [...job.sends];
        const forSend = sends.length > 0 && sends.every(admit => admit(f));
        if (!forSend) { const over = overLimits(this.live().files, f, limits); if (over) { fail(over, f.name); return; } }
        let data: string;
        job.reading = f;
        try { data = b64bytes(new Uint8Array(await f.arrayBuffer())); } catch { job.reading = null; job.failed.push(f.name); fail("read", f.name); continue; }
        job.reading = null;
        // Read into whichever draft this one is by now (a new task, the thread it moved into),
        // checked again there: that one may have filled up meanwhile.
        const d = this.live();
        const over = overLimits(d.files, f, limits);
        // A send that came while it was read has it too (in its budget from the click).
        const mine = forSend || job.sends.size > 0;
        if (over && !mine) { fail(over, f.name); return; }
        // Its name, chosen here: against all attached by now, those added just before it included.
        const a = attachment({ name: name(f, d.files.map(x => x.name)), type: f.type || "application/octet-stream", size: f.size, data });
        job.results.push({ ...a });
        if (!over) d.files = withReads(d.files, [a]); // (no room in the form: the send has it)
      }
    }).catch(() => {}) // what failed was said: the queue goes on
      .finally(() => { for (let x: ComposerDraft | null = this; x; x = x.movedTo) x.reading.delete(job); });
    job.done = at.adding;
    return at.adding;
  }
}
